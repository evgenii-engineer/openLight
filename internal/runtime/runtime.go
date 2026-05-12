package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"openlight/internal/config"
	basellm "openlight/internal/llm"
	"openlight/internal/router"
	routerllm "openlight/internal/router/llm"
	"openlight/internal/skills"
	accountskills "openlight/internal/skills/accounts"
	browserskills "openlight/internal/skills/browser"
	chatskills "openlight/internal/skills/chat"
	fileskills "openlight/internal/skills/files"
	mcpskills "openlight/internal/skills/mcp"
	memoryskills "openlight/internal/skills/memory"
	networkskills "openlight/internal/skills/network"
	"openlight/internal/skills/notes"
	ocrskills "openlight/internal/skills/ocr"
	serviceskills "openlight/internal/skills/services"
	systemskills "openlight/internal/skills/system"
	visionskills "openlight/internal/skills/vision"
	visualwatchskills "openlight/internal/skills/visualwatch"
	watchskills "openlight/internal/skills/watch"
	workbenchskills "openlight/internal/skills/workbench"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
	"openlight/internal/visualwatch"
	watchengine "openlight/internal/watch"
)

type Runtime struct {
	Registry    *skills.Registry
	Classifier  router.Classifier
	Repository  storage.Repository
	Watch       *watchengine.Service
	VisualWatch *visualwatch.Service
	Closer      io.Closer

	// TelegramHealth carries the Telegram connectivity probe used by the
	// /status skill. The entrypoint binds a live probe once the bot has
	// been constructed; until then State() returns "unknown".
	TelegramHealth *TelegramHealthHolder
}

// ProviderTier captures the resolved FAST and SMART LLM profile metadata so
// the system skills can show which models the agent is configured with.
// Empty when llm.enabled is false.
type ProviderTier struct {
	FastProvider   string
	FastModel      string
	FastEndpoint   string
	FastKeepAlive  string
	SmartProvider  string
	SmartModel     string
	SmartEndpoint  string
	SmartKeepAlive string
	FastFallback   bool

	// SmartProviderInstance is the live SMART provider used by the system
	// skill to query /api/ps for load status. Optional.
	SmartProviderInstance basellm.Provider

	// LatencyStore receives Chat/Classify latency observations from the
	// fast and smart providers. Optional; nil disables the
	// "Last LLM latency" section of /status.
	LatencyStore *systemskills.LatencyStore

	// TelegramHealth is the runtime-scoped indirection through which the
	// agent entrypoint plugs in a Telegram connectivity probe. Optional.
	TelegramHealth *TelegramHealthHolder
}

