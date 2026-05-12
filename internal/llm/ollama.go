package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const defaultOllamaKeepAlive = "30m"

// keepAliveValue converts a user-provided keep_alive string into the JSON
// shape Ollama actually accepts. Ollama parses duration strings ("30m",
// "1h", "24h") but rejects bare numeric strings like "-1" with a
// "missing unit in duration" error. Pure numerics must be sent as raw
// JSON numbers: -1 = pin forever, 0 = unload immediately, N = seconds.
func keepAliveValue(raw string) any {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultOllamaKeepAlive
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
	client    *http.Client
	logger    *slog.Logger
}

func NewOllamaProvider(endpoint, model string, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	return NewOllamaProviderWithKeepAlive(endpoint, model, "", timeout, logger)
}

// NewOllamaProviderWithKeepAlive lets callers pin a model in memory across
// long idle periods. Accepts a Go duration string ("30m", "168h") or "-1"
// to keep the model loaded indefinitely. Empty string falls back to 30m.
func NewOllamaProviderWithKeepAlive(endpoint, model, keepAlive string, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	keepAlive = strings.TrimSpace(keepAlive)
	if keepAlive == "" {
		keepAlive = defaultOllamaKeepAlive
	}
	return &OllamaProvider{
		endpoint:  strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		model:     strings.TrimSpace(model),
		keepAlive: keepAlive,
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
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
	payload := map[string]any{
		"model":      p.model,
		"messages":   messages,
		"stream":     false,
		"think":      false,
		"keep_alive": keepAliveValue(p.keepAlive),
		"options": map[string]any{
			"temperature":    0.2,
			"top_p":          0.9,
			"repeat_penalty": 1.05,
			"num_predict":    64,
		},
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

	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"think":      false,
		"keep_alive": keepAliveValue(keepAlive),
		"options": map[string]any{
			"temperature": 0.0,
			"num_predict": 1,
		},
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
	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"think":      false,
		"format":     format,
		"keep_alive": keepAliveValue(p.keepAlive),
		"options": map[string]any{
			"temperature": temperature,
			"top_p":       0.9,
			"num_predict": numPredict,
		},
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
