package notes

import "openlight/internal/skills"

func NewModule(store Store, listLimit int) skills.Module {
	return skills.NewModule("notes", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewAddSkill(store),
			NewListSkill(store, listLimit),
			NewDeleteSkill(store),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
