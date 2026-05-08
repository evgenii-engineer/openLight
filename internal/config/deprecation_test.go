package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadEmitsDeprecationForLegacyFilesystemKey(t *testing.T) {
	clearConfigEnv(t)

	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, `
telegram:
  bot_token: "t"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

filesystem:
  enabled: true
  allowed_roots: ["/tmp/x"]
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !containsSubstring(cfg.Deprecations, "`filesystem:`") {
		t.Fatalf("expected filesystem deprecation, got %#v", cfg.Deprecations)
	}
	// alias still works: legacy fields get merged into the canonical
	// Files field after normalize.
	if !cfg.Files.Enabled || len(cfg.Files.Allowed) == 0 {
		t.Fatalf("legacy filesystem keys should still populate Files: %#v", cfg.Files)
	}
}

func TestLoadEmitsDeprecationForLegacyAccessHosts(t *testing.T) {
	clearConfigEnv(t)

	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, `
telegram:
  bot_token: "t"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

access:
  hosts:
    pi:
      address: "192.168.1.10:22"
      user: "pi"
      private_key_path: "~/.ssh/id_ed25519"
      known_hosts_path: "~/.ssh/known_hosts"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !containsSubstring(cfg.Deprecations, "`access.hosts:`") {
		t.Fatalf("expected access.hosts deprecation, got %#v", cfg.Deprecations)
	}
	if _, ok := cfg.Access.Hosts["pi"]; !ok {
		t.Fatalf("legacy access.hosts should still resolve into Access.Hosts: %#v", cfg.Access.Hosts)
	}
}

func TestLoadNoDeprecationsForCanonicalKeys(t *testing.T) {
	clearConfigEnv(t)

	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, `
telegram:
  bot_token: "t"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

files:
  enabled: true
  allowed_roots: ["/tmp/x"]

nodes:
  pi:
    address: "192.168.1.10:22"
    user: "pi"
    private_key_path: "~/.ssh/id_ed25519"
    known_hosts_path: "~/.ssh/known_hosts"
`)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(cfg.Deprecations) != 0 {
		t.Fatalf("expected no deprecations, got %#v", cfg.Deprecations)
	}
}

func containsSubstring(values []string, needle string) bool {
	for _, v := range values {
		if strings.Contains(v, needle) {
			return true
		}
	}
	return false
}
