package external

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"openlight/internal/skills"
)

// fakeRunner stands in for [execRunner] so adapter tests don't need to
// spawn real processes. The callback receives the parsed request and
// the parent context, and returns whatever stdout/stderr/err the test
// wants. Returning a non-nil error from the callback simulates a
// subprocess that exited non-zero or failed to start.
type fakeRunner struct {
	respond func(ctx context.Context, req Request) (stdout []byte, stderr []byte, err error)
}

func (f fakeRunner) run(ctx context.Context, cmd []string, env []string, dir string, stdin []byte) ([]byte, []byte, error) {
	var req Request
	if err := json.Unmarshal(stdin, &req); err != nil {
		return nil, nil, err
	}
	return f.respond(ctx, req)
}

func newTestAdapter(t *testing.T, m Manifest, runner processRunner) *adapter {
	t.Helper()
	return newAdapter(m, slog.New(slog.DiscardHandler), runner)
}

func mustManifest(t *testing.T, yaml string) Manifest {
	t.Helper()
	m, err := ParseManifest([]byte(yaml))
	if err != nil {
		t.Fatalf("ParseManifest: %v", err)
	}
	return m
}

func TestAdapter_DefinitionMirrorsManifest(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: weather
description: forecast
group: network
aliases: [forecast]
examples: [tomorrow]
mutating: true
hidden: false
entrypoint:
  command: /bin/true
`)
	a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
		return []byte(`{"ok":true,"message":"ok"}`), nil, nil
	}})
	def := a.Definition()
	if def.Name != "weather" {
		t.Fatalf("Name = %q", def.Name)
	}
	if def.Group.Key != "network" {
		t.Fatalf("Group = %v", def.Group)
	}
	if !def.Mutating {
		t.Fatal("Mutating not propagated")
	}
	if len(def.Aliases) != 1 || def.Aliases[0] != "forecast" {
		t.Fatalf("Aliases = %v", def.Aliases)
	}
}

func TestAdapter_Execute_RequestEnvelope(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: echo
description: echo
version: 1.2.3
entrypoint:
  command: /bin/true
`)
	var captured Request
	a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
		captured = req
		return []byte(`{"ok":true,"message":"pong"}`), nil, nil
	}})
	_, err := a.Execute(context.Background(), skills.Input{
		RawText: "hello",
		Args:    map[string]string{"topic": "weather"},
		UserID:  42,
		ChatID:  99,
		Source:  "cli",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if captured.APIVersion != APIVersion {
		t.Fatalf("APIVersion = %q", captured.APIVersion)
	}
	if captured.Skill.Name != "echo" || captured.Skill.Version != "1.2.3" {
		t.Fatalf("Skill = %+v", captured.Skill)
	}
	if captured.Input.RawText != "hello" {
		t.Fatalf("RawText = %q", captured.Input.RawText)
	}
	if captured.Input.Args["topic"] != "weather" {
		t.Fatalf("Args = %v", captured.Input.Args)
	}
	if captured.Context.UserID != "42" || captured.Context.ChatID != "99" || captured.Context.Source != "cli" {
		t.Fatalf("Context = %+v", captured.Context)
	}
	if captured.RequestID == "" {
		t.Fatal("RequestID is empty")
	}
}

func TestAdapter_Execute_HappyPath(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: weather
description: forecast
entrypoint:
  command: /bin/true
`)
	a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
		return []byte(`{"ok":true,"message":"Tomorrow: 18°C","buttons":[{"text":"Refresh","action":"refresh"}]}`), []byte("debug line\n"), nil
	}})
	result, err := a.Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text != "Tomorrow: 18°C" {
		t.Fatalf("Text = %q", result.Text)
	}
	if len(result.Buttons) != 1 || len(result.Buttons[0]) != 1 {
		t.Fatalf("Buttons = %v", result.Buttons)
	}
	if result.Buttons[0][0].Text != "Refresh" || result.Buttons[0][0].CallbackData != "refresh" {
		t.Fatalf("Button = %+v", result.Buttons[0][0])
	}
}

func TestAdapter_Execute_OkFalseSurfacesUserError(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: weather
description: forecast
entrypoint:
  command: /bin/true
`)
	a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
		return []byte(`{"ok":false,"error":"api quota exceeded"}`), nil, nil
	}})
	_, err := a.Execute(context.Background(), skills.Input{})
	if err == nil {
		t.Fatal("expected error for ok=false")
	}
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	var uf skills.UserFacingError
	if !errors.As(err, &uf) {
		t.Fatalf("expected UserFacingError, got %T", err)
	}
	if uf.UserMessage() != "api quota exceeded" {
		t.Fatalf("UserMessage = %q", uf.UserMessage())
	}
}

