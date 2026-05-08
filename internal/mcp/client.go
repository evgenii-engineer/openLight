// Package mcp is a deliberately small MCP (Model Context Protocol) stdio
// client. It speaks just enough JSON-RPC 2.0 to do an initialize
// handshake, list tools, and call a tool. It exists so openLight can
// expose curated remote tools as ordinary Skills — NOT as a plugin
// runtime. There is no auto-discovery, no dynamic loading, no resource
// or prompt support; servers must be declared in YAML and tool calls
// stay inside the operator's allowlist.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"sync/atomic"
)

const protocolVersion = "2024-11-05"

// ToolDef describes a tool reported by an MCP server. We only keep the
// fields openLight surfaces in its skill catalog; richer schema info
// can be added later if a real use case shows up.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema,omitempty"`
}

// Client is one stdio subprocess running an MCP server. Operations are
// serialized through a mutex — no concurrent JSON-RPC pipelining — to
// keep the implementation small and the request/response correlation
// trivial.
type Client struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Reader
	mu     sync.Mutex
	nextID atomic.Int64
}

// Start launches `command` as a child process and prepares the stdio
// pipes. It does NOT perform the MCP initialize handshake; call
// Initialize once Start succeeds.
func Start(ctx context.Context, command []string, env []string) (*Client, error) {
	if len(command) == 0 {
		return nil, fmt.Errorf("mcp: command is required")
	}

	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("mcp: stdout pipe: %w", err)
	}
	// Discard stderr; subprocess crashes show up as broken pipes on the
	// next call, and stderr is too noisy to surface in chat without
	// rate limiting that we don't want to write yet.
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("mcp: start %q: %w", command[0], err)
	}

	return &Client{
		cmd:    cmd,
		stdin:  stdin,
		stdout: bufio.NewReader(stdout),
	}, nil
}

// Initialize performs the MCP `initialize` handshake. The server's
// declared capabilities are intentionally ignored — we only call
// tools/list and tools/call, which every MCP server must support.
func (c *Client) Initialize(ctx context.Context) error {
	_, err := c.call(ctx, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo": map[string]any{
			"name":    "openlight",
			"version": "0.1",
		},
	})
	if err != nil {
		return fmt.Errorf("mcp initialize: %w", err)
	}

	// Per the spec, the client must send a notification right after the
	// initialize response so the server knows it is safe to send
	// notifications. We send it but don't wait for a reply.
	if err := c.notify("notifications/initialized", nil); err != nil {
		return fmt.Errorf("mcp initialized notification: %w", err)
	}
	return nil
}

// ListTools fetches the server's exported tool catalog.
func (c *Client) ListTools(ctx context.Context) ([]ToolDef, error) {
	raw, err := c.call(ctx, "tools/list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("mcp tools/list: %w", err)
	}
	var resp struct {
		Tools []ToolDef `json:"tools"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("mcp tools/list decode: %w", err)
	}
	return resp.Tools, nil
}

// CallTool invokes a tool and returns the concatenated text content
// blocks. MCP also allows image and resource content blocks; we
// silently skip those — operators wanting richer payloads should reach
// for a dedicated skill instead of stuffing it through MCP.
func (c *Client) CallTool(ctx context.Context, name string, args map[string]any) (string, error) {
	raw, err := c.call(ctx, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})
	if err != nil {
		return "", fmt.Errorf("mcp tools/call %s: %w", name, err)
	}
	var resp struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return "", fmt.Errorf("mcp tools/call decode: %w", err)
	}
	var out string
	for _, b := range resp.Content {
		if b.Type == "text" {
			out += b.Text
		}
	}
	if resp.IsError {
		return out, fmt.Errorf("mcp tool %s returned error: %s", name, out)
	}
	return out, nil
}

// Close shuts down the subprocess. Idempotent; safe to call from defer.
func (c *Client) Close() error {
	if c == nil || c.cmd == nil {
		return nil
	}
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

// ---- JSON-RPC plumbing ----------------------------------------------------

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcNotification struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int64           `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// call sends a request and returns the result blob. Concurrent calls
// are serialized; that is fine for openLight's usage pattern (one
// command at a time per chat).
func (c *Client) call(ctx context.Context, method string, params any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID.Add(1)
	req := rpcRequest{JSONRPC: "2.0", ID: id, Method: method, Params: params}
	if err := c.writeFrame(req); err != nil {
		return nil, err
	}

	type readResult struct {
		resp rpcResponse
		err  error
	}
	resCh := make(chan readResult, 1)
	go func() {
		var resp rpcResponse
		err := c.readFrame(&resp)
		resCh <- readResult{resp: resp, err: err}
	}()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case r := <-resCh:
		if r.err != nil {
			return nil, r.err
		}
		if r.resp.Error != nil {
			return nil, fmt.Errorf("rpc error %d: %s", r.resp.Error.Code, r.resp.Error.Message)
		}
		if r.resp.ID != id {
			return nil, fmt.Errorf("rpc id mismatch: want %d got %d", id, r.resp.ID)
		}
		return r.resp.Result, nil
	}
}

func (c *Client) notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.writeFrame(rpcNotification{JSONRPC: "2.0", Method: method, Params: params})
}

func (c *Client) writeFrame(payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	buf = append(buf, '\n')
	if _, err := c.stdin.Write(buf); err != nil {
		return fmt.Errorf("write frame: %w", err)
	}
	return nil
}

func (c *Client) readFrame(dst any) error {
	// MCP stdio transport is newline-delimited JSON. We loop past empty
	// lines (some servers emit them between messages).
	for {
		line, err := c.stdout.ReadBytes('\n')
		if err != nil {
			return fmt.Errorf("read frame: %w", err)
		}
		if len(line) == 1 { // just '\n'
			continue
		}
		return json.Unmarshal(line, dst)
	}
}
