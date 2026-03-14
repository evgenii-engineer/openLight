package system

import "openlight/internal/skills"

func NewModule(provider Provider) skills.Module {
	return skills.NewModule("system", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewStatusSkill(provider),
			NewCPUSkill(provider),
			NewMemorySkill(provider),
			NewDiskSkill(provider),
			NewUptimeSkill(provider),
			NewHostnameSkill(provider),
			NewIPSkill(provider),
			NewTemperatureSkill(provider),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
