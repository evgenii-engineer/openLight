package external

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseManifest_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string
	}{
		{
			name: "missing name",
			yaml: `api_version: v1
description: A skill
entrypoint:
  command: /bin/true`,
			want: "name is required",
		},
		{
			name: "missing description",
			yaml: `api_version: v1
name: weather
entrypoint:
  command: /bin/true`,
			want: "description is required",
		},
		{
			name: "missing entrypoint",
			yaml: `api_version: v1
name: weather
description: hi`,
			want: "entrypoint.command or entrypoint.path is required",
		},
		{
			name: "conflicting entrypoint",
			yaml: `api_version: v1
name: weather
description: hi
entrypoint:
  command: /bin/true
  path: run.sh`,
			want: "mutually exclusive",
		},
		{
			name: "unsupported api version",
			yaml: `api_version: v2
name: weather
description: hi
entrypoint:
  command: /bin/true`,
			want: "api_version",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseManifest([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestParseManifest_NormalizesIdentifiers(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
api_version: v1
name: "  Weather "
description: forecast
aliases: ["WEATHER_NOW", "weather_now", "  ", "/forecast"]
triggers: ["Weather", "weather", "rain"]
entrypoint:
  command: /usr/bin/env
  args: [python3, run.py]
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Name != "weather" {
		t.Fatalf("Name = %q, want %q", manifest.Name, "weather")
	}
	if len(manifest.Aliases) != 2 || manifest.Aliases[0] != "weather_now" || manifest.Aliases[1] != "forecast" {
		t.Fatalf("Aliases = %v", manifest.Aliases)
	}
	if len(manifest.Triggers) != 2 || manifest.Triggers[0] != "weather" || manifest.Triggers[1] != "rain" {
		t.Fatalf("Triggers = %v", manifest.Triggers)
	}
	if got := manifest.CommandLine(); len(got) != 3 || got[0] != "/usr/bin/env" {
		t.Fatalf("CommandLine = %v", got)
	}
}

func TestParseManifest_ClampsTimeout(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
api_version: v1
name: slow
description: too slow
timeout: 10m
entrypoint:
  command: /bin/true
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Timeout != MaxTimeout {
		t.Fatalf("Timeout = %v, want %v", manifest.Timeout, MaxTimeout)
	}
}

func TestParseManifest_DefaultsTimeout(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
api_version: v1
name: quick
description: quick
entrypoint:
  command: /bin/true
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	if manifest.Timeout != DefaultTimeout {
		t.Fatalf("Timeout = %v, want %v", manifest.Timeout, DefaultTimeout)
	}
}

func TestParseManifestFile_ResolvesEntrypointPath(t *testing.T) {
	dir := t.TempDir()
	script := filepath.Join(dir, "run.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	manifestPath := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte(`
api_version: v1
name: shell
description: shell skill
entrypoint:
  path: run.sh
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	manifest, err := ParseManifestFile(manifestPath)
	if err != nil {
		t.Fatalf("ParseManifestFile: %v", err)
	}
	if !filepath.IsAbs(manifest.Entrypoint.Path) {
		t.Fatalf("entrypoint not absolute: %s", manifest.Entrypoint.Path)
	}
	if manifest.Dir != dir {
		t.Fatalf("Dir = %s, want %s", manifest.Dir, dir)
	}
}

func TestParseManifestFile_MissingPath(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, ManifestFileName)
	if err := os.WriteFile(manifestPath, []byte(`
api_version: v1
name: shell
description: shell skill
entrypoint:
  path: run.sh
`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	_, err := ParseManifestFile(manifestPath)
	if err == nil {
		t.Fatal("expected error for missing entrypoint path")
	}
	if !strings.Contains(err.Error(), "run.sh") {
		t.Fatalf("error = %v, want mention of run.sh", err)
	}
}

func TestManifest_EnvSliceIsSorted(t *testing.T) {
	manifest, err := ParseManifest([]byte(`
api_version: v1
name: envtest
description: env
entrypoint:
  command: /bin/true
  env:
    Z_KEY: "1"
    A_KEY: "2"
    M_KEY: "3"
`))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	env := manifest.EnvSlice()
	if len(env) != 3 {
		t.Fatalf("EnvSlice len = %d, want 3", len(env))
	}
	if env[0] != "A_KEY=2" || env[1] != "M_KEY=3" || env[2] != "Z_KEY=1" {
		t.Fatalf("EnvSlice = %v, want sorted", env)
	}
}

// Ensure LoadError unwraps to the underlying error so callers can use
// errors.Is for structural checks.
func TestLoadErrorUnwrap(t *testing.T) {
	sentinel := errors.New("boom")
	wrapped := LoadError{Dir: "/x", Err: sentinel}
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("errors.Is failed for LoadError unwrap")
	}
}

// Sanity guard: DefaultTimeout must not exceed MaxTimeout, otherwise
// the clamp logic in normalize is unreachable for unset manifests.
func TestTimeoutOrdering(t *testing.T) {
	if DefaultTimeout > MaxTimeout {
		t.Fatalf("DefaultTimeout %v > MaxTimeout %v", DefaultTimeout, MaxTimeout)
	}
	if DefaultTimeout <= 0 {
		t.Fatalf("DefaultTimeout must be positive, got %v", DefaultTimeout)
	}
	// Smoke check: round-trip parses don't pick up a stray nanosecond.
	if MaxTimeout < time.Second {
		t.Fatalf("MaxTimeout suspiciously small: %v", MaxTimeout)
	}
}
