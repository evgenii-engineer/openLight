package memory

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestConversationEpisodeIsSummarisedOnceNotPerTurn(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.conversations = ConversationOptions{
			AutoMemory: true, Summarize: true,
			IdleTimeout: time.Millisecond, MinTurns: 2, MaxTurns: 100,
		}
	})
	h.chatter.response = `{"topic":"Storage","summary":"The Pi got a 1 TB SSD.",
		"facts":[{"subject":"raspberry","predicate":"storage","value":"1 TB SSD","category":"hardware","confidence":0.9}],
		"decisions":["Keep the SSD on USB 3"]}`

	// Stamp the episode in the past so the idle cutoff is deterministic
	// rather than racing wall-clock jitter.
	now := time.Now().UTC().Add(-time.Hour)
	episode, err := h.store.OpenEpisode(ctx, 42, newID(), now)
	requireNoError(t, err, "open episode")

	// Filler turns of exactly the kind that must never each become their
	// own searchable chunk.
	for _, turn := range []struct{ role, text string }{
		{"user", "У Raspberry теперь SSD на 1 TB."},
		{"assistant", "Понял, запомнил."},
		{"user", "ага"},
		{"assistant", "ок"},
		{"user", "спасибо"},
	} {
		requireNoError(t, h.store.AppendTurn(ctx, episode.ID, turn.role, turn.text, now), "append turn")
	}

	h.service.consolidateIdle(ctx)
	h.drain(ctx)

	if h.chatter.callCount() != 1 {
		t.Fatalf("smart model called %d times, want exactly one per episode", h.chatter.callCount())
	}

	// Exactly one source: the distilled summary, not five turns.
	sources, err := h.store.ListSources(ctx, "", 20)
	requireNoError(t, err, "list sources")
	if len(sources) != 1 {
		t.Fatalf("expected one conversation source, got %d", len(sources))
	}
	if sources[0].Type != TypeConversation {
		t.Fatalf("source type = %q, want %q", sources[0].Type, TypeConversation)
	}
	if !strings.Contains(sources[0].Title, "Storage") {
		t.Fatalf("summary title = %q, want the distilled topic", sources[0].Title)
	}

	// The raw turns are still there — the summary is an addition, not a
	// replacement.
	turns, err := h.store.EpisodeTurns(ctx, episode.ID, 50)
	requireNoError(t, err, "episode turns")
	if len(turns) != 5 {
		t.Fatalf("raw conversation was lost: %d turns", len(turns))
	}

	// And the durable fact was promoted.
	fact, err := h.store.CurrentFact(ctx, "raspberry", "storage")
	requireNoError(t, err, "current fact")
	if fact.Value != "1 TB SSD" {
		t.Fatalf("fact value = %q", fact.Value)
	}
}

func TestEpisodeWithNothingDurableIsClosedWithoutIndexing(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.conversations = ConversationOptions{
			AutoMemory: true, Summarize: true,
			IdleTimeout: time.Millisecond, MinTurns: 2, MaxTurns: 100,
		}
	})
	h.chatter.response = `{"topic":"","summary":"","facts":[],"decisions":[]}`

	now := time.Now().UTC().Add(-time.Hour)
	episode, err := h.store.OpenEpisode(ctx, 7, newID(), now)
	requireNoError(t, err, "open episode")
	requireNoError(t, h.store.AppendTurn(ctx, episode.ID, "user", "ок", now), "turn 1")
	requireNoError(t, h.store.AppendTurn(ctx, episode.ID, "assistant", "ага", now), "turn 2")

	h.service.consolidateIdle(ctx)
	h.drain(ctx)

	sources, err := h.store.ListSources(ctx, "", 10)
	requireNoError(t, err, "list sources")
	if len(sources) != 0 {
		t.Fatalf("an empty distillation must not be indexed, got %d sources", len(sources))
	}
	if h.vectors.Len() != 0 {
		t.Fatal("empty distillation reached the vector index")
	}
}

