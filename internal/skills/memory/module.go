package memory

import "openlight/internal/skills"

func NewModule(store Store, limit int, enabled bool) skills.Module {
	return skills.NewModule("memory", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewRememberSkill(store, enabled),
			NewListSkill(store, limit, enabled),
			NewForgetSkill(store, limit, enabled),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
