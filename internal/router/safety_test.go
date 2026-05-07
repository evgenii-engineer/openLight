package router_test

import (
	"context"
	"errors"
	"testing"

	"openlight/internal/router"
	"openlight/internal/skills"
)

// TestRouterEmptyInputIsUnknown documents that the router never panics on
// empty or whitespace-only input. The Mode is ModeUnknown so callers can fall
// back to a help/skills response without doing extra parsing.
func TestRouterEmptyInputIsUnknown(t *testing.T) {
	t.Parallel()

	cases := []string{"", "   ", "\n", "\t\t"}

	registry := skills.NewRegistry()
	r := router.New(registry, nil)
	for _, text := range cases {
		decision, err := r.Route(context.Background(), text)
		if err != nil {
			t.Fatalf("Route(%q) returned error: %v", text, err)
		}
		if decision.Mode != router.ModeUnknown {
			t.Fatalf("Route(%q) Mode=%q, want %q", text, decision.Mode, router.ModeUnknown)
		}
		if decision.SkillName != "" {
			t.Fatalf("Route(%q) selected unexpected skill %q", text, decision.SkillName)
		}
	}
}

// TestRouterUnknownSlashFallsThroughToUnknown ensures a slash command we have
// no built-in for does not panic and does not silently route somewhere
// unexpected. Without an LLM classifier the agent should hand control back as
// unknown so the caller can show /skills.
func TestRouterUnknownSlashFallsThroughToUnknown(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	decision, err := router.New(registry, nil).Route(context.Background(), "/totally-not-a-command-foo")
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if decision.Mode != router.ModeUnknown {
		t.Fatalf("expected ModeUnknown, got %q (skill=%q)", decision.Mode, decision.SkillName)
	}
}

// TestRouterDoesNotInvokeClassifierOnSlashMatch protects the rule "slash beats
// classifier". A misbehaving classifier must not be able to override an
// explicit slash command.
func TestRouterDoesNotInvokeClassifierOnSlashMatch(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu"})

	classifier := &recordingClassifier{
		decision: router.Decision{Mode: router.ModeLLM, SkillName: "memory", Confidence: 0.99},
		ok:       true,
	}

	decision, err := router.New(registry, classifier).Route(context.Background(), "/cpu")
	if err != nil {
		t.Fatalf("Route returned error: %v", err)
	}
	if decision.SkillName != "cpu" {
		t.Fatalf("expected slash to win over classifier, got skill %q", decision.SkillName)
	}
	if classifier.calls != 0 {
		t.Fatalf("expected classifier not to be invoked when slash matches, got %d calls", classifier.calls)
	}
}

// TestRouterPropagatesClassifierError makes sure the agent surfaces an LLM
// failure instead of swallowing it and replying as if nothing matched.
// Without this, a broken classifier could degrade silently to "unknown".
func TestRouterPropagatesClassifierError(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	classifier := &recordingClassifier{err: errors.New("ollama unreachable")}

	// Use a phrase that no rule-based parser matches so the classifier is
	// the only path that can decide.
	_, err := router.New(registry, classifier).Route(context.Background(), "could you maybe summarize the situation for me please")
	if err == nil {
		t.Fatalf("expected classifier error to propagate")
	}
	if classifier.calls != 1 {
		t.Fatalf("expected exactly one classifier invocation, got %d", classifier.calls)
	}
}

type recordingClassifier struct {
	decision router.Decision
	ok       bool
	err      error
	calls    int
}

func (c *recordingClassifier) Classify(context.Context, string) (router.Decision, bool, error) {
	c.calls++
	return c.decision, c.ok, c.err
}
