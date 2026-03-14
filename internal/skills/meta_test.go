package skills

import (
	"context"
	"strings"
	"testing"
)

func TestSkillsSkillGroupsOutput(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{Name: "chat", Group: GroupChat, Description: "chat"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_add", Group: GroupNotes, Description: "add"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_list", Group: GroupNotes, Description: "list"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_delete", Group: GroupNotes, Description: "delete"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "service_logs", Group: GroupServices, Description: "logs"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "cpu", Group: GroupSystem, Description: "cpu"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "help", Group: GroupCore, Description: "help"}})

	result, err := NewSkillsSkill(registry).Execute(context.Background(), Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	text := result.Text
	if !strings.Contains(text, "Available skill groups:") {
		t.Fatalf("unexpected summary: %q", text)
	}
	for _, section := range []string{
		"- Chat: 1 skill(s). Use skills chat",
		"- Notes: 3 skill(s). Use skills notes",
		"- Services: 1 skill(s). Use skills services",
		"- System: 1 skill(s). Use skills system",
		"- Core: 1 skill(s). Use skills core",
	} {
		if !strings.Contains(text, section) {
			t.Fatalf("expected section %q in output, got:\n%s", section, text)
		}
	}
}

func TestSkillsSkillExpandsGroup(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{Name: "file_list", Group: GroupFiles, Description: "list", Usage: "/files [path]"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "file_read", Group: GroupFiles, Description: "read", Usage: "/read <path>"}})

	result, err := NewSkillsSkill(registry).Execute(context.Background(), Input{
		Args: map[string]string{"topic": "files"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	text := result.Text
	if !strings.Contains(text, "Files: Read, list, write, and replace text inside whitelisted paths.") {
		t.Fatalf("unexpected group details: %q", text)
	}
	if !strings.Contains(text, "file_list: list") || !strings.Contains(text, "Usage: files [path]") {
		t.Fatalf("expected file_list details, got:\n%s", text)
	}
	if !strings.Contains(text, "file_read: read") || !strings.Contains(text, "Usage: read <path>") {
		t.Fatalf("expected file_read details, got:\n%s", text)
	}
}

func TestSkillsSkillCanShowSingleSkill(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{
		Name:        "file_read",
		Group:       GroupFiles,
		Description: "Read a file.",
		Usage:       "/read <path>",
		Aliases:     []string{"read file"},
		Examples:    []string{"read /etc/hostname"},
	}})

	result, err := NewSkillsSkill(registry).Execute(context.Background(), Input{
		Args: map[string]string{"topic": "file_read"},
	})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	text := result.Text
	if !strings.Contains(text, "file_read: Read a file.") {
		t.Fatalf("unexpected skill details: %q", text)
	}
	if !strings.Contains(text, "Aliases: read file") || !strings.Contains(text, "Examples: read /etc/hostname") {
		t.Fatalf("expected aliases/examples, got:\n%s", text)
	}
}
