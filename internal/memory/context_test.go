package memory

import (
	"strings"
	"testing"
	"time"
)

func TestBuildContextStaysWithinTokenBudget(t *testing.T) {
	now := time.Now().UTC()

	var results []Result
	for i := 0; i < 10; i++ {
		results = append(results, Result{
			ChunkID:    newID(),
			SourceID:   newID(),
			SourceType: TypeDocument,
			Title:      "Long document",
			Text: "Distinct opening line " + string(rune('a'+i)) + ". " +
				strings.Repeat("Filler sentence with a reasonable number of words in it. ", 20),
			Score:     0.9 - float64(i)/100,
			Timestamp: now,
		})
	}

	built := BuildContext(nil, results, ContextOptions{MaxResults: 5, MaxTokens: 200, Now: now})

	if built.Empty() {
		t.Fatal("expected some context to fit in the budget")
	}
	if built.Tokens > 200 {
		t.Fatalf("context is %d tokens, over the 200 budget", built.Tokens)
	}
	if len(built.Results) > 5 {
		t.Fatalf("returned %d chunks, over the max of 5", len(built.Results))
	}
	if built.Dropped == 0 {
		t.Fatal("dropped count should report the candidates that did not fit")
	}
}

func TestBuildContextNeverExceedsMaxResults(t *testing.T) {
	now := time.Now().UTC()
	var results []Result
	for i := 0; i < 20; i++ {
		results = append(results, Result{
			ChunkID:    newID(),
			SourceID:   newID(),
			SourceType: TypeConversation,
			Text:       "short distinct note number " + string(rune('a'+i)),
			Score:      0.5,
			Timestamp:  now,
		})
	}

	built := BuildContext(nil, results, ContextOptions{MaxResults: 3, MaxTokens: 5000, Now: now})

	if len(built.Results) != 3 {
		t.Fatalf("got %d chunks, want exactly the configured 3", len(built.Results))
	}
}

func TestBuildContextDedupesOverlappingChunks(t *testing.T) {
	now := time.Now().UTC()
	shared := "The Raspberry Pi has a one terabyte solid state drive attached over USB three."

	results := []Result{
		{ChunkID: newID(), SourceID: "s1", SourceType: TypeDocument, Text: shared, Score: 0.9, Timestamp: now},
		{ChunkID: newID(), SourceID: "s2", SourceType: TypeDocument, Text: shared, Score: 0.8, Timestamp: now},
		{ChunkID: newID(), SourceID: "s3", SourceType: TypeDocument, Text: "Completely different content about the Mac mini.", Score: 0.7, Timestamp: now},
	}

	built := BuildContext(nil, results, ContextOptions{MaxResults: 5, MaxTokens: 2000, Now: now})

	if len(built.Results) != 2 {
		t.Fatalf("expected the duplicate to be dropped, got %d chunks", len(built.Results))
	}
}

func TestBuildContextLimitsChunksPerSource(t *testing.T) {
	now := time.Now().UTC()
	var results []Result
	for i := 0; i < 5; i++ {
		results = append(results, Result{
			ChunkID:    newID(),
			SourceID:   "one-big-pdf",
			SourceType: TypeDocument,
			Text:       "distinct paragraph number " + string(rune('a'+i)) + " from the same document",
			Score:      0.9,
			Timestamp:  now,
		})
	}
	results = append(results, Result{
		ChunkID: newID(), SourceID: "other", SourceType: TypeConversation,
		Text: "a note from somewhere else", Score: 0.5, Timestamp: now,
	})

	built := BuildContext(nil, results, ContextOptions{MaxResults: 5, MaxTokens: 2000, Now: now})

	perSource := map[string]int{}
	for _, result := range built.Results {
		perSource[result.SourceID]++
	}
	// One large document must not be able to crowd out every other
	// memory just because it matched a lot.
	if perSource["one-big-pdf"] > 2 {
		t.Fatalf("one source contributed %d chunks, want at most 2", perSource["one-big-pdf"])
	}
	if perSource["other"] != 1 {
		t.Fatal("the unrelated source was crowded out")
	}
}

