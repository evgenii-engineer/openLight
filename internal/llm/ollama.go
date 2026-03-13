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

const defaultDecisionNumPredict = 64

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

func (p *OllamaProvider) ClassifyIntent(ctx context.Context, text string, request ClassificationRequest) (Classification, error) {
	prompt := buildIntentPrompt(limitText(text, request.InputChars), request)
	responseText, err := p.generate(ctx, prompt, 0.1, decisionNumPredict(request.NumPredict))
	if err != nil {
		return Classification{}, err
	}
	if p.logger != nil {
		p.logger.Debug("ollama decision raw response", "response", responseText)
	}

	var classification Classification
	if err := json.Unmarshal([]byte(extractJSON(responseText)), &classification); err != nil {
		return Classification{}, fmt.Errorf("decode ollama intent response: %w", err)
	}

	return normalizeClassification(classification), nil
}

func (p *OllamaProvider) Summarize(ctx context.Context, text string) (string, error) {
	responseText, err := p.generate(ctx, buildSummaryPrompt(text), 0.0, 64)
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

func (p *OllamaProvider) generate(ctx context.Context, prompt string, temperature float64, numPredict int) (string, error) {
	payload := map[string]any{
		"model":      p.model,
		"prompt":     prompt,
		"stream":     false,
		"format":     "json",
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
	}
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode ollama response: %w", err)
	}

	responseText := strings.TrimSpace(decoded.Response)
	if p.logger != nil {
		p.logger.Debug("ollama generate completed",
			"prompt_chars", utf8.RuneCountInString(prompt),
			"response_chars", utf8.RuneCountInString(responseText),
		)
	}

	return responseText, nil
}

func buildIntentPrompt(text string, request ClassificationRequest) string {
	return fmt.Sprintf(
		"You are a routing classifier for a Telegram-first local agent.\n\n"+
			"Your job is to choose exactly one intent from this list:\n%s\n\n"+
			"Return only valid JSON with this schema:\n"+
			"{\n"+
			"  \"intent\": \"string\",\n"+
			"  \"arguments\": {},\n"+
			"  \"confidence\": 0.0,\n"+
			"  \"needs_clarification\": false,\n"+
			"  \"clarification_question\": \"\"\n"+
			"}\n\n"+
			"Rules:\n"+
			"- Never invent unsupported intents.\n"+
			"- If the message is ambiguous, set needs_clarification=true.\n"+
			"- If a service name is present, put it in arguments.service.\n"+
			"- If note text is present, put it in arguments.text.\n"+
			"- If note id is present, put it in arguments.id.\n"+
			"- If the user clearly wants free-form conversation, use \"chat\".\n"+
			"- If unsure, use \"unknown\".\n"+
			"- Return JSON only.\n\n"+
			"User message:\n%q\n\n"+
			"Allowed services:\n%s\n",
		strings.Join(request.AllowedIntents, "\n"),
		text,
		encodePromptList(request.AllowedServices),
	)
}

func buildSummaryPrompt(text string) string {
	return fmt.Sprintf(
		"Return only valid JSON with a single field named summary.\n"+
			"Summarize this text briefly and clearly.\n"+
			"Text: %q\n",
		text,
	)
}

func extractJSON(value string) string {
	value = strings.TrimSpace(value)
	start := strings.Index(value, "{")
	end := strings.LastIndex(value, "}")
	if start >= 0 && end >= start {
		return value[start : end+1]
	}
	return value
}

func totalChatMessageChars(messages []ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += utf8.RuneCountInString(message.Content)
	}
	return total
}

func decisionNumPredict(value int) int {
	if value <= 0 {
		return defaultDecisionNumPredict
	}
	return value
}

func encodePromptList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}
