package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"openlight/internal/telegram"
)

type StartSkill struct{}

func NewStartSkill() Skill {
	return StartSkill{}
}

func (StartSkill) Definition() Definition {
	return Definition{
		Name:        "start",
		Group:       GroupCore,
		Description: "Show a short introduction and the main commands.",
		Aliases:     []string{"hello", "hi"},
		Usage:       "/start",
	}
}

func (StartSkill) Execute(_ context.Context, _ Input) (Result, error) {
	return Result{
		Text: "openLight is ready.\nEnable monitoring to get your first useful alert fast.\n- Docker: container or compose service goes down -> alert -> Restart / Logs / Status.\n- System: CPU, memory, and disk alerts with safe defaults.\n\nUse /enable docker, /enable system, or /enable auto-heal.",
		Buttons: [][]telegram.Button{
			{
				{Text: "Docker", CallbackData: "enable docker"},
				{Text: "System", CallbackData: "enable system"},
			},
			{
				{Text: "Auto-heal", CallbackData: "enable auto-heal"},
			},
		},
	}, nil
}

type PingSkill struct{}

func NewPingSkill() Skill {
	return PingSkill{}
}

func (PingSkill) Definition() Definition {
	return Definition{
		Name:        "ping",
		Group:       GroupCore,
		Description: "Quick connectivity check.",
		Aliases:     []string{"healthcheck"},
		Usage:       "/ping",
	}
}

func (PingSkill) Execute(_ context.Context, _ Input) (Result, error) {
	return Result{Text: "pong"}, nil
}

type SkillsSkill struct {
	registry *Registry
}

func NewSkillsSkill(registry *Registry) Skill {
	return &SkillsSkill{registry: registry}
}

func (s *SkillsSkill) Definition() Definition {
	return Definition{
		Name:        "skills",
		Group:       GroupCore,
		Description: "List skill groups or expand one group.",
		Aliases:     []string{"list skills", "what can you do"},
		Usage:       "/skills [group|skill]",
	}
}

func (s *SkillsSkill) Execute(_ context.Context, input Input) (Result, error) {
	topic := strings.TrimSpace(input.Args["topic"])
	if topic == "" {
		return Result{Text: s.renderGroupSummary()}, nil
	}

	if group, definitions, ok := s.resolveGroup(topic); ok {
		return Result{Text: renderGroupDetails(group, definitions)}, nil
	}

	skill, ok := s.registry.ResolveIdentifier(topic)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrSkillNotFound, topic)
	}

	return Result{Text: renderSkillDetails(s.definitionForSkill(skill))}, nil
}

type HelpSkill struct {
	registry *Registry
}

func NewHelpSkill(registry *Registry) Skill {
	return &HelpSkill{registry: registry}
}

func (h *HelpSkill) Definition() Definition {
	return Definition{
		Name:        "help",
		Group:       GroupCore,
		Description: "Show usage for a command or skill.",
		Aliases:     []string{"usage", "manual"},
		Usage:       "/help [skill]",
	}
}

func (h *HelpSkill) Execute(_ context.Context, input Input) (Result, error) {
	topic := strings.TrimSpace(input.Args["topic"])
	if topic == "" {
		return Result{
			Text: "You can talk to me normally.\nExamples: explain memory pressure; how much disk space is left; покажи логи tailscale; read /etc/hostname; run /usr/bin/uptime.\nUse chat <message> to force LLM chat.\nUse skills for built-in tools or help <skill> for a specific skill.",
		}, nil
	}

	skill, ok := h.registry.ResolveIdentifier(topic)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrSkillNotFound, topic)
	}

	definition, ok := h.registry.Definition(skill.Definition().Name)
	if !ok {
		definition = skill.Definition()
	}
	return Result{Text: renderSkillDetails(definition)}, nil
}

