package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	qdrantstore "openlight/internal/memory/vectorstore/qdrant"
)

// End-to-end coverage against a real Qdrant. Skipped unless
// OPENLIGHT_QDRANT_URL is set, so the default `go test ./...` needs no
// containers:
//
//	OPENLIGHT_QDRANT_URL=http://127.0.0.1:6334 go test ./internal/memory/
//
// The fake store covers ranking logic; this covers the seam the fake
// cannot: that a chunk written through the gRPC client comes back joined
// to its SQLite row, with provenance intact.
func newLiveHarness(t *testing.T, collection string) *harness {
	t.Helper()

	url := strings.TrimSpace(os.Getenv("OPENLIGHT_QDRANT_URL"))
	if url == "" {
		t.Skip("set OPENLIGHT_QDRANT_URL to run Qdrant integration tests")
	}

	h := newHarness(t, func(o *harnessOptions) {
		o.retrieval = RetrievalOptions{Candidates: 8, MaxResults: 3, MaxContextTokens: 500}
	})

	store, err := qdrantstore.New(qdrantstore.Options{
		URL: url, Collection: collection, Timeout: 15 * time.Second,
	})
	if err != nil {
		t.Fatalf("qdrant: %v", err)
	}
	t.Cleanup(func() {
		_ = store.DeleteCollection(context.Background())
		_ = store.Close()
	})
	_ = store.DeleteCollection(context.Background())

	h.service.deps.Vectors = store
	return h
}

func TestLiveIngestSearchRoundTrip(t *testing.T) {
	ctx := context.Background()
	h := newLiveHarness(t, "openlight_it_e2e")

	h.mustIngestText(ctx, "hardware.md",
		"# Storage\n\nThe raspberry pi has a 1 TB SSD attached over USB 3.\n")
	h.mustIngestText(ctx, "cooking.md",
		"# Risotto\n\nArborio rice, hot stock, and constant stirring.\n")
	h.drain(ctx)

	sources, err := h.store.ListSources(ctx, StatusCompleted, 10)
	requireNoError(t, err, "list completed")
	if len(sources) != 2 {
		t.Fatalf("completed %d of 2 sources", len(sources))
	}

	results, err := h.service.Search(ctx, "raspberry SSD storage", SearchOptions{Candidates: 8, MaxResults: 3})
	requireNoError(t, err, "search")
	if len(results) == 0 {
		t.Fatal("no matches from a live Qdrant")
	}

	top := results[0]
	if !strings.Contains(strings.ToLower(top.Text), "ssd") {
		t.Fatalf("top match is wrong: %q", top.Text)
	}
	// The join back to SQLite is the whole point: a chunk id that did
	// not survive the hex→UUID→hex round trip would drop every result.
	if top.ChunkID == "" || top.SourceID == "" || top.Path == "" {
		t.Fatalf("provenance lost through the real store: %+v", top)
	}
	if top.SourceType != TypeDocument {
		t.Fatalf("source type = %q", top.SourceType)
	}

	built := h.service.ContextFor(ctx, 1, "какой диск подключен к raspberry?")
	if built.Empty() {
		t.Fatal("ContextFor returned nothing against a live store")
	}
	if !strings.Contains(built.Block, "SSD") {
		t.Fatalf("context missing the answer:\n%s", built.Block)
	}
	if built.Tokens > 500 {
		t.Fatalf("context is %d tokens, over budget", built.Tokens)
	}
}

func TestLiveReindexRebuildsFromRawStorage(t *testing.T) {
	ctx := context.Background()
	h := newLiveHarness(t, "openlight_it_reindex")

	h.mustIngestText(ctx, "notes.md", "The mac mini serves embeddings over ollama.")
	h.drain(ctx)

	before, err := h.service.deps.Vectors.Count(ctx)
	requireNoError(t, err, "count")
	if before == 0 {
		t.Fatal("nothing indexed")
	}

	// The "someone wiped the Qdrant volume" scenario.
	requireNoError(t, h.service.deps.Vectors.DeleteCollection(ctx), "drop collection")

	queued, err := h.service.Reindex(ctx, ReindexOptions{All: true})
	requireNoError(t, err, "reindex")
	if queued != 1 {
		t.Fatalf("queued %d jobs, want 1", queued)
	}
	h.drain(ctx)

	after, err := h.service.deps.Vectors.Count(ctx)
	requireNoError(t, err, "count after reindex")
	if after != before {
		t.Fatalf("rebuilt %d points, want %d", after, before)
	}

	results, err := h.service.Search(ctx, "mac mini ollama embeddings", SearchOptions{})
	requireNoError(t, err, "search after reindex")
	if len(results) == 0 {
		t.Fatal("search still empty after reindex")
	}
}
