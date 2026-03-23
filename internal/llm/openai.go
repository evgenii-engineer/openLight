package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
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
	Model             string         `json:"model"`
	Instructions      string         `json:"instructions,omitempty"`
	Input             any            `json:"input"`
	Temperature       float64        `json:"temperature,omitempty"`
	MaxOutputTokens   int            `json:"max_output_tokens,omitempty"`
	Store             bool           `json:"store"`
	Text              openAITextSpec `json:"text"`
	Tools             []openAITool   `json:"tools,omitempty"`
	ToolChoice        any            `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool          `json:"parallel_tool_calls,omitempty"`
}

type openAITextSpec struct {
	Format any `json:"format"`
}

type openAITool struct {
	Type        string         `json:"type"`
	Name        string         `json:"name,omitempty"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
	Strict      bool           `json:"strict,omitempty"`
}

type openAIResponse struct {
	Output []openAIOutputItem `json:"output"`
}

type openAIOutputItem struct {
	ID        string                `json:"id"`
	Type      string                `json:"type"`
	Status    string                `json:"status"`
	Role      string                `json:"role"`
	Name      string                `json:"name"`
	Arguments string                `json:"arguments"`
	CallID    string                `json:"call_id"`
	Content   []openAIOutputContent `json:"content"`
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
	response, err := p.createResponse(ctx, openAIResponsesRequest{
		Model:           p.model,
		Instructions:    buildSkillToolInstructions(request),
		Input:           limitText(text, request.InputChars),
		Temperature:     0.1,
		MaxOutputTokens: decisionNumPredict(request.NumPredict),
		Store:           false,
		Text: openAITextSpec{
			Format: map[string]any{
				"type": "text",
			},
		},
		Tools:             openAISkillTools(request),
		ToolChoice:        "required",
		ParallelToolCalls: openAIBool(false),
	})
	if err != nil {
		return Classification{}, err
	}

	call, ok := openAIFirstFunctionCall(response)
	if !ok {
		responseText := strings.TrimSpace(openAIResponseText(response))
		if p.logger != nil {
			p.logger.Debug("openai skill raw response", "response", responseText)
		}
		if responseText == "" {
			return Classification{}, fmt.Errorf("openai skill response missing function call")
		}
		return Classification{}, fmt.Errorf("openai skill response missing function call: %s", responseText)
	}

	if p.logger != nil {
		p.logger.Debug("openai skill raw response", "tool", call.Name, "arguments", call.Arguments)
	}

	classification, err := openAIClassificationFromFunctionCall(call)
	if err != nil {
		return Classification{}, err
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
	response, err := p.createResponse(ctx, payload)
	if err != nil {
		return "", err
	}

	responseText := strings.TrimSpace(openAIResponseText(response))
	if responseText == "" {
		return "", fmt.Errorf("empty openai response text")
	}

	return responseText, nil
}

func (p *OpenAIProvider) createResponse(ctx context.Context, payload openAIResponsesRequest) (openAIResponse, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return openAIResponse{}, fmt.Errorf("marshal openai responses payload: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint+"/responses", bytes.NewReader(body))
	if err != nil {
		return openAIResponse{}, fmt.Errorf("create openai responses request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+p.apiKey)

	response, err := p.client.Do(request)
	if err != nil {
		return openAIResponse{}, fmt.Errorf("call openai responses endpoint: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		content, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return openAIResponse{}, fmt.Errorf("openai responses endpoint returned %d: %s", response.StatusCode, strings.TrimSpace(string(content)))
	}

	var decoded openAIResponse
	if err := json.NewDecoder(response.Body).Decode(&decoded); err != nil {
		return openAIResponse{}, fmt.Errorf("decode openai responses response: %w", err)
	}

	responseText := strings.TrimSpace(openAIResponseText(decoded))
	if p.logger != nil {
		p.logger.Debug("openai response completed",
			"model", p.model,
			"response_chars", utf8.RuneCountInString(responseText),
			"function_calls", openAIFunctionCallCount(decoded),
		)
	}

	return decoded, nil
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

func openAIFirstFunctionCall(response openAIResponse) (openAIOutputItem, bool) {
	for _, item := range response.Output {
		if item.Type == "function_call" {
			return item, true
		}
	}
	return openAIOutputItem{}, false
}

func openAIFunctionCallCount(response openAIResponse) int {
	count := 0
	for _, item := range response.Output {
		if item.Type == "function_call" {
			count++
		}
	}
	return count
}

func openAIClassificationFromFunctionCall(call openAIOutputItem) (Classification, error) {
	arguments, err := openAIArgumentsStringMap(call.Arguments)
	if err != nil {
		return Classification{}, fmt.Errorf("decode openai skill function arguments: %w", err)
	}

	if call.Name == openAIClarificationToolName {
		return Classification{
			NeedsClarification:    true,
			ClarificationQuestion: strings.TrimSpace(arguments["question"]),
		}, nil
	}

	return Classification{
		Skill:     strings.TrimSpace(call.Name),
		Arguments: arguments,
	}, nil
}

func openAIArgumentsStringMap(raw string) (map[string]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return map[string]string{}, nil
	}

	var decoded map[string]any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, err
	}

	result := make(map[string]string, len(decoded))
	for key, value := range decoded {
		result[strings.TrimSpace(key)] = openAIArgumentString(value)
	}
	return result, nil
}

func openAIArgumentString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		if math.Trunc(typed) == typed {
			return strconv.FormatInt(int64(typed), 10)
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
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
		"schema": openAIStrictSchema(schema),
	}
}

func openAIBool(value bool) *bool {
	return &value
}

func openAIStrictSchema(schema map[string]any) map[string]any {
	cloned, ok := deepCloneJSONValue(schema).(map[string]any)
	if !ok {
		return schema
	}
	openAIStrictifySchemaNode(cloned)
	return cloned
}

func openAIStrictifySchemaNode(node map[string]any) {
	properties, ok := node["properties"].(map[string]any)
	if !ok {
		return
	}

	required := make([]string, 0, len(properties))
	for key, raw := range properties {
		required = append(required, key)
		child, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		openAIStrictifySchemaNode(child)
	}
	node["required"] = required
}

func deepCloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(typed))
		for key, child := range typed {
			result[key] = deepCloneJSONValue(child)
		}
		return result
	case []any:
		result := make([]any, len(typed))
		for idx, child := range typed {
			result[idx] = deepCloneJSONValue(child)
		}
		return result
	case []string:
		result := make([]any, len(typed))
		for idx, child := range typed {
			result[idx] = child
		}
		return result
	default:
		return value
	}
}
