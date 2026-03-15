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
	fields := strings.Fields(text)
	for idx, field := range fields {
		if field != keyword {
			continue
		}
		if service := firstConcreteServiceToken(fields[idx+1:]); service != "" {
			return service
		}
	}
	return ""
}

func extractLogsService(text string) string {
	fields := strings.Fields(text)
	for idx, field := range fields {
		if field != "logs" && field != "log" {
			continue
		}
		if service := firstMeaningfulToken(fields[idx+1:]); service != "" {
			return service
		}
		if service := lastMeaningfulToken(fields[:idx]); service != "" {
			return service
		}
	}
	return ""
}

func extractStatusService(text string) string {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return ""
	}

	if fields[0] == "status" {
		return firstConcreteServiceToken(fields[1:])
	}

	if fields[0] == "service" {
		return firstConcreteServiceToken(fields[1:])
	}

	if idx := indexSequence(fields, "service", "status"); idx > 0 {
		return lastConcreteServiceToken(fields[:idx])
	}

	if idx := indexSequence(fields, "status", "of"); idx >= 0 {
		return firstConcreteServiceToken(fields[idx+2:])
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

func firstMeaningfulToken(fields []string) string {
	for _, field := range fields {
		if !isLogsContextWord(field) {
			return field
		}
	}
	return ""
}

func lastMeaningfulToken(fields []string) string {
	for idx := len(fields) - 1; idx >= 0; idx-- {
		if !isLogsContextWord(fields[idx]) {
			return fields[idx]
		}
	}
	return ""
}

func firstConcreteServiceToken(fields []string) string {
	for _, field := range fields {
		if !isGenericServiceWord(field) {
			return field
		}
	}
	return ""
}

func lastConcreteServiceToken(fields []string) string {
	for idx := len(fields) - 1; idx >= 0; idx-- {
		if !isGenericServiceWord(fields[idx]) {
			return fields[idx]
		}
	}
	return ""
}

func isLogsContextWord(field string) bool {
	return isGenericServiceWord(field)
}

func isGenericServiceWord(field string) bool {
	switch field {
	case "", "show", "check", "service", "services", "status", "restart", "logs", "log", "for", "of", "the", "me", "please", "recent", "latest", "last", "system", "overall", "agent", "all", "whole", "entire":
		return true
	default:
		return false
	}
}

func indexSequence(fields []string, sequence ...string) int {
	if len(sequence) == 0 || len(fields) < len(sequence) {
		return -1
	}
	for idx := 0; idx <= len(fields)-len(sequence); idx++ {
		match := true
		for offset, field := range sequence {
			if fields[idx+offset] != field {
				match = false
				break
			}
		}
		if match {
			return idx
		}
	}
	return -1
}
