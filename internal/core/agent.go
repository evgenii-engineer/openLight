package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"openlight/internal/auth"
	"openlight/internal/models"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/storage"
	"openlight/internal/telegram"
)

type Transport interface {
	Poll(ctx context.Context, handler func(context.Context, telegram.IncomingMessage) error) error
	SendText(ctx context.Context, chatID int64, text string) error
}

type Agent struct {
	transport      Transport
	authorizer     *auth.Authorizer
	router         *router.Router
	registry       *skills.Registry
	repository     storage.Repository
	logger         *slog.Logger
	requestTimeout time.Duration
}

const maxLoggedMessageChars = 160

func NewAgent(
	transport Transport,
	authorizer *auth.Authorizer,
	router *router.Router,
	registry *skills.Registry,
	repository storage.Repository,
	logger *slog.Logger,
	requestTimeout time.Duration,
) *Agent {
	return &Agent{
		transport:      transport,
		authorizer:     authorizer,
		router:         router,
		registry:       registry,
		repository:     repository,
		logger:         logger,
		requestTimeout: requestTimeout,
	}
}

func (a *Agent) Run(ctx context.Context) error {
	return a.transport.Poll(ctx, a.HandleMessage)
}

func (a *Agent) HandleMessage(ctx context.Context, message telegram.IncomingMessage) error {
	if strings.TrimSpace(message.Text) == "" {
		return nil
	}

	a.saveMessage(ctx, models.Message{
		TelegramUserID: message.UserID,
		TelegramChatID: message.ChatID,
		Role:           models.RoleUser,
		Text:           message.Text,
	})

	if err := a.authorizer.Error(message.UserID, message.ChatID); err != nil {
		a.logWarn("blocked unauthorized message", "error", err)
		return a.reply(ctx, message.ChatID, message.UserID, "access denied")
	}

	decision, err := a.router.Route(ctx, message.Text)
	if err != nil {
		a.logError("route message", "error", err)
		return a.reply(ctx, message.ChatID, message.UserID, "internal error")
	}

	a.logRouteDecision("router decision", message, decision)

	if decision.ShouldClarify() {
		a.logInfo(
			"requesting clarification",
			"message_text", shortTextForLog(message.Text),
			"mode", decision.Mode,
			"skill", decision.SkillName,
			"args", decision.Args,
			"confidence", decision.Confidence,
			"question", decision.ClarificationQuestion,
		)
		return a.reply(ctx, message.ChatID, message.UserID, decision.ClarificationQuestion)
	}

	if !decision.Matched() {
		if !strings.HasPrefix(strings.TrimSpace(message.Text), "/") {
			if _, ok := a.registry.Get("chat"); ok {
				decision = router.Decision{
					Mode:      router.ModeUnknown,
					SkillName: "chat",
					Args:      map[string]string{"text": message.Text},
				}
				a.logRouteDecision("router chat fallback", message, decision)
			}
		}
	}

	if !decision.Matched() {
		a.logWarn(
			"router did not match any skill",
			"message_text", shortTextForLog(message.Text),
			"chat_id", message.ChatID,
			"user_id", message.UserID,
		)
		return a.reply(ctx, message.ChatID, message.UserID, "skill not found. Try /help or /skills.")
	}

	skill, ok := a.registry.Get(decision.SkillName)
	if !ok {
		a.logWarn("router returned unknown skill", "skill", decision.SkillName)
		return a.reply(ctx, message.ChatID, message.UserID, "skill not found")
	}

	startedAt := time.Now()
	execCtx, cancel := context.WithTimeout(ctx, a.requestTimeout)
	defer cancel()

	a.logDebug(
		"skill execution started",
		"skill", skill.Definition().Name,
		"mode", decision.Mode,
		"args", decision.Args,
		"message_text", shortTextForLog(message.Text),
		"chat_id", message.ChatID,
		"user_id", message.UserID,
	)

	result, execErr := skill.Execute(execCtx, skills.Input{
		RawText: message.Text,
		Args:    decision.Args,
		UserID:  message.UserID,
		ChatID:  message.ChatID,
	})

	durationMS := time.Since(startedAt).Milliseconds()

	a.saveSkillCall(ctx, models.SkillCall{
		SkillName:  skill.Definition().Name,
		InputText:  message.Text,
		ArgsJSON:   marshalArgs(decision.Args),
		Status:     callStatus(execErr),
		ErrorText:  errorText(execErr),
		DurationMS: durationMS,
	})

	if execErr != nil {
		a.logError(
			"execute skill",
			"skill", skill.Definition().Name,
			"mode", decision.Mode,
			"args", decision.Args,
			"duration_ms", durationMS,
			"error", execErr,
		)
		return a.reply(ctx, message.ChatID, message.UserID, userMessageForError(execErr))
	}

	a.logDebug(
		"skill execution completed",
		"skill", skill.Definition().Name,
		"mode", decision.Mode,
		"args", decision.Args,
		"duration_ms", durationMS,
	)

	return a.reply(ctx, message.ChatID, message.UserID, result.Text)
}

