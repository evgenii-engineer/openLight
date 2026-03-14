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

func TestRegistryDefaultsMissingGroupToOther(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{
		Name: "custom_tool",
	}})

	definitions := registry.List()
	if len(definitions) != 1 {
		t.Fatalf("unexpected definitions: %#v", definitions)
	}
	if definitions[0].Group.Key != GroupOther.Key {
		t.Fatalf("expected default other group, got %#v", definitions[0].Group)
	}
}

func TestRegistryListsGroupsAndSkillsByGroup(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{
		Name:        "cpu",
		Group:       GroupSystem,
		Description: "cpu",
	}})
	registry.MustRegister(testSkill{definition: Definition{
		Name:        "note_add",
		Group:       GroupNotes,
		Description: "add note",
	}})

	groups := registry.ListGroups()
	if len(groups) != 2 {
		t.Fatalf("unexpected groups: %#v", groups)
	}
	if groups[0].Key != GroupNotes.Key || groups[1].Key != GroupSystem.Key {
		t.Fatalf("unexpected group order: %#v", groups)
	}

	systemSkills := registry.ListByGroup(GroupSystem.Key)
	if len(systemSkills) != 1 || systemSkills[0].Name != "cpu" {
		t.Fatalf("unexpected system skills: %#v", systemSkills)
	}
}
