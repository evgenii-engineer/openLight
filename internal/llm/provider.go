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
)

type Classification struct {
	Intent                string            `json:"intent"`
	Arguments             map[string]string `json:"arguments"`
	Confidence            float64           `json:"confidence"`
	NeedsClarification    bool              `json:"needs_clarification"`
	ClarificationQuestion string            `json:"clarification_question"`

	// Backward-compatible fields for older generic providers.
	SkillName string            `json:"skill_name"`
	Args      map[string]string `json:"args"`
}

type ClassificationRequest struct {
	AllowedIntents  []string
	AllowedServices []string
	InputChars      int
	NumPredict      int
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Provider interface {
	ClassifyIntent(ctx context.Context, text string, request ClassificationRequest) (Classification, error)
	Summarize(ctx context.Context, text string) (string, error)
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
}

type HTTPProvider struct {
	endpoint string
	client   *http.Client
	logger   *slog.Logger
}

func NewHTTPProvider(endpoint string, timeout time.Duration, logger *slog.Logger) *HTTPProvider {
	return &HTTPProvider{
		endpoint: strings.TrimSpace(endpoint),
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

func (p *HTTPProvider) ClassifyIntent(ctx context.Context, text string, request ClassificationRequest) (Classification, error) {
	text = limitText(text, request.InputChars)

	var response Classification
	if err := p.do(ctx, map[string]any{
		"task":             "intent",
		"text":             text,
		"skills":           request.AllowedIntents,
		"intents":          request.AllowedIntents,
		"allowed_services": request.AllowedServices,
		"input_chars":      request.InputChars,
		"num_predict":      request.NumPredict,
		"response_schema":  "decision_v1",
	}, &response); err != nil {
		return Classification{}, err
	}
	return normalizeClassification(response), nil
}

func (p *HTTPProvider) Summarize(ctx context.Context, text string) (string, error) {
	var response struct {
		Summary string `json:"summary"`
	}
	if err := p.do(ctx, map[string]any{
		"task": "summarize",
		"text": text,
	}, &response); err != nil {
		return "", err
	}
	return strings.TrimSpace(response.Summary), nil
}

func (p *HTTPProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	var response struct {
		Response string `json:"response"`
		Answer   string `json:"answer"`
		Text     string `json:"text"`
	}
	if err := p.do(ctx, map[string]any{
		"task":     "chat",
		"messages": messages,
	}, &response); err != nil {
		return "", err
	}

	for _, value := range []string{response.Response, response.Answer, response.Text} {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value), nil
		}
	}

	return "", fmt.Errorf("empty chat response")
}

func (p *HTTPProvider) do(ctx context.Context, payload map[string]any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal llm payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create llm request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := p.client.Do(request)
	if err != nil {
		return fmt.Errorf("call llm endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 2048))
		return fmt.Errorf("llm endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	if err := json.NewDecoder(response.Body).Decode(out); err != nil {
		return fmt.Errorf("decode llm response: %w", err)
	}

	return nil
}

func normalizeClassification(classification Classification) Classification {
	intent := strings.TrimSpace(classification.Intent)
	if intent == "" {
		intent = strings.TrimSpace(classification.SkillName)
	}

	arguments := normalizeStringMap(classification.Arguments)
	if len(arguments) == 0 {
		arguments = normalizeStringMap(classification.Args)
	}

	confidence := classification.Confidence
	switch {
	case confidence < 0:
		confidence = 0
	case confidence > 1:
		confidence = 1
	}

	return Classification{
		Intent:                intent,
		Arguments:             arguments,
		Confidence:            confidence,
		NeedsClarification:    classification.NeedsClarification,
		ClarificationQuestion: strings.TrimSpace(classification.ClarificationQuestion),
	}
}

func normalizeStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}

	result := make(map[string]string, len(values))
	for key, value := range values {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}
	if len(result) == 0 {
		return map[string]string{}
	}
	return result
}

func limitText(value string, maxChars int) string {
	value = strings.TrimSpace(value)
	if maxChars <= 0 {
		return value
	}

	runes := []rune(value)
	if len(runes) <= maxChars {
		return value
	}

	return strings.TrimSpace(string(runes[:maxChars]))
}
