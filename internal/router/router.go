package router

import (
	"context"
	"strings"

	"openlight/internal/router/rules"
	"openlight/internal/skills"
)

type Mode string

const (
	ModeSlash    Mode = "slash"
	ModeExplicit Mode = "explicit"
	ModeAlias    Mode = "alias"
	ModeRule     Mode = "rule"
	ModeLLM      Mode = "llm"
	ModeUnknown  Mode = "unknown"
)

type Decision struct {
	Mode                  Mode
	SkillName             string
	Args                  map[string]string
	Confidence            float64
	NeedsClarification    bool
	ClarificationQuestion string
}

func (d Decision) Matched() bool {
	return strings.TrimSpace(d.SkillName) != ""
}

func (d Decision) ShouldClarify() bool {
	return d.NeedsClarification && strings.TrimSpace(d.ClarificationQuestion) != ""
}

type Classifier interface {
	Classify(ctx context.Context, text string) (Decision, bool, error)
}

type Router struct {
	registry   *skills.Registry
	classifier Classifier
}

func New(registry *skills.Registry, classifier Classifier) *Router {
	return &Router{
		registry:   registry,
		classifier: classifier,
	}
}

func (r *Router) Route(ctx context.Context, text string) (Decision, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return Decision{Mode: ModeUnknown}, nil
	}

	if decision, ok := routeSlash(text); ok {
		return decision, nil
	}

	if decision, ok := routeExplicit(text); ok {
		return decision, nil
	}

	if skill, ok := r.registry.ResolveIdentifier(text); ok {
		return Decision{
			Mode:      ModeAlias,
			SkillName: skill.Definition().Name,
			Args:      map[string]string{},
		}, nil
	}

	if match, ok := rules.Parse(text); ok {
		return Decision{
			Mode:      ModeRule,
			SkillName: match.SkillName,
			Args:      match.Args,
		}, nil
	}

	if r.classifier != nil {
		decision, ok, err := r.classifier.Classify(ctx, text)
		if err != nil {
			return Decision{}, err
		}
		if ok {
			return decision, nil
		}
	}

	return Decision{Mode: ModeUnknown}, nil
}

func routeSlash(text string) (Decision, bool) {
	if !strings.HasPrefix(text, "/") {
		return Decision{}, false
	}

	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Decision{}, false
	}

	command := normalizeRouteCommand(fields[0])
	argsText := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))

	return routeCommand(command, argsText, ModeSlash)
}

func routeExplicit(text string) (Decision, bool) {
	fields := strings.Fields(text)
	if len(fields) == 0 {
		return Decision{}, false
	}

	maxParts := len(fields)
	if maxParts > 3 {
		maxParts = 3
	}

	for parts := maxParts; parts >= 1; parts-- {
		command := normalizeRouteCommand(strings.Join(fields[:parts], " "))
		argsText := strings.TrimSpace(strings.Join(fields[parts:], " "))
		if decision, ok := routeCommand(command, argsText, ModeExplicit); ok {
			return decision, true
		}
	}

	return Decision{}, false
}

func routeCommand(command, argsText string, mode Mode) (Decision, bool) {
	switch command {
	case "start":
		return routeNoArgCommand(mode, "start", argsText)
	case "help":
		return Decision{Mode: mode, SkillName: "help", Args: map[string]string{"topic": argsText}}, true
	case "ping":
		return routeNoArgCommand(mode, "ping", argsText)
	case "skills":
		return routeNoArgCommand(mode, "skills", argsText)
	case "status":
		if argsText == "" {
			return Decision{Mode: mode, SkillName: "status", Args: map[string]string{}}, true
		}
		return Decision{Mode: mode, SkillName: "service_status", Args: map[string]string{"service": argsText}}, true
	case "cpu":
		return routeNoArgCommand(mode, "cpu", argsText)
	case "memory", "ram":
		return routeNoArgCommand(mode, "memory", argsText)
	case "disk", "storage":
		return routeNoArgCommand(mode, "disk", argsText)
	case "uptime":
		return routeNoArgCommand(mode, "uptime", argsText)
	case "ip":
		return routeNoArgCommand(mode, "ip", argsText)
	case "hostname", "host":
		return routeNoArgCommand(mode, "hostname", argsText)
	case "temp", "temperature":
		return routeNoArgCommand(mode, "temperature", argsText)
	case "services", "service list":
		return routeNoArgCommand(mode, "service_list", argsText)
	case "service", "service status":
		return Decision{Mode: mode, SkillName: "service_status", Args: map[string]string{"service": argsText}}, true
	case "restart", "service restart":
		return Decision{Mode: mode, SkillName: "service_restart", Args: map[string]string{"service": argsText}}, true
	case "logs", "log", "service logs":
		return Decision{Mode: mode, SkillName: "service_logs", Args: map[string]string{"service": argsText}}, true
	case "note delete", "delete note", "remove note", "note remove", "note_delete":
		return Decision{Mode: mode, SkillName: "note_delete", Args: map[string]string{"id": argsText}}, true
	case "note", "note add", "add note", "remember":
		return Decision{Mode: mode, SkillName: "note_add", Args: map[string]string{"text": argsText}}, true
	case "notes", "note list", "list notes":
		return routeNoArgCommand(mode, "note_list", argsText)
	case "chat", "ask":
		return Decision{Mode: mode, SkillName: "chat", Args: map[string]string{"text": argsText}}, true
	default:
		return Decision{}, false
	}
}

func routeNoArgCommand(mode Mode, skillName, argsText string) (Decision, bool) {
	if mode != ModeSlash && strings.TrimSpace(argsText) != "" {
		return Decision{}, false
	}

	return Decision{Mode: mode, SkillName: skillName, Args: map[string]string{}}, true
}

func normalizeRouteCommand(value string) string {
	value = strings.TrimPrefix(strings.TrimSpace(value), "/")
	if idx := strings.Index(value, "@"); idx >= 0 {
		value = value[:idx]
	}
	value = strings.ToLower(value)
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}
