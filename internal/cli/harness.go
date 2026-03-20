package cli

import (
	"context"
	"strings"

	"openlight/internal/app"
	"openlight/internal/auth"
	"openlight/internal/config"
	"openlight/internal/core"
	"openlight/internal/router"
	"openlight/internal/telegram"
)

type Harness struct {
	agent     *core.Agent
	transport *captureTransport
	userID    int64
	chatID    int64
	nextID    int64
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
		transport: transport,
		userID:    userID,
		chatID:    chatID,
		nextID:    1,
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

type captureTransport struct {
	replies []string
}

func (t *captureTransport) Poll(context.Context, func(context.Context, telegram.IncomingMessage) error) error {
	return nil
}

func (t *captureTransport) SendText(_ context.Context, _ int64, text string) error {
	t.replies = append(t.replies, text)
	return nil
}

func (t *captureTransport) reset() {
	t.replies = nil
}

func (t *captureTransport) output() string {
	return strings.TrimSpace(strings.Join(t.replies, "\n"))
}
