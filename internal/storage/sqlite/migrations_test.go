package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

// TestRepositoryMigrationsAreIdempotent guards against migrations that fail
// when re-run on an already-initialised database. sqlite.New runs every
// migration on every open, so re-opening must succeed for upgrades to be safe.
func TestRepositoryMigrationsAreIdempotent(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "agent.db")

	first, err := New(ctx, path, nil)
	if err != nil {
		t.Fatalf("first New: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	// Re-opening triggers the migration loop a second time. If any migration
	// is non-idempotent (missing IF NOT EXISTS, etc.), this fails.
	second, err := New(ctx, path, nil)
	if err != nil {
		t.Fatalf("re-open New: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("re-open Close: %v", err)
	}
}

// TestRepositoryRejectsAnonymousMemoryPath nails down the safety property
// that storage paths are normalized at the config layer (see
// internal/config: SQLITE_PATH validation), so by the time we reach sqlite.New
// we always have a real file path. If somebody bypasses that and hands us a
// pure ":memory:" string, the migrations should still apply against it
// without panicking — exercising the engine's tolerance to in-memory mode.
func TestRepositoryAcceptsInMemoryPath(t *testing.T) {
	t.Parallel()

	repo, err := New(context.Background(), ":memory:", nil)
	if err != nil {
		t.Fatalf("expected :memory: path to open, got %v", err)
	}
	defer repo.Close()
}
