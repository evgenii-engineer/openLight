package memory

import "openlight/internal/skills"

func NewModule(store Store, limit int, enabled bool) skills.Module {
	return NewModuleWithLongTerm(store, nil, limit, enabled)
}

// NewModuleWithLongTerm additionally wires /remember into the automatic
// long-term memory subsystem. Nil longTerm keeps the previous behaviour.
func NewModuleWithLongTerm(store Store, longTerm LongTerm, limit int, enabled bool) skills.Module {
	return skills.NewModule("memory", func(registry *skills.Registry) error {
		for _, skill := range []skills.Skill{
			NewRememberSkillWithLongTerm(store, longTerm, enabled),
			NewListSkill(store, limit, enabled),
			NewForgetSkill(store, limit, enabled),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
