package llm

import (
	"fmt"
	"strings"
)

const openAIClarificationToolName = "request_clarification"

func buildSkillToolInstructions(request SkillClassificationRequest) string {
	skills := effectiveCandidateSkills(request)
	availableSkills := renderCandidateSkills(skills)
	allowedServicesSection := ""
	if len(request.AllowedServices) > 0 {
		allowedServicesSection = fmt.Sprintf("Allowed services: %s\n", encodePromptList(request.AllowedServices))
	}
	allowedRuntimesSection := ""
	if len(request.AllowedRuntimes) > 0 {
		allowedRuntimesSection = fmt.Sprintf("Allowed runtimes: %s\n", encodePromptList(request.AllowedRuntimes))
	}

	return fmt.Sprintf(
		"Choose exactly one function for the already selected skill group.\n"+
			"Never answer with plain text.\n"+
			"Call one skill function when you can map the request to a concrete skill.\n"+
			"Call %q when the request is ambiguous, missing required details, or needs confirmation.\n\n"+
			"Available skills:\n%s\n"+
			"%s%s\n"+
			"Rules:\n"+
			"- only call one function\n"+
			"- choose the most specific matching skill\n"+
			"- only use allowed service names and runtimes\n"+
			"- copy file paths and note text from the user when present\n"+
			"- for watch_add, build a formal watch rule string in arguments.spec\n"+
			"- examples for arguments.spec: \"service synapse ask for 30s cooldown 10m\", \"memory > 90%% for 5m cooldown 15m\"\n"+
			"- use only the arguments relevant to the selected skill\n"+
			"- if the request is unclear or incomplete, call %q with one short question\n",
		openAIClarificationToolName,
		availableSkills,
		allowedServicesSection,
		allowedRuntimesSection,
		openAIClarificationToolName,
	)
}

func openAISkillTools(request SkillClassificationRequest) []openAITool {
	skills := effectiveCandidateSkills(request)
	tools := make([]openAITool, 0, len(skills)+1)

	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}

		description := strings.TrimSpace(skill.Description)
		if description == "" {
			description = "Select this skill."
		}
		if skill.Mutating {
			description += " This action changes state."
		} else {
			description += " This is read-only."
		}

		tools = append(tools, openAITool{
			Type:        "function",
			Name:        name,
			Description: description,
			Parameters:  openAISkillFunctionParameters(name, request),
			Strict:      false,
		})
	}

	tools = append(tools, openAITool{
		Type:        "function",
		Name:        openAIClarificationToolName,
		Description: "Ask one short follow-up question when the request is ambiguous or missing required details.",
		Parameters: map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"question": map[string]any{
					"type":        "string",
					"description": "A short clarification question for the user.",
				},
			},
			"required": []string{"question"},
		},
		Strict: true,
	})

	return tools
}

func openAISkillFunctionParameters(skillName string, request SkillClassificationRequest) map[string]any {
	properties := map[string]any{}

	addService := func() {
		properties["service"] = openAINullableStringProperty(
			"Allowed service name when the skill works with services.",
			request.AllowedServices,
		)
	}
	addText := func(description string) {
		properties["text"] = openAINullableStringProperty(description, nil)
	}
	addID := func(description string) {
		properties["id"] = openAINullableStringProperty(description, nil)
	}
	addPath := func(description string) {
		properties["path"] = openAINullableStringProperty(description, nil)
	}
	addContent := func() {
		properties["content"] = openAINullableStringProperty("File content when writing a file.", nil)
	}
	addFindReplace := func() {
		properties["find"] = openAINullableStringProperty("Text to replace.", nil)
		properties["replace"] = openAINullableStringProperty("Replacement text.", nil)
	}
	addRuntimeCode := func() {
		properties["runtime"] = openAINullableStringProperty(
			"Allowed runtime when the skill executes code.",
			request.AllowedRuntimes,
		)
		properties["code"] = openAINullableStringProperty("Source code to execute.", nil)
	}
	addSpec := func() {
		properties["spec"] = openAINullableStringProperty(
			"Formal watch rule string for watch_add, for example: service synapse ask for 30s cooldown 10m",
			nil,
		)
	}

	switch skillName {
	case "service_status", "service_restart", "service_logs":
		addService()
	case "note_add":
		addText("Free-form user text, such as note text.")
	case "note_delete", "watch_pause", "watch_remove", "watch_test", "watch_history":
		addID("Identifier such as a note ID or watch ID.")
	case "file_list", "file_read", "exec_file":
		addPath("Filesystem path when the skill needs one.")
	case "file_write":
		addPath("Filesystem path when the skill needs one.")
		addContent()
	case "file_replace":
		addPath("Filesystem path when the skill needs one.")
		addFindReplace()
	case "exec_code":
		addRuntimeCode()
	case "watch_add":
		addSpec()
	}

	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties":           properties,
	}
}

func openAINullableStringProperty(description string, enum []string) map[string]any {
	property := map[string]any{
		"type":        []string{"string", "null"},
		"description": description,
	}
	if len(enum) > 0 {
		values := make([]any, 0, len(enum)+1)
		for _, value := range enum {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			values = append(values, value)
		}
		values = append(values, nil)
		property["enum"] = values
	}
	return property
}
