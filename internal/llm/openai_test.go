package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOpenAIProviderClassifyRoute(t *testing.T) {
	t.Parallel()

	provider := NewOpenAIProvider("https://api.openai.com/v1", "gpt-4o-mini", "secret", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/v1/responses" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer secret" {
				t.Fatalf("unexpected authorization header: %q", got)
			}

			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload["model"] != "gpt-4o-mini" {
				t.Fatalf("unexpected model: %#v", payload["model"])
			}
			if payload["store"] != false {
				t.Fatalf("unexpected store value: %#v", payload["store"])
			}
			if payload["max_output_tokens"] != float64(64) {
				t.Fatalf("unexpected max_output_tokens: %#v", payload["max_output_tokens"])
			}
			if payload["temperature"] != 0.1 {
				t.Fatalf("unexpected temperature: %#v", payload["temperature"])
			}

			text, ok := payload["input"].(string)
			if !ok || !strings.Contains(text, "restart tailscale") {
				t.Fatalf("unexpected input payload: %#v", payload["input"])
			}

			textConfig, ok := payload["text"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected text config: %#v", payload["text"])
			}
			format, ok := textConfig["format"].(map[string]any)
			if !ok || format["type"] != "json_schema" {
				t.Fatalf("unexpected format config: %#v", textConfig["format"])
			}
			if format["name"] != "route_response" {
				t.Fatalf("unexpected format name: %#v", format["name"])
			}

			return jsonHTTPResponse(map[string]any{
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": `{"intent":"services","confidence":0.93,"needs_clarification":false,"clarification_question":""}`,
							},
						},
					},
				},
			}), nil
		}),
	}

	classification, err := provider.ClassifyRoute(context.Background(), "restart tailscale", RouteClassificationRequest{
		Groups: []GroupOption{
			{Key: "services", Title: "Services", Description: "Inspect whitelisted services, view logs, and restart them."},
			{Key: "system", Title: "System", Description: "Read system metrics and host information."},
		},
		InputChars: 160,
		NumPredict: 64,
	})
	if err != nil {
		t.Fatalf("ClassifyRoute returned error: %v", err)
	}
	if classification.Intent != "services" {
		t.Fatalf("unexpected intent: %q", classification.Intent)
	}
}

