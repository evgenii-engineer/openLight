package chat

import (
	"openlight/internal/llm"
	"openlight/internal/skills"
)

func NewModule(provider llm.Provider, store HistoryStore, options Options) skills.Module {
	return NewModuleWithDeep(provider, nil, store, options)
}

// NewModuleWithDeep registers both /chat (smart provider) and /think (deep
// provider). When deepProvider is nil, /think returns ErrUnavailable.
func NewModuleWithDeep(provider, deepProvider llm.Provider, store HistoryStore, options Options) skills.Module {
	return skills.NewModule("chat", func(registry *skills.Registry) error {
		if err := registry.Register(NewSkillWithOptions(provider, store, options)); err != nil {
			return err
		}
		return registry.Register(NewThinkSkill(deepProvider, store, options))
	})
}
