package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeBrain stands in for openLight's brain API.
type fakeBrain struct {
	mu sync.Mutex

	embedCalls  int
	pullCalls   int
	lastInput   []string
	dims        int
	model       string
	embedStatus int
	pullStatus  int
	pulled      bool
	healthy     bool
}

func (b *fakeBrain) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		healthy := b.healthy
		b.mu.Unlock()
		if !healthy {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	mux.HandleFunc("/embed", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Input []string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		b.mu.Lock()
		b.embedCalls++
		b.lastInput = append([]string(nil), req.Input...)
		status, dims, model := b.embedStatus, b.dims, b.model
		b.mu.Unlock()

		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("brain is unhappy"))
			return
		}

		vectors := make([][]float32, 0, len(req.Input))
		for range req.Input {
			vectors = append(vectors, make([]float32, dims))
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"embeddings": vectors, "model": model, "dimensions": dims,
		})
	})

	mux.HandleFunc("/embed/pull", func(w http.ResponseWriter, _ *http.Request) {
		b.mu.Lock()
		b.pullCalls++
		status, pulled, model := b.pullStatus, b.pulled, b.model
		b.mu.Unlock()

		if status != 0 && status != http.StatusOK {
			w.WriteHeader(status)
			_, _ = w.Write([]byte("no embedder configured"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pulled": pulled, "model": model})
	})

	return mux
}

func newRemoteHarness(t *testing.T, brain *fakeBrain) *RemoteEmbedder {
	t.Helper()
	if brain.dims == 0 {
		brain.dims = 4
	}
	if brain.model == "" {
		brain.model = "bge-m3:latest"
	}
	server := httptest.NewServer(brain.handler())
	t.Cleanup(server.Close)
	return NewRemoteEmbedder(RemoteEmbedderOptions{
		BrainURL: server.URL, Model: "bge-m3", Batch: 2, Timeout: 5 * time.Second,
	})
}

func TestRemoteEmbedderRoutesThroughTheBrain(t *testing.T) {
	brain := &fakeBrain{healthy: true}
	embedder := newRemoteHarness(t, brain)

	vectors, err := embedder.Embed(context.Background(), []string{"a", "b", "c"})
	requireNoError(t, err, "embed")

	if len(vectors) != 3 {
		t.Fatalf("got %d vectors, want 3", len(vectors))
	}
	// Batch size 2 means three inputs take two round trips.
	if brain.embedCalls != 2 {
		t.Fatalf("embed calls = %d, want 2 batches", brain.embedCalls)
	}
	// The brain owns the model, so its answer wins over local config —
	// otherwise /status could report a model that is not in use.
	if embedder.Model() != "bge-m3:latest" {
		t.Fatalf("model = %q, want the brain's answer", embedder.Model())
	}
}

func TestRemoteEmbedderDiscoversDimensions(t *testing.T) {
	brain := &fakeBrain{healthy: true, dims: 1024}
	embedder := newRemoteHarness(t, brain)

	dims, err := embedder.Dimensions(context.Background())
	requireNoError(t, err, "dimensions")
	if dims != 1024 {
		t.Fatalf("dimensions = %d", dims)
	}

	// Cached: a second call must not cost another round trip.
	before := brain.embedCalls
	if _, err := embedder.Dimensions(context.Background()); err != nil {
		t.Fatalf("second Dimensions: %v", err)
	}
	if brain.embedCalls != before {
		t.Fatal("Dimensions re-probed the brain instead of using the cache")
	}
}

func TestRemoteEmbedderEnsureModelAsksTheBrainToPull(t *testing.T) {
	brain := &fakeBrain{healthy: true, pulled: true}
	embedder := newRemoteHarness(t, brain)

	// An edge node has no shell on the brain; this is how it provisions.
	pulled, err := embedder.EnsureModel(context.Background())
	requireNoError(t, err, "ensure model")
	if !pulled || brain.pullCalls != 1 {
		t.Fatalf("pulled=%v calls=%d", pulled, brain.pullCalls)
	}
}

func TestRemoteEmbedderReportsBrainFailuresAsUnavailable(t *testing.T) {
	brain := &fakeBrain{healthy: true, embedStatus: http.StatusServiceUnavailable}
	embedder := newRemoteHarness(t, brain)

	_, err := embedder.Embed(context.Background(), []string{"a"})
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "brain returned 503")

	brain.embedStatus = 0
	brain.pullStatus = http.StatusServiceUnavailable
	_, err = embedder.EnsureModel(context.Background())
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "brain has no embedder")
}

func TestRemoteEmbedderHealthFollowsTheBrain(t *testing.T) {
	brain := &fakeBrain{healthy: true}
	embedder := newRemoteHarness(t, brain)

	requireNoError(t, embedder.Health(context.Background()), "health while up")

	brain.mu.Lock()
	brain.healthy = false
	brain.mu.Unlock()

	requireErrorIs(t, embedder.Health(context.Background()), ErrEmbeddingsUnavailable, "health while down")
}

func TestRemoteEmbedderNeedsABrainURL(t *testing.T) {
	embedder := NewRemoteEmbedder(RemoteEmbedderOptions{Model: "bge-m3"})

	_, err := embedder.Embed(context.Background(), []string{"a"})
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "embed with no brain url")

	_, err = embedder.EnsureModel(context.Background())
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "ensure with no brain url")

	requireErrorIs(t, embedder.Health(context.Background()), ErrEmbeddingsUnavailable, "health with no brain url")
}

func TestRemoteEmbedderUnreachableBrainKeepsDataQueued(t *testing.T) {
	embedder := NewRemoteEmbedder(RemoteEmbedderOptions{
		BrainURL: "http://127.0.0.1:1", Model: "bge-m3", Timeout: time.Second,
	})

	// The sentinel matters: the ingestion queue treats it as transient
	// and retries forever rather than parking the source.
	_, err := embedder.Embed(context.Background(), []string{"a"})
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "unreachable brain")
}

// Both embedders must be interchangeable wherever the service uses one.
var (
	_ Embedder         = (*RemoteEmbedder)(nil)
	_ ModelProvisioner = (*RemoteEmbedder)(nil)
	_ Embedder         = (*OllamaEmbedder)(nil)
	_ ModelProvisioner = (*OllamaEmbedder)(nil)
)
