package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// defaultDecisionNumPredict caps generation for Ollama classifier calls when
// the request leaves NumPredict unset. The schema-constrained output is a
// single small JSON object, so we keep this tight to minimise wall-clock.
const defaultDecisionNumPredict = 16

// buildOllamaRoutePrompt produces the first-stage (group) prompt. The shape is
// deliberately telegraphic: Ollama enforces the response schema, so the model
// only needs to know which intents are allowed and a one-word hint per group.
// Every byte that is not the user's text inflates prefill latency on a 1.5B
// quantised model running on a Mac mini.
func buildOllamaRoutePrompt(text string, request RouteClassificationRequest) string {
	return fmt.Sprintf(
		"intent?\nallowed: %s\nhints: %s\ntext: %q\n",
		encodePromptList(routeIntentChoices(request.Groups)),
		renderOllamaRouteGuide(request.Groups),
		text,
	)
}

// buildOllamaSkillPrompt produces the second-stage (skill) prompt. The group
// is already constrained by the caller, so the prompt only carries the allowed
// skill names, the argument keys the model may emit, and the user's text.
func buildOllamaSkillPrompt(text string, request SkillClassificationRequest) string {
	allowedSkills := allowedSkillNames(request)
	argKeys := allowedArgumentKeysForSkills(allowedSkills)

	var b strings.Builder
	// Pre-allocate for the common shape; avoids the tiny grow steps the
	// default builder does for each Write.
	b.Grow(96 + len(text))

	b.WriteString("skill?\nallowed: ")
	b.WriteString(encodePromptList(allowedSkills))
	b.WriteString("\nargs: ")
	b.WriteString(encodePromptList(argKeys))
	b.WriteByte('\n')

	if len(request.AllowedServices) > 0 {
		b.WriteString("services: ")
		b.WriteString(encodePromptList(request.AllowedServices))
		b.WriteByte('\n')
	}
	if len(request.AllowedRuntimes) > 0 {
		b.WriteString("runtimes: ")
		b.WriteString(encodePromptList(request.AllowedRuntimes))
		b.WriteByte('\n')
	}

	fmt.Fprintf(&b, "text: %q\n", text)
	return b.String()
}

func buildSummaryPrompt(text string) string {
	return fmt.Sprintf(
		"Return only valid JSON with a single field named summary.\n"+
			"Summarize this text briefly and clearly.\n"+
			"Text: %q\n",
		text,
	)
}

func decisionNumPredict(value int) int {
	if value <= 0 {
		return defaultDecisionNumPredict
	}
	return value
}

func encodePromptList(values []string) string {
	if len(values) == 0 {
		return "[]"
	}

	encoded, err := json.Marshal(values)
	if err != nil {
		return "[]"
	}

	return string(encoded)
}

func renderCandidateSkills(skills []SkillOption) string {
	if len(skills) == 0 {
		return "(none)"
	}

	lines := make([]string, 0, len(skills))
	for _, skill := range skills {
		mode := "read"
		if skill.Mutating {
			mode = "write"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s [%s]", skill.Name, skill.Description, mode))
	}
	return strings.Join(lines, "\n")
}

func effectiveCandidateSkills(request SkillClassificationRequest) []SkillOption {
	if len(request.CandidateSkills) > 0 {
		return request.CandidateSkills
	}

	if len(request.AllowedSkills) == 0 {
		return nil
	}

	skills := make([]SkillOption, 0, len(request.AllowedSkills))
	for _, name := range request.AllowedSkills {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		skills = append(skills, SkillOption{
			Name:        name,
			Description: "Select this skill when it best matches the request.",
		})
	}
	return skills
}

func renderGroupOptions(groups []GroupOption) string {
	if len(groups) == 0 {
		return "(none)"
	}

	lines := make([]string, 0, len(groups))
	for _, group := range groups {
		lines = append(lines, fmt.Sprintf("- %s: %s", group.Key, group.Description))
	}
	return strings.Join(lines, "\n")
}

func routeIntentChoices(groups []GroupOption) []string {
	result := []string{"chat", "unknown"}
	for _, group := range groups {
		key := strings.TrimSpace(group.Key)
		if key == "" {
			continue
		}
		result = append(result, key)
	}
	return result
}

// renderOllamaRouteGuide produces a one-line, semicolon-delimited hint string
// for groups whose keys aren't self-explanatory. "chat" and "unknown" are
// omitted because their names already describe the intent; including hints for
// them is pure prompt bloat.
func renderOllamaRouteGuide(groups []GroupOption) string {
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		key := strings.TrimSpace(group.Key)
		if key == "" {
			continue
		}
		parts = append(parts, key+"="+shortGroupGuide(key))
	}
	return strings.Join(parts, ";")
}

func shortGroupGuide(key string) string {
	switch key {
	case "files":
		return "read/write/list files"
	case "workbench":
		return "run code"
	case "system":
		return "cpu/mem/disk/host"
	case "services":
		return "svc status/logs/restart"
	case "watch":
		return "watch rules"
	case "accounts":
		return "users"
	case "notes":
		return "notes"
	case "core":
		return "help/start/ping"
	default:
		return "tool group"
	}
}

func allowedSkillNames(request SkillClassificationRequest) []string {
	if len(request.AllowedSkills) > 0 {
		return request.AllowedSkills
	}

	skills := effectiveCandidateSkills(request)
	if len(skills) == 0 {
		return nil
	}

	result := make([]string, 0, len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		result = append(result, name)
	}
	return result
}
