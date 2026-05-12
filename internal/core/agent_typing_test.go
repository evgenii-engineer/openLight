package core

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"openlight/internal/auth"
	"openlight/internal/router"
	"openlight/internal/skills"
)

// typingTransportStub records every SendChatAction call so tests can assert
// the indicator was fired. SendText is the only other surface used by
// HandleMessage on the success path here.
type typingTransportStub struct {
	fakeTransport
	chatActions int32
}

func (t *typingTransportStub) SendChatAction(_ context.Context, _ int64, _ string) error {
	atomic.AddInt32(&t.chatActions, 1)
	return nil
}

func (t *typingTransportStub) ActionCount() int {
	return int(atomic.LoadInt32(&t.chatActions))
}

// transportWithoutTyping deliberately omits SendChatAction so the agent's
// optional-interface fallback takes the no-op path (mirrors the CLI).
type transportWithoutTyping struct {
	fakeTransport
}

func TestStartTypingFiresIndicatorImmediately(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	registry := skills.NewRegistry()
	registry.MustRegister(skills.NewHelpSkill(registry))

	transport := &typingTransportStub{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		nil, // repository — not exercised on this path
		nil,
		nil,
		time.Second,
	)
	_ = agent // satisfy unused

	stop := agent.startTyping(ctx, 200)
	defer stop()

	// Give the goroutine a moment to fire the initial action.
	deadline := time.Now().Add(500 * time.Millisecond)
	for transport.ActionCount() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if transport.ActionCount() == 0 {
		t.Fatalf("expected at least one chat action to be fired after startTyping")
	}
}

func TestStartTypingNoopWithoutTransportSupport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	registry := skills.NewRegistry()

	transport := &transportWithoutTyping{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		nil,
		nil,
		nil,
		time.Second,
	)

	// Should not panic, should return a callable cancel that's safe to call.
	stop := agent.startTyping(ctx, 200)
	stop()
	stop() // second call should be safe (cancel of an already-cancelled context).
}
