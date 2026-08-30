package qdrant

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"openlight/internal/memory/vectorstore"
)

// Integration coverage for the real gRPC client. Skipped unless
// OPENLIGHT_QDRANT_URL points at a live instance, so `go test ./...` on a
// machine with no Qdrant stays green:
//
//	OPENLIGHT_QDRANT_URL=http://127.0.0.1:6334 go test ./internal/memory/vectorstore/qdrant/
//
// The fake in the parent package covers ranking semantics; what only a
// real server can check is the wire contract — point-id encoding, payload
// round-tripping, filters, and collection lifecycle.
func liveStore(t *testing.T, collection string) *Store {
	t.Helper()

	url := strings.TrimSpace(os.Getenv("OPENLIGHT_QDRANT_URL"))
	if url == "" {
		t.Skip("set OPENLIGHT_QDRANT_URL to run Qdrant integration tests")
	}

	store, err := New(Options{URL: url, Collection: collection, Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteCollection(context.Background())
		_ = store.Close()
	})

	// A leftover collection from a failed run would make assertions lie.
	_ = store.DeleteCollection(context.Background())
	return store
}

func TestLiveHealthAndCollectionLifecycle(t *testing.T) {
	ctx := context.Background()
	store := liveStore(t, "openlight_it_lifecycle")

	if err := store.Health(ctx); err != nil {
		t.Fatalf("Health: %v", err)
	}

	if err := store.EnsureCollection(ctx, 8); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	// Must be idempotent: every ingest job calls it.
	if err := store.EnsureCollection(ctx, 8); err != nil {
		t.Fatalf("EnsureCollection (second call): %v", err)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Fatalf("fresh collection has %d points", count)
	}
}

func TestLiveUpsertSearchDelete(t *testing.T) {
	ctx := context.Background()
	store := liveStore(t, "openlight_it_points")

	if err := store.EnsureCollection(ctx, 4); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}

	// 32-hex chunk ids, exactly what the service generates.
	near := "0123456789abcdef0123456789abcdef"
	far := "fedcba9876543210fedcba9876543210"

	err := store.Upsert(ctx, []vectorstore.Point{
		{
			ID:     near,
			Vector: []float32{1, 0, 0, 0},
			Payload: map[string]any{
				"chunk_id":    near,
				"source_id":   "src-1",
				"source_type": "documents",
				"title":       "report.pdf",
				"ordinal":     int64(3),
			},
		},
		{
			ID:     far,
			Vector: []float32{0, 0, 0, 1},
			Payload: map[string]any{
				"chunk_id":    far,
				"source_id":   "src-2",
				"source_type": "conversations",
			},
		},
	})
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 2 {
		t.Fatalf("Count = %d, want 2", count)
	}

	hits, err := store.Search(ctx, []float32{1, 0, 0, 0}, 10, vectorstore.Filter{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}

	// The id must survive the hex → UUID → hex round trip, or retrieval
	// could never join a hit back to its chunk row in SQLite.
	if hits[0].ID != near {
		t.Fatalf("top hit id = %q, want %q", hits[0].ID, near)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores not ordered: %v vs %v", hits[0].Score, hits[1].Score)
	}
	if title, _ := hits[0].Payload["title"].(string); title != "report.pdf" {
		t.Fatalf("payload lost the title: %+v", hits[0].Payload)
	}
	if ordinal, ok := hits[0].Payload["ordinal"].(int64); !ok || ordinal != 3 {
		t.Fatalf("payload lost the integer ordinal: %+v", hits[0].Payload)
	}

	// source_type filtering is what SearchOptions.Types compiles to.
	filtered, err := store.Search(ctx, []float32{1, 0, 0, 0}, 10, vectorstore.Filter{Types: []string{"conversations"}})
	if err != nil {
		t.Fatalf("filtered Search: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ID != far {
		t.Fatalf("type filter did not apply: %+v", filtered)
	}

	// Re-upserting the same id replaces rather than duplicates, which is
	// what makes a re-run of an interrupted ingest job idempotent.
	if err := store.Upsert(ctx, []vectorstore.Point{{
		ID: near, Vector: []float32{1, 0, 0, 0},
		Payload: map[string]any{"chunk_id": near, "source_type": "documents", "title": "report-v2.pdf"},
	}}); err != nil {
		t.Fatalf("re-Upsert: %v", err)
	}
	count, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("Count after re-upsert: %v", err)
	}
	if count != 2 {
		t.Fatalf("re-upsert duplicated a point: count = %d", count)
	}

	if err := store.Delete(ctx, []string{far}); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	count, err = store.Count(ctx)
	if err != nil {
		t.Fatalf("Count after delete: %v", err)
	}
	if count != 1 {
		t.Fatalf("Count = %d after deleting one of two", count)
	}
}

func TestLiveCountOnAMissingCollectionIsZeroNotAnError(t *testing.T) {
	ctx := context.Background()
	store := liveStore(t, "openlight_it_absent")

	// `memory status` calls Count before anything has been indexed.
	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count on a missing collection: %v", err)
	}
	if count != 0 {
		t.Fatalf("Count = %d, want 0", count)
	}
}

func TestLiveDeleteCollectionThenRebuild(t *testing.T) {
	ctx := context.Background()
	store := liveStore(t, "openlight_it_rebuild")

	if err := store.EnsureCollection(ctx, 4); err != nil {
		t.Fatalf("EnsureCollection: %v", err)
	}
	id := "aaaabbbbccccddddeeeeffff00001111"
	if err := store.Upsert(ctx, []vectorstore.Point{{
		ID: id, Vector: []float32{1, 0, 0, 0}, Payload: map[string]any{"source_type": "documents"},
	}}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// This is the `memory reindex --all` path.
	if err := store.DeleteCollection(ctx); err != nil {
		t.Fatalf("DeleteCollection: %v", err)
	}
	if err := store.EnsureCollection(ctx, 4); err != nil {
		t.Fatalf("EnsureCollection after drop: %v", err)
	}

	count, err := store.Count(ctx)
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if count != 0 {
		t.Fatalf("rebuilt collection still has %d points", count)
	}
}
