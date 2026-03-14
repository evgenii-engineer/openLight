package llm

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/router/semantic"
	"openlight/internal/skills"
)

const (
	defaultExecuteThreshold         = 0.80
	defaultMutatingExecuteThreshold = 0.95
	defaultClarifyThreshold         = 0.60
	defaultRouteInputChars          = 96
	defaultRouteNumPredict          = 64
	defaultSkillInputChars          = 128
	defaultSkillNumPredict          = 96
)

type Options struct {
	AllowedServices          []string
	ExecuteThreshold         float64
	MutatingExecuteThreshold float64
	ClarifyThreshold         float64
	InputChars               int
	NumPredict               int
}

type Classifier struct {
	provider                 basellm.Provider
	registry                 *skills.Registry
	logger                   *slog.Logger
	allowedServices          []string
	skillCatalog             map[string]skills.Definition
	executeThreshold         float64
	mutatingExecuteThreshold float64
	clarifyThreshold         float64
	routeInputChars          int
	routeNumPredict          int
	skillInputChars          int
	skillNumPredict          int
}

func NewClassifier(provider basellm.Provider, registry *skills.Registry, options Options, logger *slog.Logger) *Classifier {
	executeThreshold := options.ExecuteThreshold
	if executeThreshold <= 0 || executeThreshold > 1 {
		executeThreshold = defaultExecuteThreshold
	}

	mutatingExecuteThreshold := options.MutatingExecuteThreshold
	if mutatingExecuteThreshold < executeThreshold || mutatingExecuteThreshold > 1 {
		mutatingExecuteThreshold = defaultMutatingExecuteThreshold
	}

	clarifyThreshold := options.ClarifyThreshold
	if clarifyThreshold <= 0 || clarifyThreshold >= executeThreshold {
		clarifyThreshold = defaultClarifyThreshold
	}

	return &Classifier{
		provider:                 provider,
		registry:                 registry,
		logger:                   logger,
		allowedServices:          normalizeList(options.AllowedServices),
		skillCatalog:             buildSkillCatalog(registry),
		executeThreshold:         executeThreshold,
		mutatingExecuteThreshold: mutatingExecuteThreshold,
		clarifyThreshold:         clarifyThreshold,
		routeInputChars:          effectiveLayerInputChars(options.InputChars, defaultRouteInputChars),
		routeNumPredict:          effectiveLayerNumPredict(options.NumPredict, defaultRouteNumPredict),
		skillInputChars:          effectiveLayerInputChars(options.InputChars, defaultSkillInputChars),
		skillNumPredict:          effectiveLayerNumPredict(options.NumPredict, defaultSkillNumPredict),
	}
}

