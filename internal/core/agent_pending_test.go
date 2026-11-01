package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"openlight/internal/auth"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/skills/notes"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
)

// pendingUI is a UI whose only interesting state is one open input flow.
type pendingUI struct {
	pending   bool
	consumed  []string
	cancelled int
}

func (p *pendingUI) HandleCallback(context.Context, telegram.IncomingMessage) error { return nil }

func (p *pendingUI) HandlePendingInput(_ context.Context, msg telegram.IncomingMessage) (bool, error) {
	if !p.pending {
		return false, nil
	}
	p.consumed = append(p.consumed, msg.Text)
	p.pending = false
	return true, nil
}

func (p *pendingUI) HasPendingInput(int64) bool { return p.pending }

func (p *pendingUI) StartSkillInput(context.Context, int64, int64, skills.Skill, map[string]string) (bool, error) {
	return false, nil
}

func (p *pendingUI) CancelPending(int64) {
	p.cancelled++
	p.pending = false
}

func (p *pendingUI) MapReplyKeyboard(string) (string, bool)                 { return "", false }
func (p *pendingUI) OpenScreen(context.Context, int64, int64, string) error { return nil }
func (p *pendingUI) SendHome(context.Context, int64) error                  { return nil }
func (p *pendingUI) IsFreeChat(int64) bool                                  { return false }
func (p *pendingUI) SetFreeChat(int64, bool)                                {}

func newPendingAgent(t *testing.T) (*Agent, *pendingUI, *fakeTransport) {
	t.Helper()

	repo, err := sqlite.New(context.Background(), filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	t.Cleanup(func() { repo.Close() })

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		nil,
		time.Second,
	)
	ui := &pendingUI{pending: true}
	agent.SetUI(ui)
	return agent, ui, transport
}

// A command typed while a prompt is open is a new intent, not an answer to the
// old question. Before this it was the prompt that won: the command text was
// filed as the answer and the command itself never ran.
func TestCommandCancelsPendingInput(t *testing.T) {
	t.Parallel()

	agent, ui, transport := newPendingAgent(t)
	if err := agent.HandleMessage(context.Background(), telegram.IncomingMessage{
		ChatID: 200, UserID: 100, Text: "/note buy milk",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if ui.cancelled != 1 {
		t.Fatalf("the open flow should have been cancelled once, got %d", ui.cancelled)
	}
	if len(ui.consumed) != 0 {
		t.Fatalf("the command must not be eaten as an answer, got %q", ui.consumed)
	}
	if len(transport.sent) != 1 || transport.sent[0] != "Saved note #1" {
		t.Fatalf("the command should have run: %#v", transport.sent)
	}
}

// Telegram clients glue @botname onto commands in groups, which is exactly
// where people get stuck behind a prompt they cannot answer.
func TestBotSuffixedCommandCancelsPendingInput(t *testing.T) {
	t.Parallel()

	agent, ui, transport := newPendingAgent(t)
	if err := agent.HandleMessage(context.Background(), telegram.IncomingMessage{
		ChatID: 200, UserID: 100, Text: "/note@openlight_bot buy milk",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if ui.cancelled != 1 || len(ui.consumed) != 0 {
		t.Fatalf("suffixed command should cancel, not answer (cancelled=%d consumed=%q)", ui.cancelled, ui.consumed)
	}
	if len(transport.sent) != 1 || transport.sent[0] != "Saved note #1" {
		t.Fatalf("the command should have run: %#v", transport.sent)
	}
}

// The flip side, and the reason the cancel checks the router rather than the
// leading slash: an absolute path is a legitimate answer to "Image path?".
func TestPathAnswerStillFeedsPendingInput(t *testing.T) {
	t.Parallel()

	for _, answer := range []string{"/home/damk/shot.png", "/tmp/out.png", "mistake with ad"} {
		agent, ui, _ := newPendingAgent(t)
		if err := agent.HandleMessage(context.Background(), telegram.IncomingMessage{
			ChatID: 200, UserID: 100, Text: answer,
		}); err != nil {
			t.Fatalf("HandleMessage(%q) returned error: %v", answer, err)
		}
		if ui.cancelled != 0 {
			t.Fatalf("%q should not cancel the flow", answer)
		}
		if len(ui.consumed) != 1 || ui.consumed[0] != answer {
			t.Fatalf("%q should have been consumed as the answer, got %q", answer, ui.consumed)
		}
	}
}

// With no flow open nothing changes: the command routes as it always did.
func TestCommandWithoutPendingInputIsUnaffected(t *testing.T) {
	t.Parallel()

	agent, ui, transport := newPendingAgent(t)
	ui.pending = false
	if err := agent.HandleMessage(context.Background(), telegram.IncomingMessage{
		ChatID: 200, UserID: 100, Text: "/note buy milk",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}
	if ui.cancelled != 0 {
		t.Fatalf("nothing to cancel, got %d calls", ui.cancelled)
	}
	if len(transport.sent) != 1 || transport.sent[0] != "Saved note #1" {
		t.Fatalf("the command should have run: %#v", transport.sent)
	}
}
