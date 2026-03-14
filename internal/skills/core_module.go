package skills

func NewCoreModule() Module {
	return NewModule("core", func(registry *Registry) error {
		for _, skill := range []Skill{
			NewStartSkill(),
			NewPingSkill(),
			NewSkillsSkill(registry),
			NewHelpSkill(registry),
		} {
			if err := registry.Register(skill); err != nil {
				return err
			}
		}
		return nil
	})
}
