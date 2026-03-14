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
			prompt, ok := payload["prompt"].(string)
			if !ok {
				t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "restart tailscale") {
				t.Fatalf("unexpected prompt: %v", payload["prompt"])
			}
			if !strings.Contains(prompt, "Choose one route for a Telegram local agent.") {
				t.Fatalf("prompt is missing route instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "- services: Inspect whitelisted services") {
				t.Fatalf("prompt is missing route group context: %s", prompt)
			}
			options, ok := payload["options"].(map[string]any)
			if !ok {
				t.Fatalf("missing options payload: %#v", payload)
			}
			if options["num_predict"] != float64(64) {
				t.Fatalf("unexpected num_predict: %#v", options["num_predict"])
			}
			if options["temperature"] != 0.1 {
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
			prompt, ok := payload["prompt"].(string)
			if !ok {
				t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if !strings.Contains(prompt, "Available skills:") {
				t.Fatalf("prompt is missing available skills heading: %s", prompt)
			}
			if !strings.Contains(prompt, "Choose one skill inside the already selected group.") {
				t.Fatalf("prompt is missing second-layer instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "service_restart: Restart a whitelisted service.") {
				t.Fatalf("prompt is missing skill description: %s", prompt)
			}
			if !strings.Contains(prompt, "Allowed services") || !strings.Contains(prompt, "tailscale") {
				t.Fatalf("prompt is missing services: %s", prompt)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"service_restart","arguments":{"service":"tailscale","text":"","id":"","path":"","content":"","find":"","replace":"","runtime":"","code":""},"confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
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
			prompt, ok := payload["prompt"].(string)
			if !ok {
				t.Fatalf("unexpected prompt payload: %#v", payload["prompt"])
			}
			if strings.Contains(prompt, "Allowed services") {
				t.Fatalf("did not expect allowed services section: %s", prompt)
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"status","arguments":{"service":"","text":"","id":"","path":"","content":"","find":"","replace":"","runtime":"","code":""},"confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
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
