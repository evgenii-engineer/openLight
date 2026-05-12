package system

import "openlight/internal/skills"

func NewModule(provider Provider, models ModelsInfo) skills.Module {
	return skills.NewModule("system", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewStatusSkill(provider, models),
			NewCPUSkill(provider),
			NewMemorySkill(provider),
			NewDiskSkill(provider),
			NewUptimeSkill(provider),
			NewHostnameSkill(provider),
			NewIPSkill(provider),
			NewTemperatureSkill(provider),
			NewModelsSkill(models),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