func (a *Agent) reply(ctx context.Context, chatID, userID int64, text string) error {
	if err := a.transport.SendText(ctx, chatID, text); err != nil {
		return fmt.Errorf("send reply: %w", err)
	}

	a.saveMessage(ctx, models.Message{
		TelegramUserID: userID,
		TelegramChatID: chatID,
		Role:           models.RoleAssistant,
		Text:           text,
	})

	return nil
}

func (a *Agent) saveMessage(ctx context.Context, message models.Message) {
	if err := a.repository.SaveMessage(ctx, message); err != nil {
		a.logError("save message", "error", err)
	}
}

func (a *Agent) saveSkillCall(ctx context.Context, call models.SkillCall) {
	if err := a.repository.SaveSkillCall(ctx, call); err != nil {
		a.logError("save skill call", "error", err)
	}
}

func (a *Agent) logWarn(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Warn(msg, args...)
	}
}

func (a *Agent) logInfo(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Info(msg, args...)
	}
}

func (a *Agent) logDebug(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Debug(msg, args...)
	}
}

func (a *Agent) logError(msg string, args ...any) {
	if a.logger != nil {
		a.logger.Error(msg, args...)
	}
}

func (a *Agent) logRouteDecision(msg string, message telegram.IncomingMessage, decision router.Decision) {
	a.logDebug(
		msg,
		"message_text", shortTextForLog(message.Text),
		"chat_id", message.ChatID,
		"user_id", message.UserID,
		"mode", decision.Mode,
		"skill", decision.SkillName,
		"args", decision.Args,
		"confidence", decision.Confidence,
		"needs_clarification", decision.NeedsClarification,
		"clarification_question", decision.ClarificationQuestion,
	)
}

func marshalArgs(args map[string]string) string {
	payload, err := json.Marshal(args)
	if err != nil {
		return "{}"
	}
	return string(payload)
}

func callStatus(err error) string {
	if err != nil {
		return models.SkillCallFailed
	}
	return models.SkillCallSuccess
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func userMessageForError(err error) string {
	switch {
	case errors.Is(err, skills.ErrInvalidArguments):
		return "invalid arguments"
	case errors.Is(err, skills.ErrSkillNotFound):
		return "skill not found"
	case errors.Is(err, skills.ErrNotFound):
		return "not found"
	case errors.Is(err, skills.ErrAccessDenied):
		return "service not allowed"
	case errors.Is(err, skills.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	default:
		return "internal error"
	}
}

func shortTextForLog(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}

	runes := []rune(value)
	if len(runes) <= maxLoggedMessageChars {
		return value
	}

	return strings.TrimSpace(string(runes[:maxLoggedMessageChars])) + fmt.Sprintf("...(%d chars)", utf8.RuneCountInString(value))
}
