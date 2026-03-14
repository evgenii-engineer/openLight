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
	Skill                 string            `json:"skill"`
	Arguments             map[string]string `json:"arguments"`
	Confidence            float64           `json:"confidence"`
	NeedsClarification    bool              `json:"needs_clarification"`
	ClarificationQuestion string            `json:"clarification_question"`
}

type RouteClassification struct {
	Intent                string  `json:"intent"`
	Confidence            float64 `json:"confidence"`
	NeedsClarification    bool    `json:"needs_clarification"`
	ClarificationQuestion string  `json:"clarification_question"`
}

type SkillOption struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Mutating    bool   `json:"mutating"`
}

type GroupOption struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	Description string `json:"description"`
}

type RouteClassificationRequest struct {
	Groups     []GroupOption
	InputChars int
	NumPredict int
}

type SkillClassificationRequest struct {
	AllowedSkills   []string
	AllowedServices []string
	CandidateSkills []SkillOption
	InputChars      int
	NumPredict      int
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Provider interface {
	ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error)
	ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error)
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

func (p *HTTPProvider) ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error) {
	text = limitText(text, request.InputChars)

	var response RouteClassification
	if err := p.do(ctx, map[string]any{
		"task":            "route",
		"text":            text,
		"groups":          request.Groups,
		"input_chars":     request.InputChars,
		"num_predict":     request.NumPredict,
		"response_schema": "route_v1",
	}, &response); err != nil {
		return RouteClassification{}, err
	}
	return normalizeRouteClassification(response), nil
}

func (p *HTTPProvider) ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error) {
	text = limitText(text, request.InputChars)

	var response Classification
	if err := p.do(ctx, map[string]any{
		"task":             "skill",
		"text":             text,
		"skills":           request.AllowedSkills,
		"allowed_services": request.AllowedServices,
		"candidate_skills": request.CandidateSkills,
		"input_chars":      request.InputChars,
		"num_predict":      request.NumPredict,
		"response_schema":  "skill_v1",
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
	skill := strings.TrimSpace(classification.Skill)
	arguments := normalizeStringMap(classification.Arguments)

	return Classification{
		Skill:                 skill,
		Arguments:             arguments,
		Confidence:            clampConfidence(classification.Confidence),
		NeedsClarification:    classification.NeedsClarification,
		ClarificationQuestion: strings.TrimSpace(classification.ClarificationQuestion),
	}
}

func normalizeRouteClassification(classification RouteClassification) RouteClassification {
	return RouteClassification{
		Intent:                strings.TrimSpace(classification.Intent),
		Confidence:            clampConfidence(classification.Confidence),
		NeedsClarification:    classification.NeedsClarification,
		ClarificationQuestion: strings.TrimSpace(classification.ClarificationQuestion),
	}
}

func clampConfidence(confidence float64) float64 {
	switch {
	case confidence < 0:
		return 0
	case confidence > 1:
		return 1
	default:
		return confidence
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
