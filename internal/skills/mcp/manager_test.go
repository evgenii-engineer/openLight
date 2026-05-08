package mcp

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"openlight/internal/config"
	"openlight/internal/skills"
)

func TestManagerRegistersAllowedToolsOnly(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake MCP server is a POSIX shell script")
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fake-mcp.sh")
	const script = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"0.1"}}}\n' "$id"
      ;;
    *'"method":"notifications/initialized"'*) ;;
    *'"method":"tools/list"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"echo back"},{"name":"shutdown","description":"dangerous"}]}}\n' "$id"
      ;;
    *'"name":"echo"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"echoed"}]}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr, err := NewManager(ctx, map[string]config.MCPServerConfig{
		"local": {
			Command:      []string{"sh", scriptPath},
			AllowedTools: []string{"echo"}, // 'shutdown' must be filtered out
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()

	if len(mgr.Servers()) != 1 {
		t.Fatalf("expected 1 server, got %d", len(mgr.Servers()))
	}
	srv := mgr.Servers()[0]
	if len(srv.Tools) != 1 || srv.Tools[0].Name != "echo" {
		t.Fatalf("expected only the 'echo' tool to survive the allowlist, got %#v", srv.Tools)
	}

	// Now verify the module produces a Skill that actually invokes the
	// remote tool round-trip.
	registry := skills.NewRegistry()
	if err := NewModule(mgr).Register(registry); err != nil {
		t.Fatalf("module register: %v", err)
	}
	skill, ok := registry.Get("local_echo")
	if !ok {
		t.Fatalf("expected local_echo skill to be registered")
	}
	res, err := skill.Execute(ctx, skills.Input{Args: map[string]string{"args": `{"message":"hi"}`}})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(res.Text, "echoed") {
		t.Fatalf("unexpected result: %q", res.Text)
	}
}

func TestManagerSkipsServersWithEmptyAllowlistAfterIntersection(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake MCP server is a POSIX shell script")
	}

	dir := t.TempDir()
	scriptPath := filepath.Join(dir, "fake.sh")
	const script = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"0.1"}}}\n' "$id"
      ;;
    *'"method":"notifications/initialized"'*) ;;
    *'"method":"tools/list"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"only_one","description":"x"}]}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr, err := NewManager(ctx, map[string]config.MCPServerConfig{
		"local": {
			Command:      []string{"sh", scriptPath},
			AllowedTools: []string{"nope"},
		},
	}, nil)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer mgr.Close()
	if len(mgr.Servers()) != 0 {
		t.Fatalf("expected no servers (every advertised tool was filtered out), got %d", len(mgr.Servers()))
	}
}
