package llm

import (
	"context"
	"slices"
	"testing"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/skills"
)

type stubProvider struct {
	routeClassification basellm.RouteClassification
	skillClassification basellm.Classification
	routeRequest        basellm.RouteClassificationRequest
	skillRequest        basellm.SkillClassificationRequest
}

func (s *stubProvider) ClassifyRoute(_ context.Context, _ string, request basellm.RouteClassificationRequest) (basellm.RouteClassification, error) {
	s.routeRequest = request
	return s.routeClassification, nil
}

func (s *stubProvider) ClassifySkill(_ context.Context, _ string, request basellm.SkillClassificationRequest) (basellm.Classification, error) {
	s.skillRequest = request
	return s.skillClassification, nil
}

func (s *stubProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (s *stubProvider) Chat(context.Context, []basellm.ChatMessage) (string, error) {
	return "", nil
}

type testSkill struct {
	name     string
	group    skills.Group
	mutating bool
}

func (s testSkill) Definition() skills.Definition {
	return skills.Definition{Name: s.name, Group: s.group, Mutating: s.mutating}
}

func (s testSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: s.name}, nil
}

func TestClassifierRoutesHighConfidenceIntent(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart", group: skills.GroupServices, mutating: true})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "services",
			Confidence: 0.97,
		},
		skillClassification: basellm.Classification{
			Skill:     "service_restart",
			Arguments: map[string]string{"service": "tailscale"},
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

func TestClassifierAsksForClarificationWhenRequiredArgsAreMissing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart", group: skills.GroupServices, mutating: true})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "services",
			Confidence: 0.95,
		},
		skillClassification: basellm.Classification{
			Skill:     "service_restart",
			Arguments: map[string]string{},
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
	registry.MustRegister(testSkill{name: "chat", group: skills.GroupChat})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "unknown",
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
	registry.MustRegister(testSkill{name: "chat", group: skills.GroupChat})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "chat",
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
	registry.MustRegister(testSkill{name: "service_status", group: skills.GroupServices})

	provider := &stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "services",
			Confidence: 0.91,
		},
		skillClassification: basellm.Classification{
			Skill:     "service_status",
			Arguments: map[string]string{},
		},
	}

	classifier := NewClassifier(provider, registry, Options{
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
	if !slices.Equal(provider.skillRequest.AllowedServices, []string{"tailscale"}) {
		t.Fatalf("expected allowed services for services group, got %#v", provider.skillRequest.AllowedServices)
	}
}

func TestClassifierRoutesMutatingSkillWhenRouteIsConfident(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_restart", group: skills.GroupServices, mutating: true})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "services",
			Confidence: 0.91,
		},
		skillClassification: basellm.Classification{
			Skill:     "service_restart",
			Arguments: map[string]string{"service": "tailscale"},
		},
	}, registry, Options{
		AllowedServices: []string{"tailscale"},
	}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "restart tailscale")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected classifier match")
	}
	if decision.ShouldClarify() {
		t.Fatalf("did not expect clarification for mutating skill, got %#v", decision)
	}
	if decision.SkillName != "service_restart" || decision.Args["service"] != "tailscale" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Confidence != 0.91 {
		t.Fatalf("expected route confidence, got %#v", decision)
	}
}

func TestClassifierUsesRouteConfidenceForValidSkillSelection(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "status", group: skills.GroupSystem})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "system",
			Confidence: 0.95,
		},
		skillClassification: basellm.Classification{
			Skill:     "status",
			Arguments: map[string]string{},
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "общий статус")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected status decision")
	}
	if decision.SkillName != "status" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Confidence != 0.95 {
		t.Fatalf("expected route confidence, got %#v", decision)
	}
}

func TestClassifierClarifiesMissingRequiredArgsUsingRouteConfidence(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_read", group: skills.GroupFiles})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "files",
			Confidence: 0.94,
		},
		skillClassification: basellm.Classification{
			Skill:     "file_read",
			Arguments: map[string]string{},
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "read something")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatalf("expected clarification decision")
	}
	if !decision.ShouldClarify() {
		t.Fatalf("expected clarification, got %#v", decision)
	}
	if decision.ClarificationQuestion != "Which file should I read?" {
		t.Fatalf("unexpected clarification question: %q", decision.ClarificationQuestion)
	}
	if decision.Confidence != 0.94 {
		t.Fatalf("expected route confidence, got %#v", decision)
	}
}