func TestEpisodeSummarisationRetriesWhenTheBrainIsOffline(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		o.conversations = ConversationOptions{
			AutoMemory: true, Summarize: true,
			IdleTimeout: time.Millisecond, MinTurns: 2, MaxTurns: 100,
		}
	})
	h.chatter.err = context.DeadlineExceeded

	now := time.Now().UTC().Add(-time.Hour)
	episode, err := h.store.OpenEpisode(ctx, 9, newID(), now)
	requireNoError(t, err, "open episode")
	requireNoError(t, h.store.AppendTurn(ctx, episode.ID, "user", "important context here", now), "turn 1")
	requireNoError(t, h.store.AppendTurn(ctx, episode.ID, "assistant", "acknowledged", now), "turn 2")

	h.service.consolidateIdle(ctx)
	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Kind != JobSummarize || jobs[0].Status != JobPending {
		t.Fatalf("summarisation should stay queued for retry, got %+v", jobs)
	}

	// Brain returns; the same job succeeds with no user involvement.
	h.chatter.err = nil
	h.chatter.response = `{"topic":"Context","summary":"Some durable context.","facts":[],"decisions":[]}`
	h.forceDue(ctx)
	h.drain(ctx)

	sources, err := h.store.ListSources(ctx, "", 10)
	requireNoError(t, err, "list sources")
	if len(sources) != 1 {
		t.Fatalf("expected the summary after recovery, got %d sources", len(sources))
	}
}

func TestRecordTurnIsInertUntilTheServiceRuns(t *testing.T) {
	h := newHarness(t, func(o *harnessOptions) {
		o.conversations = ConversationOptions{AutoMemory: true}
	})

	// Without Run there is no writer goroutine; RecordTurn must simply
	// drop rather than fill a buffer or block a reply.
	for i := 0; i < 1000; i++ {
		h.service.RecordTurn(1, "user", "hello")
	}
}

func TestParseDistillationToleratesFencedOutput(t *testing.T) {
	response := "Sure, here you go:\n```json\n" +
		`{"topic":"T","summary":"S","facts":[{"subject":"a","predicate":"b","value":"c"}],"decisions":["d"]}` +
		"\n```\nHope that helps."

	distillation, err := ParseDistillation(response)
	requireNoError(t, err, "parse")

	if distillation.Topic != "T" || distillation.Summary != "S" {
		t.Fatalf("parsed wrong: %+v", distillation)
	}
	if len(distillation.Facts) != 1 || distillation.Facts[0].Value != "c" {
		t.Fatalf("facts parsed wrong: %+v", distillation.Facts)
	}
	// A fact with no stated confidence gets a middling default rather
	// than zero, which would otherwise sort it below everything.
	if distillation.Facts[0].Confidence == 0 {
		t.Fatal("missing confidence should default, not stay zero")
	}
}

func TestParseDistillationDropsIncompleteFacts(t *testing.T) {
	distillation, err := ParseDistillation(
		`{"summary":"s","facts":[{"subject":"a","predicate":"","value":"c"},{"subject":"x","predicate":"y","value":"z"}]}`)
	requireNoError(t, err, "parse")

	if len(distillation.Facts) != 1 || distillation.Facts[0].Subject != "x" {
		t.Fatalf("incomplete fact was not dropped: %+v", distillation.Facts)
	}
}

func TestParseDistillationRejectsNonJSON(t *testing.T) {
	if _, err := ParseDistillation("I could not produce JSON, sorry."); err == nil {
		t.Fatal("expected an error for a response with no JSON object")
	}
}

func TestDistillationTextRendersTheIndexedShape(t *testing.T) {
	text := Distillation{
		Topic:    "Storage",
		Summary:  "The Pi got a bigger disk.",
		Facts:    []ExtractedFact{{Subject: "raspberry", Predicate: "storage", Value: "4 TB SSD"}},
		Decision: []string{"Move the archive to the SSD"},
	}.Text()

	for _, expected := range []string{"Topic: Storage", "Summary:", "Important facts:", "4 TB SSD", "Decisions:"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("rendered summary is missing %q:\n%s", expected, text)
		}
	}
}
