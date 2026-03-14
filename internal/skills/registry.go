package skills

import (
	"fmt"
	"sort"
	"strings"
)

type Registry struct {
	byName      map[string]Skill
	definitions map[string]Definition
	byAlias     map[string]string
	orderedKeys []string
}

func NewRegistry() *Registry {
	return &Registry{
		byName:      make(map[string]Skill),
		definitions: make(map[string]Definition),
		byAlias:     make(map[string]string),
	}
}

func (r *Registry) Register(skill Skill) error {
	definition := normalizeDefinition(skill.Definition())
	name := normalizeIdentifier(definition.Name)
	if name == "" {
		return fmt.Errorf("skill name is required")
	}

	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("skill %q already registered", name)
	}

	r.byName[name] = skill
	r.definitions[name] = definition
	r.orderedKeys = append(r.orderedKeys, name)

	identifiers := append([]string{name}, definition.Aliases...)
	for _, identifier := range identifiers {
		normalized := normalizeIdentifier(identifier)
		if normalized == "" {
			continue
		}
		if existing, exists := r.byAlias[normalized]; exists {
			if existing == name {
				continue
			}
			return fmt.Errorf("identifier %q already registered for %q", normalized, existing)
		}
		r.byAlias[normalized] = name
	}

	sort.Strings(r.orderedKeys)
	return nil
}

func (r *Registry) MustRegister(skill Skill) {
	if err := r.Register(skill); err != nil {
		panic(err)
	}
}

func (r *Registry) Get(name string) (Skill, bool) {
	key := normalizeIdentifier(name)
	skill, ok := r.byName[key]
	return skill, ok
}

func (r *Registry) ResolveIdentifier(identifier string) (Skill, bool) {
	normalized := normalizeIdentifier(identifier)
	if normalized == "" {
		return nil, false
	}

	if skill, ok := r.byName[normalized]; ok {
		return skill, true
	}

	name, ok := r.byAlias[normalized]
	if !ok {
		return nil, false
	}

	skill, ok := r.byName[name]
	return skill, ok
}

func (r *Registry) List() []Definition {
	result := make([]Definition, 0, len(r.orderedKeys))
	for _, key := range r.orderedKeys {
		result = append(result, r.definitions[key])
	}
	return result
}

func (r *Registry) Definition(name string) (Definition, bool) {
	key := normalizeIdentifier(name)
	definition, ok := r.definitions[key]
	return definition, ok
}

func (r *Registry) ListGroups() []Group {
	groupMap := make(map[string]Group)
	for _, key := range r.orderedKeys {
		definition := r.definitions[key]
		if definition.Hidden {
			continue
		}
		group := normalizeGroup(definition.Group)
		groupMap[group.Key] = group
	}

	result := make([]Group, 0, len(groupMap))
	for _, group := range groupMap {
		result = append(result, group)
	}

	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Order != result[j].Order {
			return result[i].Order < result[j].Order
		}
		return result[i].Key < result[j].Key
	})

	return result
}

func (r *Registry) ListByGroup(groupKey string) []Definition {
	groupKey = strings.ToLower(strings.TrimSpace(groupKey))
	if groupKey == "" {
		return nil
	}

	result := make([]Definition, 0)
	for _, key := range r.orderedKeys {
		definition := r.definitions[key]
		if definition.Hidden {
			continue
		}
		if normalizeGroup(definition.Group).Key != groupKey {
			continue
		}
		result = append(result, definition)
	}

	return result
}

func (r *Registry) Names() []string {
	result := make([]string, 0, len(r.orderedKeys))
	for _, key := range r.orderedKeys {
		result = append(result, key)
	}
	return result
}

func normalizeIdentifier(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "/")
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	fields := strings.Fields(value)
	return strings.Join(fields, " ")
}

func normalizeDefinition(definition Definition) Definition {
	definition.Name = strings.TrimSpace(definition.Name)
	definition.Description = strings.TrimSpace(definition.Description)
	definition.Usage = strings.TrimSpace(definition.Usage)
	definition.Group = normalizeGroup(definition.Group)

	aliases := make([]string, 0, len(definition.Aliases))
	for _, alias := range definition.Aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		aliases = append(aliases, alias)
	}
	definition.Aliases = aliases

	examples := make([]string, 0, len(definition.Examples))
	for _, example := range definition.Examples {
		example = strings.TrimSpace(example)
		if example == "" {
			continue
		}
		examples = append(examples, example)
	}
	definition.Examples = examples

	return definition
}
