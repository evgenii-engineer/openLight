package runtime_test

import (
	"strings"
	"testing"

	"openlight/internal/runtime"
	"openlight/internal/skills"
	"openlight/internal/testkit"
)

// TestBuildRegistryDefaultSkillContract enforces the baseline shape of every
// skill that ships with openLight. It guards against:
//
//   - empty/whitespace skill names that would slip past the registry
//   - duplicate skill registration (the registry rejects this, but we want a
//     fast canary at the app-build level so a skill author finds out
//     immediately, not via a cryptic registration error)
//   - unhidden skills missing a description, which makes /skills useless
//
// The intent is not to lock in the exact list of skills — that should evolve
// freely — but to lock in the *contract* every skill must satisfy.
func TestBuildRegistryDefaultSkillContract(t *testing.T) {
	t.Parallel()

	cfg := testkit.MinimalValidConfig(t)
	repo := testkit.NewTempRepository(t)

	registry, _, err := runtime.BuildRegistry(cfg, repo, testkit.SilentLogger(), nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	defs := registry.List()
	if len(defs) == 0 {
		t.Fatalf("expected the default registry to contain at least one skill")
	}

	seenNames := make(map[string]struct{}, len(defs))
	for _, def := range defs {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			t.Fatalf("found skill with empty Name: %#v", def)
		}
		if _, exists := seenNames[name]; exists {
			t.Fatalf("duplicate skill name in registry: %q", name)
		}
		seenNames[name] = struct{}{}

		if !def.Hidden && strings.TrimSpace(def.Description) == "" {
			t.Fatalf("visible skill %q has no Description — /skills will be unhelpful", name)
		}
	}
}

// TestBuildRegistryIncludesCoreSkills documents the floor of what users
// expect to be present even when LLM, browser, vision, OCR, etc. are
// disabled. If any of these fall out, the bot stops being usable as a
// Telegram ops agent.
func TestBuildRegistryIncludesCoreSkills(t *testing.T) {
	t.Parallel()

	cfg := testkit.MinimalValidConfig(t)
	repo := testkit.NewTempRepository(t)

	registry, _, err := runtime.BuildRegistry(cfg, repo, testkit.SilentLogger(), nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	required := []string{
		"start",
		"help",
		"skills",
		"ping",
		"status",
	}
	for _, name := range required {
		if _, ok := registry.ResolveIdentifier(name); !ok {
			t.Fatalf("expected core skill %q to be registered", name)
		}
	}
}

// TestBuildRegistryGroupsAreNonEmpty asserts every group surfaced by
// /skills has at least one visible skill. Empty groups confuse users.
func TestBuildRegistryGroupsAreNonEmpty(t *testing.T) {
	t.Parallel()

	cfg := testkit.MinimalValidConfig(t)
	repo := testkit.NewTempRepository(t)

	registry, _, err := runtime.BuildRegistry(cfg, repo, testkit.SilentLogger(), nil)
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	for _, group := range registry.ListGroups() {
		if group.Key == skills.GroupOther.Key {
			continue
		}
		if defs := registry.ListByGroup(group.Key); len(defs) == 0 {
			t.Fatalf("group %q has no visible skills", group.Key)
		}
	}
}
