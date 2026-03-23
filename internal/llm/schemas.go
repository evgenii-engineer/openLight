package llm

import "strings"

func routeResponseSchema(allowedGroups []string) map[string]any {
	intentEnum := []string{"chat", "unknown"}
	for _, group := range allowedGroups {
		if strings.TrimSpace(group) == "" {
			continue
		}
		intentEnum = append(intentEnum, group)
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"intent": map[string]any{
				"type": "string",
				"enum": intentEnum,
			},
			"confidence": map[string]any{
				"type":    "number",
				"minimum": 0,
				"maximum": 1,
			},
			"needs_clarification": map[string]any{
				"type": "boolean",
			},
			"clarification_question": map[string]any{
				"type": "string",
			},
		},
		"required": []string{
			"intent",
			"confidence",
			"needs_clarification",
		},
	}
}

func skillResponseSchema(allowedSkills []string, allowedServices []string, allowedRuntimes []string) map[string]any {
	skillEnum := append([]string{""}, nonEmptyUniqueStrings(allowedSkills)...)

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"skill": map[string]any{
				"type": "string",
				"enum": skillEnum,
			},
			"arguments": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"service": stringSchemaWithAllowedValues(allowedServices),
					"text":    map[string]any{"type": "string"},
					"id":      map[string]any{"type": "string"},
					"path":    map[string]any{"type": "string"},
					"content": map[string]any{"type": "string"},
					"find":    map[string]any{"type": "string"},
					"replace": map[string]any{"type": "string"},
					"runtime": stringSchemaWithAllowedValues(allowedRuntimes),
					"code":    map[string]any{"type": "string"},
					"spec":    map[string]any{"type": "string"},
				},
			},
			"needs_clarification": map[string]any{
				"type": "boolean",
			},
			"clarification_question": map[string]any{
				"type": "string",
			},
		},
		"required": []string{
			"skill",
			"needs_clarification",
		},
	}
}

func summaryResponseSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"summary": map[string]any{
				"type": "string",
			},
		},
		"required": []string{"summary"},
	}
}

func groupKeys(groups []GroupOption) []string {
	result := make([]string, 0, len(groups))
	for _, group := range groups {
		if strings.TrimSpace(group.Key) == "" {
			continue
		}
		result = append(result, group.Key)
	}
	return result
}

func stringSchemaWithAllowedValues(values []string) map[string]any {
	result := map[string]any{
		"type": "string",
	}

	enum := append([]string{""}, nonEmptyUniqueStrings(values)...)
	if len(enum) > 1 {
		result["enum"] = enum
	}

	return result
}

func nonEmptyUniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}
