package network

import "openlight/internal/skills"

func NewModule(manager Manager) skills.Module {
	return skills.NewModule("network", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewPortCheckSkill(manager),
			NewHTTPCheckSkill(manager),
			NewCertCheckSkill(manager),
			NewDNSCheckSkill(manager),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
