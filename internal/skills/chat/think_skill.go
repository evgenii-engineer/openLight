package chat

import (
	"context"
	"fmt"
	"strings"

	"openlight/internal/llm"
	"openlight/internal/skills"
)

const defaultThinkSystemPrompt = "You are openLight in deep reasoning mode. Think carefully and thoroughly. Reply in the user's language. Provide complete, detailed, technically correct answers. For code tasks include working code. For architecture tasks include tradeoffs."

// ThinkSkill uses the deep LLM provider (think=true) for reasoning-intensive
// tasks: coding, planning, architecture, debugging, code review.
type ThinkSkill struct {
	provider         llm.Provider
	store            HistoryStore
	historyLimit     int
	historyChars     int
	maxResponseChars int
	systemPrompt     string
}

func NewThinkSkill(provider llm.Provider, store HistoryStore, opts Options) skills.Skill {
	if opts.HistoryLimit <= 0 {
		opts.HistoryLimit = defaultHistoryLimit
	}
	if opts.HistoryChars <= 0 {
		opts.HistoryChars = defaultHistoryChars
	}
	if opts.MaxResponseChars <= 0 {
		opts.MaxResponseChars = 2000
	}
	if strings.TrimSpace(opts.SystemPrompt) == "" {
		opts.SystemPrompt = defaultThinkSystemPrompt
	}
	return &ThinkSkill{
		provider:         provider,
		store:            store,
		historyLimit:     opts.HistoryLimit,
		historyChars:     opts.HistoryChars,
		maxResponseChars: opts.MaxResponseChars,
		systemPrompt:     strings.TrimSpace(opts.SystemPrompt),
	}
}

func (s *ThinkSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "think",
		Group:       skills.GroupChat,
		Description: "Deep reasoning with the local LLM (think=true). Use for coding, architecture, planning, debugging, or code review.",
		Aliases:     []string{"deep", "reason", "analyze"},
		Usage:       "/think <question or task>",
		Examples: []string{
			"/think напиши функцию на Go для парсинга JWT без библиотек",
			"/think как лучше организовать кэширование в этом проекте",
			"/think проверь этот код на ошибки: func foo() { ... }",
		},
	}
}

func (s *ThinkSkill) UI() skills.UIDescriptor {
	return skills.UIDescriptor{
		Inputs: []skills.InputField{
			{Name: "text", Prompt: "What do you want to think about?", Placeholder: "write a Go function that..."},
		},
	}
}

func (s *ThinkSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	text := strings.TrimSpace(input.Args["text"])
	if text == "" {
		text = strings.TrimSpace(input.RawText)
	}
	text = normalizeThinkInput(text)
	if text == "" {
		return skills.Result{}, fmt.Errorf("%w: think text is required", skills.ErrInvalidArguments)
	}

	if s.provider == nil {
		return skills.Result{}, fmt.Errorf("%w: deep LLM profile is not configured", skills.ErrUnavailable)
	}

	messages, err := s.buildMessages(ctx, input.ChatID, input.RawText, text)
	if err != nil {
		return skills.Result{}, err
	}

	response, err := s.provider.Chat(ctx, messages)
	if err != nil {
		return skills.Result{}, err
	}
	if strings.TrimSpace(response) == "" {
		return skills.Result{}, fmt.Errorf("%w: empty llm response", skills.ErrUnavailable)
	}

	return skills.Result{Text: trimResponse(response, s.maxResponseChars)}, nil
}

func (s *ThinkSkill) buildMessages(ctx context.Context, chatID int64, rawText, normalizedText string) ([]llm.ChatMessage, error) {
	messages := []llm.ChatMessage{{
		Role:    "system",
		Content: s.systemPrompt,
	}}

	history, err := s.store.ListMessagesByChat(ctx, chatID, s.historyLimit*3+6)
	if err != nil {
		return nil, err
	}

	selected := make([]llm.ChatMessage, 0, s.historyLimit)
	historyChars := 0
	for i := 0; i < len(history) && len(selected) < s.historyLimit; i++ {
		msg := history[i]
		if msg.Role == "user" && msg.Text == rawText {
			continue
		}
		role := chatRole(msg.Role)
		if role == "" || !isChatHistoryCandidate(msg) {
			continue
		}
		text := strings.TrimSpace(msg.Text)
		chars := len([]rune(text))
		if chars == 0 || historyChars+chars > s.historyChars {
			continue
		}
		selected = append(selected, llm.ChatMessage{Role: role, Content: text})
		historyChars += chars
	}
	for i := len(selected) - 1; i >= 0; i-- {
		messages = append(messages, selected[i])
	}
	messages = append(messages, llm.ChatMessage{Role: "user", Content: normalizedText})
	return messages, nil
}

func normalizeThinkInput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return text
	}
	switch strings.ToLower(fields[0]) {
	case "think", "/think", "deep", "/deep", "reason", "/reason", "analyze", "/analyze":
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	default:
		return text
	}
}
