package memory

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"openlight/internal/memory/vectorstore"
)

func TestQdrantUnavailableKeepsDataAndRecovers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.vectors.Down = true
	source := h.mustIngestText(ctx, "doc", "content that arrives while qdrant is restarting")

	// The archive is written before anything touches the vector store,
	// so the file survives regardless.
	if _, err := os.Stat(source.RawPath); err != nil {
		t.Fatalf("raw file missing during outage: %v", err)
	}

	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Status != JobPending {
		t.Fatalf("job did not survive the outage: %+v", jobs)
	}

	stored, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if stored.Status == StatusCompleted {
		t.Fatal("source was marked completed even though indexing failed")
	}

	// Qdrant comes back.
	h.vectors.Down = false
	h.forceDue(ctx)
	h.drain(ctx)

	recovered, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if recovered.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed after recovery", recovered.Status)
	}
	if h.vectors.Len() == 0 {
		t.Fatal("nothing indexed after recovery")
	}
}

func TestSearchDegradesQuietlyWhenBackendsAreDown(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "doc", "indexed while everything was healthy")
	h.drain(ctx)

	h.vectors.Down = true
	_, err := h.service.Search(ctx, "anything", SearchOptions{})
	requireErrorIs(t, err, vectorstore.ErrUnavailable, "search with vector store down")

	h.vectors.Down = false
	h.embedder.setDown(true)
	_, err = h.service.Search(ctx, "anything", SearchOptions{})
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "search with embeddings down")

	// The read path surfaces errors to its caller, but the agent-facing
	// entry point must swallow them and answer without memory.
	if built := h.service.ContextFor(ctx, 1, "помнишь что мы обсуждали?"); !built.Empty() {
		t.Fatalf("expected an empty context while degraded, got %q", built.Block)
	}
}

func TestStatusReportsDegradedBackends(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "doc", "some content")
	h.drain(ctx)

	healthy := h.service.Status(ctx)
	if !healthy.Enabled {
		t.Fatal("status should report the subsystem as enabled")
	}
	if !healthy.VectorOnline || !healthy.EmbeddingsOnline {
		t.Fatalf("expected both backends online: %+v", healthy)
	}
	if healthy.Sources != 1 || healthy.Chunks == 0 {
		t.Fatalf("counters look wrong: %+v", healthy)
	}

	h.vectors.Down = true
	h.embedder.setDown(true)
	// Force the cached probe to refresh.
	h.service.healthAt = h.service.healthAt.Add(-time.Hour)

	degraded := h.service.Status(ctx)
	if degraded.VectorOnline || degraded.EmbeddingsOnline {
		t.Fatalf("expected both backends offline: %+v", degraded)
	}
	if degraded.VectorError == "" || degraded.EmbeddingsError == "" {
		t.Fatal("degraded status should carry the reason")
	}
	// Counters still come from SQLite, which is local and unaffected.
	if degraded.Sources != 1 {
		t.Fatalf("sources = %d, want 1 even while degraded", degraded.Sources)
	}
}

func TestNilServiceIsAFullyInertMemory(t *testing.T) {
	ctx := context.Background()
	var service *Service

	// Every entry point must tolerate the disabled case so callers never
	// have to guard, and so memory.rag.enabled=false really is a no-op.
	if _, err := service.Ingest(ctx, Item{Text: "x"}); err != nil {
		t.Fatalf("Ingest on nil service: %v", err)
	}
	if results, err := service.Search(ctx, "x", SearchOptions{}); err != nil || results != nil {
		t.Fatalf("Search on nil service: %v / %+v", err, results)
	}
	if built := service.ContextFor(ctx, 1, "помнишь?"); !built.Empty() {
		t.Fatal("ContextFor on nil service returned content")
	}
	if status := service.Status(ctx); status.Enabled {
		t.Fatal("Status on nil service should report disabled")
	}
	service.RecordTurn(1, "user", "hello")
	if err := service.Close(); err != nil {
		t.Fatalf("Close on nil service: %v", err)
	}
}

func TestUnsupportedMIMEDoesNotBlockLaterJobs(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, err := h.service.Ingest(ctx, Item{
		Type: TypeDocument, Title: "bad", Text: "binary-ish",
		MIMEType: "application/x-tar", Filename: "bundle.tar",
	})
	requireNoError(t, err, "ingest unsupported")
	good := h.mustIngestText(ctx, "good", "a perfectly indexable note")

	h.drain(ctx)

	// One bad file must not stall the worker for everything behind it.
	stored, err := h.store.Source(ctx, good.ID)
	requireNoError(t, err, "reload good source")
	if stored.Status != StatusCompleted {
		t.Fatalf("good source status = %q; a bad neighbour blocked the queue", stored.Status)
	}
}

func TestMalformedPDFIsParkedWithAnActionableReason(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, err := h.service.Ingest(ctx, Item{
		Type: TypeDocument, Title: "broken", Text: "%PDF-1.4\nnot really a pdf at all",
		MIMEType: "application/pdf", Filename: "broken.pdf",
	})
	requireNoError(t, err, "ingest")

	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Status != JobFailed {
		t.Fatalf("expected the malformed pdf parked, got %+v", jobs)
	}
	if !strings.Contains(strings.ToLower(jobs[0].LastError), "pdf") {
		t.Fatalf("parked reason is not actionable: %q", jobs[0].LastError)
	}
}
