package llm

import (
	"context"
	"testing"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/skills"
)

type stubProvider struct {
	classification basellm.Classification
	request        basellm.ClassificationRequest
}

func (s *stubProvider) ClassifyIntent(_ context.Context, _ string, request basellm.ClassificationRequest) (basellm.Classification, error) {
	s.request = request
	return s.classification, nil
}

func (s *stubProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (s *stubProvider) Chat(context.Context, []basellm.ChatMessage) (string, error) {
	return "", nil
}

type testSkill struct {
	name string
}

func (s testSkill) Definition() skills.Definition {
	return skills.Definition{Name: s.name}
}

func (s testSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: s.name}, nil
}

func TestClassifierRoutesHighConfidenceIntent(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart"})

	classifier := NewClassifier(&stubProvider{
		classification: basellm.Classification{
			Intent:     "service_restart",
			Arguments:  map[string]string{"service": "tailscale"},
			Confidence: 0.93,
		},
	}, registry, Options{
		AllowedServices: []string{"tailscale"},
	}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "перезапусти tailscale")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected classifier match")
	}
	if decision.Mode != router.ModeLLM || decision.SkillName != "service_restart" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["service"] != "tailscale" {
		t.Fatalf("unexpected args: %#v", decision.Args)
	}
}

func TestClassifierAsksForClarificationOnMidConfidence(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart"})

	classifier := NewClassifier(&stubProvider{
		classification: basellm.Classification{
			Intent:     "service_restart",
			Arguments:  map[string]string{},
			Confidence: 0.72,
		},
	}, registry, Options{
		AllowedServices: []string{"tailscale"},
	}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "перезапусти сервис")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected clarification decision")
	}
	if !decision.ShouldClarify() {
		t.Fatalf("expected clarification, got %#v", decision)
	}
	if decision.ClarificationQuestion != "Which service should I restart?" {
		t.Fatalf("unexpected clarification question: %q", decision.ClarificationQuestion)
	}
}

func TestClassifierFallsBackOnLowConfidenceUnknown(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "chat"})

	classifier := NewClassifier(&stubProvider{
		classification: basellm.Classification{
			Intent:     "unknown",
			Arguments:  map[string]string{},
			Confidence: 0.32,
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "инет тупит")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if ok {
		t.Fatalf("expected no match, got %#v", decision)
	}
}

func TestClassifierRoutesChatWithOriginalText(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "chat"})

	classifier := NewClassifier(&stubProvider{
		classification: basellm.Classification{
			Intent:     "chat",
			Arguments:  map[string]string{"text": "ignored"},
			Confidence: 0.88,
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "привет, как дела")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected chat decision")
	}
	if decision.SkillName != "chat" {
		t.Fatalf("unexpected skill: %#v", decision)
	}
	if decision.Args["text"] != "привет, как дела" {
		t.Fatalf("unexpected chat args: %#v", decision.Args)
	}
}

func TestClassifierDefaultsSingleAllowedService(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_status"})

	classifier := NewClassifier(&stubProvider{
		classification: basellm.Classification{
			Intent:     "service_status",
			Arguments:  map[string]string{},
			Confidence: 0.91,
		},
	}, registry, Options{
		AllowedServices: []string{"tailscale"},
	}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "что со статусом сервиса")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected service_status decision")
	}
	if decision.SkillName != "service_status" || decision.Args["service"] != "tailscale" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestClassifierPassesDecisionLimitsToProvider(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu"})

	provider := &stubProvider{
		classification: basellm.Classification{
			Intent:     "cpu",
			Arguments:  map[string]string{},
			Confidence: 0.91,
		},
	}

	classifier := NewClassifier(provider, registry, Options{
		InputChars: 120,
		NumPredict: 48,
	}, nil)

	_, ok, err := classifier.Classify(context.Background(), "что с cpu")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected classifier match")
	}
	if provider.request.InputChars != 120 {
		t.Fatalf("unexpected input chars: %#v", provider.request)
	}
	if provider.request.NumPredict != 48 {
		t.Fatalf("unexpected num_predict: %#v", provider.request)
	}
}
