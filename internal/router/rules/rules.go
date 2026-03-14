package rules

import (
	"strings"

	"openlight/internal/router/semantic"
)

type Match struct {
	SkillName string
	Args      map[string]string
}

func Parse(text string) (Match, bool) {
	normalized := normalize(text)
	if normalized == "" {
		return Match{}, false
	}

	if service := extractAfterKeyword(normalized, "restart"); service != "" {
		return Match{
			SkillName: "service_restart",
			Args:      map[string]string{"service": service},
		}, true
	}

	if service := extractLogsService(normalized); service != "" {
		return Match{
			SkillName: "service_logs",
			Args:      map[string]string{"service": service},
		}, true
	}

	if service := extractStatusService(normalized); service != "" {
		return Match{
			SkillName: "service_status",
			Args:      map[string]string{"service": service},
		}, true
	}

	if note := extractNoteText(normalized); note != "" {
		return Match{
			SkillName: "note_add",
			Args:      map[string]string{"text": note},
		}, true
	}

	if id := extractNoteDeleteID(normalized); id != "" {
		return Match{
			SkillName: "note_delete",
			Args:      map[string]string{"id": id},
		}, true
	}

	if containsAny(normalized, "list notes", "show notes", "notes list") {
		return Match{SkillName: "note_list", Args: map[string]string{}}, true
	}

	if topic := extractHelpTopic(normalized); topic != "" {
		return Match{
			SkillName: "help",
			Args:      map[string]string{"topic": topic},
		}, true
	}

	switch {
	case containsAny(normalized, "system status", "overall status", "agent status"):
		return Match{SkillName: "status", Args: map[string]string{}}, true
	case containsAny(normalized, "cpu", "processor usage"):
		return Match{SkillName: "cpu", Args: map[string]string{}}, true
	case containsAny(normalized, "memory", "ram"):
		return Match{SkillName: "memory", Args: map[string]string{}}, true
	case containsAny(normalized, "disk", "storage", "space left", "disk space"):
		return Match{SkillName: "disk", Args: map[string]string{}}, true
	case containsAny(normalized, "uptime", "how long has", "running for"):
		return Match{SkillName: "uptime", Args: map[string]string{}}, true
	case containsAny(normalized, "ip address", "what is my ip", "local ip"):
		return Match{SkillName: "ip", Args: map[string]string{}}, true
	case containsAny(normalized, "hostname", "host name"):
		return Match{SkillName: "hostname", Args: map[string]string{}}, true
	case containsAny(normalized, "temperature", "system temp", "cpu temp"):
		return Match{SkillName: "temperature", Args: map[string]string{}}, true
	case containsAny(normalized, "what can you do", "list skills", "available skills"):
		return Match{SkillName: "skills", Args: map[string]string{}}, true
	}

	return Match{}, false
}

func normalize(value string) string {
	return semantic.Normalize(value)
}

func containsAny(text string, patterns ...string) bool {
	for _, pattern := range patterns {
		if strings.Contains(text, pattern) {
			return true
		}
	}
	return false
}

func extractAfterKeyword(text, keyword string) string {
	prefix := keyword + " "
	if !strings.Contains(text, prefix) {
		return ""
	}
	idx := strings.Index(text, prefix)
	if idx < 0 {
		return ""
	}
	service := strings.TrimSpace(text[idx+len(prefix):])
	return firstToken(service)
}

func extractLogsService(text string) string {
	switch {
	case strings.HasPrefix(text, "logs "):
		return firstToken(strings.TrimPrefix(text, "logs "))
	case strings.HasPrefix(text, "log "):
		return firstToken(strings.TrimPrefix(text, "log "))
	case strings.Contains(text, " logs"):
		prefix := strings.TrimSpace(strings.Split(text, " logs")[0])
		return lastToken(prefix)
	case strings.Contains(text, " log"):
		prefix := strings.TrimSpace(strings.Split(text, " log")[0])
		return lastToken(prefix)
	default:
		return ""
	}
}

func extractStatusService(text string) string {
	if strings.HasPrefix(text, "status ") {
		return firstToken(strings.TrimPrefix(text, "status "))
	}
	if strings.HasPrefix(text, "service ") {
		return firstToken(strings.TrimPrefix(text, "service "))
	}
	if strings.Contains(text, " service status") {
		prefix := strings.TrimSpace(strings.Split(text, " service status")[0])
		return lastToken(prefix)
	}
	if strings.Contains(text, "status of ") {
		return firstToken(strings.TrimSpace(strings.Split(text, "status of ")[1]))
	}
	return ""
}

func extractNoteText(text string) string {
	for _, prefix := range []string{"add note ", "note ", "remember "} {
		if strings.HasPrefix(text, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(text, prefix))
		}
	}
	return ""
}

func extractHelpTopic(text string) string {
	if strings.HasPrefix(text, "help ") {
		return strings.TrimSpace(strings.TrimPrefix(text, "help "))
	}
	if strings.HasPrefix(text, "usage ") {
		return strings.TrimSpace(strings.TrimPrefix(text, "usage "))
	}
	return ""
}

func extractNoteDeleteID(text string) string {
	for _, prefix := range []string{"delete note ", "remove note ", "note delete ", "note remove "} {
		if strings.HasPrefix(text, prefix) {
			return firstToken(strings.TrimSpace(strings.TrimPrefix(text, prefix)))
		}
	}
	return ""
}

func firstToken(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[0]
}

func lastToken(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}
	return fields[len(fields)-1]
}
