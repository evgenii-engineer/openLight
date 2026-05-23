package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ProviderConfig describes how to construct a Provider implementation.
type ProviderConfig struct {
	Provider string
	Endpoint string
	Model    string
	APIKey   string
	Timeout  time.Duration
	Logger   *slog.Logger
}

// BuildProvider returns a Provider for the configured backend, or a nil
// provider when the backend is "none" or empty (callers should disable
// vision in that case).
func BuildProvider(cfg ProviderConfig) (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	switch provider {
	case "", "none":
		return nil, nil
	case "ollama", "qwen_vl", "moondream", "minicpm", "llava":
		return newOllamaProvider(cfg), nil
	case "openai":
		return newOpenAIVisionProvider(cfg), nil
	case "http", "generic":
		return newGenericHTTPProvider(cfg), nil
	default:
		return nil, fmt.Errorf("vision: unsupported provider %q", provider)
	}
}

type httpClient struct {
	client  *http.Client
	logger  *slog.Logger
	timeout time.Duration
}

func newHTTPClient(timeout time.Duration, logger *slog.Logger) httpClient {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return httpClient{
		client:  &http.Client{Timeout: timeout + 5*time.Second},
		logger:  logger,
		timeout: timeout,
	}
}

type ollamaProvider struct {
	endpoint string
	model    string
	http     httpClient
}

func newOllamaProvider(cfg ProviderConfig) *ollamaProvider {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "qwen2.5vl:3b"
	}
	return &ollamaProvider{
		endpoint: endpoint,
		model:    model,
		http:     newHTTPClient(cfg.Timeout, cfg.Logger),
	}
}

// Prewarm fires a no-op text-only generation so Ollama loads the vision
// model into memory. The first AnalyzeImage call would otherwise pay a
// 10-30s cold-start cost on Mac mini hardware.
func (p *ollamaProvider) Prewarm(ctx context.Context) error {
	if strings.TrimSpace(p.model) == "" {
		return fmt.Errorf("ollama vision: prewarm requires a model name")
	}
	payload := map[string]any{
		"model":      p.model,
		"prompt":     "hi",
		"stream":     false,
		"keep_alive": -1,
		"options": map[string]any{
			"temperature": 0.0,
			"num_predict": 1,
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("ollama vision: marshal prewarm: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("ollama vision: build prewarm request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.http.client.Do(request)
	if err != nil {
		return fmt.Errorf("ollama vision: %w", err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	if response.StatusCode >= http.StatusBadRequest {
		return fmt.Errorf("ollama vision: prewarm returned %d", response.StatusCode)
	}
	return nil
}

func (p *ollamaProvider) AnalyzeImage(ctx context.Context, imagePath, prompt string) (string, error) {
	encoded, err := readImageBase64(imagePath)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"model":  p.model,
		"prompt": prompt,
		"images": []string{encoded},
		"stream": false,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("ollama vision: marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ollama vision: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := p.http.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("ollama vision: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("ollama vision: %d %s", response.StatusCode, strings.TrimSpace(string(text)))
	}
	var decoded struct {
		Response string `json:"response"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("ollama vision: decode response: %w", err)
	}
	return strings.TrimSpace(decoded.Response), nil
}

type openAIVisionProvider struct {
	endpoint string
	model    string
	apiKey   string
	http     httpClient
}

func newOpenAIVisionProvider(cfg ProviderConfig) *openAIVisionProvider {
	endpoint := strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	model := strings.TrimSpace(cfg.Model)
	if model == "" {
		model = "gpt-4o-mini"
	}
	return &openAIVisionProvider{
		endpoint: endpoint,
		model:    model,
		apiKey:   strings.TrimSpace(cfg.APIKey),
		http:     newHTTPClient(cfg.Timeout, cfg.Logger),
	}
}

func (p *openAIVisionProvider) AnalyzeImage(ctx context.Context, imagePath, prompt string) (string, error) {
	encoded, err := readImageBase64(imagePath)
	if err != nil {
		return "", err
	}
	dataURL := "data:" + guessImageMimeType(imagePath) + ";base64," + encoded
	payload := map[string]any{
		"model": p.model,
		"messages": []map[string]any{
			{
				"role": "user",
				"content": []map[string]any{
					{"type": "text", "text": prompt},
					{"type": "image_url", "image_url": map[string]any{"url": dataURL}},
				},
			},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("openai vision: marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("openai vision: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.http.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("openai vision: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("openai vision: %d %s", response.StatusCode, strings.TrimSpace(string(text)))
	}
	var decoded struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("openai vision: decode response: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return "", fmt.Errorf("openai vision: empty response")
	}
	return strings.TrimSpace(decoded.Choices[0].Message.Content), nil
}

type genericHTTPProvider struct {
	endpoint string
	model    string
	apiKey   string
	http     httpClient
}

func newGenericHTTPProvider(cfg ProviderConfig) *genericHTTPProvider {
	return &genericHTTPProvider{
		endpoint: strings.TrimRight(strings.TrimSpace(cfg.Endpoint), "/"),
		model:    strings.TrimSpace(cfg.Model),
		apiKey:   strings.TrimSpace(cfg.APIKey),
		http:     newHTTPClient(cfg.Timeout, cfg.Logger),
	}
}

func (p *genericHTTPProvider) AnalyzeImage(ctx context.Context, imagePath, prompt string) (string, error) {
	if p.endpoint == "" {
		return "", fmt.Errorf("generic vision: endpoint is required")
	}
	encoded, err := readImageBase64(imagePath)
	if err != nil {
		return "", err
	}
	payload := map[string]any{
		"task":      "vision",
		"prompt":    prompt,
		"image":     encoded,
		"image_b64": encoded,
		"model":     p.model,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("generic vision: marshal request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("generic vision: build request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if p.apiKey != "" {
		request.Header.Set("Authorization", "Bearer "+p.apiKey)
	}
	response, err := p.http.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("generic vision: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode >= http.StatusBadRequest {
		text, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return "", fmt.Errorf("generic vision: %d %s", response.StatusCode, strings.TrimSpace(string(text)))
	}
	var decoded struct {
		Response    string `json:"response"`
		Description string `json:"description"`
		Text        string `json:"text"`
		Answer      string `json:"answer"`
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("generic vision: decode response: %w", err)
	}
	for _, value := range []string{decoded.Response, decoded.Description, decoded.Text, decoded.Answer} {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed, nil
		}
	}
	return "", fmt.Errorf("generic vision: empty response")
}

func readImageBase64(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read image: %w", err)
	}
	return base64.StdEncoding.EncodeToString(data), nil
}

func guessImageMimeType(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	default:
		return "application/octet-stream"
	}
}
