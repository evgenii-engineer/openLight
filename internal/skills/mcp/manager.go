// Package mcp wires configured MCP servers into the openLight skills
// catalog. Each remote tool becomes one ordinary openLight Skill named
// `<server>_<tool>`. Registration is fully deterministic and gated by
// the per-server allowed_tools list — there is no auto-discovery, no
// hot-reload, and no way for a server to add tools after startup.
package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sort"
	"strings"

	"openlight/internal/config"
	"openlight/internal/mcp"
)

// Server represents one running MCP subprocess after Initialize +
// ListTools have completed. The slice of Tools is already filtered by
// the server's allowed_tools allowlist.
type Server struct {
	Name   string
	Client *mcp.Client
	Tools  []mcp.ToolDef
}

// Manager owns the long-lived subprocesses and exposes the union of
// every allowed tool across every server. It is created at runtime
// startup and torn down on shutdown.
type Manager struct {
	servers []*Server
	logger  *slog.Logger
}

func (m *Manager) Servers() []*Server {
	if m == nil {
		return nil
	}
	return m.servers
}

// NewManager spawns every configured server, runs the MCP handshake,
// and lists their tools. Servers that fail to start are skipped with a
// warning so one broken server does not bring the whole agent down.
// The caller is responsible for invoking Close() on shutdown.
func NewManager(ctx context.Context, cfg map[string]config.MCPServerConfig, logger *slog.Logger) (*Manager, error) {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	names := make([]string, 0, len(cfg))
	for name := range cfg {
		names = append(names, name)
	}
	sort.Strings(names)

	servers := make([]*Server, 0, len(cfg))
	for _, name := range names {
		spec := cfg[name]
		srv, err := startServer(ctx, name, spec)
		if err != nil {
			logger.Warn("mcp server failed to start", "server", name, "error", err)
			continue
		}
		servers = append(servers, srv)
		logger.Info("mcp server registered", "server", name, "tools", toolNames(srv.Tools))
	}

	return &Manager{servers: servers, logger: logger}, nil
}

// Close shuts down every running subprocess.
func (m *Manager) Close() error {
	if m == nil {
		return nil
	}
	for _, s := range m.servers {
		_ = s.Client.Close()
	}
	return nil
}

func startServer(ctx context.Context, name string, spec config.MCPServerConfig) (*Server, error) {
	if len(spec.Command) == 0 {
		return nil, fmt.Errorf("mcp.servers.%s.command is required", name)
	}

	env := buildEnv(spec)
	client, err := mcp.Start(ctx, spec.Command, env)
	if err != nil {
		return nil, err
	}
	if err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize: %w", err)
	}
	tools, err := client.ListTools(ctx)
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("list tools: %w", err)
	}

	allowed := normalizeAllowedTools(spec.AllowedTools)
	filtered := make([]mcp.ToolDef, 0, len(tools))
	for _, t := range tools {
		if !allowed.permits(t.Name) {
			continue
		}
		filtered = append(filtered, t)
	}
	if len(filtered) == 0 {
		_ = client.Close()
		return nil, fmt.Errorf("no allowed tools (%d advertised, %d allowed by config)", len(tools), len(allowed.set))
	}

	return &Server{Name: name, Client: client, Tools: filtered}, nil
}

func toolNames(tools []mcp.ToolDef) []string {
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		out = append(out, t.Name)
	}
	return out
}

// allowedTools captures the per-server allowlist. Empty/nil means
// "all tools the server advertises are allowed" — useful for trusted
// local servers like a filesystem MCP rooted in a safe directory.
type allowedTools struct {
	all bool
	set map[string]struct{}
}

func normalizeAllowedTools(values []string) allowedTools {
	if len(values) == 0 {
		return allowedTools{all: true}
	}
	set := make(map[string]struct{}, len(values))
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		set[v] = struct{}{}
	}
	if len(set) == 0 {
		return allowedTools{all: true}
	}
	return allowedTools{set: set}
}

func (a allowedTools) permits(name string) bool {
	if a.all {
		return true
	}
	_, ok := a.set[name]
	return ok
}

// buildEnv assembles the child process's environment from inline
// values plus env_from indirections. The agent's own environment is
// NOT inherited — MCP servers should only see the variables their
// operator explicitly granted them.
func buildEnv(spec config.MCPServerConfig) []string {
	if len(spec.Env) == 0 && len(spec.EnvFrom) == 0 {
		return nil
	}
	env := make([]string, 0, len(spec.Env)+len(spec.EnvFrom))
	for k, v := range spec.Env {
		env = append(env, k+"="+v)
	}
	for k, src := range spec.EnvFrom {
		v := os.Getenv(src)
		env = append(env, k+"="+v)
	}
	sort.Strings(env)
	return env
}