func (c *Classifier) Classify(ctx context.Context, text string) (router.Decision, bool, error) {
	normalizedText := semantic.Normalize(text)
	availableGroups := c.buildAvailableGroups()

	if c.logger != nil {
		c.logger.Debug(
			"llm route request",
			"text", text,
			"normalized_text", normalizedText,
			"available_groups", groupOptionKeys(availableGroups),
			"input_chars", c.routeInputChars,
			"num_predict", c.routeNumPredict,
		)
	}

	routeStartedAt := time.Now()
	routeClassification, err := c.provider.ClassifyRoute(ctx, text, basellm.RouteClassificationRequest{
		Groups:     availableGroups,
		InputChars: c.routeInputChars,
		NumPredict: c.routeNumPredict,
	})
	routeLatencyMS := time.Since(routeStartedAt).Milliseconds()
	if err != nil {
		return router.Decision{}, false, err
	}

	if c.logger != nil {
		c.logger.Debug(
			"llm route completed",
			"route_intent", routeClassification.Intent,
			"route_confidence", routeClassification.Confidence,
			"route_needs_clarification", routeClassification.NeedsClarification,
			"route_available_groups", groupOptionKeys(availableGroups),
			"route_source", "llm",
			"route_latency_ms", routeLatencyMS,
		)
	}

	if decision, ok, groupKey, continueToSkill := c.resolveRouteDecision(text, availableGroups, routeClassification); !continueToSkill {
		return decision, ok, nil
	} else {
		availableSkills := c.buildAvailableSkillsForGroup(groupKey)
		allowedSkills := skillOptionNames(availableSkills)
		allowedServices := c.allowedServicesForGroup(groupKey)

		if c.logger != nil {
			c.logger.Debug(
				"llm skill request",
				"text", text,
				"normalized_text", normalizedText,
				"group", groupKey,
				"allowed_skills", allowedSkills,
				"allowed_services", allowedServices,
				"available_skills", skillOptionNames(availableSkills),
				"input_chars", c.skillInputChars,
				"num_predict", c.skillNumPredict,
			)
		}

		skillStartedAt := time.Now()
		classification, err := c.provider.ClassifySkill(ctx, text, basellm.SkillClassificationRequest{
			AllowedSkills:   allowedSkills,
			AllowedServices: allowedServices,
			CandidateSkills: availableSkills,
			InputChars:      c.skillInputChars,
			NumPredict:      c.skillNumPredict,
		})
		skillLatencyMS := time.Since(skillStartedAt).Milliseconds()
		if err != nil {
			return router.Decision{}, false, err
		}

		decision, ok := c.resolveSkillDecision(text, allowedSkills, classification)
		if c.logger != nil {
			c.logger.Debug(
				"llm skill completed",
				"group", groupKey,
				"decision_skill", classification.Skill,
				"decision_confidence", classification.Confidence,
				"decision_args", classification.Arguments,
				"decision_needs_clarification", classification.NeedsClarification,
				"decision_available_skills", skillOptionNames(availableSkills),
				"decision_source", "llm",
				"decision_latency_ms", skillLatencyMS,
				"decision_routed_skill", decision.SkillName,
				"decision_matched", decision.Matched(),
				"decision_clarification", decision.ShouldClarify(),
			)
		}
		return decision, ok, nil
	}
}

func effectiveLayerInputChars(base int, limit int) int {
	switch {
	case base <= 0:
		return limit
	case limit <= 0:
		return base
	case base < limit:
		return base
	default:
		return limit
	}
}

func effectiveLayerNumPredict(base int, limit int) int {
	switch {
	case base <= 0:
		return limit
	case limit <= 0:
		return base
	case base < limit:
		return base
	default:
		return limit
	}
}

func (c *Classifier) allowedServicesForGroup(groupKey string) []string {
	if groupKey != "services" {
		return nil
	}
	return c.allowedServices
}

func (c *Classifier) resolveRouteDecision(text string, availableGroups []basellm.GroupOption, classification basellm.RouteClassification) (router.Decision, bool, string, bool) {
	intent := normalizeIntent(classification.Intent)

	if question := c.routeClarificationQuestion(intent, availableGroups, classification.ClarificationQuestion); classification.NeedsClarification && question != "" {
		if c.logger != nil {
			c.logger.Debug(
				"llm route requested clarification",
				"intent", intent,
				"confidence", classification.Confidence,
				"question", question,
			)
		}
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            classification.Confidence,
			NeedsClarification:    true,
			ClarificationQuestion: question,
		}, true, "", false
	}

	if intent == "" || intent == "unknown" {
		if c.logger != nil {
			c.logger.Debug(
				"llm route returned no executable intent",
				"intent", intent,
				"confidence", classification.Confidence,
			)
		}
		return router.Decision{}, false, "", false
	}

	if intent == "chat" {
		if question := c.chatClarificationQuestion(classification.Confidence, classification.ClarificationQuestion); question != "" {
			return router.Decision{
				Mode:                  router.ModeLLM,
				Confidence:            classification.Confidence,
				NeedsClarification:    true,
				ClarificationQuestion: question,
			}, true, "", false
		}

		if classification.Confidence < c.executeThreshold {
			if c.logger != nil {
				c.logger.Debug(
					"llm route chat decision below execute threshold",
					"confidence", classification.Confidence,
					"execute_threshold", c.executeThreshold,
					"clarify_threshold", c.clarifyThreshold,
				)
			}
			return router.Decision{}, false, "", false
		}

		if _, ok := c.registry.Get("chat"); !ok {
			if c.logger != nil {
				c.logger.Warn("llm requested chat but chat skill is not registered")
			}
			return router.Decision{}, false, "", false
		}

		return router.Decision{
			Mode:       router.ModeLLM,
			SkillName:  "chat",
			Args:       map[string]string{"text": strings.TrimSpace(text)},
			Confidence: classification.Confidence,
		}, true, "", false
	}

	if slices.Contains(groupOptionKeys(availableGroups), intent) {
		if classification.Confidence < c.executeThreshold {
			if c.logger != nil {
				c.logger.Debug(
					"llm route group decision below execute threshold",
					"group", intent,
					"confidence", classification.Confidence,
					"execute_threshold", c.executeThreshold,
					"clarify_threshold", c.clarifyThreshold,
				)
			}
			if classification.Confidence >= c.clarifyThreshold {
				if question := clarificationQuestionForGroup(intent, classification.ClarificationQuestion); question != "" {
					return router.Decision{
						Mode:                  router.ModeLLM,
						Confidence:            classification.Confidence,
						NeedsClarification:    true,
						ClarificationQuestion: question,
					}, true, "", false
				}
			}
			return router.Decision{}, false, "", false
		}
		return router.Decision{}, false, intent, true
	}

	if c.logger != nil {
		c.logger.Warn("llm returned unsupported route intent", "intent", intent)
	}
	return router.Decision{}, false, "", false
}

