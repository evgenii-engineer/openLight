package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildRoutePromptStaysCompact(t *testing.T) {
	t.Parallel()

	prompt := buildRoutePrompt("Дай мне общий статус системы", RouteClassificationRequest{
		Groups: []GroupOption{
			{Key: "notes"},
			{Key: "files"},
			{Key: "workbench"},
			{Key: "services"},
			{Key: "watch"},
			{Key: "accounts"},
			{Key: "system"},
			{Key: "core"},
		},
		InputChars: 96,
		NumPredict: 32,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 1200 {
		t.Fatalf("route prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "Allowed intents:") {
		t.Fatalf("route prompt is missing intents list: %s", prompt)
	}
}

func TestBuildSkillPromptStaysCompact(t *testing.T) {
	t.Parallel()

	prompt := buildSkillPrompt("Дай мне общий статус системы", SkillClassificationRequest{
		AllowedSkills: []string{"cpu", "disk", "hostname", "ip", "memory", "status", "temperature", "uptime"},
		InputChars:    128,
		NumPredict:    48,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 1500 {
		t.Fatalf("skill prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "Allowed skills:") {
		t.Fatalf("skill prompt is missing skills list: %s", prompt)
	}
}

func TestBuildOllamaRoutePromptStaysCompact(t *testing.T) {
	t.Parallel()

	prompt := buildOllamaRoutePrompt("Дай мне общий статус системы", RouteClassificationRequest{
		Groups: []GroupOption{
			{Key: "notes"},
			{Key: "files"},
			{Key: "workbench"},
			{Key: "services"},
			{Key: "watch"},
			{Key: "accounts"},
			{Key: "system"},
			{Key: "core"},
		},
		InputChars: 96,
		NumPredict: 32,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 900 {
		t.Fatalf("ollama route prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "Intent hints:") {
		t.Fatalf("ollama route prompt is missing hints: %s", prompt)
	}
}

func TestBuildOllamaSkillPromptStaysCompact(t *testing.T) {
	t.Parallel()

	prompt := buildOllamaSkillPrompt("Дай мне общий статус системы", SkillClassificationRequest{
		AllowedSkills: []string{"cpu", "disk", "hostname", "ip", "memory", "status", "temperature", "uptime"},
		InputChars:    128,
		NumPredict:    48,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 1100 {
		t.Fatalf("ollama skill prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "Skill hints:") {
		t.Fatalf("ollama skill prompt is missing hints: %s", prompt)
	}
	if !strings.Contains(prompt, "Allowed argument keys: []") {
		t.Fatalf("ollama skill prompt is missing empty argument guide: %s", prompt)
	}
}
