package workbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

func TestExecCodeSkillRunsAllowedRuntime(t *testing.T) {
	t.Parallel()

	manager, err := NewLocalManager(t.TempDir(), []string{"sh"}, nil, 4096)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := NewExecCodeSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{
			"runtime": "sh",
			"code":    "printf 'hello\\n'",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Runtime: sh") || !strings.Contains(result.Text, "hello") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestExecCodeSkillReturnsExitCodeAndOutput(t *testing.T) {
	t.Parallel()

	manager, err := NewLocalManager(t.TempDir(), []string{"sh"}, nil, 4096)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := NewExecCodeSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{
			"runtime": "sh",
			"code":    "echo boom >&2\nexit 7",
		},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Exit code: 7") || !strings.Contains(result.Text, "boom") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestExecFileSkillRunsAllowedFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	scriptPath := filepath.Join(root, "backup.sh")
	if err := os.WriteFile(scriptPath, []byte("#!/bin/sh\nprintf 'done\\n'"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager, err := NewLocalManager(filepath.Join(root, "workspace"), nil, []string{scriptPath}, 4096)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := NewExecFileSkill(manager).Execute(context.Background(), skills.Input{
		Args: map[string]string{"path": scriptPath},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Allowed file: "+manager.AllowedFiles()[0]) || !strings.Contains(result.Text, "done") {
		t.Fatalf("unexpected response: %q", result.Text)
	}
}

func TestLocalManagerRejectsUnlistedExecFile(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	allowed := filepath.Join(root, "allowed.sh")
	other := filepath.Join(root, "other.sh")
	if err := os.WriteFile(allowed, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.WriteFile(other, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager, err := NewLocalManager(filepath.Join(root, "workspace"), nil, []string{allowed}, 4096)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	_, err = manager.ExecFile(context.Background(), other)
	if err == nil {
		t.Fatal("expected running unlisted file to fail")
	}
}

func TestWorkspaceCleanSkillRemovesTemporaryFiles(t *testing.T) {
	t.Parallel()

	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "run.py"), []byte("print('hi')"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.Mkdir(filepath.Join(workspace, "nested"), 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	manager, err := NewLocalManager(workspace, []string{"python"}, nil, 4096)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := NewWorkspaceCleanSkill(manager).Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}
	if !strings.Contains(result.Text, "Workspace cleaned: "+manager.Workspace()) {
		t.Fatalf("unexpected response: %q", result.Text)
	}

	entries, err := os.ReadDir(workspace)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected empty workspace, got %d entries", len(entries))
	}
}
