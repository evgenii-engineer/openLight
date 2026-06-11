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

	"openlight/internal/auth"
	clitransport "openlight/internal/cli"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/logging"
	"openlight/internal/router"
	"openlight/internal/runtime"
	"openlight/internal/telegram"
)

func runCLI(args []string) error {
	fs := flag.NewFlagSet("cli", flag.ContinueOnError)
	configPath := fs.String("config", "", "Path to YAML configuration file")
	execText := fs.String("exec", "", "Run one message and exit")
	smoke := fs.Bool("smoke", false, "Run the built-in smoke suite and print a result table")
	smokeChat := fs.Bool("smoke-chat", false, "Include direct chat skill and plain-text LLM chat smoke checks")
	smokeRouting := fs.Bool("smoke-routing", false, "Include end-to-end LLM fallback smoke checks for built-in skills")
	smokeRestart := fs.Bool("smoke-restart", false, "Include disruptive service restart smoke checks")
	smokeAll := fs.Bool("smoke-all", false, "Enable all smoke checks, including LLM fallback, chat, and service restart")
	userID := fs.Int64("user-id", 0, "Override CLI user id (defaults to first allowed user id)")
	chatID := fs.Int64("chat-id", 0, "Override CLI chat id (defaults to first allowed chat id)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	logger := logging.New(cfg.Log.Level)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	rt, err := runtime.BuildRuntimeWithOptions(ctx, cfg, logger, runtime.BuildOptions{StartBrainServer: false})
	if err != nil {
		return err
	}
	defer runtime.CloseRuntime(rt)

	resolvedUserID := resolveCLIUserID(cfg, *userID)
	resolvedChatID := resolveCLIChatID(cfg, *chatID, resolvedUserID)

	if *smoke || *smokeAll {
		fmt.Fprintln(os.Stdout, "Smoke started. Progress will stream below; the summary table will print at the end.")
		report, smokeErr := clitransport.RunSmoke(ctx, cfg, rt, resolvedUserID, resolvedChatID, clitransport.SmokeOptions{
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
	if rt.Watch != nil {
		rt.Watch.SetNotifier(transport)
	}

	agent := core.NewAgent(
		transport,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.NewWithLogger(rt.Registry, rt.Classifier, logger.With("component", "router")),
		rt.Registry,
		rt.Repository,
		rt.Watch,
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
