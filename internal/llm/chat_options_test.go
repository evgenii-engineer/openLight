package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// capture records the JSON body of the last request a provider sent.
type capture struct {
	body map[string]any
}

func (c *capture) server(t *testing.T, reply string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		c.body = map[string]any{}
		_ = json.Unmarshal(raw, &c.body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(server.Close)
	return server
}

func (c *capture) options(t *testing.T) map[string]any {
	t.Helper()
	opts, ok := c.body["options"].(map[string]any)
	if !ok {
		t.Fatalf("no options in payload: %+v", c.body)
	}
	return opts
}

func TestOllamaChatDefaultsToTheShortReplyBudget(t *testing.T) {
	c := &capture{}
	server := c.server(t, `{"message":{"content":"ok"}}`)
	provider := NewOllamaProvider(server.URL, "m", 5*time.Second, nil)

	if _, err := provider.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	if got := c.options(t)["num_predict"]; got != float64(defaultChatNumPredict) {
		t.Fatalf("num_predict = %v, want the %d default", got, defaultChatNumPredict)
	}
}

func TestOllamaChatWithOptionsWidensTheBudget(t *testing.T) {
	c := &capture{}
	server := c.server(t, `{"message":{"content":"ok"}}`)
	provider := NewOllamaProvider(server.URL, "m", 5*time.Second, nil)

	// This is the case that broke memory distillation: the JSON answer
	// needs far more than 64 tokens, and the prompt needs the window
	// widened to make room for it.
	_, err := provider.ChatWithOptions(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}},
		ChatOptions{NumPredict: 640, NumCtx: 4096})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	opts := c.options(t)
	if opts["num_predict"] != float64(640) {
		t.Fatalf("num_predict = %v, want 640", opts["num_predict"])
	}
	if opts["num_ctx"] != float64(4096) {
		t.Fatalf("num_ctx = %v, want 4096", opts["num_ctx"])
	}
}

func TestOllamaChatOptionsDoNotLeakIntoTheNextCall(t *testing.T) {
	c := &capture{}
	server := c.server(t, `{"message":{"content":"ok"}}`)
	provider := NewOllamaProvider(server.URL, "m", 5*time.Second, nil)

	if _, err := provider.ChatWithOptions(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}},
		ChatOptions{NumPredict: 640, NumCtx: 4096}); err != nil {
		t.Fatalf("first: %v", err)
	}
	if _, err := provider.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("second: %v", err)
	}

	opts := c.options(t)
	if opts["num_predict"] != float64(defaultChatNumPredict) {
		t.Fatalf("a per-call override leaked into the next call: %v", opts["num_predict"])
	}
	if _, present := opts["num_ctx"]; present {
		t.Fatalf("num_ctx leaked into a call that did not ask for it: %v", opts["num_ctx"])
	}
}

func TestHTTPProviderForwardsChatOptionsToTheBrain(t *testing.T) {
	c := &capture{}
	server := c.server(t, `{"response":"ok"}`)
	provider := NewHTTPProvider(server.URL, 5*time.Second, nil)

	_, err := provider.ChatWithOptions(context.Background(),
		[]ChatMessage{{Role: "user", Content: "hi"}},
		ChatOptions{NumPredict: 512, NumCtx: 2048})
	if err != nil {
		t.Fatalf("chat: %v", err)
	}

	if c.body["task"] != "chat" {
		t.Fatalf("task = %v", c.body["task"])
	}
	if c.body["num_predict"] != float64(512) || c.body["num_ctx"] != float64(2048) {
		t.Fatalf("options not forwarded: %+v", c.body)
	}
}

func TestHTTPProviderOmitsUnsetChatOptions(t *testing.T) {
	c := &capture{}
	server := c.server(t, `{"response":"ok"}`)
	provider := NewHTTPProvider(server.URL, 5*time.Second, nil)

	if _, err := provider.Chat(context.Background(), []ChatMessage{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("chat: %v", err)
	}

	// An older brain build ignores unknown keys, but sending zeros would
	// tell it to generate nothing at all.
	if _, present := c.body["num_predict"]; present {
		t.Fatalf("num_predict sent when unset: %+v", c.body)
	}
	if _, present := c.body["num_ctx"]; present {
		t.Fatalf("num_ctx sent when unset: %+v", c.body)
	}
}

// plainProvider implements Provider but not OptionChatter.
type plainProvider struct{ called bool }

func (p *plainProvider) Chat(context.Context, []ChatMessage) (string, error) {
	p.called = true
	return "ok", nil
}
func (p *plainProvider) ClassifyRoute(context.Context, string, RouteClassificationRequest) (RouteClassification, error) {
	return RouteClassification{}, nil
}
func (p *plainProvider) ClassifySkill(context.Context, string, SkillClassificationRequest) (Classification, error) {
	return Classification{}, nil
}
func (p *plainProvider) Summarize(context.Context, string) (string, error) { return "", nil }

func TestChatWithFallsBackForProvidersWithoutOptions(t *testing.T) {
	provider := &plainProvider{}

	result, err := ChatWith(context.Background(), provider,
		[]ChatMessage{{Role: "user", Content: "hi"}}, ChatOptions{NumPredict: 640})
	if err != nil || result != "ok" {
		t.Fatalf("ChatWith = %q / %v", result, err)
	}
	if !provider.called {
		t.Fatal("expected the plain Chat path to be used")
	}
}

// Both real providers must be usable wherever options are passed.
var (
	_ OptionChatter = (*OllamaProvider)(nil)
	_ OptionChatter = (*HTTPProvider)(nil)
	_ OptionChatter = (*RemoteLLMProvider)(nil)
)
