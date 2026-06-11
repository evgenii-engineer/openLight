package llm

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// ErrBrainOffline is returned by RemoteLLMProvider when the brain node
// cannot be reached. Edge nodes must never fall back to a local model;
// instead this error surfaces the degraded-mode message to the caller.
var ErrBrainOffline = errors.New("Brain node is offline. LLM requests are temporarily unavailable.")

// RemoteLLMProvider forwards all LLM requests to the brain node's
// /llm/generate endpoint. It never calls a local Ollama or any other
// local model — if the brain is unreachable it returns ErrBrainOffline.
//
// The wire protocol is identical to HTTPProvider: task-based JSON payloads
// sent to a single endpoint. This lets RemoteLLMProvider reuse HTTPProvider
// as its inner transport and only add the offline-detection wrapper.
type RemoteLLMProvider struct {
	inner *HTTPProvider
}

// NewRemoteLLMProvider creates a provider that sends all inference requests
// to brainURL/llm/generate. profile ("fast" or "smart") is included in every
// request so the brain can route to the correct local model.
// It satisfies the Provider interface; it does NOT implement Prewarmer so
// runtime warmup is skipped on edge nodes.
func NewRemoteLLMProvider(brainURL, profile string, timeout time.Duration, logger *slog.Logger) *RemoteLLMProvider {
	endpoint := strings.TrimRight(strings.TrimSpace(brainURL), "/") + "/llm/generate"
	return &RemoteLLMProvider{
		inner: NewHTTPProvider(endpoint, timeout, logger).WithProfile(profile),
	}
}

func (p *RemoteLLMProvider) ClassifyRoute(ctx context.Context, text string, request RouteClassificationRequest) (RouteClassification, error) {
	result, err := p.inner.ClassifyRoute(ctx, text, request)
	return result, p.wrapErr(err)
}

func (p *RemoteLLMProvider) ClassifySkill(ctx context.Context, text string, request SkillClassificationRequest) (Classification, error) {
	result, err := p.inner.ClassifySkill(ctx, text, request)
	return result, p.wrapErr(err)
}

func (p *RemoteLLMProvider) Summarize(ctx context.Context, text string) (string, error) {
	result, err := p.inner.Summarize(ctx, text)
	return result, p.wrapErr(err)
}

func (p *RemoteLLMProvider) Chat(ctx context.Context, messages []ChatMessage) (string, error) {
	result, err := p.inner.Chat(ctx, messages)
	return result, p.wrapErr(err)
}

// wrapErr converts network-level errors into ErrBrainOffline so callers
// see a clear degraded-mode message instead of a raw dial error. Other
// errors (HTTP 4xx/5xx, JSON parse failures) are passed through unchanged.
func (p *RemoteLLMProvider) wrapErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	if strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "dial tcp") ||
		strings.Contains(msg, "connect: ") ||
		strings.Contains(msg, "EOF") {
		return ErrBrainOffline
	}
	return err
}
