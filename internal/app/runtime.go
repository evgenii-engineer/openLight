package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"

	"openlight/internal/config"
	basellm "openlight/internal/llm"
	"openlight/internal/router"
	routerllm "openlight/internal/router/llm"
	"openlight/internal/skills"
	accountskills "openlight/internal/skills/accounts"
	chatskills "openlight/internal/skills/chat"
	fileskills "openlight/internal/skills/files"
	"openlight/internal/skills/notes"
	serviceskills "openlight/internal/skills/services"
	systemskills "openlight/internal/skills/system"
	watchskills "openlight/internal/skills/watch"
	workbenchskills "openlight/internal/skills/workbench"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
	watchengine "openlight/internal/watch"
)

type Runtime struct {
	Registry   *skills.Registry
	Classifier router.Classifier
	Repository storage.Repository
	Watch      *watchengine.Service
	Closer     io.Closer
}

func BuildRuntime(ctx context.Context, cfg config.Config, logger *slog.Logger) (Runtime, error) {
	repository, err := sqlite.New(ctx, cfg.Storage.SQLitePath, logger.With("component", "sqlite"))
	if err != nil {
		return Runtime{}, err
	}

	var llmProvider basellm.Provider
	if cfg.LLM.Enabled {
		llmProvider, err = buildLLMProvider(cfg, logger)
		if err != nil {
			_ = repository.Close()
			return Runtime{}, err
		}
	}

	systemProvider := systemskills.NewLocalProvider()
	serviceManager, err := serviceskills.NewManager(cfg.Services.Allowed, cfg.Access.Hosts, logger.With("component", "services"))
	if err != nil {
		_ = repository.Close()
		return Runtime{}, err
	}

	registry, watchService, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, logger, llmProvider, systemProvider, serviceManager)
	if err != nil {
		_ = repository.Close()
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
		Registry:   registry,
		Classifier: classifier,
		Repository: repository,
		Watch:      watchService,
		Closer:     repository,
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

	registry, _, allowedServiceNames, err := BuildRegistryWithWatch(cfg, repository, logger, llmProvider, systemProvider, serviceManager)
	if err != nil {
		return nil, nil, err
	}
	return registry, allowedServiceNames, nil
}

func BuildRegistryWithWatch(
	cfg config.Config,
	repository storage.Repository,
	logger *slog.Logger,
	llmProvider basellm.Provider,
	systemProvider systemskills.Provider,
	serviceManager serviceskills.Manager,
) (*skills.Registry, *watchengine.Service, []string, error) {
	registry := skills.NewRegistry()

	allowedServiceNames, err := serviceskills.AllowedServiceNames(cfg.Services.Allowed)
	if err != nil {
		return nil, nil, nil, err
	}
	fileManager, err := fileskills.NewLocalManager(cfg.Files.Allowed, cfg.Files.MaxReadBytes, cfg.Files.ListLimit)
	if err != nil {
		return nil, nil, nil, err
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

	modules := []skills.Module{
		systemskills.NewModule(systemProvider),
		fileskills.NewModule(fileManager),
		serviceskills.NewModule(serviceManager, cfg.Services.LogLines, cfg.Services.MaxLogChars),
		notes.NewModule(repository, cfg.Notes.ListLimit),
		watchskills.NewModule(watchService),
	}
	if len(cfg.Accounts.Providers) > 0 {
		accountManager, err := accountskills.NewManager(cfg.Accounts.Providers, serviceManager)
		if err != nil {
			return nil, nil, nil, err
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
			return nil, nil, nil, err
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
		return nil, nil, nil, err
	}

	return registry, watchService, allowedServiceNames, nil
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
