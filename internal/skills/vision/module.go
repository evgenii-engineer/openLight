package vision

import "openlight/internal/skills"

// NewModule registers the vision_analyze and vision_compare skills.
func NewModule(manager Manager) skills.Module {
	return skills.NewModule("vision", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewAnalyzeSkill(manager),
			NewCompareSkill(manager),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