func (s *SkillsSkill) renderGroupSummary() string {
	lines := []string{"Available skill groups:"}
	for _, group := range s.registry.ListGroups() {
		lines = append(lines, fmt.Sprintf("- %s: %d skill(s). Use skills %s", group.Title, len(s.registry.ListByGroup(group.Key)), group.Key))
	}
	lines = append(lines, "", "Use skills <group> to expand one group or help <skill> for a specific command.")
	return strings.Join(lines, "\n")
}

func (s *SkillsSkill) resolveGroup(topic string) (Group, []Definition, bool) {
	normalized := normalizeGroupTopic(topic)
	if normalized == "" {
		return Group{}, nil, false
	}

	for _, group := range s.registry.ListGroups() {
		if normalizeGroupTopic(group.Key) == normalized || normalizeGroupTopic(group.Title) == normalized {
			return group, s.registry.ListByGroup(group.Key), true
		}
	}

	return Group{}, nil, false
}

func (s *SkillsSkill) definitionForSkill(skill Skill) Definition {
	definition, ok := s.registry.Definition(skill.Definition().Name)
	if !ok {
		return skill.Definition()
	}
	return definition
}

func renderGroupDetails(group Group, definitions []Definition) string {
	sort.SliceStable(definitions, func(i, j int) bool {
		left := skillOrder(definitions[i].Name)
		right := skillOrder(definitions[j].Name)
		if left != right {
			return left < right
		}
		return definitions[i].Name < definitions[j].Name
	})

	lines := []string{
		fmt.Sprintf("%s: %s", group.Title, group.Description),
	}
	for _, definition := range definitions {
		lines = append(lines, "", renderSkillDetails(definition))
	}
	lines = append(lines, "", "Use help <skill> for aliases and extra details.")
	return strings.Join(lines, "\n")
}

func renderSkillDetails(definition Definition) string {
	lines := []string{
		fmt.Sprintf("%s: %s", definition.Name, definition.Description),
	}
	if definition.Usage != "" {
		lines = append(lines, "Usage: "+displayCommandText(definition.Usage))
	}
	if len(definition.Aliases) > 0 {
		lines = append(lines, "Aliases: "+strings.Join(definition.Aliases, ", "))
	}
	if len(definition.Examples) > 0 {
		lines = append(lines, "Examples: "+strings.Join(displayExamples(definition.Examples), "; "))
	}
	return strings.Join(lines, "\n")
}

func normalizeGroupTopic(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", " ")
	value = strings.ReplaceAll(value, "-", " ")
	return strings.Join(strings.Fields(value), " ")
}

func displayExamples(examples []string) []string {
	result := make([]string, 0, len(examples))
	for _, example := range examples {
		result = append(result, displayCommandText(example))
	}
	return result
}

func displayCommandText(value string) string {
	value = strings.TrimSpace(value)
	return strings.TrimPrefix(value, "/")
}

func skillOrder(name string) int {
	switch name {
	case "chat":
		return 0
	case "note_add":
		return 10
	case "note_list":
		return 11
	case "note_delete":
		return 12
	case "file_list":
		return 20
	case "file_read":
		return 21
	case "file_write":
		return 22
	case "file_replace":
		return 23
	case "exec_code":
		return 24
	case "exec_file":
		return 25
	case "workspace_clean":
		return 26
	case "service_list":
		return 30
	case "service_status":
		return 31
	case "service_restart":
		return 32
	case "service_logs":
		return 33
	case "watch_add":
		return 34
	case "watch_enable":
		return 35
	case "watch_list":
		return 36
	case "watch_pause":
		return 37
	case "watch_remove":
		return 38
	case "watch_history":
		return 39
	case "watch_test":
		return 40
	case "user_providers":
		return 41
	case "user_list":
		return 41
	case "user_add":
		return 42
	case "user_delete":
		return 43
	case "status":
		return 44
	case "cpu":
		return 45
	case "memory":
		return 46
	case "disk":
		return 47
	case "uptime":
		return 48
	case "temperature":
		return 49
	case "hostname":
		return 50
	case "ip":
		return 51
	case "start":
		return 52
	case "help":
		return 53
	case "skills":
		return 54
	case "ping":
		return 55
	default:
		return 1000
	}
}
