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
	AllowedRuntimes []string
	CandidateSkills []SkillOption
	InputChars      int
	NumPredict      int
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatOptions tunes a single Chat call.
type ChatOptions struct {
	// NumPredict caps the generated tokens. Zero keeps the provider's
	// default, which is sized for short Telegram replies.
	NumPredict int

	// NumCtx overrides the context window for this call. Zero keeps the
	// profile's configured value. A caller asking for a long answer has
	// to widen the window too, or the prompt gets squeezed out to make
	// room for the output.
	NumCtx int
}

// OptionChatter is implemented by providers that accept per-call chat
// options.
//
// It exists because the default output cap is deliberately small — chat
// replies are trimmed to a few hundred characters anyway — but callers
// that need a long structured answer (memory distillation asking for a
// JSON object) would otherwise get silently truncated output. Optional
// interface so providers that cannot honour it keep working unchanged.
type OptionChatter interface {
	ChatWithOptions(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error)
}

// ChatWith calls ChatWithOptions when the provider supports it and falls
// back to plain Chat otherwise.
func ChatWith(ctx context.Context, provider Provider, messages []ChatMessage, opts ChatOptions) (string, error) {
	if optioned, ok := provider.(OptionChatter); ok {
		return optioned.ChatWithOptions(ctx, messages, opts)
	}
	return provider.Chat(ctx, messages)
}

type Provider interface {
	ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error)
	ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error)
	Summarize(ctx context.Context, text string) (string, error)
	Chat(ctx context.Context, messages []ChatMessage) (string, error)
}

// Prewarmer is an optional interface implemented by providers that benefit
// from a no-op request on startup. Local providers (Ollama) pay a heavy
// cold-start cost when a model is loaded into memory for the first time;
// remote providers (OpenAI) do not implement this.
type Prewarmer interface {
	Prewarm(ctx context.Context) error
}

// PrewarmOptions lets the warmup runner override the request payload for a
// specific prewarm call without changing the provider's stored defaults.
// Useful when warmup needs keep_alive=-1 while normal traffic uses a
// shorter TTL.
type PrewarmOptions struct {
	Prompt    string
	KeepAlive string
}

type HTTPProvider struct {
	endpoint string
	profile  string // optional; when set, sent as "profile" in every request
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

// WithProfile returns a copy of the provider that includes "profile": name
// in every request payload. Used by RemoteLLMProvider so the brain can route
// to the correct local model (fast vs smart).
func (p *HTTPProvider) WithProfile(name string) *HTTPProvider {
	copy := *p
	copy.profile = name
	return &copy
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
		"allowed_runtimes": request.AllowedRuntimes,
		"candidate_skills": request.CandidateSkills,
		"input_chars":      request.InputChars,
		"num_predict":      request.NumPredict,
		"response_schema":  "skill_v3",
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
	return p.ChatWithOptions(ctx, messages, ChatOptions{})
}

// ChatWithOptions forwards the output cap to the brain, which applies it
// to its local provider.
func (p *HTTPProvider) ChatWithOptions(ctx context.Context, messages []ChatMessage, opts ChatOptions) (string, error) {
	var response struct {
		Response string `json:"response"`
		Answer   string `json:"answer"`
		Text     string `json:"text"`
	}
	payload := map[string]any{
		"task":     "chat",
		"messages": messages,
	}
	if opts.NumPredict > 0 {
		payload["num_predict"] = opts.NumPredict
	}
	if opts.NumCtx > 0 {
		payload["num_ctx"] = opts.NumCtx
	}
	if err := p.do(ctx, payload, &response); err != nil {
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
	if p.profile != "" {
		payload["profile"] = p.profile
	}
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
