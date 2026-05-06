package render

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"openlight/internal/skills"
)

// MaxRunes is the soft cap used when truncating skill output for inline edits.
// Long replies still fall through the existing splitTelegramMessage in the
// transport layer; this just keeps the *editable* card readable.
const MaxRunes = 1500

// SkillCard formats a skill execution result for an editable Telegram card.
// It prepends a header with the skill title and clamps the body if it grows
// beyond MaxRunes.
func SkillCard(def skills.Definition, body string) string {
	header := strings.TrimSpace(def.Group.Title)
	title := strings.TrimSpace(humanize(def.Name))
	heading := title
	if header != "" {
		heading = header + " · " + title
	}

	body = strings.TrimSpace(body)
	if body == "" {
		body = "(no output)"
	}
	body = clamp(body, MaxRunes)

	return heading + "\n" + body
}

// GroupCard formats the description block shown when opening a group.
func GroupCard(group skills.Group) string {
	title := strings.TrimSpace(group.Title)
	desc := strings.TrimSpace(group.Description)
	if desc == "" {
		return title
	}
	return title + "\n" + desc + "\n\nChoose a skill:"
}

// ErrorCard formats a user-facing error message for an editable card.
func ErrorCard(def skills.Definition, message string) string {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "internal error"
	}
	return fmt.Sprintf("✗ %s\n%s", humanize(def.Name), clamp(message, MaxRunes))
}

func humanize(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(name, "_", " "))
	parts := strings.Fields(name)
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func clamp(value string, max int) string {
	if max <= 0 {
		return value
	}
	if utf8.RuneCountInString(value) <= max {
		return value
	}
	runes := []rune(value)
	return strings.TrimSpace(string(runes[:max])) + "\n…"
}
