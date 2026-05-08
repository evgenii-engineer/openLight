package mcp

import "openlight/internal/skills"

func NewModule(manager *Manager) skills.Module {
	return skills.NewModule("mcp", func(registry *skills.Registry) error {
		if manager == nil {
			return nil
		}
		for _, server := range manager.Servers() {
			for _, tool := range server.Tools {
				skill := newToolSkill(server.Name, tool, server.Client)
				if err := registry.Register(skill); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
