package chat

import (
	"openlight/internal/llm"
	"openlight/internal/skills"
)

func NewModule(provider llm.Provider, store HistoryStore, options Options) skills.Module {
	return skills.NewModule("chat", func(registry *skills.Registry) error {
		return registry.Register(NewSkillWithOptions(provider, store, options))
	})
}
