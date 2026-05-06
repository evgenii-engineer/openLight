package memory

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"openlight/internal/models"
	"openlight/internal/skills"
)

type Store interface {
	AddMemory(ctx context.Context, memory models.Memory) (models.Memory, error)
	ListMemories(ctx context.Context, limit int) ([]models.Memory, error)
	SearchMemories(ctx context.Context, query string, limit int) ([]models.Memory, error)
	DeleteMemory(ctx context.Context, id int64) error
}

type rememberSkill struct {
	store   Store
	enabled bool
}

func NewRememberSkill(store Store, enabled bool) skills.Skill {
	return &rememberSkill{store: store, enabled: enabled}
}

func (s *rememberSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory_add",
		Group:       skills.GroupMemory,
		Description: "Remember a durable fact, note, or preference in SQLite storage.",
		Aliases:     []string{"remember", "memory add"},
		Usage:       "/remember <text>",
		Examples: []string{
			"remember that my Mac mini is the main inference node",
			"remember note: Synapse is exposed through Tailscale Funnel",
		},
		Mutating: true,
	}
}

func (s *rememberSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "text", Prompt: "What should I remember?", Placeholder: "synapse runs on mac mini"},
		},
	}
}

func (s *rememberSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	if !s.enabled {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "memory is disabled")
	}

	parsed, err := parseMemoryText(input.Args["text"])
	if err != nil {
		return skills.Result{}, err
	}

	memory, err := s.store.AddMemory(ctx, models.Memory{
		Text:   parsed.Text,
		Kind:   parsed.Kind,
		Tags:   parsed.Tags,
		Source: normalizeSource(input.Source),
	})
	if err != nil {
		return skills.Result{}, err
	}

	return skills.Result{Text: fmt.Sprintf("Saved memory #%d", memory.ID)}, nil
}

type listSkill struct {
	store   Store
	limit   int
	enabled bool
}

func NewListSkill(store Store, limit int, enabled bool) skills.Skill {
	return &listSkill{store: store, limit: limit, enabled: enabled}
}

func (s *listSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory_list",
		Group:       skills.GroupMemory,
		Description: "List recent memories or search them by text.",
		Aliases:     []string{"memories", "memory list", "what do you remember"},
		Usage:       "/memories [query]",
	}
}

func (s *listSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	if !s.enabled {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "memory is disabled")
	}

	query := strings.TrimSpace(input.Args["query"])

	var (
		memories []models.Memory
		err      error
	)
	if query == "" {
		memories, err = s.store.ListMemories(ctx, s.limit)
	} else {
		memories, err = s.store.SearchMemories(ctx, query, s.limit)
	}
	if err != nil {
		return skills.Result{}, err
	}
	if len(memories) == 0 {
		if query == "" {
			return skills.Result{Text: "No memories saved yet."}, nil
		}
		return skills.Result{Text: "No matching memories."}, nil
	}

	lines := make([]string, 0, len(memories))
	for _, memory := range memories {
		line := fmt.Sprintf("- #%d [%s]", memory.ID, memory.Kind)
		if len(memory.Tags) > 0 {
			line += " {" + strings.Join(memory.Tags, ", ") + "}"
		}
		line += " " + memory.Text
		lines = append(lines, line)
	}

	header := "Memories:"
	if query != "" {
		header = "Matching memories:"
	}
	return skills.Result{Text: header + "\n" + strings.Join(lines, "\n")}, nil
}

type forgetSkill struct {
	store   Store
	limit   int
	enabled bool
}

func NewForgetSkill(store Store, limit int, enabled bool) skills.Skill {
	return &forgetSkill{store: store, limit: limit, enabled: enabled}
}

func (s *forgetSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory_delete",
		Group:       skills.GroupMemory,
		Description: "Forget a memory by id or matching text.",
		Aliases:     []string{"forget", "memory delete", "memory forget"},
		Usage:       "/forget <id or text>",
		Mutating:    true,
	}
}

