package skills

import (
	"context"
	"testing"
)

type testSkill struct {
	definition Definition
}

func (s testSkill) Definition() Definition {
	return s.definition
}

func (s testSkill) Execute(context.Context, Input) (Result, error) {
	return Result{Text: s.definition.Name}, nil
}

func TestRegistryResolvesAliases(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{
		Name:    "cpu",
		Aliases: []string{"processor usage"},
	}})

	skill, ok := registry.ResolveIdentifier("processor usage")
	if !ok {
		t.Fatal("expected alias to resolve")
	}
	if skill.Definition().Name != "cpu" {
		t.Fatalf("expected cpu skill, got %q", skill.Definition().Name)
	}
}

func TestRegistryRejectsDuplicateAlias(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{
		Name:    "cpu",
		Aliases: []string{"metrics"},
	}})

	err := registry.Register(testSkill{definition: Definition{
		Name:    "memory",
		Aliases: []string{"metrics"},
	}})
	if err == nil {
		t.Fatal("expected duplicate alias registration to fail")
	}
}

func TestRegistryAllowsAliasThatNormalizesToOwnName(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	err := registry.Register(testSkill{definition: Definition{
		Name:    "service_status",
		Aliases: []string{"service status"},
	}})
	if err != nil {
		t.Fatalf("expected self-normalizing alias to be allowed, got: %v", err)
	}
}
