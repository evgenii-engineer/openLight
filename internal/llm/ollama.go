package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"
)

const ollamaKeepAlive = "30m"

type OllamaProvider struct {
	endpoint string
	model    string
	client   *http.Client
	logger   *slog.Logger
}

func NewOllamaProvider(endpoint, model string, timeout time.Duration, logger *slog.Logger) *OllamaProvider {
	return &OllamaProvider{
		endpoint: strings.TrimRight(strings.TrimSpace(endpoint), "/"),
		model:    strings.TrimSpace(model),
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
		"keep_alive": ollamaKeepAlive,
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

	content := strings.TrimSpace(decoded.Message.Content)
	if p.logger != nil {
		p.logger.Debug("ollama chat completed",
			"messages", len(messages),
			"prompt_chars", totalChatMessageChars(messages),
			"response_chars", utf8.RuneCountInString(content),
		)
	}

	return content, nil
}

func (p *OllamaProvider) generate(ctx context.Context, prompt string, format any, temperature float64, numPredict int) (string, error) {
	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"think":      false,
		"format":     format,
		"keep_alive": ollamaKeepAlive,
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

	responseText := strings.TrimSpace(decoded.Response)
	thinkingText := strings.TrimSpace(decoded.Thinking)
	if p.logger != nil {
		p.logger.Debug("ollama generate completed",
			"prompt_chars", utf8.RuneCountInString(prompt),
			"response_chars", utf8.RuneCountInString(responseText),
			"thinking_chars", utf8.RuneCountInString(thinkingText),
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
