package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"openlight/internal/mcp"
	"openlight/internal/skills"
)

// toolSkill exposes one MCP tool as an openLight Skill. Arguments are
// passed through verbatim as a single JSON blob; richer schemas can be
// surfaced later if a real workflow needs structured prompts.
type toolSkill struct {
	server     string
	def        mcp.ToolDef
	client     *mcp.Client
}

func newToolSkill(server string, def mcp.ToolDef, client *mcp.Client) *toolSkill {
	return &toolSkill{server: server, def: def, client: client}
}

func (s *toolSkill) Definition() skills.Definition {
	desc := strings.TrimSpace(s.def.Description)
	if desc == "" {
		desc = fmt.Sprintf("Remote MCP tool exposed by server %q.", s.server)
	}
	name := skillName(s.server, s.def.Name)
	return skills.Definition{
		Name:        name,
		Group:       skills.GroupMCP,
		Description: desc,
		Usage:       fmt.Sprintf("/%s {\"key\":\"value\"}", name),
	}
}

func (s *toolSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "args", Prompt: "JSON arguments (or leave blank)", Placeholder: "{}"},
		},
	}
}

func (s *toolSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	args := map[string]any{}
	rawArgs := strings.TrimSpace(firstNonEmpty(input.Args["args"], input.RawText))
	if rawArgs != "" && rawArgs != "{}" {
		if err := json.Unmarshal([]byte(rawArgs), &args); err != nil {
			return skills.Result{}, fmt.Errorf("%w: arguments must be valid JSON: %v", skills.ErrInvalidArguments, err)
		}
	}

	out, err := s.client.CallTool(ctx, s.def.Name, args)
	if err != nil {
		return skills.Result{}, err
	}
	if strings.TrimSpace(out) == "" {
		out = "(no text content returned by tool)"
	}
	return skills.Result{Text: out}, nil
}

// skillName produces a readable, deterministic skill identifier from a
// server name and tool name. Both halves are sanitized to lowercase
// with `_` separators so they survive openLight's slash-command parser.
func skillName(server, tool string) string {
	return sanitizeIdent(server) + "_" + sanitizeIdent(tool)
}

func sanitizeIdent(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if out == "" {
		out = "tool"
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if t := strings.TrimSpace(v); t != "" {
			return t
		}
	}
	return ""
}
