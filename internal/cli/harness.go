package cli

import (
	"context"
	"errors"
	"strings"
	"time"

	"openlight/internal/app"
	"openlight/internal/auth"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/telegram"
)

type Harness struct {
	agent     *core.Agent
	registry  *skills.Registry
	transport *captureTransport
	userID    int64
	chatID    int64
	nextID    int64
	timeout   time.Duration
}

func NewHarness(cfg config.Config, runtime app.Runtime, userID, chatID int64) *Harness {
	transport := &captureTransport{}
	if runtime.Watch != nil {
		runtime.Watch.SetNotifier(transport)
	}
	agent := core.NewAgent(
		transport,
		auth.New(cfg.Auth.AllowedUserIDs, cfg.Auth.AllowedChatIDs),
		router.New(runtime.Registry, runtime.Classifier),
		runtime.Registry,
		runtime.Repository,
		runtime.Watch,
		nil,
		cfg.Agent.RequestTimeout,
	)
	return &Harness{
		agent:     agent,
		registry:  runtime.Registry,
		transport: transport,
		userID:    userID,
		chatID:    chatID,
		nextID:    1,
		timeout:   cfg.Agent.RequestTimeout,
	}
}

func (h *Harness) Exec(ctx context.Context, text string) (string, error) {
	h.transport.reset()
	err := h.agent.HandleMessage(ctx, telegram.IncomingMessage{
		MessageID: h.nextID,
		ChatID:    h.chatID,
		UserID:    h.userID,
		Text:      text,
	})
	h.nextID++
	return strings.TrimSpace(h.transport.output()), err
}

func (h *Harness) ExecDecision(ctx context.Context, text string, decision router.Decision) (string, error) {
	if !decision.Matched() {
		return "skill not found", nil
	}

	skill, ok := h.registry.Get(decision.SkillName)
	if !ok {
		return "skill not found", nil
	}

	execCtx := ctx
	cancel := func() {}
	if h.timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, h.timeout)
	}
	defer cancel()

	result, err := skill.Execute(execCtx, skills.Input{
		RawText: text,
		Args:    decision.Args,
		UserID:  h.userID,
		ChatID:  h.chatID,
	})
	if err != nil {
		return harnessUserMessageForError(err), nil
	}

	return strings.TrimSpace(result.Text), nil
}

func harnessUserMessageForError(err error) string {
	switch {
	case errors.Is(err, skills.ErrInvalidArguments):
		return "invalid arguments"
	case errors.Is(err, skills.ErrSkillNotFound):
		return "skill not found"
	case errors.Is(err, skills.ErrNotFound):
		return "not found"
	case errors.Is(err, skills.ErrAccessDenied):
		return "access denied"
	case errors.Is(err, skills.ErrUnavailable):
		return "unavailable"
	case errors.Is(err, context.DeadlineExceeded):
		return "request timed out"
	default:
		return "internal error"
	}
}

type captureTransport struct {
	replies []string
	buttons [][][]telegram.Button
}

func (t *captureTransport) Poll(context.Context, func(context.Context, telegram.IncomingMessage) error) error {
	return nil
}

func (t *captureTransport) SendText(_ context.Context, _ int64, text string) error {
	t.replies = append(t.replies, text)
	t.buttons = append(t.buttons, nil)
	return nil
}

func (t *captureTransport) SendTextWithButtons(_ context.Context, _ int64, text string, buttons [][]telegram.Button) error {
	t.replies = append(t.replies, text)
	t.buttons = append(t.buttons, buttons)
	return nil
}

func (t *captureTransport) reset() {
	t.replies = nil
	t.buttons = nil
}

func (t *captureTransport) output() string {
	return strings.TrimSpace(strings.Join(t.replies, "\n"))
}
