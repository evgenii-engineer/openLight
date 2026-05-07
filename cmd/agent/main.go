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
	if runtime.VisualWatch != nil {
		runtime.VisualWatch.SetNotifier(visualWatchNotifier{bot: bot})
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
	if cfg.Vision.Enabled || cfg.OCR.Enabled {
		agent.SetImageInbox(core.NewImageInbox(runtime.Registry, core.ImageInboxOptions{
			VisionEnabled: cfg.Vision.Enabled,
			OCREnabled:    cfg.OCR.Enabled,
			DefaultPrompt: cfg.Vision.DefaultPrompt,
		}))
	}

	var watchErrCh chan error
	if cfg.Watch.Enabled && runtime.Watch != nil {
		watchErrCh = make(chan error, 1)
		go func() {
			watchErrCh <- runtime.Watch.Run(runCtx)
		}()
	}

	var visualWatchErrCh chan error
	if cfg.VisualWatch.Enabled && runtime.VisualWatch != nil {
		visualWatchErrCh = make(chan error, 1)
		go func() {
			visualWatchErrCh <- runtime.VisualWatch.Run(runCtx)
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
	if visualWatchErrCh != nil {
		if vwErr := <-visualWatchErrCh; !isExpectedShutdown(vwErr) {
			return vwErr
		}
	}

	logger.Info("agent stopped")
	return nil
}

// visualWatchNotifier adapts the Telegram bot to the visualwatch.Notifier
// interface. It sends the screenshot as a photo with the alert text as the
// caption and falls back to a plain text reply if the file is missing.
type visualWatchNotifier struct {
	bot *telegram.Bot
}

func (n visualWatchNotifier) SendVisualAlert(ctx context.Context, chatID int64, text, screenshotPath string) error {
	if strings.TrimSpace(screenshotPath) == "" {
		return n.bot.SendText(ctx, chatID, text)
	}
	if _, err := os.Stat(screenshotPath); err != nil {
		return n.bot.SendText(ctx, chatID, text)
	}
	return n.bot.SendPhoto(ctx, chatID, screenshotPath, text)
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
