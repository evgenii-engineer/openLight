package vectorstore

import (
	"context"
	"errors"
	"testing"
)

func TestFakeSearchRanksByCosineSimilarity(t *testing.T) {
	ctx := context.Background()
	store := NewFake()

	requireNoErr(t, store.EnsureCollection(ctx, 3), "ensure collection")
	requireNoErr(t, store.Upsert(ctx, []Point{
		{ID: "near", Vector: []float32{1, 0, 0}, Payload: map[string]any{"source_type": "documents"}},
		{ID: "far", Vector: []float32{0, 0, 1}, Payload: map[string]any{"source_type": "documents"}},
	}), "upsert")

	hits, err := store.Search(ctx, []float32{1, 0, 0}, 10, Filter{})
	requireNoErr(t, err, "search")

	if len(hits) != 2 {
		t.Fatalf("got %d hits, want 2", len(hits))
	}
	if hits[0].ID != "near" {
		t.Fatalf("ranking is wrong: %+v", hits)
	}
	if hits[0].Score <= hits[1].Score {
		t.Fatalf("scores are not ordered: %v vs %v", hits[0].Score, hits[1].Score)
	}
}

func TestFakeSearchAppliesTypeFilterAndLimit(t *testing.T) {
	ctx := context.Background()
	store := NewFake()

	requireNoErr(t, store.Upsert(ctx, []Point{
		{ID: "a", Vector: []float32{1, 0}, Payload: map[string]any{"source_type": "documents"}},
		{ID: "b", Vector: []float32{1, 0}, Payload: map[string]any{"source_type": "conversations"}},
		{ID: "c", Vector: []float32{1, 0}, Payload: map[string]any{"source_type": "documents"}},
	}), "upsert")

	hits, err := store.Search(ctx, []float32{1, 0}, 10, Filter{Types: []string{"conversations"}})
	requireNoErr(t, err, "filtered search")
	if len(hits) != 1 || hits[0].ID != "b" {
		t.Fatalf("filter not applied: %+v", hits)
	}

	hits, err = store.Search(ctx, []float32{1, 0}, 2, Filter{})
	requireNoErr(t, err, "limited search")
	if len(hits) != 2 {
		t.Fatalf("limit not applied: %d hits", len(hits))
	}
}

func TestFakeUpsertReplacesByID(t *testing.T) {
	ctx := context.Background()
	store := NewFake()

	requireNoErr(t, store.Upsert(ctx, []Point{{ID: "x", Vector: []float32{1, 0}}}), "first")
	requireNoErr(t, store.Upsert(ctx, []Point{{ID: "x", Vector: []float32{0, 1}}}), "second")

	// Re-ingesting a source must overwrite its points, not accumulate
	// stale copies alongside the new ones.
	if store.Len() != 1 {
		t.Fatalf("upsert duplicated a point: %d stored", store.Len())
	}
}

func TestFakeDeleteAndDropCollection(t *testing.T) {
	ctx := context.Background()
	store := NewFake()

	requireNoErr(t, store.EnsureCollection(ctx, 2), "ensure")
	requireNoErr(t, store.Upsert(ctx, []Point{
		{ID: "a", Vector: []float32{1, 0}},
		{ID: "b", Vector: []float32{0, 1}},
	}), "upsert")

	requireNoErr(t, store.Delete(ctx, []string{"a"}), "delete")
	if store.Len() != 1 {
		t.Fatalf("delete removed %d points", 2-store.Len())
	}

	requireNoErr(t, store.DeleteCollection(ctx), "drop")
	if store.Len() != 0 || store.Created() {
		t.Fatal("dropping the collection should clear both points and creation state")
	}
}

func TestFakeReportsUnavailableWhenDown(t *testing.T) {
	ctx := context.Background()
	store := NewFake()
	store.Down = true

	if err := store.EnsureCollection(ctx, 2); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("EnsureCollection: %v", err)
	}
	if err := store.Upsert(ctx, []Point{{ID: "a", Vector: []float32{1}}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Upsert: %v", err)
	}
	if _, err := store.Search(ctx, []float32{1}, 1, Filter{}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Search: %v", err)
	}
	if err := store.Health(ctx); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Health: %v", err)
	}
}

func TestNoopFailsWritesSoWorkIsNeverMarkedDone(t *testing.T) {
	ctx := context.Background()
	var store Store = Noop{}

	// The no-op store must report failure on writes: silently accepting
	// them would let the queue mark a source completed that was never
	// actually indexed.
	if err := store.Upsert(ctx, []Point{{ID: "a"}}); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("Upsert on Noop returned %v, want ErrUnavailable", err)
	}
	if err := store.EnsureCollection(ctx, 8); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("EnsureCollection on Noop returned %v", err)
	}

	// Reads are quiet: retrieval just finds nothing.
	hits, err := store.Search(ctx, []float32{1}, 5, Filter{})
	if err != nil || len(hits) != 0 {
		t.Fatalf("Search on Noop = %v / %d hits", err, len(hits))
	}
}

func TestCosineHandlesDegenerateInput(t *testing.T) {
	if got := cosine(nil, []float32{1, 2}); got != 0 {
		t.Fatalf("cosine(nil, v) = %v, want 0", got)
	}
	if got := cosine([]float32{1, 2}, []float32{1, 2, 3}); got != 0 {
		t.Fatalf("mismatched lengths should score 0, got %v", got)
	}
	if got := cosine([]float32{0, 0}, []float32{1, 1}); got != 0 {
		t.Fatalf("a zero vector should score 0, got %v", got)
	}
}

func requireNoErr(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}
