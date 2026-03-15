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
	skillParameters := openAISkillFunctionParameters(request)
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
			Parameters:  skillParameters,
			Strict:      true,
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

func openAISkillFunctionParameters(request SkillClassificationRequest) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"service": openAINullableStringProperty(
				"Allowed service name when the skill works with services.",
				request.AllowedServices,
			),
			"text": openAINullableStringProperty(
				"Free-form user text, such as note text.",
				nil,
			),
			"id": openAINullableStringProperty(
				"Identifier such as a note ID.",
				nil,
			),
			"path": openAINullableStringProperty(
				"Filesystem path when the skill needs one.",
				nil,
			),
			"content": openAINullableStringProperty(
				"File content when writing a file.",
				nil,
			),
			"find": openAINullableStringProperty(
				"Text to replace.",
				nil,
			),
			"replace": openAINullableStringProperty(
				"Replacement text.",
				nil,
			),
			"runtime": openAINullableStringProperty(
				"Allowed runtime when the skill executes code.",
				request.AllowedRuntimes,
			),
			"code": openAINullableStringProperty(
				"Source code to execute.",
				nil,
			),
		},
		"required": []string{
			"service",
			"text",
			"id",
			"path",
			"content",
			"find",
			"replace",
			"runtime",
			"code",
		},
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
