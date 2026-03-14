package workbench

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

type execCodeSkill struct {
	manager Manager
}

func NewExecCodeSkill(manager Manager) skills.Skill {
	return &execCodeSkill{manager: manager}
}

func (s *execCodeSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "exec_code",
		Group:       skills.GroupWorkbench,
		Description: "Write temporary code into the workspace, run it with an allowed runtime, and return stdout/stderr.",
		Aliases:     []string{"run code", "execute code"},
		Usage:       "/exec_code <runtime> :: <code>",
		Examples: []string{
			"run python:\nprint(\"hello\")",
			"exec_code sh :: printf 'hello\\n'",
		},
		Mutating: true,
	}
}

func (s *execCodeSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.ExecCode(ctx, input.Args["runtime"], input.Args["code"])
	if err != nil {
		var execErr *ExecutionError
		if errors.As(err, &execErr) {
			return skills.Result{Text: formatRunResult("Temporary code", execErr.Result, execErr.ExitCode, true)}, nil
		}
		return skills.Result{}, err
	}
	return skills.Result{Text: formatRunResult("Temporary code", result, 0, false)}, nil
}

type execFileSkill struct {
	manager Manager
}

func NewExecFileSkill(manager Manager) skills.Skill {
	return &execFileSkill{manager: manager}
}

func (s *execFileSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "exec_file",
		Group:       skills.GroupWorkbench,
		Description: "Run an allowlisted file and return stdout/stderr.",
		Aliases:     []string{"run file", "execute file"},
		Usage:       "/exec_file <path>",
		Examples: []string{
			"run /usr/bin/uptime",
			"exec_file /usr/bin/uptime",
		},
		Mutating: true,
	}
}

func (s *execFileSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	result, err := s.manager.ExecFile(ctx, input.Args["path"])
	if err != nil {
		var execErr *ExecutionError
		if errors.As(err, &execErr) {
			return skills.Result{Text: formatRunResult("Allowed file", execErr.Result, execErr.ExitCode, true)}, nil
		}
		return skills.Result{}, err
	}
	return skills.Result{Text: formatRunResult("Allowed file", result, 0, false)}, nil
}

type workspaceCleanSkill struct {
	manager Manager
}

func NewWorkspaceCleanSkill(manager Manager) skills.Skill {
	return &workspaceCleanSkill{manager: manager}
}

func (s *workspaceCleanSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "workspace_clean",
		Group:       skills.GroupWorkbench,
		Description: "Delete temporary files from the workbench workspace.",
		Aliases:     []string{"clean workspace", "workspace clear"},
		Usage:       "/workspace_clean",
		Examples: []string{
			"workspace_clean",
		},
		Mutating: true,
	}
}

func (s *workspaceCleanSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	result, err := s.manager.CleanWorkspace(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	if result.Removed == 0 {
		return skills.Result{Text: "Workspace already clean: " + result.Workspace}, nil
	}
	return skills.Result{Text: fmt.Sprintf("Workspace cleaned: %s (%d item(s) removed)", result.Workspace, result.Removed)}, nil
}

func formatRunResult(label string, result RunResult, exitCode int, failed bool) string {
	lines := []string{
		label + ": " + result.Path,
	}
	if strings.TrimSpace(result.Runtime) != "" {
		lines = append(lines, "Runtime: "+result.Runtime)
	}
	if failed {
		lines = append(lines, fmt.Sprintf("Exit code: %d", exitCode))
	}

	output := result.Output
	if strings.TrimSpace(output) == "" {
		output = "(no output)"
	}
	lines = append(lines, "Output:\n"+output)
	if result.Truncated {
		lines = append(lines, "(output truncated)")
	}

	return strings.Join(lines, "\n")
}