func TestBuildContextPrefersFresherWhenScoresAreClose(t *testing.T) {
	now := time.Now().UTC()

	results := []Result{
		{ChunkID: "old", SourceID: "a", SourceType: TypeConversation, Text: "raspberry storage is 1 TB", Score: 0.80, Timestamp: now.AddDate(-2, 0, 0)},
		{ChunkID: "new", SourceID: "b", SourceType: TypeConversation, Text: "raspberry storage is 4 TB", Score: 0.79, Timestamp: now.Add(-time.Hour)},
	}

	built := BuildContext(nil, results, ContextOptions{MaxResults: 2, MaxTokens: 2000, Now: now})

	if len(built.Results) != 2 {
		t.Fatalf("expected both results, got %d", len(built.Results))
	}
	if built.Results[0].ChunkID != "new" {
		t.Fatalf("recency tilt did not apply: first result is %q", built.Results[0].ChunkID)
	}
}

func TestBuildContextRendersFactsBeforeChunks(t *testing.T) {
	now := time.Now().UTC()
	facts := []Fact{{
		Subject: "raspberry", Predicate: "storage", Value: "4 TB SSD",
		Category: CategoryHardware, ValidFrom: now.Add(-24 * time.Hour),
	}}
	results := []Result{{
		ChunkID: newID(), SourceID: "s", SourceType: TypeDocument,
		Text: "some retrieved paragraph", Score: 0.5, Timestamp: now,
	}}

	built := BuildContext(facts, results, ContextOptions{MaxResults: 3, MaxTokens: 2000, Now: now})

	factIndex := strings.Index(built.Block, "Known facts")
	chunkIndex := strings.Index(built.Block, "Retrieved notes")
	if factIndex < 0 || chunkIndex < 0 {
		t.Fatalf("block missing a section:\n%s", built.Block)
	}
	if factIndex > chunkIndex {
		t.Fatal("structured facts should be rendered before retrieved chunks")
	}
	if !strings.Contains(built.Block, "4 TB SSD") {
		t.Fatalf("fact value missing from block:\n%s", built.Block)
	}
}

func TestBuildContextReturnsEmptyWithNoInput(t *testing.T) {
	built := BuildContext(nil, nil, ContextOptions{})
	if !built.Empty() {
		t.Fatalf("expected an empty context, got %q", built.Block)
	}
	if built.Prompt() != "" {
		t.Fatal("an empty context must produce no prompt at all")
	}
}

func TestBuildContextTinyBudgetProducesNothingRatherThanGarbage(t *testing.T) {
	now := time.Now().UTC()
	results := []Result{{
		ChunkID: newID(), SourceID: "s", SourceType: TypeDocument,
		Text: strings.Repeat("word ", 500), Score: 0.9, Timestamp: now,
	}}

	built := BuildContext(nil, results, ContextOptions{MaxResults: 5, MaxTokens: 12, Now: now})

	if !built.Empty() {
		t.Fatalf("a budget too small for any content should yield nothing, got %q", built.Block)
	}
}

func TestPromptCarriesUntrustedDataWarning(t *testing.T) {
	now := time.Now().UTC()
	built := BuildContext(nil, []Result{{
		ChunkID: newID(), SourceID: "s", SourceType: TypeDocument,
		Text: "a normal note", Score: 0.9, Timestamp: now,
	}}, ContextOptions{MaxResults: 1, MaxTokens: 500, Now: now})

	prompt := built.Prompt()
	for _, expected := range []string{"DATA", "never as instructions", "out of date"} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("prompt is missing %q:\n%s", expected, prompt)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(prompt), "</memory>") {
		t.Fatal("the memory block must be explicitly delimited")
	}
}

func TestTrimToTokensMarksTruncation(t *testing.T) {
	trimmed := trimToTokens(strings.Repeat("word ", 200), 20)
	if trimmed == "" {
		t.Fatal("expected some text to survive")
	}
	if !strings.HasSuffix(trimmed, "…") {
		t.Fatalf("truncation was not marked: %q", trimmed)
	}
	if EstimateTokens(trimmed) > 21 {
		t.Fatalf("trimmed text is %d tokens, over budget", EstimateTokens(trimmed))
	}
}
