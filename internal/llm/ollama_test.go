package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestOllamaProviderClassifyRoute(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/generate" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}

			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			if payload["model"] != "phi3" {
				t.Fatalf("unexpected model: %v", payload["model"])
			}
			if payload["keep_alive"] != ollamaKeepAlive {
				t.Fatalf("unexpected keep_alive: %#v", payload["keep_alive"])
			}
			if payload["think"] != false {
				t.Fatalf("unexpected think flag: %#v", payload["think"])
			}
			prompt, ok := payload["prompt"].(string)
			if !ok {
				t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "restart tailscale") {
				t.Fatalf("unexpected prompt: %v", payload["prompt"])
			}
			if !strings.Contains(prompt, "Pick one intent for a local Telegram agent.") {
				t.Fatalf("prompt is missing route instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "- services: service status,logs,restart") {
				t.Fatalf("prompt is missing route group context: %s", prompt)
			}
			options, ok := payload["options"].(map[string]any)
			if !ok {
				t.Fatalf("missing options payload: %#v", payload)
			}
			format, ok := payload["format"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected format payload: %#v", payload["format"])
			}
			properties, ok := format["properties"].(map[string]any)
			if !ok || properties["intent"] == nil {
				t.Fatalf("route format is missing intent schema: %#v", format)
			}
			if options["num_predict"] != float64(64) {
				t.Fatalf("unexpected num_predict: %#v", options["num_predict"])
			}
			if options["temperature"] != 0.0 {
				t.Fatalf("unexpected temperature: %#v", options["temperature"])
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"intent":"services","confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
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

func TestOllamaProviderClassifyRouteWithoutClarificationQuestion(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": `{"intent":"system","confidence":0.95,"needs_clarification":false}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifyRoute(context.Background(), "show status", RouteClassificationRequest{
		Groups: []GroupOption{
			{Key: "system", Title: "System", Description: "Read system metrics and host information."},
		},
		InputChars: 96,
		NumPredict: 32,
	})
	if err != nil {
		t.Fatalf("ClassifyRoute returned error: %v", err)
	}
	if classification.Intent != "system" || classification.ClarificationQuestion != "" {
		t.Fatalf("unexpected classification: %#v", classification)
	}
}

func TestOllamaProviderClassifySkill(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}
				prompt, ok := payload["prompt"].(string)
				if !ok {
					t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "Allowed skills: [\"service_restart\"]") {
				t.Fatalf("prompt is missing allowed skills list: %s", prompt)
			}
			if !strings.Contains(prompt, "Pick one skill inside the selected group.") {
				t.Fatalf("prompt is missing second-layer instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "Allowed services") || !strings.Contains(prompt, "tailscale") {
				t.Fatalf("prompt is missing services: %s", prompt)
			}
			format, ok := payload["format"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected format payload: %#v", payload["format"])
			}
			properties, ok := format["properties"].(map[string]any)
			if !ok || properties["skill"] == nil || properties["arguments"] == nil {
				t.Fatalf("skill format is missing properties: %#v", format)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"service_restart","arguments":{"service":"tailscale","text":"","id":"","path":"","content":"","find":"","replace":"","runtime":"","code":"","spec":""},"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "restart tailscale", SkillClassificationRequest{
		AllowedSkills:   []string{"service_restart"},
		AllowedServices: []string{"tailscale"},
		CandidateSkills: []SkillOption{
			{Name: "service_restart", Description: "Restart a whitelisted service.", Mutating: true},
		},
		InputChars: 160,
		NumPredict: 64,
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

func TestOllamaProviderClassifySkillWithoutArguments(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"status","needs_clarification":false}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "show overall status", SkillClassificationRequest{
		AllowedSkills: []string{"status"},
		InputChars:    128,
		NumPredict:    48,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if classification.Skill != "status" {
		t.Fatalf("unexpected skill: %q", classification.Skill)
	}
	if len(classification.Arguments) != 0 {
		t.Fatalf("expected empty arguments, got %#v", classification.Arguments)
	}
}

func TestOllamaProviderClassifySkillOmitsAllowedServicesWhenEmpty(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}
				prompt, ok := payload["prompt"].(string)
				if !ok {
					t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if strings.Contains(prompt, "Allowed services") {
				t.Fatalf("did not expect allowed services section: %s", prompt)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"status","arguments":{"service":"","text":"","id":"","path":"","content":"","find":"","replace":"","runtime":"","code":"","spec":""},"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "show overall status", SkillClassificationRequest{
		AllowedSkills: []string{"status"},
		CandidateSkills: []SkillOption{
			{Name: "status", Description: "Show overall host status.", Mutating: false},
		},
		InputChars: 160,
		NumPredict: 64,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if classification.Skill != "status" {
		t.Fatalf("unexpected skill: %q", classification.Skill)
	}
}

func TestOllamaProviderClassifySkillFallsBackToAllowedSkillsWhenCandidatesMissing(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}
				prompt, ok := payload["prompt"].(string)
				if !ok {
					t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "Allowed skills: [\"status\"]") {
				t.Fatalf("prompt is missing allowed-skills list: %s", prompt)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"status","arguments":{"service":"","text":"","id":"","path":"","content":"","find":"","replace":"","runtime":"","code":"","spec":""},"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "show overall status", SkillClassificationRequest{
		AllowedSkills: []string{"status"},
		InputChars:    160,
		NumPredict:    64,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if classification.Skill != "status" {
		t.Fatalf("unexpected skill: %q", classification.Skill)
	}
}

func TestOllamaProviderClassifyRouteTruncatesInputText(t *testing.T) {
	t.Parallel()

	const rawText = "restart tailscale right now please"

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}

				prompt, ok := payload["prompt"].(string)
				if !ok {
				t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "restart ta") {
				t.Fatalf("expected truncated message in prompt: %s", prompt)
			}
			if strings.Contains(prompt, rawText) {
				t.Fatalf("did not expect full message in prompt: %s", prompt)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"intent":"services","confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	_, err := provider.ClassifyRoute(context.Background(), rawText, RouteClassificationRequest{
		InputChars: 10,
		NumPredict: 64,
	})
	if err != nil {
		t.Fatalf("ClassifyRoute returned error: %v", err)
	}
}

func TestOllamaProviderSummarize(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
			Timeout: time.Second,
			Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
				var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}
				return jsonHTTPResponse(map[string]any{
					"response": `{"summary":"short summary"}`,
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

func TestOllamaProviderClassifyRouteEmptyResponse(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": "",
			}), nil
		}),
	}

	_, err := provider.ClassifyRoute(context.Background(), "show status", RouteClassificationRequest{
		InputChars: 160,
		NumPredict: 64,
	})
	if err == nil {
		t.Fatal("expected ClassifyRoute to return an error")
	}
	if !strings.Contains(err.Error(), "empty ollama response text") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOllamaProviderClassifyRouteThinkingOnlyResponse(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": "",
				"thinking": "let me think",
			}), nil
		}),
	}

	_, err := provider.ClassifyRoute(context.Background(), "show status", RouteClassificationRequest{
		InputChars: 160,
		NumPredict: 64,
	})
	if err == nil {
		t.Fatal("expected ClassifyRoute to return an error")
	}
	if !strings.Contains(err.Error(), "thinking output present") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOllamaProviderChat(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			if r.URL.Path != "/api/chat" {
				t.Fatalf("unexpected path: %s", r.URL.Path)
			}
			var payload map[string]any
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Fatalf("decode request: %v", err)
				}
				if payload["keep_alive"] != ollamaKeepAlive {
					t.Fatalf("unexpected keep_alive: %#v", payload["keep_alive"])
				}
				if payload["think"] != false {
					t.Fatalf("unexpected think flag: %#v", payload["think"])
				}
				options, ok := payload["options"].(map[string]any)
				if !ok {
					t.Fatalf("missing options payload: %#v", payload)
			}
			if options["num_predict"] != float64(64) {
				t.Fatalf("unexpected num_predict: %#v", options["num_predict"])
			}
			return jsonHTTPResponse(map[string]any{
				"message": map[string]any{
					"content": "chat reply",
				},
			}), nil
		}),
	}

	reply, err := provider.Chat(context.Background(), []ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	if reply != "chat reply" {
		t.Fatalf("unexpected reply: %q", reply)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func jsonHTTPResponse(payload any) *http.Response {
	body, _ := json.Marshal(payload)
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type": []string{"application/json"},
		},
		Body: io.NopCloser(bytes.NewReader(body)),
	}
}