func (c *Classifier) resolveSkillDecision(text string, allowedSkills []string, classification basellm.Classification) (router.Decision, bool) {
	skillName := normalizeIntent(classification.Skill)
	arguments := normalizeArguments(classification.Arguments)

	if question := clarificationQuestionForSkillResponse(skillName, arguments, classification.ClarificationQuestion); classification.NeedsClarification && question != "" {
		if c.logger != nil {
			c.logger.Debug(
				"llm skill requested clarification",
				"skill", skillName,
				"confidence", classification.Confidence,
				"question", question,
				"args", arguments,
			)
		}
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            classification.Confidence,
			NeedsClarification:    true,
			ClarificationQuestion: question,
		}, true
	}

	if skillName == "" && len(allowedSkills) == 1 {
		skillName = allowedSkills[0]
		if c.logger != nil {
			c.logger.Debug(
				"llm skill defaulted to the only available skill",
				"skill", skillName,
				"confidence", classification.Confidence,
				"args", arguments,
			)
		}
	}

	if skillName == "" {
		if c.logger != nil {
			c.logger.Debug(
				"llm skill returned no executable skill",
				"confidence", classification.Confidence,
				"args", arguments,
			)
		}
		return router.Decision{}, false
	}

	if !slices.Contains(allowedSkills, skillName) {
		if c.logger != nil {
			c.logger.Warn("llm returned unsupported skill", "skill", skillName)
		}
		return router.Decision{}, false
	}

	if question := c.requiredArgumentQuestion(skillName, arguments); question != "" {
		if c.logger != nil {
			c.logger.Debug(
				"llm skill missing required arguments",
				"skill", skillName,
				"confidence", classification.Confidence,
				"question", question,
				"args", arguments,
			)
		}
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            classification.Confidence,
			NeedsClarification:    true,
			ClarificationQuestion: question,
		}, true
	}

	skill, ok := c.registry.Get(skillName)
	if !ok {
		if c.logger != nil {
			c.logger.Warn("llm returned unregistered skill", "skill", skillName)
		}
		return router.Decision{}, false
	}

	executeThreshold := c.executeThreshold
	if skill.Definition().Mutating {
		executeThreshold = c.mutatingExecuteThreshold
	}
	if classification.Confidence < executeThreshold {
		if c.logger != nil {
			c.logger.Debug(
				"llm skill below execute threshold",
				"skill", skillName,
				"confidence", classification.Confidence,
				"execute_threshold", executeThreshold,
				"clarify_threshold", c.clarifyThreshold,
				"mutating", skill.Definition().Mutating,
				"args", arguments,
			)
		}
		if classification.Confidence >= c.clarifyThreshold {
			if question := clarificationQuestionForSkillResponse(skillName, arguments, classification.ClarificationQuestion); question != "" {
				if c.logger != nil {
					c.logger.Debug(
						"llm skill downgraded to clarification",
						"skill", skillName,
						"confidence", classification.Confidence,
						"question", question,
						"args", arguments,
					)
				}
				return router.Decision{
					Mode:                  router.ModeLLM,
					Confidence:            classification.Confidence,
					NeedsClarification:    true,
					ClarificationQuestion: question,
				}, true
			}
		}
		return router.Decision{}, false
	}

	return router.Decision{
		Mode:       router.ModeLLM,
		SkillName:  skill.Definition().Name,
		Args:       c.routeArguments(text, skillName, arguments),
		Confidence: classification.Confidence,
	}, true
}

