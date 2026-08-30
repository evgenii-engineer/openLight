package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrEmbeddingsUnavailable marks a transient embedding failure: the
// brain node is down, the model is still loading, the request timed out.
// The queue retries these with backoff and never drops the source.
var ErrEmbeddingsUnavailable = errors.New("memory: embeddings unavailable")

// Embedder turns text into vectors. Embeddings run on the Mac mini, not
// the Pi — the Pi orchestrates, stores, and serves.
type Embedder interface {
	// Embed returns one vector per input, in order.
	Embed(ctx context.Context, inputs []string) ([][]float32, error)

	// Dimensions reports the vector width, discovering it on first use.
	Dimensions(ctx context.Context) (int, error)

	// Health probes the backend. Returns nil when embeddings can run.
	Health(ctx context.Context) error

	// Model names the embedding model, for status output.
	Model() string
}

// OllamaEmbedder calls Ollama's /api/embed endpoint. A dedicated
// embedding model is used — never the fast or smart generative model,
// whose hidden states make poor retrieval vectors and whose weights we
// do not want evicted from VRAM by embedding traffic.
type OllamaEmbedder struct {
	endpoint  string
	model     string
	keepAlive string
	batch     int
	client    *http.Client

	// dims is discovered on the first successful call and cached; a
	// value of 0 means "not yet known".
	dims int
}

// OllamaEmbedderOptions configures the embedder.
type OllamaEmbedderOptions struct {
	Endpoint string
	Model    string
	// KeepAlive is passed straight through to Ollama. Embedding models
	// are small; keeping one resident avoids a reload per document.
	KeepAlive string
	// Batch caps how many texts go in one request. Ollama handles
	// batches fine, but a Pi-side timeout is easier to reason about with
	// modest batches.
	Batch   int
	Timeout time.Duration
}

func NewOllamaEmbedder(opts OllamaEmbedderOptions) *OllamaEmbedder {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	batch := opts.Batch
	if batch <= 0 {
		batch = 16
	}
	return &OllamaEmbedder{
		endpoint:  strings.TrimRight(strings.TrimSpace(opts.Endpoint), "/"),
		model:     strings.TrimSpace(opts.Model),
		keepAlive: strings.TrimSpace(opts.KeepAlive),
		batch:     batch,
		client:    &http.Client{Timeout: timeout},
	}
}

func (e *OllamaEmbedder) Model() string { return e.model }

func (e *OllamaEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if e.endpoint == "" || e.model == "" {
		return nil, fmt.Errorf("%w: embeddings endpoint or model is not configured", ErrEmbeddingsUnavailable)
	}

	out := make([][]float32, 0, len(inputs))
	for start := 0; start < len(inputs); start += e.batch {
		end := start + e.batch
		if end > len(inputs) {
			end = len(inputs)
		}
		vectors, err := e.embedBatch(ctx, inputs[start:end])
		if err != nil {
			return nil, err
		}
		out = append(out, vectors...)
	}
	if len(out) != len(inputs) {
		return nil, fmt.Errorf("%w: expected %d vectors, got %d", ErrEmbeddingsUnavailable, len(inputs), len(out))
	}
	if len(out) > 0 {
		e.dims = len(out[0])
	}
	return out, nil
}

type ollamaEmbedRequest struct {
	Model     string   `json:"model"`
	Input     []string `json:"input"`
	KeepAlive any      `json:"keep_alive,omitempty"`
}

type ollamaEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Error      string      `json:"error"`
}

func (e *OllamaEmbedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	payload := ollamaEmbedRequest{Model: e.model, Input: inputs}
	if e.keepAlive != "" {
		payload.KeepAlive = e.keepAlive
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read embed response: %v", ErrEmbeddingsUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: embed returned %d: %s", ErrEmbeddingsUnavailable, response.StatusCode, truncate(string(content), 200))
	}

	var decoded ollamaEmbedResponse
	if err := json.Unmarshal(content, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode embed response: %v", ErrEmbeddingsUnavailable, err)
	}
	if strings.TrimSpace(decoded.Error) != "" {
		return nil, fmt.Errorf("%w: %s", ErrEmbeddingsUnavailable, decoded.Error)
	}
	if len(decoded.Embeddings) == 0 {
		return nil, fmt.Errorf("%w: embed returned no vectors", ErrEmbeddingsUnavailable)
	}
	return decoded.Embeddings, nil
}

