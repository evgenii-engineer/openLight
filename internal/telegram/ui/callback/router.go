package callback

import (
	"context"
	"fmt"
)

// Message carries the minimal context a callback handler needs.
type Message struct {
	ChatID    int64
	UserID    int64
	MessageID int64
	Raw       string
	Source    string
}

// Handler reacts to one decoded callback action.
type Handler func(ctx context.Context, msg Message, a Action) error

// Router dispatches callbacks by Action.Kind.
type Router struct {
	handlers map[string]Handler
	fallback Handler
}

func NewRouter() *Router {
	return &Router{handlers: make(map[string]Handler)}
}

func (r *Router) On(kind string, h Handler) {
	r.handlers[kind] = h
}

func (r *Router) Fallback(h Handler) {
	r.fallback = h
}

func (r *Router) Dispatch(ctx context.Context, msg Message) error {
	a, err := Decode(msg.Raw)
	if err != nil {
		if r.fallback != nil {
			return r.fallback(ctx, msg, Action{Kind: "unknown"})
		}
		return fmt.Errorf("decode callback: %w", err)
	}
	if h, ok := r.handlers[a.Kind]; ok {
		return h(ctx, msg, a)
	}
	if r.fallback != nil {
		return r.fallback(ctx, msg, a)
	}
	return fmt.Errorf("no handler for callback kind %q", a.Kind)
}
