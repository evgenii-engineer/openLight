package external

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"openlight/internal/skills"
)

// writeSkillDir is a fixture helper that drops a skill.yaml plus
// (optionally) an executable script into a temp subdirectory. It
// returns the parent directory for use as a loader root.
func writeSkillDir(t *testing.T, parent, name, manifest, scriptName, scriptBody string) {
	t.Helper()
	dir := filepath.Join(parent, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestFileName), []byte(manifest), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if scriptName != "" {
		if err := os.WriteFile(filepath.Join(dir, scriptName), []byte(scriptBody), 0o755); err != nil {
			t.Fatalf("write script: %v", err)
		}
	}
}

func TestDiscoverRoots_HappyPath(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "weather", `
api_version: v1
name: weather
description: forecast
entrypoint:
  command: /bin/true
`, "", "")
	writeSkillDir(t, root, "echo", `
api_version: v1
name: echo
description: echo lines
entrypoint:
  path: run.sh
`, "run.sh", "#!/bin/sh\necho hi\n")

	result := DiscoverRoots([]string{root}, slog.New(slog.DiscardHandler))
	if len(result.Errors) != 0 {
		t.Fatalf("unexpected errors: %+v", result.Errors)
	}
	if len(result.Manifests) != 2 {
		t.Fatalf("got %d manifests, want 2", len(result.Manifests))
	}
	// Sorted by name: echo, weather.
	if result.Manifests[0].Name != "echo" || result.Manifests[1].Name != "weather" {
		t.Fatalf("unexpected order: %s, %s", result.Manifests[0].Name, result.Manifests[1].Name)
	}
}

func TestDiscoverRoots_MissingRootIsSkipped(t *testing.T) {
	result := DiscoverRoots([]string{filepath.Join(t.TempDir(), "does-not-exist")}, slog.New(slog.DiscardHandler))
	if len(result.Manifests) != 0 || len(result.Errors) != 0 {
		t.Fatalf("expected silent skip, got manifests=%d errors=%d", len(result.Manifests), len(result.Errors))
	}
}

func TestDiscoverRoots_BrokenManifestReported(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "broken", "this is not valid yaml: : :", "", "")
	result := DiscoverRoots([]string{root}, slog.New(slog.DiscardHandler))
	if len(result.Errors) != 1 {
		t.Fatalf("want 1 error, got %d", len(result.Errors))
	}
	if !strings.Contains(result.Errors[0].Dir, "broken") {
		t.Fatalf("error dir = %s, want broken in path", result.Errors[0].Dir)
	}
}

func TestDiscoverRoots_DirectoryWithoutManifestIgnored(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "not-a-skill"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	result := DiscoverRoots([]string{root}, slog.New(slog.DiscardHandler))
	if len(result.Manifests) != 0 || len(result.Errors) != 0 {
		t.Fatalf("expected nothing, got manifests=%d errors=%d", len(result.Manifests), len(result.Errors))
	}
}

func TestDiscoverRoots_DuplicateNameRejected(t *testing.T) {
	rootA := t.TempDir()
	rootB := t.TempDir()
	writeSkillDir(t, rootA, "weather", `
api_version: v1
name: weather
description: A
entrypoint:
  command: /bin/true
`, "", "")
	writeSkillDir(t, rootB, "weather", `
api_version: v1
name: weather
description: B
entrypoint:
  command: /bin/true
`, "", "")
	result := DiscoverRoots([]string{rootA, rootB}, slog.New(slog.DiscardHandler))
	if len(result.Manifests) != 1 {
		t.Fatalf("want 1 manifest after dedupe, got %d", len(result.Manifests))
	}
	if result.Manifests[0].Description != "A" {
		t.Fatalf("first root should win, got description %q", result.Manifests[0].Description)
	}
	if len(result.Errors) != 1 {
		t.Fatalf("want 1 duplicate error, got %d", len(result.Errors))
	}
	if !strings.Contains(result.Errors[0].Err.Error(), "duplicate") {
		t.Fatalf("error %v should mention duplicate", result.Errors[0].Err)
	}
}

func TestModule_RegistersDiscoveredSkills(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "weather", `
api_version: v1
name: weather
description: forecast
entrypoint:
  command: /bin/true
`, "", "")

	registry := skills.NewRegistry()
	module := NewModule(Options{Enabled: true, Roots: []string{root}}, slog.New(slog.DiscardHandler))
	if err := module.Register(registry); err != nil {
		t.Fatalf("module.Register: %v", err)
	}
	skill, ok := registry.Get("weather")
	if !ok {
		t.Fatal("expected weather skill to be registered")
	}
	if skill.Definition().Description != "forecast" {
		t.Fatalf("definition not loaded: %+v", skill.Definition())
	}
}

func TestModule_DisabledIsNoop(t *testing.T) {
	root := t.TempDir()
	writeSkillDir(t, root, "weather", `
api_version: v1
name: weather
description: forecast
entrypoint:
  command: /bin/true
`, "", "")

	registry := skills.NewRegistry()
	module := NewModule(Options{Enabled: false, Roots: []string{root}}, slog.New(slog.DiscardHandler))
	if err := module.Register(registry); err != nil {
		t.Fatalf("module.Register: %v", err)
	}
	if _, ok := registry.Get("weather"); ok {
		t.Fatal("disabled module should not register skills")
	}
}

func TestResolveGroup(t *testing.T) {
	cases := map[string]string{
		"":         "other",
		"OTHER":    "other",
		"system":   "system",
		"Network":  "network",
		"unknown":  "other",
		"visual-watch": "visual_watch",
	}
	for in, want := range cases {
		if got := resolveGroup(in).Key; got != want {
			t.Fatalf("resolveGroup(%q).Key = %q, want %q", in, got, want)
		}
	}
}
