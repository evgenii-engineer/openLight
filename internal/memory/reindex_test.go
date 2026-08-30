package memory

import (
	"context"
	"strings"
	"testing"
)

func TestReindexAllRebuildsTheIndexFromRawStorage(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.mustIngestText(ctx, "one", "raspberry storage notes about the attached SSD")
	h.mustIngestText(ctx, "two", "mac mini runs ollama and serves embeddings")
	h.drain(ctx)

	pointsBefore := h.vectors.Len()
	if pointsBefore == 0 {
		t.Fatal("nothing was indexed to begin with")
	}

	// Scenario: the Qdrant volume was wiped. RAW files and SQLite are
	// intact, which is exactly what reindex is supposed to rebuild from.
	requireNoError(t, h.vectors.DeleteCollection(ctx), "drop collection")
	if h.vectors.Len() != 0 {
		t.Fatal("collection was not dropped")
	}
	if results, err := h.service.Search(ctx, "raspberry SSD", SearchOptions{}); err != nil || len(results) != 0 {
		t.Fatalf("search should find nothing after the wipe: %v / %d", err, len(results))
	}

	queued, err := h.service.Reindex(ctx, ReindexOptions{All: true})
	requireNoError(t, err, "reindex")
	if queued != 2 {
		t.Fatalf("queued %d jobs, want 2", queued)
	}

	h.drain(ctx)

	if h.vectors.Len() != pointsBefore {
		t.Fatalf("rebuilt %d points, want %d", h.vectors.Len(), pointsBefore)
	}
	results, err := h.service.Search(ctx, "raspberry SSD storage", SearchOptions{})
	requireNoError(t, err, "search after reindex")
	if len(results) == 0 {
		t.Fatal("search is still empty after reindex")
	}
	if !strings.Contains(strings.ToLower(results[0].Text), "ssd") {
		t.Fatalf("reindexed content looks wrong: %q", results[0].Text)
	}
}

func TestReindexIsResumableBecauseItGoesThroughTheQueue(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	for i := 0; i < 3; i++ {
		h.mustIngestText(ctx, "doc", "distinct content number "+string(rune('a'+i)))
	}
	h.drain(ctx)

	requireNoError(t, h.vectors.DeleteCollection(ctx), "drop collection")
	_, err := h.service.Reindex(ctx, ReindexOptions{All: true})
	requireNoError(t, err, "reindex")

	// Process a single job, then "restart" mid-pass.
	if !h.service.processNext(ctx, silentLogger()) {
		t.Fatal("expected work to be available")
	}
	reclaimed, err := h.store.ReclaimRunningJobs(ctx)
	requireNoError(t, err, "reclaim")
	_ = reclaimed

	remaining, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(remaining) != 2 {
		t.Fatalf("expected 2 jobs still queued after the partial pass, got %d", len(remaining))
	}

	// Resume finishes the rest without redoing the completed one.
	h.drain(ctx)
	if left, _ := h.store.ListJobs(ctx, 10); len(left) != 0 {
		t.Fatalf("queue not drained: %+v", left)
	}
}

func TestReindexSingleSourceTouchesOnlyThatSource(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	target := h.mustIngestText(ctx, "one", "the source that will be reindexed")
	h.mustIngestText(ctx, "two", "an unrelated source that must be left alone")
	h.drain(ctx)

	queued, err := h.service.Reindex(ctx, ReindexOptions{SourceID: target.ID})
	requireNoError(t, err, "reindex source")
	if queued != 1 {
		t.Fatalf("queued %d jobs, want 1", queued)
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].SourceID != target.ID {
		t.Fatalf("wrong job queued: %+v", jobs)
	}
}

func TestReindexFailedRequeuesParkedSources(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, err := h.service.Ingest(ctx, Item{
		Type: TypeDocument, Title: "bad", Text: "x",
		MIMEType: "application/x-tar", Filename: "b.tar",
	})
	requireNoError(t, err, "ingest unsupported")
	h.mustIngestText(ctx, "good", "an indexable note")
	h.drain(ctx)

	queued, err := h.service.Reindex(ctx, ReindexOptions{Failed: true})
	requireNoError(t, err, "reindex failed")
	if queued == 0 {
		t.Fatal("expected the skipped source to be re-queued")
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	for _, job := range jobs {
		if job.Status == JobFailed {
			t.Fatalf("a parked job survived the retry pass: %+v", job)
		}
	}
}

func TestReindexUnknownSourceReportsNotFound(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	_, err := h.service.Reindex(ctx, ReindexOptions{SourceID: "does-not-exist"})
	requireErrorIs(t, err, ErrNotFound, "reindex unknown source")
}
