package files

import "openlight/internal/skills"

func NewModule(manager Manager) skills.Module {
	return skills.NewModule("files", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewListSkill(manager),
			NewReadSkill(manager),
			NewWriteSkill(manager),
			NewReplaceSkill(manager),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
