package services

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/skills"
)

type listSkill struct {
	manager Manager
}

func NewListSkill(manager Manager) skills.Skill {
	return &listSkill{manager: manager}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "service_list",
		Group:       skills.GroupServices,
		Description: "List whitelisted services and their current status.",
		Aliases:     []string{"services", "list services"},
		Usage:       "/services",
	}
}

func (s *listSkill) Execute(ctx context.Context, _ skills.Input) (skills.Result, error) {
	services, err := s.manager.List(ctx)
	if err != nil {
		return skills.Result{}, err
	}
	if len(services) == 0 {
		return skills.Result{Text: "No services are whitelisted."}, nil
	}

	lines := make([]string, 0, len(services))
	for _, service := range services {
		name := displayInfoName(service)
		state := service.ActiveState
		if state == "" {
			state = "unknown"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", name, state))
	}
	return skills.Result{Text: "Allowed services:\n" + strings.Join(lines, "\n")}, nil
}

type statusSkill struct {
	manager Manager
}

func NewStatusSkill(manager Manager) skills.Skill {
	return &statusSkill{manager: manager}
}

func (s *statusSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "service_status",
		Group:       skills.GroupServices,
		Description: "Show status for a whitelisted service.",
		Aliases:     []string{"service status"},
		Usage:       "/service [name]",
	}
}

func (s *statusSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	service, err := resolveOptionalService(ctx, s.manager, input.Args["service"])
	if err != nil {
		return skills.Result{}, err
	}

	info, err := s.manager.Status(ctx, service)
	if err != nil {
		return skills.Result{}, err
	}

	text := fmt.Sprintf(
		"Service: %s\n%sLoad: %s\nActive: %s\nSub: %s\nDescription: %s",
		displayServiceName(info.Name),
		formatHostLine(info.Host),
		emptyOrUnknown(info.LoadState),
		emptyOrUnknown(info.ActiveState),
		emptyOrUnknown(info.SubState),
		emptyOrUnknown(info.Description),
	)
	return skills.Result{Text: text}, nil
}

type restartSkill struct {
	manager Manager
}

func NewRestartSkill(manager Manager) skills.Skill {
	return &restartSkill{manager: manager}
}

func (s *restartSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "service_restart",
		Group:       skills.GroupServices,
		Description: "Restart a whitelisted service.",
		Aliases:     []string{"restart service"},
		Usage:       "/restart <name>",
		Mutating:    true,
	}
}

func (s *restartSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	service := strings.TrimSpace(input.Args["service"])
	if service == "" {
		return skills.Result{}, fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
	}

	if err := s.manager.Restart(ctx, service); err != nil {
		return skills.Result{}, err
	}

	return skills.Result{Text: "Service restarted: " + service}, nil
}

type logsSkill struct {
	manager  Manager
	lines    int
	maxChars int
}

func NewLogsSkill(manager Manager, lines int, maxChars int) skills.Skill {
	return &logsSkill{
		manager:  manager,
		lines:    lines,
		maxChars: maxChars,
	}
}

func (s *logsSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "service_logs",
		Group:       skills.GroupServices,
		Description: "Show recent logs for a whitelisted service.",
		Aliases:     []string{"logs", "service logs"},
		Usage:       "/logs [name]",
	}
}

func (s *logsSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	service, err := resolveOptionalService(ctx, s.manager, input.Args["service"])
	if err != nil {
		return skills.Result{}, err
	}

	logsText, err := s.manager.Logs(ctx, service, s.lines)
	if err != nil {
		return skills.Result{}, err
	}

	if logsText == "" {
		logsText = "No log lines found."
	} else {
		logsText = limitLogOutput(logsText, s.maxChars)
	}
	return skills.Result{Text: fmt.Sprintf("Logs for %s:\n%s", displayServiceName(service), logsText)}, nil
}

func emptyOrUnknown(value string) string {
	if strings.TrimSpace(value) == "" {
		return "unknown"
	}
	return value
}

func displayInfoName(info Info) string {
	name := displayServiceName(info.Name)
	if strings.TrimSpace(info.Host) == "" {
		return name
	}
	return name + "@" + strings.TrimSpace(info.Host)
}

func formatHostLine(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	return "Host: " + host + "\n"
}

func resolveOptionalService(ctx context.Context, manager Manager, value string) (string, error) {
	service := strings.TrimSpace(value)
	if service != "" {
		return service, nil
	}

	services, err := manager.List(ctx)
	if err != nil {
		return "", err
	}
	if len(services) == 1 {
		return displayServiceName(services[0].Name), nil
	}

	return "", fmt.Errorf("%w: service name is required", skills.ErrInvalidArguments)
}

func limitLogOutput(text string, maxChars int) string {
	if maxChars <= 0 {
		return text
	}

	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}

	suffix := "\n...\n[truncated]"
	suffixRunes := []rune(suffix)
	if maxChars <= len(suffixRunes) {
		return string(suffixRunes[:maxChars])
	}

	limit := maxChars - len(suffixRunes)
	split := limit
	floor := limit / 2
	for idx := limit; idx > floor; idx-- {
		if runes[idx-1] == '\n' {
			split = idx
			break
		}
	}

	trimmed := strings.TrimRight(string(runes[:split]), "\n")
	return trimmed + suffix
}
