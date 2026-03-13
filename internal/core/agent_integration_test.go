package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"openlight/internal/auth"
	"openlight/internal/llm"
	"openlight/internal/router"
	"openlight/internal/skills"
	chatskills "openlight/internal/skills/chat"
	"openlight/internal/skills/notes"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
)

type fakeTransport struct {
	sent []string
}

func (f *fakeTransport) Poll(context.Context, func(context.Context, telegram.IncomingMessage) error) error {
	return nil
}

func (f *fakeTransport) SendText(_ context.Context, _ int64, text string) error {
	f.sent = append(f.sent, text)
	return nil
}

func TestAgentHandleMessagePersistsConversationAndNotes(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(skills.NewHelpSkill(registry))

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "/note buy milk",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "Saved note #1" {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}

	notesList, err := repo.ListNotes(ctx, 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notesList) != 1 || notesList[0].Text != "buy milk" {
		t.Fatalf("unexpected notes: %#v", notesList)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	defer db.Close()

	var messageCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages`).Scan(&messageCount); err != nil {
		t.Fatalf("count messages: %v", err)
	}
	if messageCount != 2 {
		t.Fatalf("expected 2 messages to be persisted, got %d", messageCount)
	}

	var skillCallCount int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM skill_calls`).Scan(&skillCallCount); err != nil {
		t.Fatalf("count skill calls: %v", err)
	}
	if skillCallCount != 1 {
		t.Fatalf("expected 1 skill call to be persisted, got %d", skillCallCount)
	}
}

type stubLLMProvider struct{}

func (stubLLMProvider) ClassifyIntent(context.Context, string, []string) (llm.Classification, error) {
	return llm.Classification{}, nil
}

func (stubLLMProvider) Summarize(context.Context, string) (string, error) {
	return "", nil
}

func (stubLLMProvider) Chat(context.Context, []llm.ChatMessage) (string, error) {
	return "llm fallback answer", nil
}

func TestAgentFallsBackToChatSkillForUnknownText(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(chatskills.NewSkill(stubLLMProvider{}, repo, 8))

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "привет, расскажи про память",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "llm fallback answer" {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}
}

func TestAgentHandleExplicitNoteAddWithoutSlash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(notes.NewListSkill(repo, 10))

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "note_add привет",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "Saved note #1" {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}

	notesList, err := repo.ListNotes(ctx, 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notesList) != 1 || notesList[0].Text != "привет" {
		t.Fatalf("unexpected notes: %#v", notesList)
	}
}

func TestAgentHandleExplicitNoteDeleteWithoutSlash(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	if _, err := repo.AddNote(ctx, "привет"); err != nil {
		t.Fatalf("AddNote returned error: %v", err)
	}

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(notes.NewListSkill(repo, 10))
	registry.MustRegister(notes.NewDeleteSkill(repo))

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "note_delete 1",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "Deleted note #1" {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}

	notesList, err := repo.ListNotes(ctx, 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notesList) != 0 {
		t.Fatalf("expected notes to be empty after delete, got %#v", notesList)
	}
}
