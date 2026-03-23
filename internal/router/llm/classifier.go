package llm

import (
	"context"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"time"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/router/semantic"
	"openlight/internal/skills"
)

const (
	defaultExecuteThreshold = 0.80
	defaultClarifyThreshold = 0.60
	defaultRouteInputChars  = 96
	defaultRouteNumPredict  = 32
	defaultSkillInputChars  = 128
	defaultSkillNumPredict  = 48
)

type Options struct {
	AllowedServices          []string
	AllowedWorkbenchRuntimes []string
	ExecuteThreshold         float64
	ClarifyThreshold         float64
	InputChars               int
	NumPredict               int
}

type Classifier struct {
	provider                 basellm.Provider
	registry                 *skills.Registry
	logger                   *slog.Logger
	allowedServices          []string
	allowedWorkbenchRuntimes []string
	skillCatalog             map[string]skills.Definition
	executeThreshold         float64
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

	clarifyThreshold := options.ClarifyThreshold
	if clarifyThreshold <= 0 || clarifyThreshold >= executeThreshold {
		clarifyThreshold = defaultClarifyThreshold
	}

	return &Classifier{
		provider:                 provider,
		registry:                 registry,
		logger:                   logger,
		allowedServices:          normalizeList(options.AllowedServices),
		allowedWorkbenchRuntimes: normalizeList(options.AllowedWorkbenchRuntimes),
		skillCatalog:             buildSkillCatalog(registry),
		executeThreshold:         executeThreshold,
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
	if heuristicDecision, heuristicScore, heuristicOK := c.heuristicSkillDecision(text, nil, c.executeThreshold); heuristicOK && c.shouldUsePreLLMHeuristic(heuristicDecision, heuristicScore) {
		if c.logger != nil {
			c.logger.Debug(
				"llm route short-circuited by heuristic skill inference",
				"rescued_skill", heuristicDecision.SkillName,
				"rescued_args", heuristicDecision.Args,
				"rescued_confidence", heuristicDecision.Confidence,
			)
		}
		return heuristicDecision, true, nil
	}

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

	globalHeuristicDecision, globalHeuristicScore, globalHeuristicOK := c.heuristicSkillDecision(text, nil, routeClassification.Confidence)

	if decision, ok, groupKey, continueToSkill := c.resolveRouteDecision(text, availableGroups, routeClassification); !continueToSkill {
		if globalHeuristicOK && c.shouldUseRouteRescue(decision, ok, globalHeuristicDecision) {
			if c.logger != nil {
				c.logger.Debug(
					"llm route rescued by heuristic skill inference",
					"rescued_skill", globalHeuristicDecision.SkillName,
					"rescued_args", globalHeuristicDecision.Args,
					"rescued_confidence", globalHeuristicDecision.Confidence,
				)
			}
			return globalHeuristicDecision, true, nil
		}
		return decision, ok, nil
	} else {
		if globalHeuristicOK && c.shouldUseRouteGroupRescue(groupKey, globalHeuristicDecision, globalHeuristicScore) {
			if c.logger != nil {
				c.logger.Debug(
					"llm route group rescued by heuristic skill inference",
					"route_group", groupKey,
					"rescued_skill", globalHeuristicDecision.SkillName,
					"rescued_args", globalHeuristicDecision.Args,
					"rescued_confidence", globalHeuristicDecision.Confidence,
				)
			}
			return globalHeuristicDecision, true, nil
		}

		availableSkills := c.buildAvailableSkillsForGroup(groupKey)
		allowedSkills := skillOptionNames(availableSkills)
		allowedServices := c.allowedServicesForGroup(groupKey)
		allowedRuntimes := c.allowedRuntimesForGroup(groupKey)

		if c.logger != nil {
			c.logger.Debug(
				"llm skill request",
				"text", text,
				"normalized_text", normalizedText,
				"group", groupKey,
				"allowed_skills", allowedSkills,
				"allowed_services", allowedServices,
				"allowed_runtimes", allowedRuntimes,
				"available_skills", skillOptionNames(availableSkills),
				"input_chars", c.skillInputChars,
				"num_predict", c.skillNumPredict,
			)
		}

		skillStartedAt := time.Now()
		classification, err := c.provider.ClassifySkill(ctx, text, basellm.SkillClassificationRequest{
			AllowedSkills:   allowedSkills,
			AllowedServices: allowedServices,
			AllowedRuntimes: allowedRuntimes,
			CandidateSkills: availableSkills,
			InputChars:      c.skillInputChars,
			NumPredict:      c.skillNumPredict,
		})
		skillLatencyMS := time.Since(skillStartedAt).Milliseconds()
		if err != nil {
			return router.Decision{}, false, err
		}

		decision, ok := c.resolveSkillDecision(text, allowedSkills, routeClassification.Confidence, classification)
		if heuristicDecision, heuristicScore, heuristicOK := c.heuristicSkillDecision(text, allowedSkills, routeClassification.Confidence); heuristicOK && c.shouldUseSkillRescue(decision, ok, heuristicDecision, heuristicScore) {
			if c.logger != nil {
				c.logger.Debug(
					"llm skill rescued by heuristic inference",
					"group", groupKey,
					"rescued_skill", heuristicDecision.SkillName,
					"rescued_args", heuristicDecision.Args,
					"rescued_confidence", heuristicDecision.Confidence,
				)
			}
			return heuristicDecision, true, nil
		}
		if c.logger != nil {
			c.logger.Debug(
				"llm skill completed",
				"group", groupKey,
				"decision_skill", classification.Skill,
				"decision_confidence", decision.Confidence,
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

func (c *Classifier) shouldUsePreLLMHeuristic(decision router.Decision, score int) bool {
	if !decision.Matched() {
		return false
	}
	switch decision.SkillName {
	case "file_write", "file_replace", "file_read":
		return score >= 6
	case "file_list":
		return score >= 5
	default:
		return false
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
	switch groupKey {
	case "services", "watch":
		return c.allowedServices
	default:
		return nil
	}
}

func (c *Classifier) allowedRuntimesForGroup(groupKey string) []string {
	if groupKey != "workbench" {
		return nil
	}
	return c.allowedWorkbenchRuntimes
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

func (c *Classifier) resolveSkillDecision(text string, allowedSkills []string, routeConfidence float64, classification basellm.Classification) (router.Decision, bool) {
	skillName := normalizeIntent(classification.Skill)
	arguments := normalizeArguments(classification.Arguments)
	decisionConfidence := routeConfidence

	if skillName == "" && len(allowedSkills) == 1 {
		skillName = allowedSkills[0]
		if c.logger != nil {
			c.logger.Debug(
				"llm skill defaulted to the only available skill",
				"skill", skillName,
				"decision_confidence", decisionConfidence,
				"args", arguments,
			)
		}
	}

	arguments = inferArgumentsFromText(text, skillName, arguments)

	if question := clarificationQuestionForSkillResponse(skillName, arguments, classification.ClarificationQuestion); classification.NeedsClarification && question != "" {
		if c.shouldIgnoreSkillClarification(skillName, arguments) {
			if c.logger != nil {
				c.logger.Debug(
					"llm skill clarification ignored for executable read-only skill",
					"skill", skillName,
					"decision_confidence", decisionConfidence,
					"question", question,
					"args", arguments,
				)
			}
		} else {
			if c.logger != nil {
				c.logger.Debug(
					"llm skill requested clarification",
					"skill", skillName,
					"decision_confidence", decisionConfidence,
					"question", question,
					"args", arguments,
				)
			}
			return router.Decision{
				Mode:                  router.ModeLLM,
				Confidence:            decisionConfidence,
				NeedsClarification:    true,
				ClarificationQuestion: question,
			}, true
		}
	}

	if skillName == "" {
		if c.logger != nil {
			c.logger.Debug(
				"llm skill returned no executable skill",
				"decision_confidence", decisionConfidence,
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
				"decision_confidence", decisionConfidence,
				"question", question,
				"args", arguments,
			)
		}
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            decisionConfidence,
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

	return router.Decision{
		Mode:       router.ModeLLM,
		SkillName:  skill.Definition().Name,
		Args:       c.routeArguments(text, skillName, arguments),
		Confidence: decisionConfidence,
	}, true
}

func (c *Classifier) shouldIgnoreSkillClarification(skillName string, arguments map[string]string) bool {
	if skillName == "" {
		return false
	}

	definition, ok := c.skillCatalog[skillName]
	if !ok || definition.Mutating {
		return false
	}

	return c.requiredArgumentQuestion(skillName, arguments) == ""
}

func (c *Classifier) routeArguments(text, skillName string, arguments map[string]string) map[string]string {
	arguments = inferArgumentsFromText(text, skillName, arguments)
	explicitPath := explicitPathArgument(text)

	switch skillName {
	case "help", "skills":
		topic := strings.TrimSpace(arguments["topic"])
		if topic == "" {
			return map[string]string{}
		}
		return map[string]string{"topic": topic}
	case "watch_add":
		return map[string]string{"spec": strings.TrimSpace(arguments["spec"])}
	case "watch_pause", "watch_remove", "watch_test":
		return map[string]string{"id": strings.TrimSpace(arguments["id"])}
	case "watch_history":
		id := strings.TrimSpace(arguments["id"])
		if id == "" {
			return map[string]string{}
		}
		return map[string]string{"id": id}
	case "file_list":
		path := strings.TrimSpace(arguments["path"])
		if explicitPath != "" {
			path = explicitPath
		}
		if path == "" {
			return map[string]string{}
		}
		return map[string]string{"path": path}
	case "file_read":
		path := strings.TrimSpace(arguments["path"])
		if explicitPath != "" {
			path = explicitPath
		}
		return map[string]string{"path": path}
	case "file_write":
		path := strings.TrimSpace(arguments["path"])
		if explicitPath != "" {
			path = explicitPath
		}
		return map[string]string{
			"path":    path,
			"content": arguments["content"],
		}
	case "file_replace":
		path := strings.TrimSpace(arguments["path"])
		if explicitPath != "" {
			path = explicitPath
		}
		return map[string]string{
			"path":    path,
			"find":    arguments["find"],
			"replace": arguments["replace"],
		}
	case "exec_code":
		return map[string]string{
			"runtime": strings.TrimSpace(arguments["runtime"]),
			"code":    arguments["code"],
		}
	case "exec_file":
		path := strings.TrimSpace(arguments["path"])
		if explicitPath != "" {
			path = explicitPath
		}
		return map[string]string{"path": path}
	case "service_status", "service_logs":
		service := strings.TrimSpace(arguments["service"])
		if service == "" {
			service = c.extractAllowedServiceFromText(text)
		}
		if service == "" && len(c.allowedServices) == 1 {
			service = c.allowedServices[0]
		}
		if service == "" {
			return map[string]string{}
		}
		return map[string]string{"service": service}
	case "service_restart":
		service := strings.TrimSpace(arguments["service"])
		if service == "" {
			service = c.extractAllowedServiceFromText(text)
		}
		return map[string]string{"service": service}
	case "note_add":
		return map[string]string{"text": strings.TrimSpace(arguments["text"])}
	case "note_delete":
		return map[string]string{"id": strings.TrimSpace(arguments["id"])}
	case "user_add":
		return map[string]string{
			"provider": strings.TrimSpace(arguments["provider"]),
			"username": strings.TrimSpace(arguments["username"]),
			"password": strings.TrimSpace(arguments["password"]),
		}
	case "user_list":
		result := map[string]string{}
		if provider := strings.TrimSpace(arguments["provider"]); provider != "" {
			result["provider"] = provider
		}
		if pattern := strings.TrimSpace(arguments["pattern"]); pattern != "" {
			result["pattern"] = pattern
		}
		return result
	case "user_delete":
		return map[string]string{
			"provider": strings.TrimSpace(arguments["provider"]),
			"username": strings.TrimSpace(arguments["username"]),
		}
	default:
		return map[string]string{}
	}
}

func explicitPathArgument(text string) string {
	return strings.TrimSpace(firstPathLikeToken(text))
}

func inferArgumentsFromText(text, skillName string, arguments map[string]string) map[string]string {
	result := make(map[string]string, len(arguments))
	for key, value := range arguments {
		result[key] = value
	}

	switch skillName {
	case "file_list", "file_read", "file_write", "file_replace", "exec_file":
		if strings.TrimSpace(result["path"]) == "" {
			if path := firstPathLikeToken(text); path != "" {
				result["path"] = path
			}
		}
	}

	switch skillName {
	case "file_write":
		if strings.TrimSpace(result["content"]) == "" {
			if content := extractFileWriteContent(text); content != "" {
				result["content"] = content
			}
		}
	case "file_replace":
		if strings.TrimSpace(result["find"]) == "" || strings.TrimSpace(result["replace"]) == "" {
			find, replace := extractFileReplaceValues(text)
			if strings.TrimSpace(result["find"]) == "" && find != "" {
				result["find"] = find
			}
			if strings.TrimSpace(result["replace"]) == "" && replace != "" {
				result["replace"] = replace
			}
		}
	case "exec_code":
		if strings.TrimSpace(result["runtime"]) == "" {
			if runtime := extractRuntimeFromText(text); runtime != "" {
				result["runtime"] = runtime
			}
		}
		if strings.TrimSpace(result["code"]) == "" {
			if code := extractCodeSnippet(text); code != "" {
				result["code"] = code
			}
		}
	case "user_list":
		if strings.TrimSpace(result["provider"]) == "" {
			if provider := extractAccountProvider(text); provider != "" {
				result["provider"] = provider
			}
		}
		if strings.TrimSpace(result["pattern"]) == "" {
			if pattern := extractAccountPattern(text); pattern != "" {
				result["pattern"] = pattern
			}
		}
	case "note_add":
		if strings.TrimSpace(result["text"]) == "" {
			if noteText := extractNoteTextFromText(text); noteText != "" {
				result["text"] = noteText
			}
		}
	case "note_delete":
		if strings.TrimSpace(result["id"]) == "" {
			if noteID := extractNumericID(text, noteIDPattern); noteID != "" {
				result["id"] = noteID
			}
		}
	case "watch_add":
		if strings.TrimSpace(result["spec"]) == "" {
			if spec := extractWatchSpecFromText(text); spec != "" {
				result["spec"] = spec
			}
		}
	case "watch_pause", "watch_remove", "watch_test", "watch_history":
		if strings.TrimSpace(result["id"]) == "" {
			if watchID := extractNumericID(text, watchIDPattern); watchID != "" {
				result["id"] = watchID
			}
		}
	case "user_add":
		if strings.TrimSpace(result["provider"]) == "" {
			if provider := extractAccountProvider(text); provider != "" {
				result["provider"] = provider
			}
		}
		if strings.TrimSpace(result["username"]) == "" {
			if username := extractAccountUsername(text); username != "" {
				result["username"] = username
			}
		}
		if strings.TrimSpace(result["password"]) == "" {
			if password := extractAccountPassword(text); password != "" {
				result["password"] = password
			}
		}
	case "user_delete":
		if strings.TrimSpace(result["provider"]) == "" {
			if provider := extractAccountProvider(text); provider != "" {
				result["provider"] = provider
			}
		}
		if strings.TrimSpace(result["username"]) == "" {
			if username := extractAccountUsername(text); username != "" {
				result["username"] = username
			}
		}
	}

	return result
}

func firstPathLikeToken(text string) string {
	for _, field := range strings.Fields(text) {
		token := strings.TrimSpace(strings.Trim(field, "\"'`,.;:!?()[]{}"))
		if looksLikePath(token) {
			return token
		}
	}
	return ""
}

func extractFileWriteContent(text string) string {
	lowered := strings.ToLower(text)
	if idx := strings.Index(lowered, " containing "); idx >= 0 {
		return trimTrailingSentencePunctuation(text[idx+len(" containing "):])
	}
	if idx := strings.Index(lowered, " with content "); idx >= 0 {
		return trimTrailingSentencePunctuation(text[idx+len(" with content "):])
	}
	if writeIdx := strings.Index(lowered, "write "); writeIdx >= 0 {
		restLower := lowered[writeIdx+len("write "):]
		if intoIdx := strings.Index(restLower, " into "); intoIdx >= 0 {
			return strings.TrimSpace(text[writeIdx+len("write ") : writeIdx+len("write ")+intoIdx])
		}
	}
	return ""
}

func extractFileReplaceValues(text string) (string, string) {
	lowered := strings.ToLower(text)
	replaceIdx := strings.Index(lowered, "replace ")
	if replaceIdx < 0 {
		return "", ""
	}
	rest := text[replaceIdx+len("replace "):]
	restLower := lowered[replaceIdx+len("replace "):]
	withIdx := strings.Index(restLower, " with ")
	if withIdx <= 0 {
		return "", ""
	}
	find := strings.TrimSpace(rest[:withIdx])
	replacement := trimTrailingSentencePunctuation(rest[withIdx+len(" with "):])
	return find, replacement
}

func extractRuntimeFromText(text string) string {
	lowered := strings.ToLower(text)
	if idx := strings.Index(lowered, "this "); idx >= 0 {
		rest := text[idx+len("this "):]
		restLower := lowered[idx+len("this "):]
		if end := strings.Index(restLower, " snippet"); end > 0 {
			runtime := strings.TrimSpace(rest[:end])
			if looksLikeRuntime(runtime) {
				return normalizeRuntime(runtime)
			}
		}
	}
	if idx := strings.Index(lowered, "runtime "); idx >= 0 {
		if token := firstToken(strings.TrimSpace(text[idx+len("runtime "):])); looksLikeRuntime(token) {
			return normalizeRuntime(token)
		}
	}
	return ""
}

func extractCodeSnippet(text string) string {
	if idx := strings.Index(text, ":"); idx >= 0 {
		return strings.TrimSpace(text[idx+1:])
	}
	return ""
}

func extractAccountProvider(text string) string {
	lowered := strings.ToLower(text)
	if idx := strings.Index(lowered, "provider "); idx >= 0 {
		if token := firstToken(strings.TrimSpace(text[idx+len("provider "):])); token != "" {
			return token
		}
	}
	if idx := strings.Index(lowered, "from "); idx >= 0 {
		if token := firstToken(strings.TrimSpace(text[idx+len("from "):])); token != "" {
			return token
		}
	}
	if idx := strings.Index(lowered, "to "); idx >= 0 {
		if token := firstToken(strings.TrimSpace(text[idx+len("to "):])); token != "" {
			return token
		}
	}
	return ""
}

func extractAccountPattern(text string) string {
	lowered := strings.ToLower(text)
	for _, marker := range []string{"matching ", "filtered by "} {
		if idx := strings.Index(lowered, marker); idx >= 0 {
			return trimTrailingSentencePunctuation(text[idx+len(marker):])
		}
	}
	return ""
}

func trimTrailingSentencePunctuation(value string) string {
	return strings.TrimSpace(strings.TrimRight(strings.TrimSpace(value), ".!?"))
}

func firstToken(value string) string {
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(strings.Trim(fields[0], "\"'`,.;:!?()[]{}"))
}

func looksLikePath(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}

	switch {
	case strings.HasPrefix(value, "/"),
		strings.HasPrefix(value, "./"),
		strings.HasPrefix(value, "../"),
		strings.HasPrefix(value, "~"),
		strings.HasPrefix(value, "."):
		return true
	}

	if strings.ContainsRune(value, '/') || strings.ContainsRune(value, '\\') {
		return true
	}

	return filepath.Ext(value) != ""
}

func looksLikeRuntime(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "python", "python3", "sh", "shell", "bash", "node", "javascript", "js":
		return true
	default:
		return false
	}
}

func normalizeRuntime(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "shell", "bash":
		return "sh"
	case "javascript", "js":
		return "node"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func (c *Classifier) requiredArgumentQuestion(skillName string, arguments map[string]string) string {
	switch skillName {
	case "file_read":
		if strings.TrimSpace(arguments["path"]) == "" {
			return "Which file should I read?"
		}
	case "file_write":
		if strings.TrimSpace(arguments["path"]) == "" {
			return "Which file should I write?"
		}
	case "file_replace":
		if strings.TrimSpace(arguments["path"]) == "" {
			return "Which file should I edit?"
		}
		if strings.TrimSpace(arguments["find"]) == "" {
			return "What text should I replace?"
		}
	case "exec_code":
		if strings.TrimSpace(arguments["runtime"]) == "" {
			return "Which runtime should I use?"
		}
		if strings.TrimSpace(arguments["code"]) == "" {
			return "What code should I run?"
		}
	case "exec_file":
		if strings.TrimSpace(arguments["path"]) == "" {
			return "Which file should I run?"
		}
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
	case "watch_add":
		if strings.TrimSpace(arguments["spec"]) == "" {
			return "What should I watch? For example: service synapse ask for 30s cooldown 10m."
		}
	case "watch_pause":
		if strings.TrimSpace(arguments["id"]) == "" {
			return "Which watch ID should I pause?"
		}
	case "watch_remove":
		if strings.TrimSpace(arguments["id"]) == "" {
			return "Which watch ID should I remove?"
		}
	case "watch_test":
		if strings.TrimSpace(arguments["id"]) == "" {
			return "Which watch ID should I test?"
		}
	case "user_add":
		if strings.TrimSpace(arguments["username"]) == "" {
			return "Which username should I create?"
		}
		if strings.TrimSpace(arguments["password"]) == "" {
			return "What password should I set for that user?"
		}
	case "user_delete":
		if strings.TrimSpace(arguments["username"]) == "" {
			return "Which username should I delete?"
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
	case "start":
		return "Do you want the getting started message?"
	case "ping":
		return "Do you want a quick connectivity check?"
	case "skills":
		if topic := strings.TrimSpace(arguments["topic"]); topic != "" {
			return "Do you want me to show skills for " + topic + "?"
		}
		return "Do you want me to list the available skill groups?"
	case "help":
		if topic := strings.TrimSpace(arguments["topic"]); topic != "" {
			return "Do you want help for " + topic + "?"
		}
		return "Do you want me to show the general help message?"
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
	case "watch_list":
		return "Do you want me to list configured watches?"
	case "watch_pause":
		if id := strings.TrimSpace(arguments["id"]); id != "" {
			return "Do you want me to pause watch #" + id + "?"
		}
		return "Which watch ID should I pause?"
	case "watch_remove":
		if id := strings.TrimSpace(arguments["id"]); id != "" {
			return "Do you want me to remove watch #" + id + "?"
		}
		return "Which watch ID should I remove?"
	case "watch_history":
		if id := strings.TrimSpace(arguments["id"]); id != "" {
			return "Do you want me to show incidents for watch #" + id + "?"
		}
		return "Do you want me to show recent watch incidents?"
	case "watch_test":
		if id := strings.TrimSpace(arguments["id"]); id != "" {
			return "Do you want me to probe watch #" + id + "?"
		}
		return "Which watch ID should I test?"
	case "file_list":
		return "Do you want me to list whitelisted files or roots?"
	case "file_read":
		if path := strings.TrimSpace(arguments["path"]); path != "" {
			return "Do you want me to read " + path + "?"
		}
		return "Which file should I read?"
	case "file_write":
		if path := strings.TrimSpace(arguments["path"]); path != "" {
			return "Do you want me to write " + path + "?"
		}
		return "Which file should I write?"
	case "file_replace":
		if path := strings.TrimSpace(arguments["path"]); path != "" {
			return "Do you want me to replace text in " + path + "?"
		}
		return "Which file should I edit?"
	case "exec_code":
		runtime := strings.TrimSpace(arguments["runtime"])
		if runtime == "" {
			return "Which runtime should I use?"
		}
		return "Do you want me to run that " + runtime + " code?"
	case "exec_file":
		if path := strings.TrimSpace(arguments["path"]); path != "" {
			return "Do you want me to run " + path + "?"
		}
		return "Which file should I run?"
	case "workspace_clean":
		return "Do you want me to clean the workbench workspace?"
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
	case "user_providers":
		return "Do you want me to list configured account providers?"
	case "user_list":
		if provider := strings.TrimSpace(arguments["provider"]); provider != "" {
			if pattern := strings.TrimSpace(arguments["pattern"]); pattern != "" {
				return "Do you want me to list users for " + provider + " matching " + pattern + "?"
			}
			return "Do you want me to list users for " + provider + "?"
		}
		return "Do you want me to list users from the configured account provider?"
	case "user_add":
		if username := strings.TrimSpace(arguments["username"]); username != "" {
			return "Do you want me to create user " + username + "?"
		}
		return "Which username should I create?"
	case "user_delete":
		if username := strings.TrimSpace(arguments["username"]); username != "" {
			return "Do you want me to delete user " + username + "?"
		}
		return "Which username should I delete?"
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
	case "files":
		return "Do you want to read, list, write, or replace a file?"
	case "workbench":
		return "Do you want to run code, run an allowed file, or clean the workspace?"
	case "system":
		return "Do you want something from system metrics or host info?"
	case "services":
		return "Do you want something about services, logs, or restart?"
	case "watch":
		return "Do you want to add, list, pause, inspect, or remove a watch rule?"
	case "accounts":
		return "Do you want to list providers, list users, add a user, or delete a user?"
	case "notes":
		return "Do you want to add, list, or delete a note?"
	case "core":
		return "Do you want help, skills list, start, or ping?"
	case "", "other":
		return "Which kind of tool do you want: files, workbench, system, services, watch, accounts, notes, or core?"
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
		switch key {
		case "content", "find", "replace", "body", "old", "old_text", "new", "new_text", "from", "to":
			result[key] = value
		default:
			result[key] = strings.TrimSpace(value)
		}
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

	if path := strings.TrimSpace(result["path"]); path == "" {
		switch {
		case strings.TrimSpace(result["file"]) != "":
			result["path"] = strings.TrimSpace(result["file"])
		case strings.TrimSpace(result["file_path"]) != "":
			result["path"] = strings.TrimSpace(result["file_path"])
		case strings.TrimSpace(result["filename"]) != "":
			result["path"] = strings.TrimSpace(result["filename"])
		case strings.TrimSpace(result["name"]) != "":
			result["path"] = strings.TrimSpace(result["name"])
		}
	}

	if content := result["content"]; content == "" {
		switch {
		case result["body"] != "":
			result["content"] = result["body"]
		case result["value"] != "":
			result["content"] = result["value"]
		}
	}

	if runtime := strings.TrimSpace(result["runtime"]); runtime == "" {
		switch {
		case strings.TrimSpace(result["language"]) != "":
			result["runtime"] = strings.TrimSpace(result["language"])
		case strings.TrimSpace(result["interpreter"]) != "":
			result["runtime"] = strings.TrimSpace(result["interpreter"])
		}
	}

	if code := result["code"]; code == "" {
		switch {
		case result["source"] != "":
			result["code"] = result["source"]
		case result["source_code"] != "":
			result["code"] = result["source_code"]
		case result["snippet"] != "":
			result["code"] = result["snippet"]
		}
	}

	if find := result["find"]; find == "" {
		switch {
		case result["old"] != "":
			result["find"] = result["old"]
		case result["old_text"] != "":
			result["find"] = result["old_text"]
		case result["from"] != "":
			result["find"] = result["from"]
		}
	}

	if replacement := result["replace"]; replacement == "" {
		switch {
		case result["new"] != "":
			result["replace"] = result["new"]
		case result["new_text"] != "":
			result["replace"] = result["new_text"]
		case result["to"] != "":
			result["replace"] = result["to"]
		}
	}

	if id := strings.TrimSpace(result["id"]); id == "" {
		switch {
		case strings.TrimSpace(result["note_id"]) != "":
			result["id"] = strings.TrimSpace(result["note_id"])
		case strings.TrimSpace(result["note"]) != "":
			result["id"] = strings.TrimSpace(result["note"])
		case strings.TrimSpace(result["watch_id"]) != "":
			result["id"] = strings.TrimSpace(result["watch_id"])
		}
	}

	if spec := strings.TrimSpace(result["spec"]); spec == "" {
		switch {
		case strings.TrimSpace(result["rule"]) != "":
			result["spec"] = strings.TrimSpace(result["rule"])
		case strings.TrimSpace(result["watch_rule"]) != "":
			result["spec"] = strings.TrimSpace(result["watch_rule"])
		}
	}

	if provider := strings.TrimSpace(result["provider"]); provider == "" {
		switch {
		case strings.TrimSpace(result["account_provider"]) != "":
			result["provider"] = strings.TrimSpace(result["account_provider"])
		case strings.TrimSpace(result["provider_name"]) != "":
			result["provider"] = strings.TrimSpace(result["provider_name"])
		}
	}

	if username := strings.TrimSpace(result["username"]); username == "" {
		switch {
		case strings.TrimSpace(result["user"]) != "":
			result["username"] = strings.TrimSpace(result["user"])
		case strings.TrimSpace(result["user_name"]) != "":
			result["username"] = strings.TrimSpace(result["user_name"])
		case strings.TrimSpace(result["account"]) != "":
			result["username"] = strings.TrimSpace(result["account"])
		}
	}

	if password := strings.TrimSpace(result["password"]); password == "" {
		switch {
		case strings.TrimSpace(result["pass"]) != "":
			result["password"] = strings.TrimSpace(result["pass"])
		case strings.TrimSpace(result["secret"]) != "":
			result["password"] = strings.TrimSpace(result["secret"])
		}
	}

	if pattern := strings.TrimSpace(result["pattern"]); pattern == "" {
		switch {
		case strings.TrimSpace(result["query"]) != "":
			result["pattern"] = strings.TrimSpace(result["query"])
		case strings.TrimSpace(result["filter"]) != "":
			result["pattern"] = strings.TrimSpace(result["filter"])
		}
	}

	if topic := strings.TrimSpace(result["topic"]); topic == "" {
		switch {
		case strings.TrimSpace(result["command"]) != "":
			result["topic"] = strings.TrimSpace(result["command"])
		case strings.TrimSpace(result["subject"]) != "":
			result["topic"] = strings.TrimSpace(result["subject"])
		case strings.TrimSpace(result["tool"]) != "":
			result["topic"] = strings.TrimSpace(result["tool"])
		case strings.TrimSpace(result["skill_name"]) != "":
			result["topic"] = strings.TrimSpace(result["skill_name"])
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
