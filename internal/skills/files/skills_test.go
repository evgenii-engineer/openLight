package files

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

func TestListSkillShowsAllowedRoots(t *testing.T) {
	t.Parallel()

	manager, err := NewLocalManager([]string{"/tmp/a", "/tmp/b"}, 4096, 40)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

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

	manager, err := NewLocalManager([]string{root}, 4096, 40)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

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

func TestWriteSkillCreatesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "created.txt")

	manager, err := NewLocalManager([]string{root}, 4096, 40)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

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

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "hello world" {
		t.Fatalf("unexpected file content: %q", string(content))
	}
}

func TestReplaceSkillUpdatesFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "config.txt")
	if err := os.WriteFile(path, []byte("port=8080\nport=8080"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager, err := NewLocalManager([]string{root}, 4096, 40)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := NewReplaceSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{
			"path":    path,
			"find":    "8080",
			"replace": "8081",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Replaced 2 occurrence(s)") {
		t.Fatalf("unexpected response: %q", result.Text)
	}

	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if string(content) != "port=8081\nport=8081" {
		t.Fatalf("unexpected file content: %q", string(content))
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

	manager, err := NewLocalManager([]string{root}, 4096, 40)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	_, err = manager.Read(context.Background(), path)
	if err == nil {
		t.Fatal("expected reading outside allowed roots to fail")
	}
}
