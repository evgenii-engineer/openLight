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

	"openlight/internal/app"
	"openlight/internal/auth"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/logging"
	"openlight/internal/router"
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

	runtime, err := app.BuildRuntime(ctx, cfg, logger)
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
	agent := core.NewAgent(
		bot,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.NewWithLogger(runtime.Registry, runtime.Classifier, logger.With("component", "router")),
		runtime.Registry,
		runtime.Repository,
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