func (c *Classifier) routeArguments(text, skillName string, arguments map[string]string) map[string]string {
	switch skillName {
	case "service_status", "service_logs":
		service := strings.TrimSpace(arguments["service"])
		if service == "" && len(c.allowedServices) == 1 {
			service = c.allowedServices[0]
		}
		if service == "" {
			return map[string]string{}
		}
		return map[string]string{"service": service}
	case "service_restart":
		return map[string]string{"service": strings.TrimSpace(arguments["service"])}
	case "note_add":
		return map[string]string{"text": strings.TrimSpace(arguments["text"])}
	case "note_delete":
		return map[string]string{"id": strings.TrimSpace(arguments["id"])}
	default:
		return map[string]string{}
	}
}

func (c *Classifier) requiredArgumentQuestion(skillName string, arguments map[string]string) string {
	switch skillName {
	case "service_restart":
		if strings.TrimSpace(arguments["service"]) == "" {
			return "Which service should I restart?"
		}
	case "service_status":
		if strings.TrimSpace(arguments["service"]) == "" && len(c.allowedServices) != 1 {
			return "Which service should I check?"
		}
	case "service_logs":
		if strings.TrimSpace(arguments["service"]) == "" && len(c.allowedServices) != 1 {
			return "Which service logs should I show?"
		}
	case "note_add":
		if strings.TrimSpace(arguments["text"]) == "" {
			return "What note should I save?"
		}
	case "note_delete":
		if strings.TrimSpace(arguments["id"]) == "" {
			return "Which note ID should I delete?"
		}
	}
	return ""
}

func clarificationQuestion(intent, skillName string, arguments map[string]string, provided string) string {
	if question := strings.TrimSpace(provided); question != "" {
		return question
	}

	switch intent {
	case "chat":
		return "Do you want a normal chat reply?"
	case "skill":
		return clarificationQuestionForSkill(skillName, arguments)
	case "unknown", "":
		return "Could you clarify what you want me to do?"
	}

	return ""
}

func clarificationQuestionForSkill(skillName string, arguments map[string]string) string {
	switch skillName {
	case "status":
		return "Do you want me to show system status?"
	case "cpu":
		return "Do you want me to show CPU usage?"
	case "memory":
		return "Do you want me to show memory usage?"
	case "disk":
		return "Do you want me to show disk usage?"
	case "uptime":
		return "Do you want me to show system uptime?"
	case "temperature":
		return "Do you want me to show device temperature?"
	case "hostname":
		return "Do you want me to show the hostname?"
	case "ip":
		return "Do you want me to show IP addresses?"
	case "service_list":
		return "Do you want me to list allowed services?"
	case "service_restart":
		if service := strings.TrimSpace(arguments["service"]); service != "" {
			return "Do you want me to restart " + service + "?"
		}
		return "Which service should I restart?"
	case "service_status":
		if service := strings.TrimSpace(arguments["service"]); service != "" {
			return "Do you want me to check " + service + " status?"
		}
		return "Which service should I check?"
	case "service_logs":
		if service := strings.TrimSpace(arguments["service"]); service != "" {
			return "Do you want me to show logs for " + service + "?"
		}
		return "Which service logs should I show?"
	case "note_add":
		if text := strings.TrimSpace(arguments["text"]); text == "" {
			return "What note should I save?"
		}
		return "Do you want me to save that as a note?"
	case "note_list":
		return "Do you want me to list saved notes?"
	case "note_delete":
		if id := strings.TrimSpace(arguments["id"]); id == "" {
			return "Which note ID should I delete?"
		}
		return "Do you want me to delete note #" + strings.TrimSpace(arguments["id"]) + "?"
	}

	return ""
}