func BuildRuntime(ctx context.Context, cfg config.Config, logger *slog.Logger) (Runtime, error) {
	for _, msg := range cfg.Deprecations {
		logger.Warn("deprecated config", "message", msg)
	}

	repository, err := sqlite.New(ctx, cfg.Storage.SQLitePath, logger.With("component", "sqlite"))
	if err != nil {
		return Runtime{}, err
	}

	if cfg.Storage.RetentionDays > 0 {
		cutoff := time.Now().UTC().AddDate(0, 0, -cfg.Storage.RetentionDays)
		if msgs, calls, pruneErr := repository.PruneOlderThan(ctx, cutoff); pruneErr != nil {
			logger.Warn("storage retention prune failed", "error", pruneErr)
		} else if msgs > 0 || calls > 0 {
			logger.Info("storage retention prune", "cutoff", cutoff, "messages_deleted", msgs, "skill_calls_deleted", calls)
		}
	}

	closers := multiCloser{repository}
	memoryStore := repository
	if memoryDBPath := strings.TrimSpace(cfg.Memory.DBPath); memoryDBPath != "" && memoryDBPath != cfg.Storage.SQLitePath {
		memoryRepository, err := sqlite.New(ctx, memoryDBPath, logger.With("component", "memory-sqlite"))
		if err != nil {
			_ = repository.Close()
			return Runtime{}, err
		}
		closers = append(closers, memoryRepository)
		memoryStore = memoryRepository
	}

	var (
		smartProvider basellm.Provider
		fastProvider  basellm.Provider
		smartProfile  config.LLMProfileConfig
		fastProfile   config.LLMProfileConfig
		fastFallback  bool
	)
	if cfg.LLM.Enabled {
		smartProvider, smartProfile, err = buildProfileProvider(cfg, "smart", logger)
		if err != nil {
			_ = closers.Close()
			return Runtime{}, err
		}
		if cfg.LLM.HasProfile("fast") {
			fastProvider, fastProfile, err = buildProfileProvider(cfg, "fast", logger)
			if err != nil {
				_ = closers.Close()
				return Runtime{}, err
			}
		} else {
			// No dedicated FAST profile: route classification through the
			// SMART provider so existing single-model configs keep working.
			fastProvider = smartProvider
			fastProfile = smartProfile
			fastFallback = true
		}

		warmupFast := cfg.LLM.Warmup.Includes("fast")
		warmupSmart := cfg.LLM.Warmup.Includes("smart")
		if fastFallback {
			// Single provider serves both roles — one warmup covers both
			// if either is requested.
			if warmupFast || warmupSmart {
				runWarmup(ctx, smartProvider, "smart", smartProfile.Model, cfg.LLM.Warmup, logger)
			}
		} else {
			if warmupFast {
				runWarmup(ctx, fastProvider, "fast", fastProfile.Model, cfg.LLM.Warmup, logger)
			}
			if warmupSmart {
				runWarmup(ctx, smartProvider, "smart", smartProfile.Model, cfg.LLM.Warmup, logger)
			}
		}
	}
	// llmProvider remains the chat-facing (SMART) provider. Other modules
	// that historically took a single provider keep doing so.
	llmProvider := smartProvider

	// Latency tracking and Telegram health are wired before the registry
	// is built so the /status skill's hooks can close over them.
	latencyStore := systemskills.NewLatencyStore()
	telegramHealth := NewTelegramHealthHolder()
	smartForChat := wrapWithLatency(smartProvider, "smart", latencyStore)
	fastForRouter := wrapWithLatency(fastProvider, "fast", latencyStore)
	if fastFallback {
		// When fast falls back to smart, the wrapper for the router uses
		// the "fast" label so latency for classification calls is still
		// attributed correctly even though the underlying model is shared.
		fastForRouter = wrapWithLatency(smartProvider, "fast", latencyStore)
	}
	llmProvider = smartForChat

	systemProvider := systemskills.NewLocalProvider()
	serviceManager, err := serviceskills.NewManager(cfg.Services.Allowed, cfg.Access.Hosts, logger.With("component", "services"))
	if err != nil {
		_ = closers.Close()
		return Runtime{}, err
	}

	var mcpManager *mcpskills.Manager
	if cfg.MCP.Enabled && len(cfg.MCP.Servers) > 0 {
		mcpManager, err = mcpskills.NewManager(ctx, cfg.MCP.Servers, logger.With("component", "mcp"))
		if err != nil {
			_ = closers.Close()
			return Runtime{}, fmt.Errorf("mcp: %w", err)
		}
		closers = append(closers, mcpManager)
	}

	visionManager, err := buildVisionManager(cfg, logger)
	if err != nil {
		_ = closers.Close()
		return Runtime{}, err
	}

	if cfg.LLM.Warmup.Includes("vision") && cfg.Vision.Enabled {
		runVisionWarmup(ctx, visionManager, cfg.Vision.Model, cfg.LLM.Warmup, logger)
	}

	registry, watchService, visualWatchService, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, memoryStore, logger, llmProvider, systemProvider, serviceManager, mcpManager, visionManager, ProviderTier{
		FastProvider:          fastProfile.Provider,
		FastModel:             fastProfile.Model,
		FastEndpoint:          fastProfile.Endpoint,
		FastKeepAlive:         fastProfile.KeepAlive,
		SmartProvider:         smartProfile.Provider,
		SmartModel:            smartProfile.Model,
		SmartEndpoint:         smartProfile.Endpoint,
		SmartKeepAlive:        smartProfile.KeepAlive,
		FastFallback:          fastFallback,
		SmartProviderInstance: smartProvider,
		LatencyStore:          latencyStore,
		TelegramHealth:        telegramHealth,
	})
	if err != nil {
		_ = closers.Close()
		return Runtime{}, err
	}

	var classifier router.Classifier
	if fastProvider != nil {
		// Route classification uses the FAST provider — cheap, low-latency
		// JSON output. Final reasoning/answers use SMART via the chat skill.
		executeThreshold := fastProfile.ExecuteThreshold
		if executeThreshold <= 0 {
			executeThreshold = cfg.LLM.ExecuteThreshold
		}
		clarifyThreshold := fastProfile.ClarifyThreshold
		if clarifyThreshold <= 0 {
			clarifyThreshold = cfg.LLM.ClarifyThreshold
		}
		inputChars := fastProfile.DecisionInputChars
		if inputChars <= 0 {
			inputChars = cfg.LLM.DecisionInputChars
		}
		numPredict := fastProfile.DecisionNumPredict
		if numPredict <= 0 {
			numPredict = cfg.LLM.DecisionNumPredict
		}

		classifier = routerllm.NewClassifier(fastForRouter, registry, routerllm.Options{
			AllowedServices:          allowedServiceNames,
			AllowedWorkbenchRuntimes: cfg.Workbench.AllowedRuntimes,
			ExecuteThreshold:         executeThreshold,
			ClarifyThreshold:         clarifyThreshold,
			InputChars:               inputChars,
			NumPredict:               numPredict,
			Profile:                  "fast",
			Model:                    fastProfile.Model,
			FallbackUsed:             fastFallback,
		}, logger.With("component", "router-llm"))
	}

	return Runtime{
		Registry:       registry,
		Classifier:     classifier,
		Repository:     repository,
		Watch:          watchService,
		VisualWatch:    visualWatchService,
		Closer:         closers,
		TelegramHealth: telegramHealth,
	}, nil
}

