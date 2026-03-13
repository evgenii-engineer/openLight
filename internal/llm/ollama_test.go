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

func TestOllamaProviderClassifyIntent(t *testing.T) {
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
			if !strings.Contains(prompt, "service_restart") || !strings.Contains(prompt, "Allowed services") {
				t.Fatalf("prompt is missing decision instructions: %s", prompt)
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
				"response": `{"intent":"service_restart","arguments":{"service":"tailscale"},"confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	classification, err := provider.ClassifyIntent(context.Background(), "restart tailscale", ClassificationRequest{
		AllowedIntents:  []string{"service_restart", "unknown"},
		AllowedServices: []string{"tailscale"},
		InputChars:      160,
		NumPredict:      64,
	})
	if err != nil {
		t.Fatalf("ClassifyIntent returned error: %v", err)
	}
	if classification.Intent != "service_restart" {
		t.Fatalf("unexpected intent: %q", classification.Intent)
	}
	if classification.Arguments["service"] != "tailscale" {
		t.Fatalf("unexpected args: %#v", classification.Arguments)
	}
}

func TestOllamaProviderClassifyIntentTruncatesInputText(t *testing.T) {
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
				"response": `{"intent":"service_restart","arguments":{"service":"tailscale"},"confidence":0.92,"needs_clarification":false,"clarification_question":""}`,
			}), nil
		}),
	}

	_, err := provider.ClassifyIntent(context.Background(), rawText, ClassificationRequest{
		AllowedIntents:  []string{"service_restart", "unknown"},
		AllowedServices: []string{"tailscale"},
		InputChars:      10,
		NumPredict:      64,
	})
	if err != nil {
		t.Fatalf("ClassifyIntent returned error: %v", err)
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
