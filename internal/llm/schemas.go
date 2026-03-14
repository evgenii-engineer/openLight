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
			"clarification_question",
		},
	}
}

func skillResponseSchema(allowedSkills []string) map[string]any {
	skillEnum := []string{""}
	for _, skill := range allowedSkills {
		if strings.TrimSpace(skill) == "" {
			continue
		}
		skillEnum = append(skillEnum, skill)
	}

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
					"service": map[string]any{"type": "string"},
					"text":    map[string]any{"type": "string"},
					"id":      map[string]any{"type": "string"},
				},
				"required": []string{
					"service",
					"text",
					"id",
				},
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
			"skill",
			"arguments",
			"confidence",
			"needs_clarification",
			"clarification_question",
		},
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
