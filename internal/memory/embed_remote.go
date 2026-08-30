package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// RemoteEmbedder computes vectors on the brain node through openLight's
// own brain API instead of talking to Ollama directly.
//
// This is the only workable path on the normal edge/brain topology:
// Ollama binds to loopback on the brain node, so an edge node cannot
// reach :11434 at all, and exposing it to the tailnet purely to serve
// embeddings would widen the brain's attack surface for no benefit. LLM
// inference (internal/llm/remote.go) and whisper (internal/voice/remote.go)
// already route this way; embeddings now do too.
type RemoteEmbedder struct {
	brainURL string
	model    string
	batch    int
	client   *http.Client

	dims int
}

// RemoteEmbedderOptions configures the brain-routed embedder.
type RemoteEmbedderOptions struct {
	// BrainURL is the brain node's API base, e.g. "http://brain:8787".
	BrainURL string

	// Model is reported for status output. The brain owns the actual
	// model choice; this is what the edge believes it is using.
	Model string

	Batch   int
	Timeout time.Duration
}

func NewRemoteEmbedder(opts RemoteEmbedderOptions) *RemoteEmbedder {
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	batch := opts.Batch
	if batch <= 0 {
		batch = 16
	}
	return &RemoteEmbedder{
		brainURL: strings.TrimRight(strings.TrimSpace(opts.BrainURL), "/"),
		model:    strings.TrimSpace(opts.Model),
		batch:    batch,
		client:   &http.Client{Timeout: timeout},
	}
}

func (e *RemoteEmbedder) Model() string { return e.model }

func (e *RemoteEmbedder) Embed(ctx context.Context, inputs []string) ([][]float32, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	if e.brainURL == "" {
		return nil, fmt.Errorf("%w: brain url is not configured", ErrEmbeddingsUnavailable)
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

type brainEmbedResponse struct {
	Embeddings [][]float32 `json:"embeddings"`
	Model      string      `json:"model"`
	Dimensions int         `json:"dimensions"`
}

func (e *RemoteEmbedder) embedBatch(ctx context.Context, inputs []string) ([][]float32, error) {
	body, err := json.Marshal(map[string]any{"input": inputs})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.brainURL+"/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build embed request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := e.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("%w: brain %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()

	content, err := io.ReadAll(io.LimitReader(response.Body, 64<<20))
	if err != nil {
		return nil, fmt.Errorf("%w: read brain embed response: %v", ErrEmbeddingsUnavailable, err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: brain /embed returned %d: %s",
			ErrEmbeddingsUnavailable, response.StatusCode, truncate(string(content), 200))
	}

	var decoded brainEmbedResponse
	if err := json.Unmarshal(content, &decoded); err != nil {
		return nil, fmt.Errorf("%w: decode brain embed response: %v", ErrEmbeddingsUnavailable, err)
	}
	if len(decoded.Embeddings) == 0 {
		return nil, fmt.Errorf("%w: brain returned no vectors", ErrEmbeddingsUnavailable)
	}
	// The brain owns the model; trust its answer over local config so
	// status output cannot silently drift from reality.
	if strings.TrimSpace(decoded.Model) != "" {
		e.model = decoded.Model
	}
	return decoded.Embeddings, nil
}

func (e *RemoteEmbedder) Dimensions(ctx context.Context) (int, error) {
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

// EnsureModel asks the brain to make the embedding model available. The
// edge node has no shell access to the brain, so this is how a fresh
// install provisions it.
func (e *RemoteEmbedder) EnsureModel(ctx context.Context) (bool, error) {
	if e.brainURL == "" {
		return false, fmt.Errorf("%w: brain url is not configured", ErrEmbeddingsUnavailable)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, e.brainURL+"/embed/pull", strings.NewReader("{}"))
	if err != nil {
		return false, fmt.Errorf("build pull request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	// No client timeout: the brain may be downloading a gigabyte, and
	// only the caller's context should bound that.
	puller := &http.Client{}
	response, err := puller.Do(request)
	if err != nil {
		return false, fmt.Errorf("%w: brain %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()

	content, _ := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("%w: brain /embed/pull returned %d: %s",
			ErrEmbeddingsUnavailable, response.StatusCode, truncate(string(content), 200))
	}

	var decoded struct {
		Pulled bool   `json:"pulled"`
		Model  string `json:"model"`
	}
	if err := json.Unmarshal(content, &decoded); err != nil {
		return false, fmt.Errorf("%w: decode pull response: %v", ErrEmbeddingsUnavailable, err)
	}
	if strings.TrimSpace(decoded.Model) != "" {
		e.model = decoded.Model
	}
	return decoded.Pulled, nil
}

// Health probes the brain node itself. A brain that is up but has no
// embedder configured surfaces on the first Embed call rather than here,
// which keeps /status cheap.
func (e *RemoteEmbedder) Health(ctx context.Context) error {
	if e.brainURL == "" {
		return fmt.Errorf("%w: brain url is not configured", ErrEmbeddingsUnavailable)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, e.brainURL+"/health", nil)
	if err != nil {
		return fmt.Errorf("build health request: %w", err)
	}
	response, err := e.client.Do(request)
	if err != nil {
		return fmt.Errorf("%w: brain %v", ErrEmbeddingsUnavailable, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: brain /health returned %d", ErrEmbeddingsUnavailable, response.StatusCode)
	}
	return nil
}
