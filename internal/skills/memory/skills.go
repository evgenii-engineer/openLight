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

// LongTerm is the optional bridge into the automatic long-term memory
// subsystem. Declared here so this package keeps no dependency on
// internal/memory; the runtime adapts between them.
//
// It exists because a slash command is the one input path the LLM router
// cannot misclassify: /remember and its aliases resolve deterministically,
// before the classifier runs. That makes this the reliable way to put
// something into memory when the fast model is guessing badly.
type LongTerm interface {
	// RememberText archives free text, indexes it, and queues structured
	// fact extraction.
	RememberText(ctx context.Context, text, source string, chatID, userID int64) error

	// RememberFact writes a structured fact directly, with no model
	// involved — so it works with the brain node asleep.
	RememberFact(ctx context.Context, subject, predicate, value string) error
}

type rememberSkill struct {
	store    Store
	longTerm LongTerm
	enabled  bool
}

func NewRememberSkill(store Store, enabled bool) skills.Skill {
	return &rememberSkill{store: store, enabled: enabled}
}

// NewRememberSkillWithLongTerm additionally mirrors what is remembered
// into the long-term memory subsystem. Nil longTerm behaves exactly like
// NewRememberSkill.
func NewRememberSkillWithLongTerm(store Store, longTerm LongTerm, enabled bool) skills.Skill {
	return &rememberSkill{store: store, longTerm: longTerm, enabled: enabled}
}

func (s *rememberSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "memory_add",
		Group:       skills.GroupMemory,
		Description: "Remember a durable fact, note, or preference.",
		// Russian aliases matter here: the first word of a message is
		// resolved against this list before the LLM classifier runs, so
		// "запомни ..." is the one phrasing that cannot be misrouted.
		Aliases: []string{"remember", "memory add", "запомни", "запомнить", "помни"},
		Usage:   "/remember <text>   ·   /remember <subject>.<predicate> = <value>",
		Examples: []string{
			"запомни у raspberry теперь SSD на 1 ТБ",
			"remember that my Mac mini is the main inference node",
			"/remember raspberry.storage = 4 TB SSD",
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

	raw := strings.TrimSpace(input.Args["text"])
	if raw == "" {
		raw = strings.TrimSpace(input.RawText)
	}

	// Explicit "subject.predicate = value" writes a structured fact with
	// no model in the loop, so it lands immediately and works while the
	// brain node is asleep.
	if subject, predicate, value, ok := parseStructuredFact(raw); ok {
		if s.longTerm == nil {
			return skills.Result{}, skills.NewUserError(skills.ErrUnavailable,
				"long-term memory is disabled; drop the \"=\" to save a plain note")
		}
		if err := s.longTerm.RememberFact(ctx, subject, predicate, value); err != nil {
			return skills.Result{}, err
		}
		return skills.Result{Text: fmt.Sprintf("Запомнил факт: %s %s = %s", subject, predicate, value)}, nil
	}

	parsed, err := parseMemoryText(raw)
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

	if s.longTerm == nil {
		return skills.Result{Text: fmt.Sprintf("Saved memory #%d", memory.ID)}, nil
	}

	// Archiving is synchronous so the confirmation is truthful; fact
	// extraction is queued and catches up whenever the brain is awake.
	if err := s.longTerm.RememberText(ctx, parsed.Text, normalizeSource(input.Source), input.ChatID, input.UserID); err != nil {
		return skills.Result{
			Text: fmt.Sprintf("Saved memory #%d (в долговременную память не попало: %v)", memory.ID, err),
		}, nil
	}
	return skills.Result{
		Text: fmt.Sprintf("Запомнил #%d. Проиндексирую и разберу на факты в фоне.", memory.ID),
	}, nil
}

// parseStructuredFact recognises "subject.predicate = value".
//
// The dot is required: without it there is no way to tell a subject from
// a predicate, and guessing would produce junk keys that never supersede
// each other. Plain text without a dot goes the normal route, where the
// smart model does the structuring properly.
func parseStructuredFact(raw string) (subject, predicate, value string, ok bool) {
	left, right, found := strings.Cut(raw, "=")
	if !found {
		return "", "", "", false
	}
	value = strings.TrimSpace(right)
	key := strings.TrimSpace(left)
	if value == "" || key == "" || strings.ContainsAny(key, " \t") && !strings.Contains(key, ".") {
		return "", "", "", false
	}

	dot := strings.LastIndex(key, ".")
	if dot <= 0 || dot == len(key)-1 {
		return "", "", "", false
	}
	subject = strings.TrimSpace(key[:dot])
	predicate = strings.TrimSpace(key[dot+1:])
	if subject == "" || predicate == "" {
		return "", "", "", false
	}
	return subject, predicate, value, true
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
