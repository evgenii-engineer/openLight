package memory

import (
	"context"
	"testing"
	"time"
)

// TestRunProcessesQueuedWorkConcurrently exercises the real goroutine
// topology — ingestion workers, the turn writer, and the consolidator
// all sharing one SQLite connection. Worth running under -race: this is
// the only test where those three run at once.
func TestRunProcessesQueuedWorkConcurrently(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, func(o *harnessOptions) {
		o.ingestion = IngestionOptions{Workers: 3, PollInterval: 10 * time.Millisecond}
		o.conversations = ConversationOptions{
			AutoMemory: true, Summarize: false,
			IdleTimeout: time.Hour, CheckInterval: 20 * time.Millisecond, MinTurns: 2,
		}
	})

	const documents = 12
	for i := 0; i < documents; i++ {
		h.mustIngestText(ctx, "doc", "distinct indexable content number "+string(rune('a'+i)))
	}

	done := make(chan error, 1)
	go func() { done <- h.service.Run(ctx) }()

	// Concurrent conversation traffic while ingestion drains.
	for i := 0; i < 50; i++ {
		h.service.RecordTurn(int64(i%3+1), "user", "message")
	}

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		sources, err := h.store.ListSources(ctx, StatusCompleted, 100)
		requireNoError(t, err, "list completed sources")
		if len(sources) == documents {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	completed, err := h.store.ListSources(ctx, StatusCompleted, 100)
	requireNoError(t, err, "list completed sources")
	if len(completed) != documents {
		t.Fatalf("completed %d of %d documents before the deadline", len(completed), documents)
	}

	jobs, err := h.store.ListJobs(ctx, 100)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 0 {
		t.Fatalf("queue not drained: %+v", jobs)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancellation")
	}
}

func TestRunReclaimsAbandonedJobsOnStart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, func(o *harnessOptions) {
		o.ingestion = IngestionOptions{Workers: 1, PollInterval: 10 * time.Millisecond}
	})

	source := h.mustIngestText(ctx, "doc", "content interrupted by a restart")
	// Leave the job in the state a killed process would leave it.
	if _, err := h.store.ClaimJob(ctx, time.Now().UTC()); err != nil {
		t.Fatalf("claim: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- h.service.Run(ctx) }()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		stored, err := h.store.Source(ctx, source.ID)
		requireNoError(t, err, "reload source")
		if stored.Status == StatusCompleted {
			cancel()
			<-done
			return
		}
		time.Sleep(20 * time.Millisecond)
	}

	cancel()
	<-done
	t.Fatal("the abandoned job was never picked back up")
}
