package llm

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

var (
	ollamaJSONBoolPattern   = regexp.MustCompile(`"([a-z_]+)"\s*:\s*(true|false)`)
	ollamaJSONNumberPattern = regexp.MustCompile(`"([a-z_]+)"\s*:\s*([0-9]*\.?[0-9]+)`)
)

func parseOllamaRouteClassification(responseText string, allowedGroups []string) (RouteClassification, error) {
	var classification RouteClassification
	if err := json.Unmarshal([]byte(extractJSON(responseText)), &classification); err == nil {
		return classification, nil
	} else {
		recovered := recoverOllamaRouteClassification(responseText, allowedGroups)
		if recovered.Intent != "" || recovered.NeedsClarification {
			return recovered, nil
		}
		return RouteClassification{}, err
	}
}

func parseOllamaSkillClassification(responseText string, allowedSkills []string, allowedArgumentKeys []string) (Classification, error) {
	var classification Classification
	if err := json.Unmarshal([]byte(extractJSON(responseText)), &classification); err == nil {
		return classification, nil
	} else {
		recovered := recoverOllamaSkillClassification(responseText, allowedSkills, allowedArgumentKeys)
		if recovered.Skill != "" || recovered.NeedsClarification || len(recovered.Arguments) > 0 {
			return recovered, nil
		}
		return Classification{}, err
	}
}

func recoverOllamaRouteClassification(responseText string, allowedGroups []string) RouteClassification {
	allowedIntents := append([]string{"chat", "unknown"}, nonEmptyUniqueStrings(allowedGroups)...)
	intent := matchAllowedLabel(responseText, "intent", allowedIntents)
	if intent == "" {
		intent = matchPlainAllowedLabel(responseText, allowedIntents)
	}

	return RouteClassification{
		Intent:                intent,
		Confidence:            extractJSONNumberField(responseText, "confidence"),
		NeedsClarification:    extractJSONBoolField(responseText, "needs_clarification"),
		ClarificationQuestion: extractJSONStringField(responseText, "clarification_question"),
	}
}

func recoverOllamaSkillClassification(responseText string, allowedSkills []string, allowedArgumentKeys []string) Classification {
	skill := matchAllowedLabel(responseText, "skill", allowedSkills)
	if skill == "" {
		skill = matchPlainAllowedLabel(responseText, allowedSkills)
	}

	arguments := make(map[string]string)
	for _, key := range allowedArgumentKeys {
		if value := extractJSONStringField(responseText, key); value != "" {
			arguments[key] = value
		}
	}

	return Classification{
		Skill:                 skill,
		Arguments:             arguments,
		NeedsClarification:    extractJSONBoolField(responseText, "needs_clarification"),
		ClarificationQuestion: extractJSONStringField(responseText, "clarification_question"),
	}
}

func matchAllowedLabel(responseText, key string, allowed []string) string {
	value := extractJSONStringField(responseText, key)
	if value == "" {
		return ""
	}
	value = strings.TrimSpace(value)
	for _, allowedValue := range allowed {
		if value == strings.TrimSpace(allowedValue) {
			return value
		}
	}
	return ""
}

func matchPlainAllowedLabel(responseText string, allowed []string) string {
	normalized := strings.ToLower(strings.TrimSpace(responseText))
	normalized = strings.Trim(normalized, "{}[]()\"'` \n\t\r")
	for _, allowedValue := range allowed {
		allowedValue = strings.TrimSpace(allowedValue)
		if allowedValue == "" {
			continue
		}
		if normalized == strings.ToLower(allowedValue) {
			return allowedValue
		}
		if strings.HasPrefix(normalized, strings.ToLower(allowedValue)+":") {
			return allowedValue
		}
	}
	return ""
}

func extractJSONStringField(responseText, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	pattern := regexp.MustCompile(`"` + regexp.QuoteMeta(key) + `"\s*:\s*"((?:\\.|[^"\\])*)"`)
	matches := pattern.FindStringSubmatch(responseText)
	if len(matches) != 2 {
		return ""
	}

	decoded, err := strconv.Unquote(`"` + matches[1] + `"`)
	if err != nil {
		return matches[1]
	}
	return decoded
}

func extractJSONBoolField(responseText, key string) bool {
	matches := ollamaJSONBoolPattern.FindAllStringSubmatch(responseText, -1)
	for _, match := range matches {
		if len(match) != 3 || match[1] != key {
			continue
		}
		return match[2] == "true"
	}
	return false
}

func extractJSONNumberField(responseText, key string) float64 {
	matches := ollamaJSONNumberPattern.FindAllStringSubmatch(responseText, -1)
	for _, match := range matches {
		if len(match) != 3 || match[1] != key {
			continue
		}
		value, err := strconv.ParseFloat(match[2], 64)
		if err != nil {
			return 0
		}
		return value
	}
	return 0
}
