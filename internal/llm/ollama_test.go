package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
			if num, ok := payload["keep_alive"].(float64); !ok || num != -1 {
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
			if !strings.Contains(prompt, "intent?") {
				t.Fatalf("prompt is missing compact route instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "services=svc status/logs/restart") {
				t.Fatalf("prompt is missing compact route group context: %s", prompt)
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
			if !strings.Contains(prompt, "allowed: [\"service_restart\"]") {
				t.Fatalf("prompt is missing allowed skills list: %s", prompt)
			}
			if !strings.Contains(prompt, "skill?") {
				t.Fatalf("prompt is missing compact second-layer instructions: %s", prompt)
			}
			if !strings.Contains(prompt, "services:") || !strings.Contains(prompt, "tailscale") {
				t.Fatalf("prompt is missing services: %s", prompt)
			}
			if !strings.Contains(prompt, "args: [\"service\"]") {
				t.Fatalf("prompt is missing compact argument guide: %s", prompt)
			}
			format, ok := payload["format"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected format payload: %#v", payload["format"])
			}
			properties, ok := format["properties"].(map[string]any)
			if !ok || properties["skill"] == nil || properties["arguments"] == nil {
				t.Fatalf("skill format is missing properties: %#v", format)
			}
			argumentsSchema, ok := properties["arguments"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected arguments schema: %#v", properties["arguments"])
			}
			argumentProperties, ok := argumentsSchema["properties"].(map[string]any)
			if !ok || len(argumentProperties) != 1 || argumentProperties["service"] == nil {
				t.Fatalf("unexpected argument properties: %#v", argumentsSchema["properties"])
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
			if strings.Contains(prompt, "services:") {
				t.Fatalf("did not expect allowed services section: %s", prompt)
			}
			if !strings.Contains(prompt, "args: []") {
				t.Fatalf("expected empty argument guide: %s", prompt)
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
			if !strings.Contains(prompt, "allowed: [\"status\"]") {
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

func TestOllamaProviderClassifyRouteRecoversTruncatedJSON(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": `{"intent":"system","confidence":0.91,"needs_clarification":false`,
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
	if classification.Intent != "system" {
		t.Fatalf("unexpected classification: %#v", classification)
	}
}

func TestOllamaProviderClassifySkillRecoversTruncatedJSON(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"user_add","arguments":{"provider":"jitsi","username":"smoke","password":"secret"}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifySkill(context.Background(), "add user smoke to jitsi", SkillClassificationRequest{
		AllowedSkills: []string{"user_add"},
		InputChars:    128,
		NumPredict:    48,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
	}
	if classification.Skill != "user_add" {
		t.Fatalf("unexpected skill: %#v", classification)
	}
	if classification.Arguments["provider"] != "jitsi" || classification.Arguments["username"] != "smoke" || classification.Arguments["password"] != "secret" {
		t.Fatalf("unexpected arguments: %#v", classification.Arguments)
	}
}

func TestOllamaProviderClassifySkillSchemaIncludesAccountArguments(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			format, ok := payload["format"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected format payload: %#v", payload["format"])
			}
			properties, ok := format["properties"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected format properties: %#v", format["properties"])
			}
			argumentsSchema, ok := properties["arguments"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected arguments schema: %#v", properties["arguments"])
			}
			argumentProperties, ok := argumentsSchema["properties"].(map[string]any)
			if !ok {
				t.Fatalf("unexpected argument properties: %#v", argumentsSchema["properties"])
			}
			for _, key := range []string{"provider", "username", "password"} {
				if argumentProperties[key] == nil {
					t.Fatalf("expected account argument %q in schema, got %#v", key, argumentProperties)
				}
			}

			return jsonHTTPResponse(map[string]any{
				"response": `{"skill":"user_add","arguments":{"provider":"jitsi","username":"smoke","password":"secret"},"needs_clarification":false}`,
			}), nil
		}),
	}

	_, err := provider.ClassifySkill(context.Background(), "add user smoke to jitsi", SkillClassificationRequest{
		AllowedSkills: []string{"user_add"},
		InputChars:    128,
		NumPredict:    48,
	})
	if err != nil {
		t.Fatalf("ClassifySkill returned error: %v", err)
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
			if num, ok := payload["keep_alive"].(float64); !ok || num != -1 {
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

func TestOllamaProviderNumCtxOmittedWhenZero(t *testing.T) {
	t.Parallel()

	provider := NewOllamaProvider("http://ollama.local:11434", "phi3", time.Second, nil)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			options, ok := payload["options"].(map[string]any)
			if !ok {
				t.Fatalf("missing options payload: %#v", payload)
			}
			if _, present := options["num_ctx"]; present {
				t.Fatalf("num_ctx should be omitted when unset, got %#v", options["num_ctx"])
			}
			return jsonHTTPResponse(map[string]any{
				"message": map[string]any{"content": "ok"},
			}), nil
		}),
	}
	if _, err := provider.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
}

func TestOllamaProviderNumCtxSentInAllPayloads(t *testing.T) {
	t.Parallel()

	check := func(t *testing.T, path string, payload map[string]any) {
		t.Helper()
		options, ok := payload["options"].(map[string]any)
		if !ok {
			t.Fatalf("[%s] missing options payload: %#v", path, payload)
		}
		got, ok := options["num_ctx"].(float64)
		if !ok || int(got) != 1024 {
			t.Fatalf("[%s] num_ctx = %#v, want 1024", path, options["num_ctx"])
		}
	}

	provider := NewOllamaProviderWithOptions(
		"http://ollama.local:11434",
		"phi3",
		OllamaProviderOptions{NumCtx: 1024},
		time.Second,
		nil,
	)
	provider.client = &http.Client{
		Timeout: time.Second,
		Transport: roundTripperFunc(func(r *http.Request) (*http.Response, error) {
			var payload map[string]any
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode request: %v", err)
			}
			check(t, r.URL.Path, payload)
			switch r.URL.Path {
			case "/api/chat":
				return jsonHTTPResponse(map[string]any{
					"message": map[string]any{"content": "ok"},
				}), nil
			case "/api/generate":
				return jsonHTTPResponse(map[string]any{
					"response": `{"summary":"ok"}`,
				}), nil
			default:
				t.Fatalf("unexpected path: %s", r.URL.Path)
				return nil, nil
			}
		}),
	}

	// Chat path.
	if _, err := provider.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("Chat returned error: %v", err)
	}
	// generate path (via Summarize).
	if _, err := provider.Summarize(context.Background(), "text"); err != nil {
		t.Fatalf("Summarize returned error: %v", err)
	}
	// Prewarm path.
	if err := provider.Prewarm(context.Background()); err != nil {
		t.Fatalf("Prewarm returned error: %v", err)
	}
}

func TestOllamaProviderNumCtxNegativeClampsToZero(t *testing.T) {
	t.Parallel()

	p := NewOllamaProviderWithOptions(
		"http://127.0.0.1:11434",
		"x",
		OllamaProviderOptions{NumCtx: -5},
		time.Second,
		nil,
	)
	if p.numCtx != 0 {
		t.Fatalf("expected negative NumCtx to clamp to 0, got %d", p.numCtx)
	}
}

func TestOllamaProviderImplementsPrewarmer(t *testing.T) {
	t.Parallel()

	var p Provider = NewOllamaProvider("http://127.0.0.1:11434", "qwen2.5:1.5b", time.Second, nil)
	if _, ok := p.(Prewarmer); !ok {
		t.Fatalf("expected *OllamaProvider to implement Prewarmer")
	}
}

func TestOllamaProviderKeepAliveDefaults(t *testing.T) {
	t.Parallel()

	p := NewOllamaProvider("http://127.0.0.1:11434", "qwen2.5:1.5b", time.Second, nil)
	if p.keepAlive != defaultOllamaKeepAlive {
		t.Fatalf("expected default keep_alive %q, got %q", defaultOllamaKeepAlive, p.keepAlive)
	}
}

func TestOllamaProviderKeepAliveOverride(t *testing.T) {
	t.Parallel()

	p := NewOllamaProviderWithKeepAlive("http://127.0.0.1:11434", "gemma3-12b-8k", "-1", time.Second, nil)
	if p.keepAlive != "-1" {
		t.Fatalf("expected keep_alive=-1, got %q", p.keepAlive)
	}

	p2 := NewOllamaProviderWithKeepAlive("http://127.0.0.1:11434", "gemma3-12b-8k", "  ", time.Second, nil)
	if p2.keepAlive != defaultOllamaKeepAlive {
		t.Fatalf("blank keep_alive should fall back to default, got %q", p2.keepAlive)
	}
}

func TestOllamaProviderListLoadedModels(t *testing.T) {
	t.Parallel()

	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ps" || r.Method != http.MethodGet {
			http.Error(w, "wrong path", http.StatusBadRequest)
			return
		}
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"models":[{"name":"gemma3-12b-8k:latest","model":"gemma3-12b-8k:latest","size":8138551296,"size_vram":8138551296,"expires_at":"0001-01-01T00:00:00Z","processor":"100% GPU","context_length":8192,"details":{"format":"gguf","family":"gemma3","parameter_size":"12B","quantization_level":"Q4_K_M"}}]}`))
	}))
	defer server.Close()

	p := NewOllamaProvider(server.URL, "gemma3-12b-8k", 5*time.Second, nil)
	loaded, err := p.ListLoadedModels(context.Background())
	if err != nil {
		t.Fatalf("ListLoadedModels: %v", err)
	}
	if !called {
		t.Fatalf("expected /api/ps to be hit")
	}
	if len(loaded) != 1 {
		t.Fatalf("expected 1 model, got %d", len(loaded))
	}
	m := loaded[0]
	if m.Name != "gemma3-12b-8k:latest" {
		t.Fatalf("unexpected name %q", m.Name)
	}
	if m.Processor != "100% GPU" {
		t.Fatalf("unexpected processor %q", m.Processor)
	}
	if m.ContextLen != 8192 {
		t.Fatalf("unexpected context %d", m.ContextLen)
	}
	if !m.ExpiresAt.IsZero() && m.ExpiresAt.Unix() > 0 {
		t.Fatalf("expected pinned-forever expiry (epoch zero), got %v", m.ExpiresAt)
	}
}

func TestOllamaProviderPrewarmWithCustomKeepAlive(t *testing.T) {
	t.Parallel()

	var receivedKeepAlive any
	var receivedPrompt string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		receivedKeepAlive = payload["keep_alive"]
		if p, ok := payload["prompt"].(string); ok {
			receivedPrompt = p
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response":""}`))
	}))
	defer server.Close()

	p := NewOllamaProviderWithKeepAlive(server.URL, "smart-model", "30m", 5*time.Second, nil)
	err := p.PrewarmWith(context.Background(), PrewarmOptions{
		Prompt:    "warmup",
		KeepAlive: "-1",
	})
	if err != nil {
		t.Fatalf("PrewarmWith: %v", err)
	}
	// Ollama rejects "-1" as a string ("missing unit in duration"); the
	// JSON payload must use a raw number. encoding/json decodes JSON
	// numbers into float64 when targeting interface{}.
	num, ok := receivedKeepAlive.(float64)
	if !ok || num != -1 {
		t.Fatalf("expected keep_alive to arrive as numeric -1, got %T %v", receivedKeepAlive, receivedKeepAlive)
	}
	if receivedPrompt != "warmup" {
		t.Fatalf("expected prompt=warmup, got %q", receivedPrompt)
	}
}

func TestKeepAliveValueConvertsNumericsToInt(t *testing.T) {
	t.Parallel()

	cases := map[string]any{
		"":     int64(-1),
		"-1":   int64(-1),
		"0":    int64(0),
		"300":  int64(300),
		"30m":  "30m",
		"24h":  "24h",
		"168h": "168h",
	}
	for input, want := range cases {
		got := keepAliveValue(input)
		if got != want {
			t.Errorf("keepAliveValue(%q) = %T %v, want %T %v", input, got, got, want, want)
		}
	}
}
