package memory

import (
	"strings"
	"testing"
)

func TestChunkTextKeepsShortDocumentWhole(t *testing.T) {
	text := "Raspberry Pi 5 runs openLight.\n\nA 1 TB SSD is attached over USB 3."

	chunks := ChunkText(text, ChunkOptions{TargetTokens: 350, OverlapTokens: 50})

	if len(chunks) != 1 {
		t.Fatalf("expected a short document to stay in one chunk, got %d", len(chunks))
	}
	if !strings.Contains(chunks[0].Text, "1 TB SSD") {
		t.Fatalf("chunk lost content: %q", chunks[0].Text)
	}
}

func TestChunkTextSplitsOnHeadingsAndTracksBreadcrumb(t *testing.T) {
	text := strings.Join([]string{
		"# Hardware",
		"",
		"The Pi has 8 GB of RAM.",
		"",
		"## Storage",
		"",
		"A 1 TB SSD is attached.",
	}, "\n")

	chunks := ChunkText(text, ChunkOptions{TargetTokens: 350, OverlapTokens: 0})

	if len(chunks) != 2 {
		t.Fatalf("expected one chunk per section, got %d: %+v", len(chunks), chunks)
	}
	if chunks[0].Heading != "Hardware" {
		t.Fatalf("first heading = %q, want %q", chunks[0].Heading, "Hardware")
	}
	// A level-2 heading nests under the level-1 one, so a fragment
	// retrieved from deep in a document still says where it lives.
	if chunks[1].Heading != "Hardware > Storage" {
		t.Fatalf("second heading = %q, want %q", chunks[1].Heading, "Hardware > Storage")
	}
}

func TestChunkTextRespectsTargetSizeAndOverlaps(t *testing.T) {
	var builder strings.Builder
	for i := 0; i < 40; i++ {
		builder.WriteString("Paragraph number with several words in it to consume tokens steadily.\n\n")
	}

	chunks := ChunkText(builder.String(), ChunkOptions{TargetTokens: 60, OverlapTokens: 10})

	if len(chunks) < 3 {
		t.Fatalf("expected the text to split into several chunks, got %d", len(chunks))
	}
	for i, chunk := range chunks {
		// Allow one paragraph of slack: the chunker only closes a chunk
		// once adding the next block would exceed the target.
		if chunk.Tokens > 60+20 {
			t.Fatalf("chunk %d is %d tokens, well past the 60 target", i, chunk.Tokens)
		}
	}
}

func TestChunkTextSplitsAnOversizedParagraph(t *testing.T) {
	sentence := "This single sentence is deliberately verbose so that the paragraph alone exceeds the configured target. "
	text := strings.Repeat(sentence, 30)

	chunks := ChunkText(text, ChunkOptions{TargetTokens: 40, OverlapTokens: 5})

	if len(chunks) < 3 {
		t.Fatalf("an oversized paragraph must be split, got %d chunks", len(chunks))
	}
	for i, chunk := range chunks {
		if strings.TrimSpace(chunk.Text) == "" {
			t.Fatalf("chunk %d is empty", i)
		}
	}
}

func TestChunkTextIgnoresEmptyInput(t *testing.T) {
	if chunks := ChunkText("   \n\n\t ", ChunkOptions{}); len(chunks) != 0 {
		t.Fatalf("expected no chunks from blank input, got %d", len(chunks))
	}
}

func TestChunkOptionsClampRunawayOverlap(t *testing.T) {
	// An overlap at or above half the target would make each chunk mostly
	// a copy of the previous one and could stall forward progress.
	opts := ChunkOptions{TargetTokens: 100, OverlapTokens: 90}.normalized()

	if opts.OverlapTokens != 25 {
		t.Fatalf("overlap = %d, want it clamped to 25", opts.OverlapTokens)
	}
}

func TestEstimateTokensChargesCyrillicMore(t *testing.T) {
	latin := EstimateTokens(strings.Repeat("a", 100))
	cyrillic := EstimateTokens(strings.Repeat("я", 100))

	if cyrillic <= latin {
		t.Fatalf("cyrillic estimate (%d) must exceed latin (%d) for equal rune counts", cyrillic, latin)
	}
}
