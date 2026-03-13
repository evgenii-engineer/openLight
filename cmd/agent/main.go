package main

import (
	"context"
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

	logger := logging.New(cfg.LogLevel)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	repository, err := sqlite.New(ctx, cfg.SQLitePath, logger.With("component", "sqlite"))
	if err != nil {
		return err
	}
	defer repository.Close()

	var llmProvider basellm.Provider
	if cfg.LLMEnabled {
		llmProvider = buildLLMProvider(cfg, logger)
	}

	registry := buildRegistry(cfg, repository, logger, llmProvider)

	var classifier router.Classifier
	if llmProvider != nil {
		classifier = routerllm.NewClassifier(llmProvider, registry, logger.With("component", "router-llm"))
	}

	bot := telegram.NewBot(cfg.TelegramBotToken, cfg.TelegramAPIBase, cfg.PollTimeout, logger.With("component", "telegram"))
	agent := core.NewAgent(
		bot,
		auth.New(cfg.AllowedUserIDs, cfg.AllowedChatIDs),
		router.New(registry, classifier),
		registry,
		repository,
		logger.With("component", "agent"),
		cfg.RequestTimeout,
	)

	logger.Info("agent starting", slog.String("sqlite_path", cfg.SQLitePath))
	err = agent.Run(ctx)
	if err != nil && err != context.Canceled {
		return err
	}

	logger.Info("agent stopped")
	return nil
}

func buildRegistry(cfg config.Config, repository storage.Repository, logger *slog.Logger, llmProvider basellm.Provider) *skills.Registry {
	registry := skills.NewRegistry()

	systemProvider := systemskills.NewLocalProvider()
	serviceManager := serviceskills.NewSystemdManager(cfg.AllowedServices, logger.With("component", "systemd"))

	registry.MustRegister(skills.NewStartSkill())
	registry.MustRegister(skills.NewPingSkill())
	registry.MustRegister(systemskills.NewStatusSkill(systemProvider))
	registry.MustRegister(systemskills.NewCPUSkill(systemProvider))
	registry.MustRegister(systemskills.NewMemorySkill(systemProvider))
	registry.MustRegister(systemskills.NewDiskSkill(systemProvider))
	registry.MustRegister(systemskills.NewUptimeSkill(systemProvider))
	registry.MustRegister(systemskills.NewHostnameSkill(systemProvider))
	registry.MustRegister(systemskills.NewIPSkill(systemProvider))
	registry.MustRegister(systemskills.NewTemperatureSkill(systemProvider))
	registry.MustRegister(serviceskills.NewListSkill(serviceManager))
	registry.MustRegister(serviceskills.NewStatusSkill(serviceManager))
	registry.MustRegister(serviceskills.NewRestartSkill(serviceManager))
	registry.MustRegister(serviceskills.NewLogsSkill(serviceManager, cfg.ServiceLogLines))
	registry.MustRegister(notes.NewAddSkill(repository))
	registry.MustRegister(notes.NewListSkill(repository, cfg.NotesListLimit))
	registry.MustRegister(notes.NewDeleteSkill(repository))
	if llmProvider != nil {
		registry.MustRegister(chatskills.NewSkillWithOptions(llmProvider, repository, chatskills.Options{
			HistoryLimit:     cfg.ChatHistoryLimit,
			HistoryChars:     cfg.ChatHistoryChars,
			MaxResponseChars: cfg.ChatMaxRespChars,
		}))
	}
	registry.MustRegister(skills.NewSkillsSkill(registry))
	registry.MustRegister(skills.NewHelpSkill(registry))

	return registry
}

func buildLLMProvider(cfg config.Config, logger *slog.Logger) basellm.Provider {
	llmLogger := logger.With("component", "llm")

	switch cfg.LLMProvider {
	case "ollama":
		return basellm.NewOllamaProvider(cfg.LLMEndpoint, cfg.LLMModel, cfg.RequestTimeout, llmLogger)
	default:
		return basellm.NewHTTPProvider(cfg.LLMEndpoint, cfg.RequestTimeout, llmLogger)
	}
}
