package llm

import (
	"context"
	"errors"
	"testing"

	basellm "openlight/internal/llm"
	"openlight/internal/skills"
)

// errorProvider lets tests inject errors at either classification stage.
type errorProvider struct {
	routeErr error
	skillErr error

	routeClassification basellm.RouteClassification
}

func (p *errorProvider) ClassifyRoute(context.Context, string, basellm.RouteClassificationRequest) (basellm.RouteClassification, error) {
	if p.routeErr != nil {
		return basellm.RouteClassification{}, p.routeErr
	}
	return p.routeClassification, nil
}

func (p *errorProvider) ClassifySkill(context.Context, string, basellm.SkillClassificationRequest) (basellm.Classification, error) {
	if p.skillErr != nil {
		return basellm.Classification{}, p.skillErr
	}
	return basellm.Classification{}, nil
}

func (p *errorProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (p *errorProvider) Chat(context.Context, []basellm.ChatMessage) (string, error) {
	return "", nil
}

// TestClassifierPropagatesRouteError ensures a network/timeout/decode failure
// at the route stage is surfaced to the caller rather than swallowed into a
// silent "no match". The router relies on this to log and alert.
func TestClassifierPropagatesRouteError(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "chat", group: skills.GroupChat})

	classifier := NewClassifier(&errorProvider{routeErr: errors.New("ollama 503")}, registry, Options{}, nil)
	_, _, err := classifier.Classify(context.Background(), "что у тебя по статусу")
	if err == nil {
		t.Fatalf("expected route classification error to propagate")
	}
}

// TestClassifierPropagatesSkillError mirrors the route case but for the skill
// stage. We need the route to succeed first so the skill stage runs.
func TestClassifierPropagatesSkillError(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart", group: skills.GroupServices, mutating: true})

	classifier := NewClassifier(&errorProvider{
		routeClassification: basellm.RouteClassification{Intent: "services", Confidence: 0.95},
		skillErr:            errors.New("ollama timeout"),
	}, registry, Options{
		AllowedServices: []string{"tailscale"},
	}, nil)

	_, _, err := classifier.Classify(context.Background(), "skroin some service please")
	if err == nil {
		t.Fatalf("expected skill classification error to propagate")
	}
}

// TestClassifierRejectsUnknownSkillReturnedByLLM is the safety-critical case:
// the LLM hallucinates a skill name that the registry does not contain. The
// classifier must NOT route to it.
func TestClassifierRejectsUnknownSkillReturnedByLLM(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart", group: skills.GroupServices, mutating: true})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{Intent: "services", Confidence: 0.97},
		skillClassification: basellm.Classification{
			// Hallucinated skill — registry does not contain "wipe_disk".
			Skill:     "wipe_disk",
			Arguments: map[string]string{"target": "/"},
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "do something risky")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if ok && decision.SkillName == "wipe_disk" {
		t.Fatalf("classifier accepted hallucinated skill %q", decision.SkillName)
	}
}
