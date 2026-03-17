package accounts

import "openlight/internal/skills"

func NewModule(manager Manager) skills.Module {
	return skills.NewModule("accounts", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewProvidersSkill(manager),
			NewListUserSkill(manager),
			NewAddUserSkill(manager),
			NewDeleteUserSkill(manager),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
