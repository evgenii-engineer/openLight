package router_test

import (
	"context"
	"testing"

	"openlight/internal/router"
	"openlight/internal/skills"
)

// TestRouterRussianVoiceCommands locks in deterministic routing for the Russian
// voice commands documented for "openlight voice". These are the wake-word-
// stripped transcripts the voice adapter feeds into the router; no LLM
// classifier is configured, so every one must resolve via the deterministic
// pipeline (normalize → shortcut/rule).
func TestRouterRussianVoiceCommands(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	for _, name := range []string{
		"status", "temperature", "service_status", "service_restart",
		"service_logs", "note_add",
	} {
		registry.MustRegister(testSkill{name: name})
	}

	r := router.New(registry, nil)

	cases := []struct {
		name     string
		text     string
		skill    string
		argKey   string
		argValue string
	}{
		{name: "status server", text: "покажи статус сервера", skill: "status"},
		{name: "restart jitsi", text: "перезапусти джитси", skill: "service_restart", argKey: "service", argValue: "jitsi"},
		{name: "logs synapse", text: "покажи логи synapse", skill: "service_logs", argKey: "service", argValue: "synapse"},
		{name: "temperature", text: "проверь температуру raspberry pi", skill: "temperature"},
		{name: "add note", text: "создай заметку купить филамент", skill: "note_add", argKey: "text", argValue: "купить филамент"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := r.Route(context.Background(), tc.text)
			if err != nil {
				t.Fatalf("route(%q) error: %v", tc.text, err)
			}
			if decision.SkillName != tc.skill {
				t.Fatalf("route(%q) skill = %q, want %q (mode %s)", tc.text, decision.SkillName, tc.skill, decision.Mode)
			}
			if tc.argKey != "" && decision.Args[tc.argKey] != tc.argValue {
				t.Fatalf("route(%q) args[%q] = %q, want %q", tc.text, tc.argKey, decision.Args[tc.argKey], tc.argValue)
			}
		})
	}
}
