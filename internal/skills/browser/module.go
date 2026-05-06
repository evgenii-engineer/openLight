package browser

import "openlight/internal/skills"

func NewModule(manager Manager) skills.Module {
	return skills.NewModule("browser", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewTitleSkill(manager),
			NewTextSkill(manager),
			NewScreenshotSkill(manager),
			NewCheckSkill(manager),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
