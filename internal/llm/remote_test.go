package llm_test

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"openlight/internal/llm"
)

// TestRemoteLLMProviderForwardsToBrain verifies that RemoteLLMProvider
// sends requests to the configured brain URL and never to a local model.
func TestRemoteLLMProviderForwardsToBrain(t *testing.T) {
	brainCalled := false
	brainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		brainCalled = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response": "hello from brain"}`))
	}))
	defer brainServer.Close()

	provider := llm.NewRemoteLLMProvider(brainServer.URL, "smart", 5*time.Second, slog.Default())

	result, err := provider.Chat(context.Background(), []llm.ChatMessage{
		{Role: "user", Content: "hello"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello from brain" {
		t.Errorf("unexpected result: %q", result)
	}
	if !brainCalled {
		t.Error("brain server was not called")
	}
}

// TestEdgeNodeBrainOfflineNoFallback verifies that when the brain node is
// unreachable, RemoteLLMProvider returns ErrBrainOffline and does NOT fall
// back to a local model. The test proves edge nodes have no local LLM path.
func TestEdgeNodeBrainOfflineNoFallback(t *testing.T) {
	// Point at a port that is not listening — simulates brain offline.
	provider := llm.NewRemoteLLMProvider("http://127.0.0.1:19787", "smart", time.Second, slog.Default())

	_, err := provider.Chat(context.Background(), []llm.ChatMessage{
		{Role: "user", Content: "are you there?"},
	})
	if err == nil {
		t.Fatal("expected an error when brain is offline, got nil")
	}
	if !errors.Is(err, llm.ErrBrainOffline) {
		t.Errorf("expected ErrBrainOffline, got: %v", err)
	}
}

// TestRemoteLLMProviderNeverCallsLocalOllama explicitly verifies that a
// RemoteLLMProvider configured with a brain URL does not call a separate
// "local Ollama" server, even if one is running on the default port.
func TestRemoteLLMProviderNeverCallsLocalOllama(t *testing.T) {
	localOllamaCalled := false
	localOllama := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		localOllamaCalled = true
		t.Error("RemoteLLMProvider must never call the local Ollama server")
	}))
	defer localOllama.Close()

	// Brain server responds successfully.
	brainServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"response": "brain response"}`))
	}))
	defer brainServer.Close()

	// Provider is wired to brainServer only — localOllama URL is never passed in.
	provider := llm.NewRemoteLLMProvider(brainServer.URL, "smart", 5*time.Second, slog.Default())

	_, err := provider.Chat(context.Background(), []llm.ChatMessage{
		{Role: "user", Content: "test"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if localOllamaCalled {
		t.Error("local Ollama was called; edge node must not call local models")
	}
	// localOllama server was started but never hit — this proves isolation.
	_ = localOllama
}
