package llm

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	basellm "openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/skills"
)

const (
	defaultExecuteThreshold = 0.80
	defaultClarifyThreshold = 0.60
)

var supportedIntents = []string{
	"chat",
	"status",
	"cpu",
	"memory",
	"disk",
	"uptime",
	"temperature",
	"hostname",
	"ip",
	"service_list",
	"service_status",
	"service_restart",
	"service_logs",
	"note_add",
	"note_list",
	"note_delete",
	"unknown",
}

type Options struct {
	AllowedServices  []string
	ExecuteThreshold float64
	ClarifyThreshold float64
	InputChars       int
	NumPredict       int
}

type Classifier struct {
	provider         basellm.Provider
	registry         *skills.Registry
	logger           *slog.Logger
	allowedIntents   []string
	allowedServices  []string
	executeThreshold float64
	clarifyThreshold float64
	inputChars       int
	numPredict       int
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
		provider:         provider,
		registry:         registry,
		logger:           logger,
		allowedIntents:   resolveAllowedIntents(registry),
		allowedServices:  normalizeList(options.AllowedServices),
		executeThreshold: executeThreshold,
		clarifyThreshold: clarifyThreshold,
		inputChars:       options.InputChars,
		numPredict:       options.NumPredict,
	}
}

func (c *Classifier) Classify(ctx context.Context, text string) (router.Decision, bool, error) {
	startedAt := time.Now()
	classification, err := c.provider.ClassifyIntent(ctx, text, basellm.ClassificationRequest{
		AllowedIntents:  c.allowedIntents,
		AllowedServices: c.allowedServices,
		InputChars:      c.inputChars,
		NumPredict:      c.numPredict,
	})
	latencyMS := time.Since(startedAt).Milliseconds()
	if err != nil {
		return router.Decision{}, false, err
	}

	decision, ok := c.resolveDecision(text, classification)
	if c.logger != nil {
		c.logger.Debug(
			"llm decision completed",
			"decision_intent", classification.Intent,
			"decision_confidence", classification.Confidence,
			"decision_args", classification.Arguments,
			"decision_needs_clarification", classification.NeedsClarification,
			"decision_source", "llm",
			"decision_latency_ms", latencyMS,
			"decision_routed_skill", decision.SkillName,
			"decision_matched", decision.Matched(),
			"decision_clarification", decision.ShouldClarify(),
		)
	}
	return decision, ok, nil
}

func (c *Classifier) resolveDecision(text string, classification basellm.Classification) (router.Decision, bool) {
	intent := normalizeIntent(classification.Intent)
	arguments := normalizeArguments(classification.Arguments)

	if question := clarificationQuestion(intent, arguments, classification.ClarificationQuestion); classification.NeedsClarification && question != "" {
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            classification.Confidence,
			NeedsClarification:    true,
			ClarificationQuestion: question,
		}, true
	}

	if classification.Confidence < c.executeThreshold {
		if classification.Confidence >= c.clarifyThreshold {
			if question := clarificationQuestion(intent, arguments, classification.ClarificationQuestion); question != "" {
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

	if intent == "" || intent == "unknown" {
		return router.Decision{}, false
	}

	if !slices.Contains(c.allowedIntents, intent) {
		if c.logger != nil {
			c.logger.Warn("llm returned unsupported intent", "intent", intent)
		}
		return router.Decision{}, false
	}

	if question := c.requiredArgumentQuestion(intent, arguments); question != "" {
		return router.Decision{
			Mode:                  router.ModeLLM,
			Confidence:            classification.Confidence,
			NeedsClarification:    true,
			ClarificationQuestion: question,
		}, true
	}

	skill, ok := c.registry.Get(intent)
	if !ok {
		if c.logger != nil {
			c.logger.Warn("llm returned unregistered intent", "intent", intent)
		}
		return router.Decision{}, false
	}

	return router.Decision{
		Mode:       router.ModeLLM,
		SkillName:  skill.Definition().Name,
		Args:       c.routeArguments(text, intent, arguments),
		Confidence: classification.Confidence,
	}, true
}

func (c *Classifier) routeArguments(text, intent string, arguments map[string]string) map[string]string {
	switch intent {
	case "chat":
		return map[string]string{"text": strings.TrimSpace(text)}
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

func (c *Classifier) requiredArgumentQuestion(intent string, arguments map[string]string) string {
	switch intent {
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

func clarificationQuestion(intent string, arguments map[string]string, provided string) string {
	if question := strings.TrimSpace(provided); question != "" {
		return question
	}

	switch intent {
	case "chat":
		return "Do you want a normal chat reply?"
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
	case "unknown", "":
		return "Could you clarify what you want me to do?"
	}

	return ""
}

func resolveAllowedIntents(registry *skills.Registry) []string {
	result := make([]string, 0, len(supportedIntents))
	for _, intent := range supportedIntents {
		if intent == "unknown" {
			result = append(result, intent)
			continue
		}
		if _, ok := registry.Get(intent); ok {
			result = append(result, intent)
		}
	}
	return result
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
