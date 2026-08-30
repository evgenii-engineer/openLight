package memory

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"openlight/internal/memory/vectorstore"
)

// fakeEmbedder produces deterministic bag-of-words vectors. Cosine
// similarity between them tracks lexical overlap, which is enough to
// exercise ranking, dedup, and the budget without a real model.
type fakeEmbedder struct {
	mu sync.Mutex

	dims  int
	calls int

	// down makes every call report ErrEmbeddingsUnavailable, standing in
	// for an offline Mac mini.
	down bool
}

func newFakeEmbedder() *fakeEmbedder { return &fakeEmbedder{dims: 64} }

func (e *fakeEmbedder) setDown(down bool) {
	e.mu.Lock()
	e.down = down
	e.mu.Unlock()
}

func (e *fakeEmbedder) callCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func (e *fakeEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down {
		return nil, fmt.Errorf("%w: fake embedder is down", ErrEmbeddingsUnavailable)
	}
	e.calls++

	out := make([][]float32, 0, len(inputs))
	for _, input := range inputs {
		vector := make([]float32, e.dims)
		for _, field := range strings.Fields(strings.ToLower(input)) {
			hasher := fnv.New32a()
			_, _ = hasher.Write([]byte(field))
			vector[int(hasher.Sum32())%e.dims] += 1
		}
		// Guarantee a non-zero vector so cosine is defined.
		vector[0] += 0.001
		out = append(out, vector)
	}
	return out, nil
}

func (e *fakeEmbedder) Dimensions(ctx context.Context) (int, error) {
	e.mu.Lock()
	down := e.down
	e.mu.Unlock()
	if down {
		return 0, fmt.Errorf("%w: fake embedder is down", ErrEmbeddingsUnavailable)
	}
	return e.dims, nil
}

func (e *fakeEmbedder) Health(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.down {
		return fmt.Errorf("%w: fake embedder is down", ErrEmbeddingsUnavailable)
	}
	return nil
}

func (e *fakeEmbedder) Model() string { return "fake-embed" }

// fakeChatter returns a canned distillation response.
type fakeChatter struct {
	mu       sync.Mutex
	response string
	err      error
	calls    int
	lastUser string
}

func (c *fakeChatter) Chat(_ context.Context, messages []ChatMessage) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	for _, message := range messages {
		if message.Role == "user" {
			c.lastUser = message.Content
		}
	}
	if c.err != nil {
		return "", c.err
	}
	return c.response, nil
}

func (c *fakeChatter) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

// harness bundles a fully wired service over temp directories.
type harness struct {
	t        *testing.T
	root     string
	service  *Service
	store    *Store
	raw      *RawStore
	vectors  *vectorstore.Fake
	embedder *fakeEmbedder
	chatter  *fakeChatter
}

type harnessOptions struct {
	retrieval     RetrievalOptions
	conversations ConversationOptions
	factsEnabled  bool
	chunking      ChunkOptions
	ingestion     IngestionOptions
	extractors    Extractors
}

func newHarness(t *testing.T, configure func(*harnessOptions)) *harness {
	t.Helper()

	root := t.TempDir()
	raw, err := NewRawStore(root)
	if err != nil {
		t.Fatalf("new raw store: %v", err)
	}
	store, err := OpenStore(context.Background(), filepath.Join(root, "memory.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	vectors := vectorstore.NewFake()
	embedder := newFakeEmbedder()
	chatter := &fakeChatter{response: `{"topic":"t","summary":"s","facts":[],"decisions":[]}`}

	opts := harnessOptions{
		factsEnabled: true,
		extractors: Extractors{
			PDFExtractor{Reader: raw.Read},
			TextExtractor{Reader: raw.Read},
		},
	}
	if configure != nil {
		configure(&opts)
	}

	service, err := New(Options{
		Root:          root,
		DBPath:        store.Path(),
		Collection:    "test",
		Chunking:      opts.chunking,
		Retrieval:     opts.retrieval,
		Ingestion:     opts.ingestion,
		Conversations: opts.conversations,
		FactsEnabled:  opts.factsEnabled,
	}, Deps{
		Store:      store,
		Raw:        raw,
		Vectors:    vectors,
		Embedder:   embedder,
		Extractors: opts.extractors,
		Chatter:    chatter,
		Logger:     silentLogger(),
	})
	if err != nil {
		t.Fatalf("new service: %v", err)
	}

	return &harness{
		t:        t,
		root:     root,
		service:  service,
		store:    store,
		raw:      raw,
		vectors:  vectors,
		embedder: embedder,
		chatter:  chatter,
	}
}

// drain runs queued jobs to completion (or until they stop being
// runnable), synchronously. Tests use this instead of Run so they never
// depend on wall-clock timing.
func (h *harness) drain(ctx context.Context) int {
	h.t.Helper()
	processed := 0
	for i := 0; i < 200; i++ {
		if !h.service.processNext(ctx, silentLogger()) {
			break
		}
		processed++
	}
	return processed
}

// forceDue makes every pending job runnable now, cancelling any backoff.
func (h *harness) forceDue(ctx context.Context) {
	h.t.Helper()
	jobs, err := h.store.ListJobs(ctx, 500)
	if err != nil {
		h.t.Fatalf("list jobs: %v", err)
	}
	for _, job := range jobs {
		if job.Status != JobPending {
			continue
		}
		if err := h.store.RescheduleJob(ctx, job.ID, job.Attempts, job.LastError, time.Now().UTC().Add(-time.Second)); err != nil {
			h.t.Fatalf("force due: %v", err)
		}
	}
}

func (h *harness) mustIngestText(ctx context.Context, title, text string) Source {
	h.t.Helper()
	source, err := h.service.Ingest(ctx, Item{
		Type:     TypeDocument,
		Source:   "test",
		Title:    title,
		Text:     text,
		MIMEType: "text/plain",
		Filename: title + ".txt",
	})
	if err != nil {
		h.t.Fatalf("ingest %q: %v", title, err)
	}
	return source
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func requireNoError(t *testing.T, err error, what string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
}

func requireErrorIs(t *testing.T, err, target error, what string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: expected %v, got %v", what, target, err)
	}
}
