package files

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

func TestLocalManagerRejectsParentTraversal(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	root := filepath.Join(parent, "allowed")
	if err := os.Mkdir(root, 0o755); err != nil {
		t.Fatalf("mkdir root: %v", err)
	}

	// File exists outside the root and is a real, readable target. Without
	// path-traversal protection, joining root with ".." would expose it.
	outside := filepath.Join(parent, "secret.txt")
	if err := os.WriteFile(outside, []byte("secret body"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager := newTestManager(t, true, []string{root}, false, true)

	traversal := filepath.Join(root, "..", "secret.txt")
	_, err := manager.Read(context.Background(), traversal)
	if err == nil {
		t.Fatalf("expected parent-traversal read to be rejected")
	}
	if !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected ErrAccessDenied for path outside allowed roots, got %v", err)
	}
}

func TestLocalManagerEnforcesMaxReadBytes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "long.txt")
	body := strings.Repeat("a", 256)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}

	manager, err := NewLocalManager(true, []string{root}, 64, 40, false, false, true)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := manager.Read(context.Background(), path)
	if err != nil {
		t.Fatalf("Read returned error: %v", err)
	}
	if !result.Truncated {
		t.Fatalf("expected Truncated flag to be set when content exceeds max_read_bytes")
	}
	if len(result.Content) != 64 {
		t.Fatalf("expected content to be truncated to max_read_bytes (64), got %d bytes", len(result.Content))
	}
}

func TestLocalManagerEnforcesListLimit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for i := 0; i < 10; i++ {
		name := filepath.Join(root, fmt.Sprintf("entry-%02d.txt", i))
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}

	manager, err := NewLocalManager(true, []string{root}, 4096, 3, false, true, true)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	result, err := manager.List(context.Background(), root)
	if err != nil {
		t.Fatalf("List returned error: %v", err)
	}
	if len(result.Entries) != 3 {
		t.Fatalf("expected 3 entries (list_limit), got %d", len(result.Entries))
	}
	if !result.Truncated {
		t.Fatalf("expected Truncated flag when list_limit is hit")
	}
}

func TestLocalManagerDisabledReturnsAccessDenied(t *testing.T) {
	t.Parallel()

	manager, err := NewLocalManager(false, nil, 4096, 40, false, true, true)
	if err != nil {
		t.Fatalf("NewLocalManager returned error: %v", err)
	}

	_, err = manager.List(context.Background(), "")
	if err == nil {
		t.Fatalf("expected disabled manager to return error")
	}
	if !errors.Is(err, skills.ErrUnavailable) && !errors.Is(err, skills.ErrAccessDenied) {
		t.Fatalf("expected ErrUnavailable or ErrAccessDenied, got %v", err)
	}
}