func BuildRegistry(
	cfg config.Config,
	repository storage.Repository,
	logger *slog.Logger,
	llmProvider basellm.Provider,
) (*skills.Registry, []string, error) {
	systemProvider := systemskills.NewLocalProvider()
	serviceManager, err := serviceskills.NewManager(cfg.Services.Allowed, cfg.Access.Hosts, logger.With("component", "services"))
	if err != nil {
		return nil, nil, err
	}

	visionManager, err := buildVisionManager(cfg, logger)
	if err != nil {
		return nil, nil, err
	}

	registry, _, _, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, repository, logger, llmProvider, systemProvider, serviceManager, nil, visionManager, ProviderTier{
		SmartProvider: cfg.LLM.Provider,
		SmartModel:    cfg.LLM.Model,
		SmartEndpoint: cfg.LLM.Endpoint,
		FastFallback:  true,
	})
	if err != nil {
		return nil, nil, err
	}
	return registry, allowedServiceNames, nil
}

func BuildRegistryWithWatch(
	cfg config.Config,
	repository storage.Repository,
	memoryStore memoryskills.Store,
	logger *slog.Logger,
	llmProvider basellm.Provider,
	systemProvider systemskills.Provider,
	serviceManager serviceskills.Manager,
	mcpManager *mcpskills.Manager,
	visionManager visionskills.Manager,
	tier ProviderTier,
) (*skills.Registry, *watchengine.Service, *visualwatch.Service, []string, error) {
	registry := skills.NewRegistry()

	allowedServiceNames, err := serviceskills.AllowedServiceNames(cfg.Services.Allowed)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	fileManager, err := fileskills.NewLocalManager(
		cfg.Files.Enabled,
		cfg.Files.Allowed,
		cfg.Files.MaxReadBytes,
		cfg.Files.ListLimit,
		cfg.Files.AllowWrite,
		cfg.Files.RedactSecrets,
		cfg.Files.AllowSensitiveRead,
	)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	browserManager := browserskills.NewLocalManager(
		cfg.Browser.Enabled,
		cfg.Browser.AllowedDomains,
		cfg.Browser.AllowAllDomains,
		cfg.Browser.AllowPrivateNetwork,
		cfg.Browser.ArtifactsDir,
		cfg.Browser.TimeoutSeconds,
		browserskills.NewCommandRunner(cfg.Browser.NodePath, cfg.Browser.HelperPath),
	)
	ocrManager, err := buildOCRManager(cfg)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	watchService := watchengine.NewService(
		repository,
		registry,
		nil,
		systemProvider,
		serviceManager,
		logger.With("component", "watch"),
		watchengine.Options{
			PollInterval:   cfg.Watch.PollInterval,
			AskTTL:         cfg.Watch.AskTTL,
			RequestTimeout: cfg.Agent.RequestTimeout,
		},
	)
	var visualWatchService *visualwatch.Service
	if cfg.VisualWatch.Enabled {
		visualWatchService = visualwatch.NewService(
			repository,
			browserManager,
			ocrManager,
			nil,
			logger.With("component", "visual-watch"),
			visualwatch.Options{
				PollInterval:     cfg.VisualWatch.PollInterval,
				BaselinesDir:     cfg.VisualWatch.BaselinesDir,
				DefaultInterval:  cfg.VisualWatch.DefaultInterval,
				DefaultThreshold: cfg.VisualWatch.DefaultThreshold,
				DefaultCooldown:  cfg.VisualWatch.DefaultCooldown,
				RequestTimeout:   cfg.VisualWatch.RequestTimeout,
			},
		)
	}

	systemHooks := buildSystemHooks(tier.SmartProviderInstance, tier.LatencyStore, tier.TelegramHealth, os.Getpid())
	modules := []skills.Module{
		systemskills.NewModule(systemProvider, buildSystemModelsInfo(cfg, tier, tier.SmartProviderInstance, systemHooks), systemHooks),
		fileskills.NewModule(fileManager),
		browserskills.NewModule(browserManager),
		serviceskills.NewModule(serviceManager, cfg.Services.LogLines, cfg.Services.MaxLogChars),
		memoryskills.NewModule(memoryStore, cfg.Memory.ListLimit, cfg.Memory.Enabled),
		notes.NewModule(repository, cfg.Notes.ListLimit),
		watchskills.NewModule(watchService),
	}
	if cfg.Vision.Enabled {
		modules = append(modules, visionskills.NewModule(visionManager))
	}
	if cfg.OCR.Enabled {
		modules = append(modules, ocrskills.NewModule(ocrManager))
	}
	if visualWatchService != nil {
		modules = append(modules, visualwatchskills.NewModule(repository, visualWatchService))
	}
	if cfg.Network.Enabled {
		networkManager, err := networkskills.NewLocalManager(true, cfg.Network.Allowed, cfg.Network.Timeout)
		if err != nil {
			return nil, nil, nil, nil, fmt.Errorf("network skills: %w", err)
		}
		modules = append(modules, networkskills.NewModule(networkManager))
		// Watch service uses the same network manager for port_down /
		// cert_expiring evaluation. Setter pattern keeps the constructor
		// signature stable.
		watchService.SetNetworkManager(networkManager)
	}
	if mcpManager != nil {
		modules = append(modules, mcpskills.NewModule(mcpManager))
	}
	if len(cfg.Accounts.Providers) > 0 {
		accountManager, err := accountskills.NewManager(cfg.Accounts.Providers, serviceManager)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		modules = append(modules, accountskills.NewModule(accountManager))
	}
	if cfg.Workbench.Enabled {
		workbenchManager, err := workbenchskills.NewLocalManager(
			cfg.Workbench.WorkspaceDir,
			cfg.Workbench.AllowedRuntimes,
			cfg.Workbench.AllowedFiles,
			cfg.Workbench.MaxOutputBytes,
		)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		modules = append(modules, workbenchskills.NewModule(workbenchManager))
	}
	if llmProvider != nil {
		modules = append(modules, chatskills.NewModule(llmProvider, repository, chatskills.Options{
			HistoryLimit:     cfg.Chat.HistoryLimit,
			HistoryChars:     cfg.Chat.HistoryChars,
			MaxResponseChars: cfg.Chat.MaxResponseChars,
		}))
	}
	modules = append(modules, skills.NewCoreModule())

	if err := skills.RegisterModules(registry, modules...); err != nil {
		return nil, nil, nil, nil, err
	}

	return registry, watchService, visualWatchService, allowedServiceNames, nil
}