func TestAdapter_Execute_RejectsInvalidJSON(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: bad
description: bad
entrypoint:
  command: /bin/true
`)
	cases := []struct {
		name   string
		stdout []byte
	}{
		{"empty", nil},
		{"not json", []byte("hello\n")},
		{"unknown fields", []byte(`{"ok":true,"weird":1}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
				return tc.stdout, nil, nil
			}})
			_, err := a.Execute(context.Background(), skills.Input{})
			if err == nil {
				t.Fatalf("expected error for stdout=%q", tc.stdout)
			}
		})
	}
}

func TestAdapter_Execute_TimeoutSurfacesAsUserError(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: slow
description: slow
timeout: 50ms
entrypoint:
  command: /bin/true
`)
	a := newTestAdapter(t, m, fakeRunner{respond: func(ctx context.Context, _ Request) ([]byte, []byte, error) {
		// Block until the adapter's per-invocation deadline expires
		// so the test verifies the user-facing timeout path, not the
		// generic runner-error path.
		select {
		case <-time.After(500 * time.Millisecond):
			return []byte(`{"ok":true}`), nil, nil
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		}
	}})
	_, err := a.Execute(context.Background(), skills.Input{})
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !errors.Is(err, skills.ErrUnavailable) {
		t.Fatalf("expected ErrUnavailable, got %v", err)
	}
	var uf skills.UserFacingError
	if !errors.As(err, &uf) {
		t.Fatalf("expected user-facing error, got %T", err)
	}
	if !strings.Contains(uf.UserMessage(), "did not respond") {
		t.Fatalf("UserMessage = %q", uf.UserMessage())
	}
}

func TestAdapter_Execute_RunnerErrorSurfaces(t *testing.T) {
	m := mustManifest(t, `
api_version: v1
name: broken
description: broken
entrypoint:
  command: /does/not/exist
`)
	sentinel := errors.New("exec: no such file")
	a := newTestAdapter(t, m, fakeRunner{respond: func(_ context.Context, req Request) ([]byte, []byte, error) {
		return nil, []byte("startup failed\n"), sentinel
	}})
	_, err := a.Execute(context.Background(), skills.Input{})
	if err == nil {
		t.Fatal("expected runner error")
	}
	if !strings.Contains(err.Error(), "broken") {
		t.Fatalf("error %v missing skill name", err)
	}
}

// Integration smoke: actually exec a tiny shell script through the
// real [execRunner]. Skipped on platforms without /bin/sh because the
// CI matrix occasionally lacks one.
func TestAdapter_Execute_RealSubprocess(t *testing.T) {
	if _, err := os.Stat("/bin/sh"); err != nil {
		t.Skip("no /bin/sh available")
	}
	dir := t.TempDir()
	script := dir + "/run.sh"
	if err := os.WriteFile(script, []byte(`#!/bin/sh
# Echo a fixed response so the test is hermetic. Reading stdin would
# couple the test to JSON formatting, which other tests already cover.
echo '{"ok":true,"message":"hello from shell"}'
`), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	m := Manifest{
		APIVersion:  APIVersion,
		Name:        "shell",
		Description: "shell skill",
		Entrypoint:  Entrypoint{Command: script},
		Timeout:     2 * time.Second,
	}
	a := newAdapter(m, slog.New(slog.DiscardHandler), nil)
	result, err := a.Execute(context.Background(), skills.Input{})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.Text != "hello from shell" {
		t.Fatalf("Text = %q", result.Text)
	}
}