func (s *forgetSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "ref", Prompt: "Memory id or matching text to forget?", Placeholder: "42"},
		},
	}
}

func (s *forgetSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	if !s.enabled {
		return skills.Result{}, skills.NewUserError(skills.ErrUnavailable, "memory is disabled")
	}

	ref := strings.TrimSpace(input.Args["ref"])
	if ref == "" {
		return skills.Result{}, fmt.Errorf("%w: memory id or text is required", skills.ErrInvalidArguments)
	}

	if id, ok := parsePositiveInt64(ref); ok {
		if err := s.store.DeleteMemory(ctx, id); err != nil {
			return skills.Result{}, err
		}
		return skills.Result{Text: fmt.Sprintf("Forgot memory #%d", id)}, nil
	}

	matches, err := s.store.SearchMemories(ctx, ref, s.limit)
	if err != nil {
		return skills.Result{}, err
	}

	exact := make([]models.Memory, 0, len(matches))
	for _, memory := range matches {
		if strings.EqualFold(strings.TrimSpace(memory.Text), strings.TrimSpace(ref)) {
			exact = append(exact, memory)
		}
	}
	if len(exact) == 1 {
		if err := s.store.DeleteMemory(ctx, exact[0].ID); err != nil {
			return skills.Result{}, err
		}
		return skills.Result{Text: fmt.Sprintf("Forgot memory #%d", exact[0].ID)}, nil
	}

	if len(matches) == 1 {
		if err := s.store.DeleteMemory(ctx, matches[0].ID); err != nil {
			return skills.Result{}, err
		}
		return skills.Result{Text: fmt.Sprintf("Forgot memory #%d", matches[0].ID)}, nil
	}
	if len(matches) == 0 {
		return skills.Result{}, fmt.Errorf("%w: memory %q", skills.ErrNotFound, ref)
	}

	return skills.Result{}, skills.NewUserError(skills.ErrInvalidArguments, "multiple memories match; use /forget <id>")
}

type parsedMemory struct {
	Text string
	Kind string
	Tags []string
}

func parseMemoryText(raw string) (parsedMemory, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return parsedMemory{}, fmt.Errorf("%w: memory text is required", skills.ErrInvalidArguments)
	}

	kind := "fact"
	if before, after, ok := strings.Cut(text, ":"); ok && isMemoryKind(before) {
		kind = strings.ToLower(strings.TrimSpace(before))
		text = strings.TrimSpace(after)
	}

	if strings.HasPrefix(strings.ToLower(text), "that ") {
		text = strings.TrimSpace(text[5:])
	}

	tags := extractHashtagTags(text)
	if text == "" {
		return parsedMemory{}, fmt.Errorf("%w: memory text is required", skills.ErrInvalidArguments)
	}

	return parsedMemory{
		Text: text,
		Kind: kind,
		Tags: tags,
	}, nil
}

func isMemoryKind(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "fact", "note", "host", "service", "preference":
		return true
	default:
		return false
	}
}

func extractHashtagTags(text string) []string {
	fields := strings.Fields(text)
	tags := make([]string, 0, len(fields))
	for _, field := range fields {
		if !strings.HasPrefix(field, "#") {
			continue
		}
		tag := strings.TrimLeft(field, "#")
		tag = strings.TrimFunc(tag, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_'
		})
		tag = strings.ToLower(strings.TrimSpace(tag))
		if tag != "" {
			tags = append(tags, tag)
		}
	}
	return tags
}

func parsePositiveInt64(value string) (int64, bool) {
	cleaned := strings.TrimSpace(value)
	cleaned = strings.TrimPrefix(cleaned, "#")
	cleaned = strings.TrimSpace(cleaned)
	id, err := strconv.ParseInt(cleaned, 10, 64)
	if err != nil || id <= 0 {
		return 0, false
	}
	return id, true
}

func normalizeSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "system"
	}
	return value
}
