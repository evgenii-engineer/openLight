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

	"openlight/internal/app"
	"openlight/internal/auth"
	clitransport "openlight/internal/cli"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/logging"
	"openlight/internal/router"
	"openlight/internal/telegram"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "cli failed: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	configPath := flag.String("config", "", "Path to YAML configuration file")
	execText := flag.String("exec", "", "Run one message and exit")
	smoke := flag.Bool("smoke", false, "Run the built-in smoke suite and print a result table")
	smokeChat := flag.Bool("smoke-chat", false, "Include direct chat skill and plain-text LLM chat smoke checks")
	smokeRouting := flag.Bool("smoke-routing", false, "Include end-to-end LLM fallback smoke checks for built-in skills")
	smokeRestart := flag.Bool("smoke-restart", false, "Include disruptive service restart smoke checks")
	smokeAll := flag.Bool("smoke-all", false, "Enable all smoke checks, including LLM fallback, chat, and service restart")
	userID := flag.Int64("user-id", 0, "Override CLI user id (defaults to first allowed user id)")
	chatID := flag.Int64("chat-id", 0, "Override CLI chat id (defaults to first allowed chat id)")
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

	resolvedUserID := resolveCLIUserID(cfg, *userID)
	resolvedChatID := resolveCLIChatID(cfg, *chatID, resolvedUserID)

	if *smoke || *smokeAll {
		fmt.Fprintln(os.Stdout, "Smoke started. Progress will stream below; the summary table will print at the end.")
		report, smokeErr := clitransport.RunSmoke(ctx, cfg, runtime, resolvedUserID, resolvedChatID, clitransport.SmokeOptions{
			IncludeChat:    *smokeChat || *smokeAll,
			IncludeRouting: *smokeRouting || *smokeAll,
			IncludeRestart: *smokeRestart || *smokeAll,
			ProgressWriter: os.Stdout,
		})
		fmt.Fprintln(os.Stdout)
		fmt.Fprintln(os.Stdout, report.RenderTable())
		return smokeErr
	}

	transport := clitransport.NewTransport(clitransport.Options{
		In:     os.Stdin,
		Out:    os.Stdout,
		UserID: resolvedUserID,
		ChatID: resolvedChatID,
	})
	if runtime.Watch != nil {
		runtime.Watch.SetNotifier(transport)
	}

	agent := core.NewAgent(
		transport,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.NewWithLogger(runtime.Registry, runtime.Classifier, logger.With("component", "router")),
		runtime.Registry,
		runtime.Repository,
		runtime.Watch,
		logger.With("component", "agent"),
		cfg.Agent.RequestTimeout,
	)

	if strings.TrimSpace(*execText) != "" {
		return agent.HandleMessage(ctx, telegram.IncomingMessage{
			MessageID: 1,
			UserID:    resolvedUserID,
			ChatID:    resolvedChatID,
			Text:      *execText,
		})
	}

	logger.Info("cli starting", slog.Int64("user_id", resolvedUserID), slog.Int64("chat_id", resolvedChatID))
	err = agent.Run(ctx)
	if err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	logger.Info("cli stopped")
	return nil
}

func resolveCLIUserID(cfg config.Config, override int64) int64 {
	if override != 0 {
		return override
	}
	if len(cfg.Auth.AllowedUserIDs) > 0 {
		return cfg.Auth.AllowedUserIDs[0]
	}
	if len(cfg.Auth.AllowedChatIDs) > 0 {
		return cfg.Auth.AllowedChatIDs[0]
	}
	return 1
}

func resolveCLIChatID(cfg config.Config, override int64, fallbackUserID int64) int64 {
	if override != 0 {
		return override
	}
	if len(cfg.Auth.AllowedChatIDs) > 0 {
		return cfg.Auth.AllowedChatIDs[0]
	}
	if len(cfg.Auth.AllowedUserIDs) > 0 {
		return cfg.Auth.AllowedUserIDs[0]
	}
	return fallbackUserID
}
