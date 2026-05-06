package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"time"

	"openlight/internal/app"
	"openlight/internal/auth"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/logging"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/telegram"
	telegramui "openlight/internal/telegram/ui"
	"openlight/internal/telegram/ui/sessions"
	"openlight/internal/voice"
)

var defaultConfigPath = "/etc/openlight/agent.yaml"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "agent failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "Path to YAML configuration file")
	flag.Parse()

	cfg, err := config.Load(resolveConfigPath(*configPath))
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Log.Level)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	runCtx, cancelRun := context.WithCancel(ctx)
	defer cancelRun()

	runtime, err := app.BuildRuntime(runCtx, cfg, logger)
	if err != nil {
		return err
	}
	defer app.CloseRuntime(runtime)

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
	if runtime.Watch != nil {
		runtime.Watch.SetNotifier(bot)
	}
	agent := core.NewAgent(
		bot,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.NewWithLogger(runtime.Registry, runtime.Classifier, logger.With("component", "router")),
		runtime.Registry,
		runtime.Repository,
		runtime.Watch,
		logger.With("component", "agent"),
		cfg.Agent.RequestTimeout,
	)
	ui := telegramui.New(telegramui.Config{
		Registry:     runtime.Registry,
		Transport:    bot,
		Sessions:     sessions.NewStore(15 * time.Minute),
		QuickActions: telegramui.DefaultQuickActions(),
		Logger:       logger.With("component", "telegram-ui"),
	})
	agent.SetUI(ui)
	if cfg.Voice.Enabled {
		agent.SetVoiceProcessor(
			voice.NewProcessor(
				true,
				voice.FFmpegConverter{BinaryPath: cfg.Voice.FFmpegPath},
				voice.WhisperCLITranscriber{
					BinaryPath: cfg.Voice.WhisperCLIPath,
					ModelPath:  cfg.Voice.ModelPath,
				},
			),
			cfg.Voice.ReplyWithTranscript,
		)
	}

	var watchErrCh chan error
	if cfg.Watch.Enabled && runtime.Watch != nil {
		watchErrCh = make(chan error, 1)
		go func() {
			watchErrCh <- runtime.Watch.Run(runCtx)
		}()
	}

	publishCtx, publishCancel := context.WithTimeout(runCtx, 10*time.Second)
	if err := bot.SetMyCommands(publishCtx, telegramCommandsFromRegistry(runtime.Registry)); err != nil {
		logger.Warn("publish telegram commands", "error", err)
	}
	publishCancel()

	logger.Info("agent starting", slog.String("sqlite_path", cfg.Storage.SQLitePath))
	err = agent.Run(runCtx)
	cancelRun()
	if !isExpectedShutdown(err) {
		return err
	}
	if watchErrCh != nil {
		if watchErr := <-watchErrCh; !isExpectedShutdown(watchErr) {
			return watchErr
		}
	}

	logger.Info("agent stopped")
	return nil
}

func isExpectedShutdown(err error) bool {
	return err == nil || errors.Is(err, context.Canceled)
}

func telegramCommandsFromRegistry(registry *skills.Registry) []telegram.BotCommand {
	commands := []telegram.BotCommand{
		{Command: "menu", Description: "Open main menu"},
	}
	if registry == nil {
		return commands
	}
	for _, def := range registry.List() {
		if def.Hidden {
			continue
		}
		desc := def.Description
		if strings.TrimSpace(desc) == "" {
			desc = def.Name
		}
		commands = append(commands, telegram.BotCommand{
			Command:     def.Name,
			Description: desc,
		})
	}
	return commands
}

func resolveConfigPath(flagValue string) string {
	if value := strings.TrimSpace(flagValue); value != "" {
		return value
	}

	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_CONFIG")); value != "" {
		return value
	}

	if info, err := os.Stat(defaultConfigPath); err == nil && !info.IsDir() {
		return defaultConfigPath
	}

	return ""
}
