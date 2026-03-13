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
		Description: "Show a short introduction and the main commands.",
		Aliases:     []string{"hello", "hi"},
		Usage:       "/start",
	}
}

func (StartSkill) Execute(_ context.Context, _ Input) (Result, error) {
	return Result{
		Text: "openLight is ready.\nYou can write normally and I will answer through the local LLM when chat mode is enabled.\nExamples: explain memory usage; show jellyfin logs.\nUse /skills for built-in tools or /help for examples.",
	}, nil
}

type PingSkill struct{}

func NewPingSkill() Skill {
	return PingSkill{}
}

func (PingSkill) Definition() Definition {
	return Definition{
		Name:        "ping",
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
		Description: "List available skills.",
		Aliases:     []string{"list skills", "what can you do"},
		Usage:       "/skills",
	}
}

func (s *SkillsSkill) Execute(_ context.Context, _ Input) (Result, error) {
	grouped := make(map[string][]Definition)
	groupInfo := make(map[string]skillGroup)

	for _, definition := range s.registry.List() {
		if definition.Hidden {
			continue
		}

		group := skillGroupFor(definition.Name)
		grouped[group.Title] = append(grouped[group.Title], definition)
		groupInfo[group.Title] = group
	}

	titles := make([]string, 0, len(grouped))
	for title := range grouped {
		titles = append(titles, title)
	}

	sort.Slice(titles, func(i, j int) bool {
		left := groupInfo[titles[i]]
		right := groupInfo[titles[j]]
		if left.Order != right.Order {
			return left.Order < right.Order
		}
		return titles[i] < titles[j]
	})

	lines := []string{"Available skills:"}
	for _, title := range titles {
		definitions := grouped[title]
		sort.SliceStable(definitions, func(i, j int) bool {
			left := skillOrder(definitions[i].Name)
			right := skillOrder(definitions[j].Name)
			if left != right {
				return left < right
			}
			return definitions[i].Name < definitions[j].Name
		})

		lines = append(lines, "", title)
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
		Description: "Show usage for a command or skill.",
		Aliases:     []string{"usage", "manual"},
		Usage:       "/help [skill]",
	}
}

func (h *HelpSkill) Execute(_ context.Context, input Input) (Result, error) {
	topic := strings.TrimSpace(input.Args["topic"])
	if topic == "" {
		return Result{
			Text: "You can talk to me normally.\nExamples: explain memory pressure; how much disk space is left; покажи логи tailscale.\nUse /chat <message> to force LLM chat.\nUse /skills for built-in tools or /help <skill> for a specific skill.",
		}, nil
	}

	skill, ok := h.registry.ResolveIdentifier(topic)
	if !ok {
		return Result{}, fmt.Errorf("%w: %s", ErrSkillNotFound, topic)
	}

	definition := skill.Definition()
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

type skillGroup struct {
	Title string
	Order int
}

func skillGroupFor(name string) skillGroup {
	switch name {
	case "chat":
		return skillGroup{Title: "Chat", Order: 0}
	case "note_add", "note_list", "note_delete":
		return skillGroup{Title: "Notes", Order: 1}
	case "service_list", "service_status", "service_restart", "service_logs":
		return skillGroup{Title: "Services", Order: 2}
	case "status", "cpu", "memory", "disk", "uptime", "hostname", "ip", "temperature":
		return skillGroup{Title: "System", Order: 3}
	case "start", "help", "skills", "ping":
		return skillGroup{Title: "Core", Order: 4}
	default:
		return skillGroup{Title: "Other", Order: 99}
	}
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
	case "service_list":
		return 20
	case "service_status":
		return 21
	case "service_restart":
		return 22
	case "service_logs":
		return 23
	case "status":
		return 30
	case "cpu":
		return 31
	case "memory":
		return 32
	case "disk":
		return 33
	case "uptime":
		return 34
	case "temperature":
		return 35
	case "hostname":
		return 36
	case "ip":
		return 37
	case "start":
		return 40
	case "help":
		return 41
	case "skills":
		return 42
	case "ping":
		return 43
	default:
		return 1000
	}
}
