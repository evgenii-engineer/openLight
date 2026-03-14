package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const defaultDecisionNumPredict = 128

func buildRoutePrompt(text string, request RouteClassificationRequest) string {
	availableGroups := renderGroupOptions(request.Groups)
	allowedIntents := encodePromptList(routeIntentChoices(request.Groups))
	return fmt.Sprintf(
		"Choose one route for a Telegram local agent.\n"+
			"Return JSON only:\n"+
			"{\"intent\":\"string\",\"confidence\":0.0,\"needs_clarification\":false,\"clarification_question\":\"\"}\n\n"+
			"Allowed intents: %s\n"+
			"Groups:\n%s\n\n"+
			"Rules:\n"+
			"- intent must be one of %s\n"+
			"- chat only for free-form conversation\n"+
			"- use a group intent for any tool request\n"+
			"- files = read, list, write, and replace text in whitelisted files\n"+
			"- system = overall host status, cpu, memory, disk, uptime, hostname, ip, temperature\n"+
			"- services = service status, service logs, service restart\n"+
			"- notes = add, list, delete notes\n"+
			"- core = help, skills, start, ping\n"+
			"- if unsure use unknown\n"+
			"- if needs_clarification=false then clarification_question must be \"\"\n"+
			"- if needs_clarification=true then ask one short question\n\n"+
			"Examples:\n"+
			"\"привет\" -> {\"intent\":\"chat\",\"confidence\":0.90,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"read /etc/hostname\" -> {\"intent\":\"files\",\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"покажи общий статус\" -> {\"intent\":\"system\",\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"покажи логи tailscale\" -> {\"intent\":\"services\",\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"почини интернет\" -> {\"intent\":\"unknown\",\"confidence\":0.40,\"needs_clarification\":true,\"clarification_question\":\"Check status, logs, or restart a service?\"}\n\n"+
			"User: %q\n",
		allowedIntents,
		availableGroups,
		allowedIntents,
		text,
	)
}

func buildSkillPrompt(text string, request SkillClassificationRequest) string {
	availableSkills := renderCandidateSkills(request.CandidateSkills)
	allowedServicesSection := ""
	if len(request.AllowedServices) > 0 {
		allowedServicesSection = fmt.Sprintf("Allowed services: %s\n", encodePromptList(request.AllowedServices))
	}
	return fmt.Sprintf(
		"Choose one skill inside the already selected group.\n"+
			"Return JSON only:\n"+
			"{\"skill\":\"string\",\"arguments\":{\"service\":\"\",\"text\":\"\",\"id\":\"\",\"path\":\"\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.0,\"needs_clarification\":false,\"clarification_question\":\"\"}\n\n"+
			"Available skills:\n%s\n"+
			"%s\n"+
			"Rules:\n"+
			"- skill must be one of the listed skills\n"+
			"- use empty skill only when needs_clarification=true\n"+
			"- arguments.service/text/id/path/content/find/replace must always exist; use \"\" when unused\n"+
			"- if service appears, put it in arguments.service\n"+
			"- if note text appears, put it in arguments.text\n"+
			"- if note id appears, put it in arguments.id\n"+
			"- if a file path appears, put it in arguments.path\n"+
			"- if file content appears, put it in arguments.content\n"+
			"- if replacement text appears, put old text in arguments.find and new text in arguments.replace\n"+
			"- if needs_clarification=false then clarification_question must be \"\"\n"+
			"- if needs_clarification=true then ask one short question\n\n"+
			"Examples:\n"+
			"\"сколько памяти занято\" -> {\"skill\":\"memory\",\"arguments\":{\"service\":\"\",\"text\":\"\",\"id\":\"\",\"path\":\"\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"покажи логи tailscale\" -> {\"skill\":\"service_logs\",\"arguments\":{\"service\":\"tailscale\",\"text\":\"\",\"id\":\"\",\"path\":\"\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"удали заметку 3\" -> {\"skill\":\"note_delete\",\"arguments\":{\"service\":\"\",\"text\":\"\",\"id\":\"3\",\"path\":\"\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"read /etc/hostname\" -> {\"skill\":\"file_read\",\"arguments\":{\"service\":\"\",\"text\":\"\",\"id\":\"\",\"path\":\"/etc/hostname\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.95,\"needs_clarification\":false,\"clarification_question\":\"\"}\n"+
			"\"сделай что-нибудь с интернетом\" -> {\"skill\":\"\",\"arguments\":{\"service\":\"\",\"text\":\"\",\"id\":\"\",\"path\":\"\",\"content\":\"\",\"find\":\"\",\"replace\":\"\"},\"confidence\":0.40,\"needs_clarification\":true,\"clarification_question\":\"Check status, logs, or restart a service?\"}\n\n"+
			"User: %q\n",
		availableSkills,
		allowedServicesSection,
		text,
	)
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
		mode := "read-only"
		if skill.Mutating {
			mode = "write"
		} else {
			mode = "read"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s [%s]", skill.Name, skill.Description, mode))
	}
	return strings.Join(lines, "\n")
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
