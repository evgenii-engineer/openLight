package memory

import (
	"context"
	"strings"
	"testing"
)

func TestSearchReturnsRelevantChunksWithProvenance(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "hardware", "The raspberry pi has a 1 TB SSD attached over USB 3.")
	h.mustIngestText(ctx, "cooking", "A risotto needs arborio rice, stock, and patience.")
	h.drain(ctx)

	results, err := h.service.Search(ctx, "raspberry SSD storage", SearchOptions{Candidates: 8, MaxResults: 5})
	requireNoError(t, err, "search")

	if len(results) == 0 {
		t.Fatal("expected at least one match")
	}
	top := results[0]
	if !strings.Contains(strings.ToLower(top.Text), "ssd") {
		t.Fatalf("top match is not the hardware note: %q", top.Text)
	}
	// Provenance must be complete enough to answer "откуда ты это
	// знаешь?" later without re-running the search.
	if top.SourceID == "" || top.ChunkID == "" {
		t.Fatalf("result is missing identifiers: %+v", top)
	}
	if top.SourceType != TypeDocument {
		t.Fatalf("source type = %q, want %q", top.SourceType, TypeDocument)
	}
	if top.Path == "" {
		t.Fatal("result is missing the raw archive path")
	}
	if top.Timestamp.IsZero() {
		t.Fatal("result is missing a timestamp")
	}
}

func TestSearchSkipsIndexEntriesWithNoLocalChunk(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "note", "a note that will be indexed then orphaned")
	h.drain(ctx)

	// Simulate a partial reindex: the vector index still holds points
	// whose chunk rows are gone. Search must skip them rather than
	// return empty results or crash.
	source, err := h.store.ListSources(ctx, "", 1)
	requireNoError(t, err, "list sources")
	requireNoError(t, h.store.ReplaceChunks(ctx, source[0].ID, nil), "clear chunks")

	results, err := h.service.Search(ctx, "note", SearchOptions{Candidates: 8, MaxResults: 5})
	requireNoError(t, err, "search")
	if len(results) != 0 {
		t.Fatalf("expected orphaned points to be skipped, got %d results", len(results))
	}
}

func TestContextForSkipsRetrievalForCommands(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "note", "the kitchen light is on GPIO 18")
	h.drain(ctx)

	before := h.embedder.callCount()
	// "включи свет" must never pay for an embedding round trip to the
	// Mac mini — that is the whole reason the gate exists.
	if built := h.service.ContextFor(ctx, 1, "включи свет"); !built.Empty() {
		t.Fatalf("command-like input triggered retrieval: %q", built.Block)
	}
	if got := h.embedder.callCount(); got != before {
		t.Fatalf("gate let %d embedding call(s) through", got-before)
	}
}

func TestContextForRetrievesForRecallQuestions(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.retrieval = RetrievalOptions{Candidates: 8, MaxResults: 3, MaxContextTokens: 400}
	})

	h.mustIngestText(ctx, "hardware", "raspberry storage: a 1 TB SSD is attached over USB 3")
	h.drain(ctx)

	built := h.service.ContextFor(ctx, 1, "какой диск подключен к raspberry?")

	if built.Empty() {
		t.Fatal("a recall question should retrieve something")
	}
	if !strings.Contains(built.Block, "SSD") {
		t.Fatalf("retrieved block is missing the answer:\n%s", built.Block)
	}
	if built.Tokens > 400 {
		t.Fatalf("block is %d tokens, over the configured budget", built.Tokens)
	}
}

func TestContextForIncludesCurrentFactsAndNotSupersededOnes(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, _, err := RememberFact(ctx, h.store, Fact{Subject: "raspberry", Predicate: "storage", Value: "1 TB SSD"})
	requireNoError(t, err, "first fact")
	_, _, err = RememberFact(ctx, h.store, Fact{Subject: "raspberry", Predicate: "storage", Value: "4 TB SSD"})
	requireNoError(t, err, "second fact")

	built := h.service.ContextFor(ctx, 1, "какой диск подключен к raspberry?")

	if built.Empty() {
		t.Fatal("expected structured facts to be retrieved")
	}
	if !strings.Contains(built.Block, "4 TB SSD") {
		t.Fatalf("current fact missing:\n%s", built.Block)
	}
	if strings.Contains(built.Block, "1 TB SSD") {
		t.Fatalf("superseded fact leaked into the prompt:\n%s", built.Block)
	}
}

func TestContextForFallsBackToFactsWhenVectorSearchIsDown(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, _, err := RememberFact(ctx, h.store, Fact{Subject: "raspberry", Predicate: "storage", Value: "4 TB SSD"})
	requireNoError(t, err, "fact")

	h.vectors.Down = true

	built := h.service.ContextFor(ctx, 1, "какой диск подключен к raspberry?")

	// Degraded, not broken: structured memory still answers.
	if built.Empty() {
		t.Fatal("structured facts should survive a vector-store outage")
	}
	if !strings.Contains(built.Block, "4 TB SSD") {
		t.Fatalf("fact missing from degraded context:\n%s", built.Block)
	}
}

func TestRecallMatchesOnContentWords(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, _, err := RememberFact(ctx, h.store, Fact{Subject: "raspberry", Predicate: "storage", Value: "1 TB SSD"})
	requireNoError(t, err, "fact one")
	_, _, err = RememberFact(ctx, h.store, Fact{Subject: "macmini", Predicate: "role", Value: "brain node"})
	requireNoError(t, err, "fact two")

	facts, err := h.service.Recall(ctx, "raspberry", 10)
	requireNoError(t, err, "recall")

	if len(facts) != 1 || facts[0].Subject != "raspberry" {
		t.Fatalf("recall returned %+v", facts)
	}
}
