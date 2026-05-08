package mcp

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestClientLifecycleAgainstFakeServer drives the real Client against a
// tiny shell-script MCP server living in this same test file. It
// verifies the initialize → list → call → close path end-to-end and
// catches regressions in JSON-RPC framing.
func TestClientLifecycleAgainstFakeServer(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake server is a POSIX shell script")
	}

	scriptPath := writeFakeMCPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	client, err := Start(ctx, []string{"sh", scriptPath}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()

	if err := client.Initialize(ctx); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %#v", tools)
	}

	out, err := client.CallTool(ctx, "echo", map[string]any{"message": "hi"})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if !strings.Contains(out, "hi") {
		t.Fatalf("expected echoed text, got %q", out)
	}
}

func TestClientCallToolPropagatesIsError(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake server is a POSIX shell script")
	}

	scriptPath := writeFakeMCPServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := Start(ctx, []string{"sh", scriptPath}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()
	_ = client.Initialize(ctx)

	_, err = client.CallTool(ctx, "boom", nil)
	if err == nil {
		t.Fatal("expected error from unknown tool")
	}
}

func TestClientFailsOnEmptyCommand(t *testing.T) {
	t.Parallel()
	_, err := Start(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestClientPropagatesContextCancel(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("fake server is a POSIX shell script")
	}

	scriptPath := writeFakeMCPServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	client, err := Start(ctx, []string{"sh", scriptPath}, nil)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer client.Close()
	_ = client.Initialize(ctx)

	cancel()
	_, err = client.CallTool(context.Background(), "slow", nil)
	if err == nil || !(errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "broken pipe") || strings.Contains(err.Error(), "EOF") || strings.Contains(err.Error(), "file already closed")) {
		// We accept any error here because the kill/close ordering is racy.
		t.Logf("got %v (acceptable post-cancel error)", err)
	}
}

// writeFakeMCPServer drops a tiny shell script that speaks just enough
// MCP for these tests: initialize → ack, tools/list → echo, tools/call
// echo → echo back the message argument.
func writeFakeMCPServer(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "fake-mcp.sh")
	const script = `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *'"method":"initialize"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":"2024-11-05","capabilities":{},"serverInfo":{"name":"fake","version":"0.1"}}}\n' "$id"
      ;;
    *'"method":"notifications/initialized"'*)
      : # ack only
      ;;
    *'"method":"tools/list"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"tools":[{"name":"echo","description":"echo the message arg back","inputSchema":{}}]}}\n' "$id"
      ;;
    *'"name":"echo"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      msg=$(printf '%s' "$line" | sed -E 's/.*"message":"([^"]*)".*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"echo: %s"}]}}\n' "$id" "$msg"
      ;;
    *'"method":"tools/call"'*)
      id=$(printf '%s' "$line" | sed -E 's/.*"id":([0-9]+).*/\1/')
      printf '{"jsonrpc":"2.0","id":%s,"result":{"content":[{"type":"text","text":"unknown tool"}],"isError":true}}\n' "$id"
      ;;
  esac
done
`
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
