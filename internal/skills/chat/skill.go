package chat

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"openlight/internal/llm"
	"openlight/internal/models"
	"openlight/internal/skills"
)

const (
	defaultSystemPrompt     = "You are openLight. Reply in the user's language. Be brief, factual, and technically correct. If unsure, say so plainly. Do not invent facts. Prefer 1-4 short sentences. Mention Raspberry Pi only when the user asks about it or when it is clearly relevant."
	defaultHistoryLimit     = 6
	defaultHistoryChars     = 900
	defaultMaxResponseChars = 400
)

type HistoryStore interface {
	ListMessagesByChat(ctx context.Context, chatID int64, limit int) ([]models.Message, error)
}

type Options struct {
	HistoryLimit     int
	HistoryChars     int
	MaxResponseChars int
	SystemPrompt     string
}

type Skill struct {
	provider         llm.Provider
	store            HistoryStore
	historyLimit     int
	historyChars     int
	maxResponseChars int
	systemPrompt     string
}

func NewSkill(provider llm.Provider, store HistoryStore, historyLimit int) skills.Skill {
	return NewSkillWithOptions(provider, store, Options{HistoryLimit: historyLimit})
}

func NewSkillWithOptions(provider llm.Provider, store HistoryStore, options Options) skills.Skill {
	if options.HistoryLimit <= 0 {
		options.HistoryLimit = defaultHistoryLimit
	}
	if options.HistoryChars <= 0 {
		options.HistoryChars = defaultHistoryChars
	}
	if options.MaxResponseChars <= 0 {
		options.MaxResponseChars = defaultMaxResponseChars
	}
	if strings.TrimSpace(options.SystemPrompt) == "" {
		options.SystemPrompt = defaultSystemPrompt
	}

	return &Skill{
		provider:         provider,
		store:            store,
		historyLimit:     options.HistoryLimit,
		historyChars:     options.HistoryChars,
		maxResponseChars: options.MaxResponseChars,
		systemPrompt:     strings.TrimSpace(options.SystemPrompt),
	}
}

func (s *Skill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "chat",
		Group:       skills.GroupChat,
		Description: "Talk to the local LLM in free-form mode.",
		Aliases:     []string{"ask", "assistant", "llm chat"},
		Usage:       "/chat <message>",
		Examples: []string{
			"/chat explain why cpu load matters",
			"как дела у системы",
		},
	}
}

func (s *Skill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	text := strings.TrimSpace(input.Args["text"])
	if text == "" {
		text = strings.TrimSpace(input.RawText)
	}
	text = normalizeChatInput(text)
	if text == "" {
		return skills.Result{}, fmt.Errorf("%w: chat text is required", skills.ErrInvalidArguments)
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

func (s *Skill) buildMessages(ctx context.Context, chatID int64, rawText, normalizedText string) ([]llm.ChatMessage, error) {
	messages := []llm.ChatMessage{{
		Role:    "system",
		Content: s.systemPrompt,
	}}

	if shouldResetHistory(normalizedText) {
		messages = append(messages, llm.ChatMessage{
			Role:    "user",
			Content: normalizedText,
		})
		return messages, nil
	}

	history, err := s.store.ListMessagesByChat(ctx, chatID, s.historyLimit*3+6)
	if err != nil {
		return nil, err
	}

	selected := make([]llm.ChatMessage, 0, s.historyLimit)
	historyChars := 0

	// SQLite returns newest-first. Keep only the newest relevant turns and
	// cap the total history size so local models stay responsive.
	for i := 0; i < len(history) && len(selected) < s.historyLimit; i++ {
		message := history[i]

		// Skip the just-persisted raw input and add its normalized text later.
		if message.Role == models.RoleUser && message.Text == rawText {
			continue
		}

		role := chatRole(message.Role)
		if role == "" || !isChatHistoryCandidate(message) {
			continue
		}

		text := strings.TrimSpace(message.Text)
		textChars := utf8.RuneCountInString(text)
		if textChars == 0 {
			continue
		}
		if historyChars+textChars > s.historyChars {
			continue
		}

		selected = append(selected, llm.ChatMessage{
			Role:    role,
			Content: text,
		})
		historyChars += textChars
	}

	for i := len(selected) - 1; i >= 0; i-- {
		messages = append(messages, selected[i])
	}

	messages = append(messages, llm.ChatMessage{
		Role:    "user",
		Content: normalizedText,
	})

	return messages, nil
}

func chatRole(role string) string {
	switch role {
	case models.RoleUser:
		return "user"
	case models.RoleAssistant:
		return "assistant"
	default:
		return ""
	}
}

func isChatHistoryCandidate(message models.Message) bool {
	text := strings.TrimSpace(message.Text)
	if text == "" {
		return false
	}

	if message.Role == models.RoleUser {
		return !strings.HasPrefix(text, "/")
	}

	if message.Role != models.RoleAssistant {
		return false
	}

	lower := strings.ToLower(text)
	disallowedPrefixes := []string{
		"openlight is ready.",
		"use /help",
		"available skills:",
		"pong",
		"cpu usage:",
		"memory usage:",
		"disk usage:",
		"uptime:",
		"hostname:",
		"ip addresses:",
		"temperature:",
		"allowed services:",
		"service:",
		"logs for ",
		"saved note #",
		"notes:",
		"access denied",
		"skill not found",
		"invalid arguments",
		"internal error",
		"request timed out",
	}
	for _, prefix := range disallowedPrefixes {
		if strings.HasPrefix(lower, prefix) {
			return false
		}
	}

	return true
}

func trimResponse(text string, limit int) string {
	text = strings.TrimSpace(text)
	if limit <= 0 || utf8.RuneCountInString(text) <= limit {
		return text
	}

	runes := []rune(text)
	trimmed := strings.TrimSpace(string(runes[:limit]))
	lastBreak := strings.LastIndexAny(trimmed, ".!? \n")
	if lastBreak >= limit/2 {
		trimmed = strings.TrimSpace(trimmed[:lastBreak+1])
	}
	trimmed = strings.TrimRight(trimmed, " \n\t,;:-")
	if trimmed == "" {
		trimmed = strings.TrimSpace(string(runes[:limit]))
	}
	if strings.HasSuffix(trimmed, ".") || strings.HasSuffix(trimmed, "!") || strings.HasSuffix(trimmed, "?") {
		return trimmed
	}
	return trimmed + "..."
}

func normalizeChatInput(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}

	fields := strings.Fields(text)
	if len(fields) <= 1 {
		return text
	}

	switch strings.ToLower(fields[0]) {
	case "chat", "/chat", "ask", "/ask":
		return strings.TrimSpace(strings.Join(fields[1:], " "))
	default:
		return text
	}
}

func shouldResetHistory(text string) bool {
	normalized := strings.ToLower(strings.TrimSpace(text))
	normalized = strings.Trim(normalized, "!?.,:; ")

	switch normalized {
	case "hi", "hello", "hey", "привет", "здравствуй", "здравствуйте", "добрый день", "добрый вечер", "как дела", "как ты":
		return true
	default:
		return false
	}
}
