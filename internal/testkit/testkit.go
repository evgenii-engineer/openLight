// Package testkit provides shared helpers for openLight regression tests.
//
// Goals:
//   - Make P0/P1 tests easy to write without spinning up real Telegram,
//     Ollama, Docker, systemd, launchd, or networked services.
//   - Keep helpers small and additive: only what at least one test uses.
//
// All helpers honor t.TempDir() and assume tests run with `go test -race`.
package testkit

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"openlight/internal/config"
	"openlight/internal/storage/sqlite"
)

// SilentLogger returns an slog.Logger that discards every record. Use it for
// components that take a non-nil *slog.Logger but whose output we do not want
// to clutter test logs with.
func SilentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TempSQLitePath returns a sandboxed sqlite path under t.TempDir(). The file
// is removed automatically when the test finishes.
func TempSQLitePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "agent.db")
}

// NewTempRepository opens a fresh sqlite repository in a temporary directory
// and registers a cleanup to close it. Tests get a fully-migrated repository
// without having to manage paths or closers.
func NewTempRepository(t *testing.T) *sqlite.Repository {
	t.Helper()
	repo, err := sqlite.New(context.Background(), TempSQLitePath(t), SilentLogger())
	if err != nil {
		t.Fatalf("testkit: open sqlite repository: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})
	return repo
}

// MinimalValidConfig returns a Config that satisfies (config.Config).Validate
// with the smallest possible surface. Use it as a base and tweak fields you
// care about. The sqlite path lives under t.TempDir() so each test is
// hermetic.
func MinimalValidConfig(t *testing.T) config.Config {
	t.Helper()
	return config.Config{
		Telegram: config.TelegramConfig{
			BotToken:    "test-token",
			APIBaseURL:  "https://api.telegram.org",
			Mode:        "polling",
			PollTimeout: 25 * time.Second,
		},
		Auth: config.AuthConfig{
			AllowedUserIDs: []int64{1},
			AllowedChatIDs: []int64{1},
		},
		Storage: config.StorageConfig{
			SQLitePath: TempSQLitePath(t),
		},
		Files: config.FilesConfig{
			MaxReadBytes: 4096,
			ListLimit:    40,
		},
		Workbench: config.WorkbenchConfig{
			MaxOutputBytes: 8192,
		},
		Services: config.ServicesConfig{
			LogLines:    100,
			MaxLogChars: 3000,
		},
		Watch: config.WatchConfig{
			Enabled:      true,
			PollInterval: 15 * time.Second,
			AskTTL:       10 * time.Minute,
		},
		LLM: config.LLMConfig{
			Provider:           "generic",
			ExecuteThreshold:   0.80,
			ClarifyThreshold:   0.60,
			DecisionInputChars: 160,
			DecisionNumPredict: 48,
		},
		Chat: config.ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     900,
			MaxResponseChars: 400,
		},
		Notes: config.NotesConfig{
			ListLimit: 20,
		},
		Memory: config.MemoryConfig{
			Enabled:   true,
			ListLimit: 20,
		},
		Browser: config.BrowserConfig{
			TimeoutSeconds: 20,
		},
		Agent: config.AgentConfig{
			RequestTimeout: 5 * time.Second,
		},
		Log: config.LogConfig{
			Level: "info",
		},
	}
}