func (e *OllamaEmbedder) Dimensions(ctx context.Context) (int, error) {
	if e.dims > 0 {
		return e.dims, nil
	}
	vectors, err := e.Embed(ctx, []string{"dimension probe"})
	if err != nil {
		return 0, err
	}
	if len(vectors) == 0 || len(vectors[0]) == 0 {
		return 0, fmt.Errorf("%w: probe returned an empty vector", ErrEmbeddingsUnavailable)
	}
	e.dims = len(vectors[0])
	return e.dims, nil
}

// ModelProvisioner is implemented by embedders that can fetch their own
// model. The service uses it to pull the embedding model on first start
// instead of making the operator remember `ollama pull` — the same
// self-provisioning the compose stack already does for the chat model.
type ModelProvisioner interface {
	// EnsureModel makes the configured model available, reporting
	// whether a pull actually happened.
	EnsureModel(ctx context.Context) (pulled bool, err error)
}

// EnsureModel checks whether the embedding model is present on the brain
// node and pulls it when it is not.
//
// A pull of a multilingual embedding model is on the order of a gigabyte
// and several minutes, so it deliberately ignores the embedder's normal
// request timeout and is bounded only by the caller's context. It is
// also strictly best-effort: a failure here degrades memory exactly like
// any other backend outage, and the ingestion queue keeps the data.
func (e *OllamaEmbedder) EnsureModel(ctx context.Context) (bool, error) {
	if e.endpoint == "" || e.model == "" {
		return false, fmt.Errorf("%w: endpoint or model is not configured", ErrEmbeddingsUnavailable)
	}

	present, err := e.hasModel(ctx)
	if err != nil {
		return false, err
	}
	if present {
		return false, nil
	}

	body, err := json.Marshal(map[string]any{"model": e.model, "stream": false})
	if err != nil {
		return false, fmt.Errorf("marshal pull request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint+"/api/pull", bytes.NewReader(body))
	if err != nil {
		return false, fmt.Errorf("build pull request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	// A dedicated client: e.client's timeout is sized for an embedding
	// call, not for a multi-gigabyte download.
	puller := &http.Client{}
	response, err := puller.Do(request)
	if err != nil {
		return false, fmt.Errorf("%w: pull %s: %v", ErrEmbeddingsUnavailable, e.model, err)
	}
	defer response.Body.Close()

	content, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: pull %s returned %d: %s",
			ErrEmbeddingsUnavailable, e.model, response.StatusCode, truncate(string(content), 200))
	}

	// Ollama answers 200 with {"status":"..."} even for some failures,
	// so confirm the model actually landed rather than trusting the code.
	present, err = e.hasModel(ctx)
	if err != nil {
		return false, err
	}
	if !present {
		return false, fmt.Errorf("%w: %s still missing after pull: %s",
			ErrEmbeddingsUnavailable, e.model, truncate(string(content), 200))
	}
	return true, nil
}

type ollamaTagsResponse struct {
	Models []struct {
		Name  string `json:"name"`
		Model string `json:"model"`
	} `json:"models"`
}

// hasModel reports whether the configured model is installed. Ollama
// normalises a bare name to ":latest", so both forms are accepted.
func (e *OllamaEmbedder) hasModel(ctx context.Context) (bool, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, e.endpoint+"/api/tags", nil)
	if err != nil {
		return false, fmt.Errorf("build tags request: %w", err)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return false, fmt.Errorf("%w: %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return false, fmt.Errorf("%w: read tags: %v", ErrEmbeddingsUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: /api/tags returned %d", ErrEmbeddingsUnavailable, response.StatusCode)
	}

	var decoded ollamaTagsResponse
	if err := json.Unmarshal(content, &decoded); err != nil {
		return false, fmt.Errorf("%w: decode tags: %v", ErrEmbeddingsUnavailable, err)
	}

	wanted := strings.ToLower(strings.TrimSpace(e.model))
	for _, model := range decoded.Models {
		for _, name := range []string{model.Name, model.Model} {
			name = strings.ToLower(strings.TrimSpace(name))
			if name == "" {
				continue
			}
			if name == wanted || name == wanted+":latest" || strings.TrimSuffix(name, ":latest") == wanted {
				return true, nil
			}
		}
	}
	return false, nil
}

func (e *OllamaEmbedder) Health(ctx context.Context) error {
	if e.endpoint == "" || e.model == "" {
		return fmt.Errorf("%w: not configured", ErrEmbeddingsUnavailable)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, e.endpoint+"/api/tags", nil)
	if err != nil {
		return fmt.Errorf("build embed health request: %w", err)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: /api/tags returned %d", ErrEmbeddingsUnavailable, response.StatusCode)
	}
	return nil
}
