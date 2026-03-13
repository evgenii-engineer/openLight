package skills

import (
	"fmt"
	"sort"
	"strings"
)

type Registry struct {
	byName      map[string]Skill
	byAlias     map[string]string
	orderedKeys []string
}

func NewRegistry() *Registry {
	return &Registry{
		byName:  make(map[string]Skill),
		byAlias: make(map[string]string),
	}
}

func (r *Registry) Register(skill Skill) error {
	definition := skill.Definition()
	name := normalizeIdentifier(definition.Name)
	if name == "" {
		return fmt.Errorf("skill name is required")
	}

	if _, exists := r.byName[name]; exists {
		return fmt.Errorf("skill %q already registered", name)
	}

	r.byName[name] = skill
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
		result = append(result, r.byName[key].Definition())
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