func buildVisionManager(cfg config.Config, logger *slog.Logger) (visionskills.Manager, error) {
	provider, err := visionskills.BuildProvider(visionskills.ProviderConfig{
		Provider: cfg.Vision.Provider,
		Endpoint: cfg.Vision.Endpoint,
		Model:    cfg.Vision.Model,
		APIKey:   cfg.Vision.APIKey,
		Timeout:  cfg.Vision.Timeout,
		Logger:   logger.With("component", "vision"),
	})
	if err != nil {
		return nil, err
	}
	if cfg.Vision.Enabled && provider == nil {
		return nil, fmt.Errorf("vision is enabled but provider %q resolves to none", cfg.Vision.Provider)
	}
	return visionskills.NewLocalManager(visionskills.Options{
		Enabled:          cfg.Vision.Enabled,
		Provider:         provider,
		ProviderName:     cfg.Vision.Provider,
		ModelName:        cfg.Vision.Model,
		DefaultPrompt:    cfg.Vision.DefaultPrompt,
		MaxImageSizeMB:   cfg.Vision.MaxImageSizeMB,
		Timeout:          cfg.Vision.Timeout,
		MaxResponseChars: cfg.Vision.MaxResponseChars,
	}), nil
}

func buildOCRManager(cfg config.Config) (ocrskills.Manager, error) {
	provider, err := ocrskills.BuildProvider(ocrskills.ProviderConfig{
		Provider:   cfg.OCR.Provider,
		BinaryPath: cfg.OCR.BinaryPath,
	})
	if err != nil {
		return nil, err
	}
	if cfg.OCR.Enabled && provider == nil {
		return nil, fmt.Errorf("ocr is enabled but provider %q resolves to none", cfg.OCR.Provider)
	}
	return ocrskills.NewLocalManager(ocrskills.Options{
		Enabled:        cfg.OCR.Enabled,
		Provider:       provider,
		Languages:      cfg.OCR.Languages,
		Timeout:        cfg.OCR.Timeout,
		MaxImageSizeMB: cfg.OCR.MaxImageSizeMB,
	}), nil
}

