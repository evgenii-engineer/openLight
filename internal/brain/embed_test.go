package brain

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type stubEmbedder struct {
	dims       int
	model      string
	embedErr   error
	ensureErr  error
	pulled     bool
	embedCalls int
	lastInput  []string
}

func (e *stubEmbedder) Embed(_ context.Context, inputs []string) ([][]float32, error) {
	e.embedCalls++
	e.lastInput = append([]string(nil), inputs...)
	if e.embedErr != nil {
		return nil, e.embedErr
	}
	out := make([][]float32, 0, len(inputs))
	for range inputs {
		out = append(out, make([]float32, e.dims))
	}
	return out, nil
}

func (e *stubEmbedder) EnsureModel(context.Context) (bool, error) {
	if e.ensureErr != nil {
		return false, e.ensureErr
	}
	return e.pulled, nil
}

func (e *stubEmbedder) Model() string { return e.model }

func newEmbedServer(t *testing.T, embedder Embedder) *httptest.Server {
	t.Helper()
	s := NewServer(nil, ":0", "test-brain", "test-model", nil)
	if embedder != nil {
		s.SetEmbedder(embedder)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /embed", s.handleEmbed)
	mux.HandleFunc("POST /embed/pull", s.handleEmbedPull)

	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func TestHandleEmbedReturnsOneVectorPerInput(t *testing.T) {
	embedder := &stubEmbedder{dims: 1024, model: "bge-m3:latest"}
	server := newEmbedServer(t, embedder)

	response, err := http.Post(server.URL+"/embed", "application/json",
		strings.NewReader(`{"input":["alpha","beta"]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", response.StatusCode)
	}

	var decoded struct {
		Embeddings [][]float32 `json:"embeddings"`
		Model      string      `json:"model"`
		Dimensions int         `json:"dimensions"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded.Embeddings) != 2 || len(decoded.Embeddings[0]) != 1024 {
		t.Fatalf("unexpected shape: %d vectors", len(decoded.Embeddings))
	}
	if decoded.Dimensions != 1024 || decoded.Model != "bge-m3:latest" {
		t.Fatalf("metadata wrong: %+v", decoded)
	}
	if len(embedder.lastInput) != 2 || embedder.lastInput[0] != "alpha" {
		t.Fatalf("inputs not forwarded: %+v", embedder.lastInput)
	}
}

func TestHandleEmbedWithoutAnEmbedderIsUnavailable(t *testing.T) {
	server := newEmbedServer(t, nil)

	// A brain that has not been given an embedder must say so plainly;
	// the edge treats 503 as "retry later" and keeps its data queued.
	for _, path := range []string{"/embed", "/embed/pull"} {
		response, err := http.Post(server.URL+path, "application/json", strings.NewReader(`{"input":["x"]}`))
		if err != nil {
			t.Fatalf("post %s: %v", path, err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusServiceUnavailable {
			t.Fatalf("%s status = %d, want 503", path, response.StatusCode)
		}
	}
}

func TestHandleEmbedRejectsEmptyInput(t *testing.T) {
	server := newEmbedServer(t, &stubEmbedder{dims: 4})

	for _, body := range []string{`{}`, `{"input":[]}`, `not json`} {
		response, err := http.Post(server.URL+"/embed", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatalf("post: %v", err)
		}
		_ = response.Body.Close()
		if response.StatusCode != http.StatusBadRequest {
			t.Fatalf("body %q gave status %d, want 400", body, response.StatusCode)
		}
	}
}

func TestHandleEmbedSurfacesEmbedderFailuresAsUnavailable(t *testing.T) {
	server := newEmbedServer(t, &stubEmbedder{dims: 4, embedErr: errors.New("ollama is down")})

	response, err := http.Post(server.URL+"/embed", "application/json", strings.NewReader(`{"input":["x"]}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()

	// 503, not 500: the edge's retry logic keys off transient failures.
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", response.StatusCode)
	}
}

func TestHandleEmbedPullReportsWhetherItFetched(t *testing.T) {
	server := newEmbedServer(t, &stubEmbedder{dims: 4, model: "bge-m3", pulled: true})

	response, err := http.Post(server.URL+"/embed/pull", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	defer response.Body.Close()

	var decoded struct {
		Pulled bool   `json:"pulled"`
		Model  string `json:"model"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !decoded.Pulled || decoded.Model != "bge-m3" {
		t.Fatalf("unexpected response: %+v", decoded)
	}
}
