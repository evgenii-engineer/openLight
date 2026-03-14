package skills

import "fmt"

type Module interface {
	Name() string
	Register(registry *Registry) error
}

type ModuleFunc struct {
	name     string
	register func(registry *Registry) error
}

func NewModule(name string, register func(registry *Registry) error) Module {
	return ModuleFunc{
		name:     name,
		register: register,
	}
}

func (m ModuleFunc) Name() string {
	return m.name
}

func (m ModuleFunc) Register(registry *Registry) error {
	if m.register == nil {
		return nil
	}
	return m.register(registry)
}

func RegisterModules(registry *Registry, modules ...Module) error {
	for _, module := range modules {
		if module == nil {
			continue
		}
		if err := module.Register(registry); err != nil {
			return fmt.Errorf("register module %q: %w", module.Name(), err)
		}
	}
	return nil
}
