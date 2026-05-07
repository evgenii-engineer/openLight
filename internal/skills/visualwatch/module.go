package visualwatch

import (
	"openlight/internal/skills"
	"openlight/internal/storage"
)

// NewModule registers the visual_watch_* skills.
func NewModule(repository storage.Repository, service Service) skills.Module {
	return skills.NewModule("visual_watch", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			newAddSkill(repository, service),
			newListSkill(repository),
			newRemoveSkill(repository),
			newTestSkill(repository, service),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
