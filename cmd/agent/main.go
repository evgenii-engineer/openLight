package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"openlight/internal/auth"
	"openlight/internal/config"
	"openlight/internal/core"
	basellm "openlight/internal/llm"
	"openlight/internal/logging"
	"openlight/internal/router"
	routerllm "openlight/internal/router/llm"
	"openlight/internal/skills"
	chatskills "openlight/internal/skills/chat"
	"openlight/internal/skills/notes"
	serviceskills "openlight/internal/skills/services"
	systemskills "openlight/internal/skills/system"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "agent failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "Path to YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Log.Level)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repository, err := sqlite.New(ctx, cfg.Storage.SQLitePath, logger.With("component", "sqlite"))
	if err != nil {
		return err
	}
	defer repository.Close()

	var llmProvider basellm.Provider
	if cfg.LLM.Enabled {
		llmProvider, err = buildLLMProvider(cfg, logger)
		if err != nil {
			return err
		}
	}

	registry, err := buildRegistry(cfg, repository, logger, llmProvider)
	if err != nil {
		return err
	}

	var classifier router.Classifier
	if llmProvider != nil {
		classifier = routerllm.NewClassifier(llmProvider, registry, routerllm.Options{
			AllowedServices:          cfg.Services.Allowed,
			ExecuteThreshold:         cfg.LLM.ExecuteThreshold,
			MutatingExecuteThreshold: cfg.LLM.MutatingExecuteThreshold,
			ClarifyThreshold:         cfg.LLM.ClarifyThreshold,
			InputChars:               cfg.LLM.DecisionInputChars,
			NumPredict:               cfg.LLM.DecisionNumPredict,
		}, logger.With("component", "router-llm"))
	}

	bot := telegram.NewBot(telegram.Options{
		Token:       cfg.Telegram.BotToken,
		BaseURL:     cfg.Telegram.APIBaseURL,
		Mode:        cfg.Telegram.Mode,
		PollTimeout: cfg.Telegram.PollTimeout,
		Webhook: telegram.WebhookOptions{
			URL:                cfg.Telegram.Webhook.URL,
			ListenAddr:         cfg.Telegram.Webhook.ListenAddr,
			SecretToken:        cfg.Telegram.Webhook.SecretToken,
			DropPendingUpdates: cfg.Telegram.Webhook.DropPendingUpdates,
		},
		Logger: logger.With("component", "telegram"),
	})
	agent := core.NewAgent(
		bot,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.NewWithLogger(registry, classifier, logger.With("component", "router")),
		registry,
		repository,
		logger.With("component", "agent"),
		cfg.Agent.RequestTimeout,
	)

	logger.Info("agent starting", slog.String("sqlite_path", cfg.Storage.SQLitePath))
	err = agent.Run(ctx)
	if !isExpectedShutdown(err) {
		return err
	}

	logger.Info("agent stopped")
	return nil
}

func isExpectedShutdown(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}

func buildRegistry(cfg config.Config, repository storage.Repository, logger *slog.Logger, llmProvider basellm.Provider) (*skills.Registry, error) {
	registry := skills.NewRegistry()

	systemProvider := systemskills.NewLocalProvider()
	serviceManager := serviceskills.NewSystemdManager(cfg.Services.Allowed, logger.With("component", "systemd"))

	modules := []skills.Module{
		systemskills.NewModule(systemProvider),
		serviceskills.NewModule(serviceManager, cfg.Services.LogLines),
		notes.NewModule(repository, cfg.Notes.ListLimit),
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
		return nil, err
	}

	return registry, nil
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
