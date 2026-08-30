package memory

import (
	"context"
	"testing"
	"time"
)

func TestQueueSurvivesRestartMidJob(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	source := h.mustIngestText(ctx, "doc", "content that will be indexed after a restart")

	// Simulate a process that died with the job in flight.
	job, err := h.store.ClaimJob(ctx, time.Now().UTC())
	requireNoError(t, err, "claim job")
	if job.Status != JobRunning {
		t.Fatalf("claimed job status = %q, want running", job.Status)
	}

	// Nothing is runnable while the job is marked running: a second
	// worker must not pick up work the first one owns.
	if _, err := h.store.ClaimJob(ctx, time.Now().UTC()); err == nil {
		t.Fatal("a running job was claimed twice")
	}

	// Restart: reclaim orphaned work.
	reclaimed, err := h.store.ReclaimRunningJobs(ctx)
	requireNoError(t, err, "reclaim")
	if reclaimed != 1 {
		t.Fatalf("reclaimed %d jobs, want 1", reclaimed)
	}

	h.drain(ctx)

	updated, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if updated.Status != StatusCompleted {
		t.Fatalf("status after restart = %q, want completed", updated.Status)
	}
}

func TestQueueRetriesWithBackoffAndKeepsDataWhenEmbeddingsAreDown(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.embedder.setDown(true)
	source := h.mustIngestText(ctx, "doc", "this text arrives while the brain node is asleep")

	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 {
		t.Fatalf("expected the job to survive, got %+v", jobs)
	}
	if jobs[0].Status != JobPending {
		t.Fatalf("job status = %q, want it still pending for retry", jobs[0].Status)
	}
	if jobs[0].Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", jobs[0].Attempts)
	}
	if !jobs[0].NextRetryAt.After(time.Now().UTC()) {
		t.Fatal("next retry was not pushed into the future")
	}
	if jobs[0].LastError == "" {
		t.Fatal("last_error was not recorded")
	}

	// The archive is intact, so nothing is lost while the backend is out.
	stored, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if stored.RawPath == "" {
		t.Fatal("raw path was cleared")
	}

	// Brain comes back; the same job completes with no user action.
	h.embedder.setDown(false)
	h.forceDue(ctx)
	h.drain(ctx)

	completed, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if completed.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed once embeddings returned", completed.Status)
	}
	if h.vectors.Len() == 0 {
		t.Fatal("nothing was indexed after recovery")
	}
}

func TestQueueNeverParksTransientFailures(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.ingestion = IngestionOptions{MaxAttempts: 2, RetryBase: time.Millisecond, RetryMaxInterval: time.Millisecond}
	})

	h.embedder.setDown(true)
	h.mustIngestText(ctx, "doc", "content that outlives a long outage")

	// Far more attempts than MaxAttempts. A backend that is merely
	// offline must not cause the data to be given up on.
	for i := 0; i < 6; i++ {
		h.forceDue(ctx)
		h.drain(ctx)
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 {
		t.Fatalf("expected one job, got %+v", jobs)
	}
	if jobs[0].Status != JobPending {
		t.Fatalf("transient failure parked the job after %d attempts", jobs[0].Attempts)
	}
	if jobs[0].Attempts < 6 {
		t.Fatalf("attempts = %d, expected them to keep accumulating", jobs[0].Attempts)
	}
}

func TestRetryFailedJobsRequeuesParkedWork(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.extractors = Extractors{}
	})

	h.mustIngestText(ctx, "doc", "no extractor will claim this")
	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Status != JobFailed {
		t.Fatalf("expected a parked job, got %+v", jobs)
	}

	requeued, err := h.store.RetryFailedJobs(ctx)
	requireNoError(t, err, "retry")
	if requeued != 1 {
		t.Fatalf("requeued %d, want 1", requeued)
	}

	jobs, err = h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if jobs[0].Status != JobPending || jobs[0].Attempts != 0 {
		t.Fatalf("retry did not reset the job: %+v", jobs[0])
	}
}

func TestEnqueueJobIsIdempotentWhileWorkIsOutstanding(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	source := h.mustIngestText(ctx, "doc", "queued once")
	// Defensive re-enqueues (reindex racing with the initial ingest,
	// say) must not produce duplicate work.
	for i := 0; i < 3; i++ {
		requireNoError(t, h.store.EnqueueJob(ctx, source.ID, JobIngest), "enqueue")
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 {
		t.Fatalf("expected one active job, got %d", len(jobs))
	}
}

func TestBackoffDoublesAndCaps(t *testing.T) {
	base := time.Second
	max := 10 * time.Second

	cases := []struct {
		attempts int
		want     time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 10 * time.Second},
		{50, 10 * time.Second},
	}
	for _, tc := range cases {
		if got := backoff(base, max, tc.attempts); got != tc.want {
			t.Fatalf("backoff(attempt %d) = %v, want %v", tc.attempts, got, tc.want)
		}
	}
}
