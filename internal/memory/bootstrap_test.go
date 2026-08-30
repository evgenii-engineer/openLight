package memory

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeOllama stands in for the brain node's Ollama daemon.
type fakeOllama struct {
	mu sync.Mutex

	// installed is the model list /api/tags reports.
	installed []string

	// pulls records the models /api/pull was asked for.
	pulls []string

	// pullInstalls makes a pull actually add the model, as a real
	// daemon would. Setting it false simulates a pull that reports
	// success but leaves nothing behind.
	pullInstalls bool

	// pullStatus overrides the HTTP status returned by /api/pull.
	pullStatus int
}

func (f *fakeOllama) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		type model struct {
			Name  string `json:"name"`
			Model string `json:"model"`
		}
		payload := struct {
			Models []model `json:"models"`
		}{}
		for _, name := range f.installed {
			payload.Models = append(payload.Models, model{Name: name, Model: name})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	})

	mux.HandleFunc("/api/pull", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)

		f.mu.Lock()
		f.pulls = append(f.pulls, body.Model)
		if f.pullInstalls {
			f.installed = append(f.installed, body.Model+":latest")
		}
		status := f.pullStatus
		f.mu.Unlock()

		if status == 0 {
			status = http.StatusOK
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"success"}`))
	})

	return mux
}

func (f *fakeOllama) pullCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.pulls)
}

func newFakeOllama(t *testing.T, ollama *fakeOllama) *OllamaEmbedder {
	t.Helper()
	server := httptest.NewServer(ollama.handler())
	t.Cleanup(server.Close)
	return NewOllamaEmbedder(OllamaEmbedderOptions{
		Endpoint: server.URL,
		Model:    "bge-m3",
		Timeout:  5 * time.Second,
	})
}

func TestEnsureModelIsANoOpWhenTheModelIsAlreadyThere(t *testing.T) {
	// Ollama normalises a bare name to ":latest"; the check must accept
	// both forms or every start would trigger a redundant pull.
	for _, installed := range []string{"bge-m3", "bge-m3:latest"} {
		ollama := &fakeOllama{installed: []string{installed}}
		embedder := newFakeOllama(t, ollama)

		pulled, err := embedder.EnsureModel(context.Background())
		requireNoError(t, err, "ensure model")

		if pulled {
			t.Fatalf("installed as %q: reported a pull that did not happen", installed)
		}
		if ollama.pullCount() != 0 {
			t.Fatalf("installed as %q: pulled anyway", installed)
		}
	}
}

func TestEnsureModelPullsWhenMissing(t *testing.T) {
	ollama := &fakeOllama{installed: []string{"qwen2.5:1.5b-instruct-q4_K_M"}, pullInstalls: true}
	embedder := newFakeOllama(t, ollama)

	pulled, err := embedder.EnsureModel(context.Background())
	requireNoError(t, err, "ensure model")

	if !pulled {
		t.Fatal("expected the model to be pulled")
	}
	if ollama.pullCount() != 1 || ollama.pulls[0] != "bge-m3" {
		t.Fatalf("wrong pull: %+v", ollama.pulls)
	}

	// A second call is a no-op now that the model is present.
	pulled, err = embedder.EnsureModel(context.Background())
	requireNoError(t, err, "second ensure")
	if pulled || ollama.pullCount() != 1 {
		t.Fatalf("re-pulled an installed model: pulled=%v pulls=%+v", pulled, ollama.pulls)
	}
}

func TestEnsureModelRejectsAPullThatSilentlyDidNothing(t *testing.T) {
	// Ollama answers 200 with {"status":...} even for some failures, so
	// success is confirmed by re-reading the model list, not by the code.
	ollama := &fakeOllama{pullInstalls: false}
	embedder := newFakeOllama(t, ollama)

	_, err := embedder.EnsureModel(context.Background())
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "pull that installed nothing")
	if !strings.Contains(err.Error(), "still missing") {
		t.Fatalf("error should say the model never landed: %v", err)
	}
}

func TestEnsureModelSurfacesPullFailures(t *testing.T) {
	ollama := &fakeOllama{pullStatus: http.StatusInternalServerError}
	embedder := newFakeOllama(t, ollama)

	_, err := embedder.EnsureModel(context.Background())
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "failed pull")
}

func TestEnsureModelReportsAnUnreachableBrain(t *testing.T) {
	embedder := NewOllamaEmbedder(OllamaEmbedderOptions{
		Endpoint: "http://127.0.0.1:1",
		Model:    "bge-m3",
		Timeout:  time.Second,
	})

	_, err := embedder.EnsureModel(context.Background())
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "unreachable brain")
}

// provisioningEmbedder wraps the fake embedder with EnsureModel so the
// service-level bootstrap can be tested without HTTP.
type provisioningEmbedder struct {
	*fakeEmbedder

	mu      sync.Mutex
	calls   int
	failFor int // fail this many times before succeeding
}

func (e *provisioningEmbedder) EnsureModel(context.Context) (bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.calls <= e.failFor {
		return false, ErrEmbeddingsUnavailable
	}
	return e.calls == e.failFor+1, nil
}

func (e *provisioningEmbedder) callsMade() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

func TestProvisionCreatesTheCollectionOnStart(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	embedder := &provisioningEmbedder{fakeEmbedder: h.embedder}
	h.service.deps.Embedder = embedder

	// Nothing has been ingested, so nothing has created the collection
	// yet — that is exactly the state a fresh install starts in.
	if h.vectors.Created() {
		t.Fatal("collection existed before bootstrap")
	}

	requireNoError(t, h.service.provision(ctx), "provision")

	if embedder.callsMade() != 1 {
		t.Fatalf("EnsureModel called %d times, want 1", embedder.callsMade())
	}
	if !h.vectors.Created() {
		t.Fatal("bootstrap did not create the vector collection")
	}
}

func TestProvisionFailsWhenTheBrainIsAsleep(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t, nil)

	h.embedder.setDown(true)

	err := h.service.provision(ctx)
	requireErrorIs(t, err, ErrEmbeddingsUnavailable, "provision with embeddings down")
	if h.vectors.Created() {
		t.Fatal("collection was created despite an unknown vector width")
	}
}

func TestBootstrapLoopRetriesUntilTheBrainWakesUp(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	embedder := &provisioningEmbedder{fakeEmbedder: h.embedder, failFor: 1}
	h.service.deps.Embedder = embedder

	done := make(chan struct{})
	go func() {
		h.service.bootstrapLoop(ctx, 10*time.Millisecond)
		close(done)
	}()

	// First attempt fails, the loop backs off, then succeeds. The delay
	// is passed in so the test does not wait out the production one.
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		cancel()
		t.Fatal("bootstrap never converged")
	}

	if embedder.callsMade() < 2 {
		t.Fatalf("expected a retry, got %d attempts", embedder.callsMade())
	}
	if !h.vectors.Created() {
		t.Fatal("collection was not created after the retry")
	}
}

func TestBootstrapLoopStopsOnShutdown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	h := newHarness(t, nil)
	h.embedder.setDown(true) // never succeeds

	done := make(chan struct{})
	go func() {
		h.service.bootstrapLoop(ctx, 10*time.Millisecond)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("bootstrapLoop ignored cancellation")
	}
}

func TestRunSkipsBootstrapWhenAutoProvisionIsOff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := newHarness(t, nil)
	embedder := &provisioningEmbedder{fakeEmbedder: h.embedder}
	h.service.deps.Embedder = embedder
	h.service.opts.AutoProvision = false

	done := make(chan error, 1)
	go func() { done <- h.service.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done

	if embedder.callsMade() != 0 {
		t.Fatalf("auto-provision disabled but EnsureModel ran %d times", embedder.callsMade())
	}
}