func TestOpenAIProviderClassifySkill(t *testing.T) {
	t.Parallel()

	provider := NewOpenAIProvider("https://api.openai.com/v1", "gpt-4o-mini", "secret", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			if payload["tool_choice"] != "required" {
				t.Fatalf("unexpected tool_choice: %#v", payload["tool_choice"])
			}
			if payload["parallel_tool_calls"] != false {
				t.Fatalf("unexpected parallel_tool_calls: %#v", payload["parallel_tool_calls"])
			}

			textConfig, ok := payload["text"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected text config: %#v", payload["text"])
			}
			format, ok := textConfig["format"].(map[string]any)
			if !ok || format["type"] != "text" {
				t.Fatalf("unexpected format config: %#v", textConfig["format"])
			}

			tools, ok := payload["tools"].([]any)
			if !ok || len(tools) != 3 {
				t.Fatalf("unexpected tools payload: %#v", payload["tools"])
			}

			firstTool, ok := tools[0].(map[string]any)
			if !ok || firstTool["type"] != "function" || firstTool["name"] != "service_restart" {
				t.Fatalf("unexpected first tool: %#v", tools[0])
			}
			if firstTool["strict"] != true {
				t.Fatalf("expected strict function tool, got %#v", firstTool["strict"])
			}

			parameters, ok := firstTool["parameters"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected parameters schema: %#v", firstTool["parameters"])
			}
			required, ok := parameters["required"].([]any)
			if !ok || len(required) != 9 {
				t.Fatalf("unexpected required list: %#v", parameters["required"])
			}
			properties, ok := parameters["properties"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected parameters properties: %#v", parameters["properties"])
			}
			serviceProperty, ok := properties["service"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected service property: %#v", properties["service"])
			}
			serviceType, ok := serviceProperty["type"].([]any)
			if !ok || len(serviceType) != 2 || serviceType[0] != "string" || serviceType[1] != "null" {
				t.Fatalf("unexpected service type: %#v", serviceProperty["type"])
			}
			serviceEnum, ok := serviceProperty["enum"].([]any)
			if !ok || len(serviceEnum) != 2 || serviceEnum[0] != "tailscale" || serviceEnum[1] != nil {
				t.Fatalf("unexpected service enum: %#v", serviceProperty["enum"])
			}

			lastTool, ok := tools[2].(map[string]any)
			if !ok || lastTool["name"] != openAIClarificationToolName {
				t.Fatalf("unexpected clarification tool: %#v", tools[2])
			}

			return jsonHTTPResponse(map[string]any{
				"output": []map[string]any{
					{
						"type":      "function_call",
						"name":      "service_restart",
						"arguments": `{"service":"tailscale"}`,
						"status":    "completed",
					},
				},
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "restart tailscale", SkillClassificationRequest{
		AllowedSkills:   []string{"service_restart", "chat"},
		AllowedServices: []string{"tailscale"},
		InputChars:      160,
		NumPredict:      64,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if classification.Skill != "service_restart" {
		t.Fatalf("unexpected skill: %q", classification.Skill)
	}
	if classification.Arguments["service"] != "tailscale" {
		t.Fatalf("unexpected args: %#v", classification.Arguments)
	}
}

func TestOpenAIProviderClassifySkillClarificationTool(t *testing.T) {
	t.Parallel()

	provider := NewOpenAIProvider("https://api.openai.com/v1", "gpt-4o-mini", "secret", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"output": []map[string]any{
					{
						"type":      "function_call",
						"name":      openAIClarificationToolName,
						"arguments": `{"question":"Which service should I restart?"}`,
						"status":    "completed",
					},
				},
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "restart something", SkillClassificationRequest{
		AllowedSkills: []string{"service_restart"},
		InputChars:    160,
		NumPredict:    64,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if !classification.NeedsClarification {
		t.Fatalf("expected clarification classification: %#v", classification)
	}
	if classification.ClarificationQuestion != "Which service should I restart?" {
		t.Fatalf("unexpected clarification question: %q", classification.ClarificationQuestion)
	}
}

func TestOpenAIProviderSummarize(t *testing.T) {
	t.Parallel()

	provider := NewOpenAIProvider("https://api.openai.com/v1", "gpt-4o-mini", "secret", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			textConfig, ok := payload["text"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected text config: %#v", payload["text"])
			}
			format, ok := textConfig["format"].(map[string]any)
			if !ok || format["type"] != "json_schema" {
				t.Fatalf("unexpected format config: %#v", textConfig["format"])
			}
			if format["name"] != "summary_response" {
				t.Fatalf("unexpected format name: %#v", format["name"])
			}

			return jsonHTTPResponse(map[string]any{
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": `{"summary":"short summary"}`,
							},
						},
					},
				},
			}), nil
		}),
	}

	summary, err := provider.Summarize(context.Background(), "very long text")
	if err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	if summary != "short summary" {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

func TestOpenAIProviderChat(t *testing.T) {
	t.Parallel()

	provider := NewOpenAIProvider("https://api.openai.com/v1", "gpt-4o-mini", "secret", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}

			if payload["instructions"] != "system prompt" {
				t.Fatalf("unexpected instructions: %#v", payload["instructions"])
			}
			if payload["max_output_tokens"] != float64(defaultOpenAIChatMaxTokens) {
				t.Fatalf("unexpected max_output_tokens: %#v", payload["max_output_tokens"])
			}

			input, ok := payload["input"].([]any)
			if !ok || len(input) != 2 {
				t.Fatalf("unexpected input messages: %#v", payload["input"])
			}
			first, ok := input[0].(map[string]any)
			if !ok {
				t.Fatalf("unexpected first input message: %#v", input[0])
			}
			firstContent, ok := first["content"].([]any)
			if !ok || len(firstContent) != 1 {
				t.Fatalf("unexpected first content: %#v", first["content"])
			}
			firstPart, ok := firstContent[0].(map[string]any)
			if !ok || firstPart["type"] != "input_text" {
				t.Fatalf("unexpected first content part: %#v", firstContent[0])
			}

			second, ok := input[1].(map[string]any)
			if !ok {
				t.Fatalf("unexpected second input message: %#v", input[1])
			}
			secondContent, ok := second["content"].([]any)
			if !ok || len(secondContent) != 1 {
				t.Fatalf("unexpected second content: %#v", second["content"])
			}
			secondPart, ok := secondContent[0].(map[string]any)
			if !ok || secondPart["type"] != "output_text" {
				t.Fatalf("unexpected second content part: %#v", secondContent[0])
			}

			return jsonHTTPResponse(map[string]any{
				"output": []map[string]any{
					{
						"type": "message",
						"content": []map[string]any{
							{
								"type": "output_text",
								"text": "chat reply",
							},
						},
					},
				},
			}), nil
		}),
	}

	reply, err := provider.Chat(context.Background(), []ChatMessage{
		{Role: "system", Content: "system prompt"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if reply != "chat reply" {
		t.Fatalf("unexpected reply: %q", reply)
	}
}
