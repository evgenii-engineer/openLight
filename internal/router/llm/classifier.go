package llm

import (
	"context"
	"log/slog"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/skills"
)

type Classifier struct {
	provider basellm.Provider
	registry *skills.Registry
	logger   *slog.Logger
}

func NewClassifier(provider basellm.Provider, registry *skills.Registry, logger *slog.Logger) *Classifier {
	return &Classifier{
		provider: provider,
		registry: registry,
		logger:   logger,
	}
}

func (c *Classifier) Classify(ctx context.Context, text string) (router.Decision, bool, error) {
	classification, err := c.provider.ClassifyIntent(ctx, text, c.registry.Names())
	if err != nil {
		return router.Decision{}, false, err
	}

	skill, ok := c.registry.Get(classification.SkillName)
	if !ok {
		if c.logger != nil {
			c.logger.Warn("llm returned unknown skill", "skill", classification.SkillName)
		}
		return router.Decision{}, false, nil
	}

	return router.Decision{
		Mode:      router.ModeLLM,
		SkillName: skill.Definition().Name,
		Args:      classification.Args,
	}, true, nil
}