func clarificationQuestionForSkillResponse(skillName string, arguments map[string]string, provided string) string {
	if question := strings.TrimSpace(provided); question != "" {
		return question
	}
	return clarificationQuestionForSkill(skillName, arguments)
}

func clarificationQuestionForGroup(groupKey, provided string) string {
	if question := strings.TrimSpace(provided); question != "" {
		return question
	}

	switch groupKey {
	case "system":
		return "Do you want something from system metrics or host info?"
	case "services":
		return "Do you want something about services, logs, or restart?"
	case "notes":
		return "Do you want to add, list, or delete a note?"
	case "core":
		return "Do you want help, skills list, start, or ping?"
	case "", "other":
		return "Which kind of tool do you want: system, services, notes, or core?"
	default:
		return "Which tool group do you want?"
	}
}

func (c *Classifier) chatClarificationQuestion(confidence float64, provided string) string {
	if confidence < c.clarifyThreshold || confidence >= c.executeThreshold {
		return ""
	}
	return clarificationQuestion("chat", "", nil, provided)
}

func (c *Classifier) routeClarificationQuestion(intent string, availableGroups []basellm.GroupOption, provided string) string {
	if strings.TrimSpace(provided) != "" {
		return strings.TrimSpace(provided)
	}
	if intent == "chat" {
		return clarificationQuestion("chat", "", nil, "")
	}
	if slices.Contains(groupOptionKeys(availableGroups), intent) {
		return clarificationQuestionForGroup(intent, "")
	}
	return clarificationQuestion("unknown", "", nil, "")
}

func normalizeIntent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.Join(strings.Fields(value), "_")
	return value
}

func normalizeArguments(arguments map[string]string) map[string]string {
	if len(arguments) == 0 {
		return map[string]string{}
	}

	result := make(map[string]string, len(arguments))
	for key, value := range arguments {
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "" {
			continue
		}
		result[key] = strings.TrimSpace(value)
	}

	if service := strings.TrimSpace(result["service"]); service == "" {
		switch {
		case strings.TrimSpace(result["service_name"]) != "":
			result["service"] = strings.TrimSpace(result["service_name"])
		case strings.TrimSpace(result["name"]) != "":
			result["service"] = strings.TrimSpace(result["name"])
		}
	}

	if text := strings.TrimSpace(result["text"]); text == "" {
		switch {
		case strings.TrimSpace(result["note"]) != "":
			result["text"] = strings.TrimSpace(result["note"])
		case strings.TrimSpace(result["note_text"]) != "":
			result["text"] = strings.TrimSpace(result["note_text"])
		}
	}

	if id := strings.TrimSpace(result["id"]); id == "" {
		switch {
		case strings.TrimSpace(result["note_id"]) != "":
			result["id"] = strings.TrimSpace(result["note_id"])
		case strings.TrimSpace(result["note"]) != "":
			result["id"] = strings.TrimSpace(result["note"])
		}
	}

	return result
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func skillOptionNames(skills []basellm.SkillOption) []string {
	if len(skills) == 0 {
		return nil
	}

	result := make([]string, 0, len(skills))
	for _, skill := range skills {
		result = append(result, skill.Name)
	}
	return result
}

func groupOptionKeys(groups []basellm.GroupOption) []string {
	if len(groups) == 0 {
		return nil
	}

	result := make([]string, 0, len(groups))
	for _, group := range groups {
		result = append(result, group.Key)
	}
	return result
}
