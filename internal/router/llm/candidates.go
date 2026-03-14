package llm

import (
	"sort"

	basellm "openlight/internal/llm"
	"openlight/internal/skills"
)

func buildSkillCatalog(registry *skills.Registry) map[string]skills.Definition {
	catalog := make(map[string]skills.Definition)
	for _, definition := range registry.List() {
		if definition.Hidden {
			continue
		}
		catalog[definition.Name] = definition
	}
	return catalog
}

// Available skills for the LLM routing layers. We do not score or rank them
// manually; the model decides among the full visible tool list.
func (c *Classifier) buildAvailableSkills() []basellm.SkillOption {
	if len(c.skillCatalog) == 0 {
		return nil
	}

	names := make([]string, 0, len(c.skillCatalog))
	for name := range c.skillCatalog {
		if name == "chat" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	result := make([]basellm.SkillOption, 0, len(names))
	for _, name := range names {
		definition := c.skillCatalog[name]
		result = append(result, basellm.SkillOption{
			Name:        definition.Name,
			Description: shortDescription(definition),
			Mutating:    definition.Mutating,
		})
	}
	return result
}

func (c *Classifier) buildAvailableGroups() []basellm.GroupOption {
	if c.registry == nil {
		return nil
	}

	groups := c.registry.ListGroups()
	if len(groups) == 0 {
		return nil
	}

	result := make([]basellm.GroupOption, 0, len(groups))
	for _, group := range groups {
		if group.Key == skills.GroupChat.Key {
			continue
		}
		result = append(result, basellm.GroupOption{
			Key:         group.Key,
			Title:       group.Title,
			Description: group.Description,
		})
	}
	return result
}

func (c *Classifier) buildAvailableSkillsForGroup(groupKey string) []basellm.SkillOption {
	if c.registry == nil {
		return nil
	}

	definitions := c.registry.ListByGroup(groupKey)
	if len(definitions) == 0 {
		return nil
	}

	names := make([]string, 0, len(definitions))
	byName := make(map[string]skills.Definition, len(definitions))
	for _, definition := range definitions {
		if definition.Name == "chat" {
			continue
		}
		names = append(names, definition.Name)
		byName[definition.Name] = definition
	}
	sort.Strings(names)

	result := make([]basellm.SkillOption, 0, len(names))
	for _, name := range names {
		definition := byName[name]
		result = append(result, basellm.SkillOption{
			Name:        definition.Name,
			Description: shortDescription(definition),
			Mutating:    definition.Mutating,
		})
	}
	return result
}

func shortDescription(definition skills.Definition) string {
	description := definition.Description
	if description == "" {
		description = definition.Name
	}
	return description
}
