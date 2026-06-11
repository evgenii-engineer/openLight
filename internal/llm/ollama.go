package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultOllamaKeepAlive = "-1"

// keepAliveValue converts a user-provided keep_alive string into the JSON
// shape Ollama actually accepts. Ollama parses duration strings ("30m",
// "1h", "24h") but rejects bare numeric strings like "-1" with a
// "missing unit in duration" error. Pure numerics must be sent as raw
// JSON numbers: -1 = pin forever, 0 = unload immediately, N = seconds.
func keepAliveValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		raw = defaultOllamaKeepAlive
	}
	if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
		return n
	}
	return raw
}

type OllamaProvider struct {
	endpoint  string
	model     string
	keepAlive string
	numCtx    int
	think     bool
	client    *http.Client
	logger    *slog.Logger
}

// OllamaProviderOptions bundles optional knobs that have grown past what a
// positional constructor can carry cleanly.
type OllamaProviderOptions struct {
	KeepAlive string
	// NumCtx caps the context window Ollama allocates for this profile.
	// Zero leaves Ollama on its model default (typically 4096 in Ollama,
	// even for models that natively support 128k). Smaller values free
	// significant VRAM for the KV cache — useful on Mac mini where SMART
	// and FAST share GPU memory.
	NumCtx int
	// Think enables Gemma 4's reasoning mode. Use for coding, planning,
	// and architecture tasks. Avoid for normal conversation (high latency).
	Think bool
}

func NewOllamaProvider(endpoint, model string, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	return NewOllamaProviderWithOptions(endpoint, model, OllamaProviderOptions{}, timeout, logger)
}

// NewOllamaProviderWithKeepAlive lets callers pin a model in memory across
// long idle periods. Accepts a Go duration string ("30m", "168h") or "-1"
// to keep the model loaded indefinitely. Empty string falls back to 30m.
func NewOllamaProviderWithKeepAlive(endpoint, model, keepAlive string, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	return NewOllamaProviderWithOptions(endpoint, model, OllamaProviderOptions{KeepAlive: keepAlive}, timeout, logger)
}

// NewOllamaProviderWithOptions is the full constructor. Use it when you
// need to set num_ctx or any future per-profile knob alongside keep_alive.
func NewOllamaProviderWithOptions(endpoint, model string, opts OllamaProviderOptions, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	keepAlive := strings.TrimSpace(opts.KeepAlive)
	if keepAlive == "" {
		keepAlive = defaultOllamaKeepAlive
	}
	numCtx := opts.NumCtx
	if numCtx < 0 {
		numCtx = 0
	}
	return &OllamaProvider{
		endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		model:     strings.TrimSpace(model),
		keepAlive: keepAlive,
		numCtx:    numCtx,
		think:     opts.Think,
		client: &http.Client{
			Timeout:   timeout,
			Transport: newOllamaTransport(),
		},
		logger: logger,
	}
}

// newOllamaTransport returns an http.Transport tuned for high-frequency calls
// to a co-located Ollama daemon. Defaults matter here: net/http's
// DefaultTransport caps MaxIdleConnsPerHost at 2 and leaves HTTP/2 negotiation
// on, which forces a TLS-style ALPN handshake on every cold connection — wasted
// time when we're talking to http://127.0.0.1:11434. Reusing one keep-alive
// connection across route → skill classifier calls eliminates per-request
// TCP/handshake overhead, the second-largest source of fixed latency after
// prefill on a Mac mini.
func newOllamaTransport() *http.Transport {
	return &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   5 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     false,
		MaxIdleConns:          16,
		MaxIdleConnsPerHost:   8,
		MaxConnsPerHost:       8,
		IdleConnTimeout:       5 * time.Minute,
		TLSHandshakeTimeout:   5 * time.Second,
		ExpectContinueTimeout: 0,
		DisableCompression:    true,
	}
}

