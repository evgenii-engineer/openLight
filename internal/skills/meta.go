package skills

import (
	"context"
	"fmt"
	"sort"
	"strings"
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
		Text: "openLight is ready.\nYou can write normally and I will answer through the local LLM when chat mode is enabled.\nExamples: explain memory usage; show jellyfin logs; read /etc/hostname.\nUse /skills for built-in tools or /help for examples.",
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
		Description: "List available skills.",
		Aliases:     []string{"list skills", "what can you do"},
		Usage:       "/skills",
	}
}

func (s *SkillsSkill) Execute(_ context.Context, _ Input) (Result, error) {
	lines := []string{"Available skills:"}
	for _, group := range s.registry.ListGroups() {
		definitions := s.registry.ListByGroup(group.Key)
		sort.SliceStable(definitions, func(i, j int) bool {
			left := skillOrder(definitions[i].Name)
			right := skillOrder(definitions[j].Name)
			if left != right {
				return left < right
			}
			return definitions[i].Name < definitions[j].Name
		})

		lines = append(lines, "", group.Title)
		for _, definition := range definitions {
			lines = append(lines, fmt.Sprintf("- %s: %s", definition.Name, definition.Description))
		}
	}

	return Result{Text: strings.Join(lines, "\n")}, nil
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
			Text: "You can talk to me normally.\nExamples: explain memory pressure; how much disk space is left; покажи логи tailscale; read /etc/hostname.\nUse /chat <message> to force LLM chat.\nUse /skills for built-in tools or /help <skill> for a specific skill.",
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
	lines := []string{
		fmt.Sprintf("%s: %s", definition.Name, definition.Description),
	}
	if definition.Usage != "" {
		lines = append(lines, "Usage: "+definition.Usage)
	}
	if len(definition.Aliases) > 0 {
		lines = append(lines, "Aliases: "+strings.Join(definition.Aliases, ", "))
	}
	if len(definition.Examples) > 0 {
		lines = append(lines, "Examples: "+strings.Join(definition.Examples, "; "))
	}

	return Result{Text: strings.Join(lines, "\n")}, nil
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
	case "service_list":
		return 30
	case "service_status":
		return 31
	case "service_restart":
		return 32
	case "service_logs":
		return 33
	case "status":
		return 40
	case "cpu":
		return 41
	case "memory":
		return 42
	case "disk":
		return 43
	case "uptime":
		return 44
	case "temperature":
		return 45
	case "hostname":
		return 46
	case "ip":
		return 47
	case "start":
		return 50
	case "help":
		return 51
	case "skills":
		return 52
	case "ping":
		return 53
	default:
		return 1000
	}
}