func TestClassifierRoutesFileReadWithPath(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_read", group: skills.GroupFiles})

	classifier := NewClassifier(&stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "files",
			Confidence: 0.93,
		},
		skillClassification: basellm.Classification{
			Skill:     "file_read",
			Arguments: map[string]string{"path": "/etc/hostname"},
		},
	}, registry, Options{}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "read /etc/hostname")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected classifier match")
	}
	if decision.SkillName != "file_read" || decision.Args["path"] != "/etc/hostname" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestClassifierPassesWorkbenchRuntimesAndRoutesExecCode(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "exec_code", group: skills.GroupWorkbench, mutating: true})

	provider := &stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "workbench",
			Confidence: 0.98,
		},
		skillClassification: basellm.Classification{
			Skill:     "exec_code",
			Arguments: map[string]string{"runtime": "python", "code": "print('hello')"},
		},
	}

	classifier := NewClassifier(provider, registry, Options{
		AllowedWorkbenchRuntimes: []string{"python"},
	}, nil)

	decision, ok, err := classifier.Classify(context.Background(), "run python: print('hello')")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected classifier match")
	}
	if decision.SkillName != "exec_code" || decision.Args["runtime"] != "python" || decision.Args["code"] != "print('hello')" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if !slices.Equal(provider.skillRequest.AllowedRuntimes, []string{"python"}) {
		t.Fatalf("expected allowed runtimes for workbench group, got %#v", provider.skillRequest.AllowedRuntimes)
	}
}

func TestClassifierPassesVisibleSkillsToLLMWithoutHeuristicShortlist(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_read", group: skills.GroupFiles})
	registry.MustRegister(testSkill{name: "memory", group: skills.GroupSystem})
	registry.MustRegister(testSkill{name: "cpu", group: skills.GroupSystem})
	registry.MustRegister(testSkill{name: "chat", group: skills.GroupChat})
	registry.MustRegister(testSkill{name: "skills", group: skills.GroupCore})

	provider := &stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "system",
			Confidence: 0.92,
		},
		skillClassification: basellm.Classification{
			Skill:     "memory",
			Arguments: map[string]string{},
		},
	}

	classifier := NewClassifier(provider, registry, Options{}, nil)

	_, ok, err := classifier.Classify(context.Background(), "что там по оперативке")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected classifier match")
	}
	if !slices.Contains(provider.skillRequest.AllowedSkills, "memory") {
		t.Fatalf("expected memory in allowed skills, got %#v", provider.skillRequest.AllowedSkills)
	}
	if !slices.Contains(provider.skillRequest.AllowedSkills, "cpu") {
		t.Fatalf("expected cpu in allowed skills, got %#v", provider.skillRequest.AllowedSkills)
	}
	if slices.Contains(provider.skillRequest.AllowedSkills, "chat") {
		t.Fatalf("did not expect chat in skill allowed skills, got %#v", provider.skillRequest.AllowedSkills)
	}
	if len(provider.routeRequest.Groups) == 0 {
		t.Fatalf("expected groups in route request: %#v", provider.routeRequest)
	}
	if len(provider.skillRequest.CandidateSkills) == 0 {
		t.Fatalf("expected available skills in skill request: %#v", provider.skillRequest)
	}
	if !slices.Contains(groupOptionKeys(provider.routeRequest.Groups), "system") {
		t.Fatalf("expected system group in route request: %#v", provider.routeRequest.Groups)
	}
	if !slices.Contains(groupOptionKeys(provider.routeRequest.Groups), "files") {
		t.Fatalf("expected files group in route request: %#v", provider.routeRequest.Groups)
	}
	if len(provider.skillRequest.AllowedServices) != 0 {
		t.Fatalf("did not expect allowed services for non-services group, got %#v", provider.skillRequest.AllowedServices)
	}
}

func TestClassifierPassesDecisionLimitsToProvider(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu", group: skills.GroupSystem})

	provider := &stubProvider{
		routeClassification: basellm.RouteClassification{
			Intent:     "system",
			Confidence: 0.91,
		},
		skillClassification: basellm.Classification{
			Skill:     "cpu",
			Arguments: map[string]string{},
		},
	}

	classifier := NewClassifier(provider, registry, Options{
		InputChars: 160,
		NumPredict: 128,
	}, nil)

	_, ok, err := classifier.Classify(context.Background(), "что с cpu")
	if err != nil {
		t.Fatalf("Classify returned error: %v", err)
	}
	if !ok {
		t.Fatal("expected classifier match")
	}
	if provider.routeRequest.InputChars != defaultRouteInputChars {
		t.Fatalf("unexpected route input chars: %#v", provider.routeRequest)
	}
	if provider.routeRequest.NumPredict != defaultRouteNumPredict {
		t.Fatalf("unexpected route num_predict: %#v", provider.routeRequest)
	}
	if provider.skillRequest.InputChars != defaultSkillInputChars {
		t.Fatalf("unexpected skill input chars: %#v", provider.skillRequest)
	}
	if provider.skillRequest.NumPredict != defaultSkillNumPredict {
		t.Fatalf("unexpected skill num_predict: %#v", provider.skillRequest)
	}
}
