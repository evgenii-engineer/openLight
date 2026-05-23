package llm

import (
	"strings"
	"testing"
	"unicode/utf8"
)

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
		NumPredict: 8,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 350 {
		t.Fatalf("ollama route prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "intent?") {
		t.Fatalf("ollama route prompt is missing intent prefix: %s", prompt)
	}
	if !strings.Contains(prompt, "system=cpu/mem/disk/host") {
		t.Fatalf("ollama route prompt is missing compact group hint: %s", prompt)
	}
	if !strings.Contains(prompt, "allowed:") {
		t.Fatalf("ollama route prompt is missing allowed list: %s", prompt)
	}
}

func TestBuildOllamaSkillPromptStaysCompact(t *testing.T) {
	t.Parallel()

	prompt := buildOllamaSkillPrompt("Дай мне общий статус системы", SkillClassificationRequest{
		AllowedSkills: []string{"cpu", "disk", "hostname", "ip", "memory", "status", "temperature", "uptime"},
		InputChars:    128,
		NumPredict:    16,
	})

	if chars := utf8.RuneCountInString(prompt); chars > 250 {
		t.Fatalf("ollama skill prompt too large: %d chars\n%s", chars, prompt)
	}
	if !strings.Contains(prompt, "skill?") {
		t.Fatalf("ollama skill prompt is missing skill prefix: %s", prompt)
	}
	if !strings.Contains(prompt, "allowed:") {
		t.Fatalf("ollama skill prompt is missing allowed list: %s", prompt)
	}
	if !strings.Contains(prompt, "args: []") {
		t.Fatalf("ollama skill prompt is missing empty args marker: %s", prompt)
	}
}

func TestBuildOllamaSkillPromptIncludesServicesAndRuntimes(t *testing.T) {
	t.Parallel()

	prompt := buildOllamaSkillPrompt("restart tailscale", SkillClassificationRequest{
		AllowedSkills:   []string{"service_restart"},
		AllowedServices: []string{"tailscale"},
		InputChars:      128,
		NumPredict:      16,
	})
	if !strings.Contains(prompt, "services: [\"tailscale\"]") {
		t.Fatalf("expected services list in prompt: %s", prompt)
	}
	if strings.Contains(prompt, "runtimes:") {
		t.Fatalf("runtimes line should be omitted when empty: %s", prompt)
	}

	prompt = buildOllamaSkillPrompt("run script", SkillClassificationRequest{
		AllowedSkills:   []string{"exec_code"},
		AllowedRuntimes: []string{"python", "sh"},
		InputChars:      128,
		NumPredict:      16,
	})
	if !strings.Contains(prompt, "runtimes: [\"python\",\"sh\"]") {
		t.Fatalf("expected runtimes list in prompt: %s", prompt)
	}
}
