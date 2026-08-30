package memory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIngestArchivesRawAndQueuesWork(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	source := h.mustIngestText(ctx, "notes", "Raspberry has a 1 TB SSD attached over USB 3.")

	if source.Status != StatusPending {
		t.Fatalf("status = %q, want pending", source.Status)
	}
	if source.Hash == "" {
		t.Fatal("source has no content hash")
	}
	if _, err := os.Stat(source.RawPath); err != nil {
		t.Fatalf("raw file was not archived: %v", err)
	}
	// The sidecar is what makes the archive self-describing if the
	// metadata database is ever lost.
	if _, err := os.Stat(source.RawPath + ".meta.json"); err != nil {
		t.Fatalf("metadata sidecar missing: %v", err)
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Kind != JobIngest {
		t.Fatalf("expected exactly one ingest job, got %+v", jobs)
	}
}

func TestIngestDedupesIdenticalContent(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	text := "The Mac mini M1 is the brain node."
	first := h.mustIngestText(ctx, "a", text)
	h.drain(ctx)

	callsAfterFirst := h.embedder.callCount()
	pointsAfterFirst := h.vectors.Len()

	// Same bytes arriving again — a re-sent file, a duplicated Telegram
	// upload — must not produce a second archive or a second embedding.
	second := h.mustIngestText(ctx, "a", text)

	if second.ID != first.ID {
		t.Fatalf("duplicate created a new source: %s vs %s", second.ID, first.ID)
	}
	if second.Hash != first.Hash {
		t.Fatalf("duplicate hash mismatch")
	}

	h.drain(ctx)
	if got := h.embedder.callCount(); got != callsAfterFirst {
		t.Fatalf("duplicate re-embedded: %d calls, want %d", got, callsAfterFirst)
	}
	if got := h.vectors.Len(); got != pointsAfterFirst {
		t.Fatalf("duplicate added points: %d, want %d", got, pointsAfterFirst)
	}

	sources, err := h.store.ListSources(ctx, "", 10)
	requireNoError(t, err, "list sources")
	if len(sources) != 1 {
		t.Fatalf("expected one source row after dedup, got %d", len(sources))
	}
}

func TestIngestDedupesAcrossDifferentFilenames(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)
	dir := t.TempDir()

	content := "identical bytes under two different names"
	pathA := writeTempFile(t, dir, "a.txt", content)
	pathB := writeTempFile(t, dir, "b.txt", content)

	first, err := h.service.Ingest(ctx, Item{Type: TypeDocument, Path: pathA, MIMEType: "text/plain", Filename: "a.txt"})
	requireNoError(t, err, "ingest a")
	second, err := h.service.Ingest(ctx, Item{Type: TypeDocument, Path: pathB, MIMEType: "text/plain", Filename: "b.txt"})
	requireNoError(t, err, "ingest b")

	// Dedup is on content, not on name: the same document sent twice
	// under different names is still the same document.
	if first.ID != second.ID {
		t.Fatalf("content dedup did not trigger: %s vs %s", first.ID, second.ID)
	}
}

func TestIngestCompletesPipelineAndIndexesChunks(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	source := h.mustIngestText(ctx, "hardware", strings.Repeat("Raspberry Pi storage notes. ", 50))
	h.drain(ctx)

	updated, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if updated.Status != StatusCompleted {
		t.Fatalf("status = %q, want completed", updated.Status)
	}

	chunks, err := h.store.ChunksBySource(ctx, source.ID)
	requireNoError(t, err, "load chunks")
	if len(chunks) == 0 {
		t.Fatal("no chunks persisted")
	}
	if h.vectors.Len() != len(chunks) {
		t.Fatalf("vector points = %d, chunks = %d", h.vectors.Len(), len(chunks))
	}

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 0 {
		t.Fatalf("completed job was not removed from the queue: %+v", jobs)
	}
}

func TestIngestParksUnsupportedSourceWithoutEndlessRetries(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, func(o *harnessOptions) {
		// No extractor claims this MIME type.
		o.extractors = Extractors{PDFExtractor{Reader: nil}}
	})

	source, err := h.service.Ingest(ctx, Item{
		Type:     TypeDocument,
		Title:    "archive",
		Text:     "not really a zip, but declared as one",
		MIMEType: "application/zip",
		Filename: "bundle.zip",
	})
	requireNoError(t, err, "ingest")

	h.drain(ctx)

	jobs, err := h.store.ListJobs(ctx, 10)
	requireNoError(t, err, "list jobs")
	if len(jobs) != 1 || jobs[0].Status != JobFailed {
		t.Fatalf("expected the job parked as failed, got %+v", jobs)
	}

	updated, err := h.store.Source(ctx, source.ID)
	requireNoError(t, err, "reload source")
	if updated.Status != StatusSkipped {
		t.Fatalf("source status = %q, want skipped", updated.Status)
	}

	// The archive itself is untouched: a later reindex with a new
	// extractor can still pick it up.
	if _, statErr := os.Stat(updated.RawPath); statErr != nil {
		t.Fatalf("raw file was removed for an unsupported source: %v", statErr)
	}
}

func TestIngestRejectsItemsWithNeitherPathNorText(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	if _, err := h.service.Ingest(ctx, Item{Type: TypeDocument}); err == nil {
		t.Fatal("expected an error for an item with no payload")
	}
}

func TestRawStoreWritesUnderTypeAndDateDirectories(t *testing.T) {
	root := t.TempDir()
	raw, err := NewRawStore(root)
	requireNoError(t, err, "new raw store")

	created := time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC)
	stored, err := raw.Put(Item{
		Type:      TypeDocument,
		Text:      "hello",
		MIMEType:  "text/plain",
		Filename:  "hello.txt",
		CreatedAt: created,
	})
	requireNoError(t, err, "put")

	want := filepath.Join(root, "raw", TypeDocument, "2026", "03")
	if filepath.Dir(stored.Path) != want {
		t.Fatalf("stored at %q, want a file under %q", stored.Path, want)
	}
	if filepath.Ext(stored.Path) != ".txt" {
		t.Fatalf("lost the file extension: %q", stored.Path)
	}
}