func buildSystemModelsInfo(cfg config.Config, tier ProviderTier, smartProvider basellm.Provider, hooks systemskills.Hooks) systemskills.ModelsInfo {
	info := systemskills.ModelsInfo{
		LLMProfile:      cfg.LLM.Profile,
		LLMProvider:     cfg.LLM.Provider,
		LLMModel:        cfg.LLM.Model,
		LLMEndpoint:     cfg.LLM.Endpoint,
		FastProvider:    tier.FastProvider,
		FastModel:       tier.FastModel,
		FastEndpoint:    tier.FastEndpoint,
		SmartProvider:   tier.SmartProvider,
		SmartModel:      tier.SmartModel,
		SmartEndpoint:   tier.SmartEndpoint,
		FastFallback:    tier.FastFallback,
		FastKeepAlive:   tier.FastKeepAlive,
		SmartKeepAlive:  tier.SmartKeepAlive,
		WarmupEnabled:   cfg.LLM.Warmup.Enabled,
		WarmupProfiles:  append([]string(nil), cfg.LLM.Warmup.Profiles...),
		WarmupKeepAlive: cfg.LLM.Warmup.KeepAliveString(),
		WarmupPrompt:    cfg.LLM.Warmup.PromptOrDefault(),
		VisionEnabled:   cfg.Vision.Enabled,
		VisionProvider:  cfg.Vision.Provider,
		VisionModel:     cfg.Vision.Model,
		OCREnabled:      cfg.OCR.Enabled,
		OCRProvider:     cfg.OCR.Provider,
		OCRLanguages:    append([]string(nil), cfg.OCR.Languages...),
		VoiceEnabled:    cfg.Voice.Enabled,
		VoiceProvider:   cfg.Voice.Provider,
		VoiceModel:      cfg.Voice.ModelPath,
	}
	// /models shares the /status loaded-models lookup so a single /api/ps
	// hit can populate both renderings (status and models). Falls through
	// when the smart provider isn't Ollama.
	if hooks.LoadedModels != nil {
		info.LoadedModelsLookup = hooks.LoadedModels
	}
	_ = smartProvider // retained for symmetry with hook construction
	return info
}

// buildProfileProvider resolves the named profile against cfg.LLM and
// constructs the corresponding provider. The resolved profile is also
// returned so the caller can pick thresholds and report metadata.
func buildProfileProvider(cfg config.Config, name string, logger *slog.Logger) (basellm.Provider, config.LLMProfileConfig, error) {
	profile := cfg.LLM.ResolveProfile(name)
	if strings.TrimSpace(profile.Provider) == "" {
		return nil, profile, fmt.Errorf("llm: cannot resolve provider for %q profile", name)
	}

	llmLogger := logger.With(
		"component", "llm",
		"profile", strings.ToLower(strings.TrimSpace(name)),
		"model", profile.Model,
	)
	provider, err := basellm.BuildProvider(profile.Provider, basellm.ProviderConfig{
		Endpoint:  profile.Endpoint,
		Model:     profile.Model,
		APIKey:    profile.APIKey,
		Timeout:   cfg.Agent.RequestTimeout,
		KeepAlive: profile.KeepAlive,
	}, llmLogger)
	if err != nil {
		return nil, profile, fmt.Errorf("build %q llm profile: %w", name, err)
	}
	return provider, profile, nil
}