func (p *OllamaProvider) ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error) {
	prompt := buildOllamaRoutePrompt(limitText(text, request.InputChars), request)
	responseText, err := p.generate(ctx, prompt, routeResponseSchema(groupKeys(request.Groups)), 0.0, decisionNumPredict(request.NumPredict))
	if err != nil {
		return RouteClassification{}, err
	}
	if p.logger != nil {
		p.logger.Debug("ollama route raw response", "response", responseText)
	}

	classification, err := parseOllamaRouteClassification(responseText, groupKeys(request.Groups))
	if err != nil {
		return RouteClassification{}, fmt.Errorf("decode ollama route response: %w", err)
	}

	return normalizeRouteClassification(classification), nil
}

func (p *OllamaProvider) ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error) {
	prompt := buildOllamaSkillPrompt(limitText(text, request.InputChars), request)
	responseText, err := p.generate(
		ctx,
		prompt,
		skillResponseSchema(request.AllowedSkills, request.AllowedServices, request.AllowedRuntimes),
		0.0,
		decisionNumPredict(request.NumPredict),
	)
	if err != nil {
		return Classification{}, err
	}
	if p.logger != nil {
		p.logger.Debug("ollama skill raw response", "response", responseText)
	}

	classification, err := parseOllamaSkillClassification(
		responseText,
		request.AllowedSkills,
		allowedArgumentKeysForSkills(request.AllowedSkills),
	)
	if err != nil {
		return Classification{}, fmt.Errorf("decode ollama skill response: %w", err)
	}

	return normalizeClassification(classification), nil
}

func (p *OllamaProvider) Summarize(ctx context.Context, text string) (string, error) {
	responseText, err := p.generate(ctx, buildSummaryPrompt(text), summaryResponseSchema(), 0.0, 64)
	if err != nil {
		return "", err
	}

	var response struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(extractJSON(responseText)), &response); err != nil {
		return "", fmt.Errorf("decode ollama summary response: %w", err)
	}

	return strings.TrimSpace(response.Summary), nil
}

