package runtime

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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

	var llmProvider basellm.Provider
	if cfg.LLM.Enabled {
		llmProvider, err = buildLLMProvider(cfg, logger)
		if err != nil {
			_ = closers.Close()
			return Runtime{}, err
		}
	}

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

	registry, watchService, visualWatchService, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, memoryStore, logger, llmProvider, systemProvider, serviceManager, mcpManager)
	if err != nil {
		_ = closers.Close()
		return Runtime{}, err
	}

	var classifier router.Classifier
	if llmProvider != nil {
		classifier = routerllm.NewClassifier(llmProvider, registry, routerllm.Options{
			AllowedServices:          allowedServiceNames,
			AllowedWorkbenchRuntimes: cfg.Workbench.AllowedRuntimes,
			ExecuteThreshold:         cfg.LLM.ExecuteThreshold,
			ClarifyThreshold:         cfg.LLM.ClarifyThreshold,
			InputChars:               cfg.LLM.DecisionInputChars,
			NumPredict:               cfg.LLM.DecisionNumPredict,
		}, logger.With("component", "router-llm"))
	}

	return Runtime{
		Registry:    registry,
		Classifier:  classifier,
		Repository:  repository,
		Watch:       watchService,
		VisualWatch: visualWatchService,
		Closer:      closers,
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

	registry, _, _, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, repository, logger, llmProvider, systemProvider, serviceManager, nil)
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
	visionManager, err := buildVisionManager(cfg, logger)
	if err != nil {
		return nil, nil, nil, nil, err
	}
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

	modules := []skills.Module{
		systemskills.NewModule(systemProvider),
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

func buildLLMProvider(cfg config.Config, logger *slog.Logger) (basellm.Provider, error) {
	llmLogger := logger.With("component", "llm")
	return basellm.BuildProvider(cfg.LLM.Provider, basellm.ProviderConfig{
		Endpoint: cfg.LLM.Endpoint,
		Model:    cfg.LLM.Model,
		APIKey:   cfg.LLM.APIKey,
		Timeout:  cfg.Agent.RequestTimeout,
	}, llmLogger)
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