// warmupBackoff returns the sleep duration before warmup retry N (zero-
// indexed). 5s, 15s, 45s, 2m, 5m, then cap. Bounded so the loop eventually
// stops hammering Ollama if it's persistently down.
var warmupBackoff = []time.Duration{
	5 * time.Second,
	15 * time.Second,
	45 * time.Second,
	2 * time.Minute,
	5 * time.Minute,
}

const warmupMaxAttempts = 8

// runWarmup launches a goroutine that warms up the LLM profile and retries
// with backoff if Ollama is unavailable. The agent keeps running even if
// warmup never succeeds — the next user request just pays cold start.
func runWarmup(ctx context.Context, provider basellm.Provider, profileName, modelName string, warmup config.LLMWarmupConfig, logger *slog.Logger) {
	if provider == nil {
		return
	}
	pw, ok := provider.(basellm.Prewarmer)
	if !ok {
		return
	}

	type configurablePrewarmer interface {
		PrewarmWith(ctx context.Context, opts basellm.PrewarmOptions) error
	}

	go func() {
		baseLogger := logger.With(
			"component", "llm-warmup",
			"profile", profileName,
			"model", modelName,
			"keep_alive", warmup.KeepAliveString(),
		)
		baseLogger.Info("warmup scheduled")

		for attempt := 0; attempt < warmupMaxAttempts; attempt++ {
			attemptLogger := baseLogger.With("attempt", attempt+1)
			started := time.Now()
			pwCtx, cancel := context.WithTimeout(ctx, 90*time.Second)

			var err error
			if cp, supported := pw.(configurablePrewarmer); supported {
				err = cp.PrewarmWith(pwCtx, basellm.PrewarmOptions{
					Prompt:    warmup.PromptOrDefault(),
					KeepAlive: warmup.KeepAliveString(),
				})
			} else {
				err = pw.Prewarm(pwCtx)
			}
			cancel()

			latencyMS := time.Since(started).Milliseconds()
			if err == nil {
				attemptLogger.Info("warmup completed", "latency_ms", latencyMS)
				return
			}
			attemptLogger.Warn("warmup failed", "error", err, "latency_ms", latencyMS)

			if ctx.Err() != nil {
				return
			}
			delay := warmupBackoff[len(warmupBackoff)-1]
			if attempt < len(warmupBackoff) {
				delay = warmupBackoff[attempt]
			}
			attemptLogger.Info("warmup retry scheduled", "delay", delay.String())
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		baseLogger.Warn("warmup giving up", "attempts", warmupMaxAttempts)
	}()
}

// runVisionWarmup mirrors runWarmup for the vision package. Same retry
// policy; same goroutine semantics.
func runVisionWarmup(ctx context.Context, manager visionskills.Manager, modelName string, warmup config.LLMWarmupConfig, logger *slog.Logger) {
	type prewarmer interface {
		Prewarm(context.Context) error
	}
	pw, ok := manager.(prewarmer)
	if !ok {
		return
	}

	go func() {
		baseLogger := logger.With(
			"component", "vision-warmup",
			"model", modelName,
			"keep_alive", warmup.KeepAliveString(),
		)
		baseLogger.Info("warmup scheduled")

		for attempt := 0; attempt < warmupMaxAttempts; attempt++ {
			attemptLogger := baseLogger.With("attempt", attempt+1)
			started := time.Now()
			pwCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
			err := pw.Prewarm(pwCtx)
			cancel()

			latencyMS := time.Since(started).Milliseconds()
			if err == nil {
				attemptLogger.Info("warmup completed", "latency_ms", latencyMS)
				return
			}
			attemptLogger.Warn("warmup failed", "error", err, "latency_ms", latencyMS)

			if ctx.Err() != nil {
				return
			}
			delay := warmupBackoff[len(warmupBackoff)-1]
			if attempt < len(warmupBackoff) {
				delay = warmupBackoff[attempt]
			}
			attemptLogger.Info("warmup retry scheduled", "delay", delay.String())
			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return
			}
		}
		baseLogger.Warn("warmup giving up", "attempts", warmupMaxAttempts)
	}()
}

func CloseRuntime(runtime Runtime) error {
	if runtime.Closer == nil {
		return nil
	}
	if err := runtime.Closer.Close(); err != nil {
		return fmt.Errorf("close runtime: %w", err)
	}
	return nil
}

type multiCloser []io.Closer

func (c multiCloser) Close() error {
	var firstErr error
	for _, closer := range c {
		if closer == nil {
			continue
		}
		if err := closer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}
