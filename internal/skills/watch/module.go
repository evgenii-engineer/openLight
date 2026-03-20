package watch

import (
	"openlight/internal/skills"
	watchengine "openlight/internal/watch"
)

func NewModule(service *watchengine.Service) skills.Module {
	return skills.NewModule("watch", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewAddSkill(service),
			NewListSkill(service),
			NewPauseSkill(service),
			NewRemoveSkill(service),
			NewHistorySkill(service),
			NewTestSkill(service),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