func (p *OllamaProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	options := map[string]any{
		"temperature":    0.2,
		"top_p":          0.9,
		"repeat_penalty": 1.05,
		"num_predict":    64,
	}
	if p.numCtx > 0 {
		options["num_ctx"] = p.numCtx
	}
	payload := map[string]any{
		"model":      p.model,
		"messages":   messages,
		"stream":     false,
		"think":      p.think,
		"keep_alive": keepAliveValue(p.keepAlive),
		"options":    options,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal ollama chat payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create ollama chat request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	startedAt := time.Now()
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("call ollama chat endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("ollama chat endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode ollama chat response: %w", err)
	}

	latencyMS := time.Since(startedAt).Milliseconds()
	content := strings.TrimSpace(decoded.Message.Content)
	if p.logger != nil {
		p.logger.Debug("ollama chat completed",
			"messages", len(messages),
			"prompt_chars", totalChatMessageChars(messages),
			"response_chars", utf8.RuneCountInString(content),
			"latency_ms", latencyMS,
		)
	}

	return content, nil
}

// Prewarm fires a 1-token generation so Ollama loads the model into memory
// using the provider's stored keep_alive. Implements the Prewarmer
// interface for callers that don't need per-call overrides.
func (p *OllamaProvider) Prewarm(ctx context.Context) error {
	return p.PrewarmWith(ctx, PrewarmOptions{})
}

// PrewarmWith lets the caller override the warmup prompt and keep_alive
// per request. Useful for the always-warm policy where the warmup payload
// pins the model (keep_alive=-1) regardless of the provider's default
// (which applies to normal traffic).
func (p *OllamaProvider) PrewarmWith(ctx context.Context, opts PrewarmOptions) error {
	if strings.TrimSpace(p.model) == "" {
		return fmt.Errorf("ollama: prewarm requires a model name")
	}

	prompt := strings.TrimSpace(opts.Prompt)
	if prompt == "" {
		prompt = "warmup"
	}
	keepAlive := strings.TrimSpace(opts.KeepAlive)
	if keepAlive == "" {
		keepAlive = p.keepAlive
	}

	options := map[string]any{
		"temperature": 0.0,
		"num_predict": 1,
	}
	if p.numCtx > 0 {
		options["num_ctx"] = p.numCtx
	}
	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"think":      false, // warmup always skips reasoning
		"keep_alive": keepAliveValue(keepAlive),
		"options":    options,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal ollama prewarm payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create ollama prewarm request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	startedAt := time.Now()
	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("call ollama prewarm endpoint: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	latencyMS := time.Since(startedAt).Milliseconds()
	if response.StatusCode >= http.StatusBadRequest {
		if p.logger != nil {
			p.logger.Warn("ollama prewarm returned error status",
				"model", p.model,
				"status", response.StatusCode,
				"latency_ms", latencyMS,
			)
		}
		return fmt.Errorf("ollama prewarm returned %d", response.StatusCode)
	}

	if p.logger != nil {
		p.logger.Info("ollama prewarm completed",
			"model", p.model,
			"latency_ms", latencyMS,
			"keep_alive", keepAlive,
		)
	}
	return nil
}

// LoadedModel describes a single entry from Ollama's /api/ps response.
type LoadedModel struct {
	Name         string    `json:"name"`
	Model        string    `json:"model"`
	Size         int64     `json:"size"`
	SizeVRAM     int64     `json:"size_vram"`
	Digest       string    `json:"digest"`
	ExpiresAt    time.Time `json:"expires_at"`
	Processor    string    `json:"processor,omitempty"`
	ContextLen   int       `json:"context_length,omitempty"`
	Details      struct {
		Format            string   `json:"format,omitempty"`
		Family            string   `json:"family,omitempty"`
		Families          []string `json:"families,omitempty"`
		ParameterSize     string   `json:"parameter_size,omitempty"`
		QuantizationLevel string   `json:"quantization_level,omitempty"`
	} `json:"details"`
}

// PsLister exposes Ollama's /api/ps endpoint. Implemented by OllamaProvider
// so callers can render which models are currently resident without
// coupling to the concrete type.
type PsLister interface {
	ListLoadedModels(ctx context.Context) ([]LoadedModel, error)
}

// ErrPsListerUnsupported is returned by decorators that forward PsLister
// when the wrapped provider doesn't actually implement /api/ps. Callers
// can compare with errors.Is to surface "Ollama loaded: unavailable"
// rather than treating the error as a transient failure.
var ErrPsListerUnsupported = errors.New("llm: provider does not expose /api/ps")

// ListLoadedModels queries Ollama for the set of models currently held in
// memory. Returns an empty slice (not an error) when no models are loaded.
func (p *OllamaProvider) ListLoadedModels(ctx context.Context) ([]LoadedModel, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, p.endpoint+"/api/ps", nil)
	if err != nil {
		return nil, fmt.Errorf("create ollama ps request: %w", err)
	}

	response, err := p.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("call ollama ps endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return nil, fmt.Errorf("ollama ps endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded struct {
		Models []LoadedModel `json:"models"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode ollama ps response: %w", err)
	}
	return decoded.Models, nil
}

func (p *OllamaProvider) generate(ctx context.Context, prompt string, format any, temperature float64, numPredict int) (string, error) {
	options := map[string]any{
		"temperature": temperature,
		"top_p":       0.9,
		"num_predict": numPredict,
	}
	if p.numCtx > 0 {
		options["num_ctx"] = p.numCtx
	}
	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"think":      p.think,
		"format":     format,
		"keep_alive": keepAliveValue(p.keepAlive),
		"options":    options,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal ollama payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create ollama request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	startedAt := time.Now()
	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("call ollama endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("ollama endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded struct {
		Response string `json:"response"`
		Thinking string `json:"thinking"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	latencyMS := time.Since(startedAt).Milliseconds()
	responseText := strings.TrimSpace(decoded.Response)
	thinkingText := strings.TrimSpace(decoded.Thinking)
	if p.logger != nil {
		p.logger.Debug("ollama generate completed",
			"prompt_chars", utf8.RuneCountInString(prompt),
			"response_chars", utf8.RuneCountInString(responseText),
			"thinking_chars", utf8.RuneCountInString(thinkingText),
			"latency_ms", latencyMS,
		)
	}
	if responseText == "" {
		if thinkingText != "" {
			return "", fmt.Errorf("empty ollama response text (thinking output present)")
		}
		return "", fmt.Errorf("empty ollama response text")
	}

	return responseText, nil
}
