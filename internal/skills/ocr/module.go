package ocr

import "openlight/internal/skills"

// NewModule registers the ocr_extract skill.
func NewModule(manager Manager) skills.Module {
	return skills.NewModule("ocr", func(registry *skills.Registry) error {
		return registry.Register(NewExtractSkill(manager))
	})
}
