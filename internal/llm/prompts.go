package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

const defaultDecisionNumPredict = 128

func buildRoutePrompt(text string, request RouteClassificationRequest) string {
	allowedIntents := encodePromptList(routeIntentChoices(request.Groups))
	guide := renderRouteGuide(request.Groups)
	return fmt.Sprintf(
		"Pick one intent for a local Telegram agent.\n"+
			"Return JSON only:\n"+
			"{\"intent\":\"string\",\"confidence\":0.0,\"needs_clarification\":false}\n"+
			"Allowed intents: %s\n"+
			"Guide:\n%s\n"+
			"Rules:\n"+
			"- use one allowed intent\n"+
			"- chat only for normal conversation\n"+
			"- if the request wants a tool, use its tool group, not chat\n"+
			"- if unclear, use unknown or ask one short question\n"+
			"- include clarification_question only when needs_clarification=true\n"+
			"User: %q\n",
		allowedIntents,
		guide,
		text,
	)
}

func buildSkillPrompt(text string, request SkillClassificationRequest) string {
	allowedSkills := encodePromptList(allowedSkillNames(request))
	allowedServicesSection := ""
	if len(request.AllowedServices) > 0 {
		allowedServicesSection = fmt.Sprintf("Allowed services: %s\n", encodePromptList(request.AllowedServices))
	}
	allowedRuntimesSection := ""
	if len(request.AllowedRuntimes) > 0 {
		allowedRuntimesSection = fmt.Sprintf("Allowed runtimes: %s\n", encodePromptList(request.AllowedRuntimes))
	}
	return fmt.Sprintf(
		"Pick one skill inside the selected group.\n"+
			"Return JSON only:\n"+
			"{\"skill\":\"string\",\"arguments\":{},\"needs_clarification\":false}\n"+
			"Allowed skills: %s\n"+
			"%s%s"+
			"Rules:\n"+
			"- use one allowed skill\n"+
			"- use empty skill only when needs_clarification=true\n"+
			"- arguments is optional; include only fields you need\n"+
			"- do not invent values for unused arguments\n"+
			"- for status,cpu,memory,disk,uptime,hostname,ip,temperature use empty arguments {}\n"+
			"- service -> arguments.service, note text -> arguments.text, ids -> arguments.id\n"+
			"- file path -> arguments.path, file content -> arguments.content\n"+
			"- replacement old/new -> arguments.find / arguments.replace\n"+
			"- runtime -> arguments.runtime, code -> arguments.code, watch rule -> arguments.spec\n"+
			"- if one read-only skill clearly matches and needs no extra args, set needs_clarification=false\n"+
			"- ask one short question only when required details are missing\n"+
			"- include clarification_question only when needs_clarification=true\n"+
			"User: %q\n",
		allowedSkills,
		allowedServicesSection,
		allowedRuntimesSection,
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

func renderRouteGuide(groups []GroupOption) string {
	if len(groups) == 0 {
		return "- chat: normal conversation\n- unknown: unclear request"
	}

	lines := make([]string, 0, len(groups)+2)
	lines = append(lines, "- chat: normal conversation")
	for _, group := range groups {
		key := strings.TrimSpace(group.Key)
		if key == "" {
			continue
		}
		lines = append(lines, "- "+key+": "+shortGroupGuide(key))
	}
	lines = append(lines, "- unknown: unclear request")
	return strings.Join(lines, "\n")
}

func shortGroupGuide(key string) string {
	switch key {
	case "files":
		return "read/list/write/replace files"
	case "workbench":
		return "run code or allowed files"
	case "system":
		return "status,cpu,memory,disk,uptime,hostname,ip,temperature"
	case "services":
		return "service status,logs,restart"
	case "watch":
		return "watch rules and incidents"
	case "accounts":
		return "list/add/delete users"
	case "notes":
		return "add/list/delete notes"
	case "core":
		return "help,skills,start,ping"
	default:
		return "use this tool group when it best matches"
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
