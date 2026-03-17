package utils

import "strings"

var sensitiveArgKeys = map[string]struct{}{
	"api_key":    {},
	"passphrase": {},
	"password":   {},
	"secret":     {},
	"token":      {},
}

func RedactSensitiveText(text string) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return text
	}

	fields := strings.Fields(trimmed)
	maxParts := len(fields)
	if maxParts > 3 {
		maxParts = 3
	}

	for parts := maxParts; parts >= 1; parts-- {
		command := normalizeRedactionCommand(strings.Join(fields[:parts], " "))
		argsText := strings.TrimSpace(strings.Join(fields[parts:], " "))
		if redactedArgs, ok := redactSensitiveCommandArgs(command, argsText); ok {
			if redactedArgs == "" {
				return strings.Join(fields[:parts], " ")
			}
			return strings.Join(append(fields[:parts], redactedArgs), " ")
		}
	}

	return text
}

func RedactSensitiveArgs(args map[string]string) map[string]string {
	if len(args) == 0 {
		return map[string]string{}
	}

	result := make(map[string]string, len(args))
	for key, value := range args {
		normalizedKey := strings.ToLower(strings.TrimSpace(key))
		if _, ok := sensitiveArgKeys[normalizedKey]; ok {
			result[key] = "[redacted]"
			continue
		}
		result[key] = value
	}
	return result
}

func normalizeRedactionCommand(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	if idx := strings.Index(value, "@"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func redactSensitiveCommandArgs(command, argsText string) (string, bool) {
	switch command {
	case "user add", "add user", "register user":
		return redactUserAddArgs(argsText), true
	default:
		return "", false
	}
}

func redactUserAddArgs(argsText string) string {
	argsText = strings.TrimSpace(argsText)
	if argsText == "" {
		return ""
	}

	fields := strings.Fields(argsText)
	switch len(fields) {
	case 0:
		return ""
	case 1:
		return fields[0]
	case 2:
		return fields[0] + " [redacted]"
	default:
		return strings.Join([]string{fields[0], fields[1], "[redacted]"}, " ")
	}
}
