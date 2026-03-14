package workbench

import (
	"strings"

	"openlight/internal/skills"
)

func NewModule(manager Manager) skills.Module {
	return skills.NewModule("workbench", func(registry *skills.Registry) error {
		registered := []skills.Skill{}
		if len(manager.AllowedRuntimes()) > 0 {
			registered = append(registered, NewExecCodeSkill(manager))
		}
		if len(manager.AllowedFiles()) > 0 {
			registered = append(registered, NewExecFileSkill(manager))
		}
		if strings.TrimSpace(manager.Workspace()) != "" {
			registered = append(registered, NewWorkspaceCleanSkill(manager))
		}

		for _, skill := range registered {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
