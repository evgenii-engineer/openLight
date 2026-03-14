package services

import "openlight/internal/skills"

func NewModule(manager Manager, logLines int) skills.Module {
	return skills.NewModule("services", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewListSkill(manager),
			NewStatusSkill(manager),
			NewRestartSkill(manager),
			NewLogsSkill(manager, logLines),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
