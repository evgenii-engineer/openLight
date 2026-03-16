package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"openlight/internal/config"
	basellm "openlight/internal/llm"
)

func TestIsExpectedShutdown(t *testing.T) {
	t.Parallel()

	if !isExpectedShutdown(nil) {
		t.Fatal("expected nil error to be treated as clean shutdown")
	}

	if !isExpectedShutdown(context.Canceled) {
		t.Fatal("expected context.Canceled to be treated as clean shutdown")
	}

	if isExpectedShutdown(errors.New("wrap: " + context.Canceled.Error())) {
		t.Fatal("plain text error should not be treated as clean shutdown")
	}
}

func TestIsExpectedShutdownWrappedCanceled(t *testing.T) {
	t.Parallel()

	err := fmt.Errorf("poll failed: %w", context.Canceled)
	if !isExpectedShutdown(err) {
		t.Fatalf("expected wrapped context cancellation to be ignored, got %v", err)
	}
}

func TestIsExpectedShutdownRejectsOtherErrors(t *testing.T) {
	t.Parallel()

	if isExpectedShutdown(context.DeadlineExceeded) {
		t.Fatal("did not expect deadline exceeded to be treated as clean shutdown")
	}
}

func TestBuildRegistryRegistersBuiltInModules(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, _, err := buildRegistry(config.Config{
		Files: config.FilesConfig{
			MaxReadBytes: 4096,
			ListLimit:    40,
		},
		Services: config.ServicesConfig{
			Allowed:  []string{"tailscale"},
			LogLines: 50,
		},
		Notes: config.NotesConfig{
			ListLimit: 10,
		},
		Chat: config.ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     200,
			MaxResponseChars: 100,
		},
	}, nil, logger, nil)
	if err != nil {
		t.Fatalf("buildRegistry returned error: %v", err)
	}

	for _, name := range []string{"start", "ping", "status", "service_status", "note_add", "file_read", "skills", "help"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected skill %q to be registered", name)
		}
	}
	if _, ok := registry.Get("chat"); ok {
		t.Fatal("did not expect chat skill without llm provider")
	}
}

func TestBuildRegistryRegistersChatModuleWhenLLMEnabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	provider := basellm.NewHTTPProvider("http://127.0.0.1:1", time.Second, logger)

	registry, _, err := buildRegistry(config.Config{
		Files: config.FilesConfig{
			MaxReadBytes: 4096,
			ListLimit:    40,
		},
		Services: config.ServicesConfig{
			Allowed:  []string{"tailscale"},
			LogLines: 50,
		},
		Notes: config.NotesConfig{
			ListLimit: 10,
		},
		Chat: config.ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     200,
			MaxResponseChars: 100,
		},
	}, nil, logger, provider)
	if err != nil {
		t.Fatalf("buildRegistry returned error: %v", err)
	}

	if _, ok := registry.Get("chat"); !ok {
		t.Fatal("expected chat skill to be registered when llm provider is configured")
	}
}

func TestBuildRegistryRegistersWorkbenchModuleWhenEnabled(t *testing.T) {
	t.Parallel()

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	registry, _, err := buildRegistry(config.Config{
		Files: config.FilesConfig{
			MaxReadBytes: 4096,
			ListLimit:    40,
		},
		Workbench: config.WorkbenchConfig{
			Enabled:         true,
			WorkspaceDir:    t.TempDir(),
			AllowedRuntimes: []string{"sh"},
			AllowedFiles:    []string{"/tmp/openlight-script.sh"},
			MaxOutputBytes:  4096,
		},
		Services: config.ServicesConfig{
			Allowed:  []string{"tailscale"},
			LogLines: 50,
		},
		Notes: config.NotesConfig{
			ListLimit: 10,
		},
		Chat: config.ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     200,
			MaxResponseChars: 100,
		},
	}, nil, logger, nil)
	if err != nil {
		t.Fatalf("buildRegistry returned error: %v", err)
	}

	for _, name := range []string{"exec_code", "exec_file", "workspace_clean"} {
		if _, ok := registry.Get(name); !ok {
			t.Fatalf("expected workbench skill %q to be registered", name)
		}
	}
}
