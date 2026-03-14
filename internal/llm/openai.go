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

const (
	defaultOpenAIBaseURL          = "https://api.openai.com/v1"
	defaultOpenAIChatMaxTokens    = 256
	defaultOpenAISummaryMaxTokens = 128
)

type OpenAIProvider struct {
	endpoint string
	model    string
	apiKey   string
	client   *http.Client
	logger   *slog.Logger
}

type openAIResponsesRequest struct {
	Model           string         `json:"model"`
	Instructions    string         `json:"instructions,omitempty"`
	Input           any            `json:"input"`
	Temperature     float64        `json:"temperature,omitempty"`
	MaxOutputTokens int            `json:"max_output_tokens,omitempty"`
	Store           bool           `json:"store"`
	Text            openAITextSpec `json:"text"`
}

type openAITextSpec struct {
	Format any `json:"format"`
}

type openAIResponse struct {
	Output []openAIOutputItem `json:"output"`
}

type openAIOutputItem struct {
	Type    string                `json:"type"`
	Content []openAIOutputContent `json:"content"`
}

type openAIOutputContent struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func NewOpenAIProvider(endpoint, model, apiKey string, timeout time.Duration, logger *slog.Logger) *OpenAIProvider {
	endpoint = strings.TrimRight(strings.TrimSpace(endpoint), "/")
	if endpoint == "" {
		endpoint = defaultOpenAIBaseURL
	}

	return &OpenAIProvider{
		endpoint: endpoint,
		model:    strings.TrimSpace(model),
		apiKey:   strings.TrimSpace(apiKey),
		client: &http.Client{
			Timeout: timeout,
		},
		logger: logger,
	}
}

func (p *OpenAIProvider) ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error) {
	prompt := buildRoutePrompt(limitText(text, request.InputChars), request)
	responseText, err := p.createTextResponse(ctx, openAIResponsesRequest{
		Model:           p.model,
		Input:           prompt,
		Temperature:     0.1,
		MaxOutputTokens: decisionNumPredict(request.NumPredict),
		Store:           false,
		Text: openAITextSpec{
			Format: openAIJSONSchemaFormat("route_response", routeResponseSchema(groupKeys(request.Groups))),
		},
	})
	if err != nil {
		return RouteClassification{}, err
	}

	if p.logger != nil {
		p.logger.Debug("openai route raw response", "response", responseText)
	}

	var classification RouteClassification
	if err := json.Unmarshal([]byte(responseText), &classification); err != nil {
		return RouteClassification{}, fmt.Errorf("decode openai route response: %w", err)
	}

	return normalizeRouteClassification(classification), nil
}

func (p *OpenAIProvider) ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error) {
	prompt := buildSkillPrompt(limitText(text, request.InputChars), request)
	responseText, err := p.createTextResponse(ctx, openAIResponsesRequest{
		Model:           p.model,
		Input:           prompt,
		Temperature:     0.1,
		MaxOutputTokens: decisionNumPredict(request.NumPredict),
		Store:           false,
		Text: openAITextSpec{
			Format: openAIJSONSchemaFormat("skill_response", skillResponseSchema(request.AllowedSkills)),
		},
	})
	if err != nil {
		return Classification{}, err
	}

	if p.logger != nil {
		p.logger.Debug("openai skill raw response", "response", responseText)
	}

	var classification Classification
	if err := json.Unmarshal([]byte(responseText), &classification); err != nil {
		return Classification{}, fmt.Errorf("decode openai skill response: %w", err)
	}

	return normalizeClassification(classification), nil
}

func (p *OpenAIProvider) Summarize(ctx context.Context, text string) (string, error) {
	responseText, err := p.createTextResponse(ctx, openAIResponsesRequest{
		Model:           p.model,
		Input:           buildSummaryPrompt(text),
		Temperature:     0,
		MaxOutputTokens: defaultOpenAISummaryMaxTokens,
		Store:           false,
		Text: openAITextSpec{
			Format: openAIJSONSchemaFormat("summary_response", map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"summary": map[string]any{"type": "string"},
				},
				"required": []string{"summary"},
			}),
		},
	})
	if err != nil {
		return "", err
	}

	var response struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal([]byte(responseText), &response); err != nil {
		return "", fmt.Errorf("decode openai summary response: %w", err)
	}

	return strings.TrimSpace(response.Summary), nil
}

func (p *OpenAIProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	input, instructions := openAIChatInput(messages)
	responseText, err := p.createTextResponse(ctx, openAIResponsesRequest{
		Model:           p.model,
		Instructions:    instructions,
		Input:           input,
		Temperature:     0.2,
		MaxOutputTokens: defaultOpenAIChatMaxTokens,
		Store:           false,
		Text: openAITextSpec{
			Format: map[string]any{
				"type": "text",
			},
		},
	})
	if err != nil {
		return "", err
	}

	return strings.TrimSpace(responseText), nil
}

func (p *OpenAIProvider) createTextResponse(ctx context.Context, payload openAIResponsesRequest) (string, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("marshal openai responses payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/responses", bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("create openai responses request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.apiKey)

	response, err := p.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("call openai responses endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return "", fmt.Errorf("openai responses endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded openAIResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return "", fmt.Errorf("decode openai responses response: %w", err)
	}

	responseText := strings.TrimSpace(openAIResponseText(decoded))
	if p.logger != nil {
		p.logger.Debug("openai response completed",
			"model", p.model,
			"response_chars", utf8.RuneCountInString(responseText),
		)
	}
	if responseText == "" {
		return "", fmt.Errorf("empty openai response text")
	}

	return responseText, nil
}

func openAIResponseText(response openAIResponse) string {
	var builder strings.Builder
	for _, item := range response.Output {
		if item.Type != "message" {
			continue
		}
		for _, content := range item.Content {
			if content.Type != "output_text" || strings.TrimSpace(content.Text) == "" {
				continue
			}
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(strings.TrimSpace(content.Text))
		}
	}
	return builder.String()
}

func openAIChatInput(messages []ChatMessage) ([]map[string]any, string) {
	input := make([]map[string]any, 0, len(messages))
	systemParts := make([]string, 0, 1)

	for _, message := range messages {
		text := strings.TrimSpace(message.Content)
		if text == "" {
			continue
		}

		if message.Role == "system" {
			systemParts = append(systemParts, text)
			continue
		}

		role := strings.TrimSpace(message.Role)
		if role != "user" && role != "assistant" {
			continue
		}

		input = append(input, map[string]any{
			"role": role,
			"content": []map[string]any{
				{
					"type": openAIMessageContentType(role),
					"text": text,
				},
			},
		})
	}

	return input, strings.Join(systemParts, "\n\n")
}

func openAIMessageContentType(role string) string {
	if role == "assistant" {
		return "output_text"
	}
	return "input_text"
}

func openAIJSONSchemaFormat(name string, schema map[string]any) map[string]any {
	return map[string]any{
		"type":   "json_schema",
		"name":   name,
		"strict": true,
		"schema": schema,
	}
}
