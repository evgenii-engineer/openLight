package files

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

func newTestManager(t *testing.T, enabled bool, roots []string, allowWrite bool, redactSecrets bool) *LocalManager {
	t.Helper()

	manager, err := NewLocalManager(enabled, roots, 4096, 40, allowWrite, redactSecrets, false)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}
	return manager
}

func TestListSkillShowsAllowedRoots(t *testing.T) {
	t.Parallel()

	manager := newTestManager(t, true, []string{"/tmp/a", "/tmp/b"}, false, true)

	result, err := NewListSkill(manager).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Allowed file roots:") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestReadSkillReadsWhitelistedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)

	result, err := NewReadSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"path": path},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "hello") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestWriteSkillRespectsAllowWrite(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "created.txt")
	manager := newTestManager(t, true, []string{root}, false, true)

	_, err := NewWriteSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{
			"path":    path,
			"content": "hello world",
		},
	})
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected write to be denied, got %v", err)
	}
}

func TestWriteSkillCreatesFileWhenEnabled(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "created.txt")
	manager := newTestManager(t, true, []string{root}, true, true)

	result, err := NewWriteSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{
			"path":    path,
			"content": "hello world",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Created file:") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestSearchSkillFindsMatches(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "config.txt")
	if err := os.WriteFile(path, []byte("OPENAI_API_KEY=sk-abc1234567890"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)
	result, err := NewSearchSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"pattern": "OPENAI_API_KEY"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "[redacted openai key]") {
		t.Fatalf("expected redacted secret in search results, got %q", result.Text)
	}
}

func TestStatSkillReportsMetadata(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)
	result, err := NewStatSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"path": path},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "File:") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestLocalManagerRejectsPathOutsideAllowedRoots(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	other := t.TempDir()
	path := filepath.Join(other, "secret.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)

	_, err := manager.Read(context.Background(), path)
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected reading outside allowed roots to fail, got %v", err)
	}
}

func TestLocalManagerBlocksSensitiveFiles(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".env")
	if err := os.WriteFile(path, []byte("TOKEN=abc"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)
	_, err := manager.Read(context.Background(), path)
	if err == nil || !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected sensitive file read to be denied, got %v", err)
	}
}

func TestReadSkillRedactsSecrets(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "config.env")
	content := strings.Join([]string{
		"telegram=123456:ABCDEFGHIJKLMNOPQRSTUVWXYZ",
		"openai=sk-abc1234567890",
		"github=ghp_abcdefghijklmnopqrstuvwxyz123456",
		"password=supersecret",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager, err := NewLocalManager(true, []string{root}, 4096, 40, false, true, true)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := manager.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if strings.Contains(result.Content, "supersecret") || strings.Contains(result.Content, "ghp_") {
		t.Fatalf("expected secrets to be redacted, got %q", result.Content)
	}
}
