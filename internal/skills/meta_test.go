package skills

import (
	"context"
	"strings"
	"testing"
)

func TestSkillsSkillGroupsOutput(t *testing.T) {
	t.Parallel()

	registry := NewRegistry()
	registry.MustRegister(testSkill{definition: Definition{Name: "chat", Description: "chat"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_add", Description: "add"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_list", Description: "list"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "note_delete", Description: "delete"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "service_logs", Description: "logs"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "cpu", Description: "cpu"}})
	registry.MustRegister(testSkill{definition: Definition{Name: "help", Description: "help"}})

	result, err := NewSkillsSkill(registry).Execute(context.Background(), Input{})
	if err != nil {
		t.Fatalf("Execute returned error: %v", err)
	}

	text := result.Text
	for _, section := range []string{"Chat", "Notes", "Services", "System", "Core"} {
		if !strings.Contains(text, "\n"+section+"\n") {
			t.Fatalf("expected section %q in output, got:\n%s", section, text)
		}
	}

	chatIdx := strings.Index(text, "\nChat\n")
	notesIdx := strings.Index(text, "\nNotes\n")
	servicesIdx := strings.Index(text, "\nServices\n")
	systemIdx := strings.Index(text, "\nSystem\n")
	coreIdx := strings.Index(text, "\nCore\n")
	if !(chatIdx < notesIdx && notesIdx < servicesIdx && servicesIdx < systemIdx && systemIdx < coreIdx) {
		t.Fatalf("unexpected section order in output:\n%s", text)
	}

	if !strings.Contains(text, "- note_delete: delete") {
		t.Fatalf("expected note_delete entry in output, got:\n%s", text)
	}
}
