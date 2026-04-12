package core

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
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
	sent    []string
	buttons [][][]telegram.Button
}

func (f *fakeTransport) Poll(context.Context, func(context.Context, telegram.IncomingMessage) error) error {
	return nil
}

func (f *fakeTransport) SendText(_ context.Context, _ int64, text string) error {
	f.sent = append(f.sent, text)
	f.buttons = append(f.buttons, nil)
	return nil
}

func (f *fakeTransport) SendTextWithButtons(_ context.Context, _ int64, text string, buttons [][]telegram.Button) error {
	f.sent = append(f.sent, text)
	f.buttons = append(f.buttons, buttons)
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

func (stubLLMProvider) ClassifyRoute(context.Context, string, llm.RouteClassificationRequest) (llm.RouteClassification, error) {
	return llm.RouteClassification{}, nil
}

func (stubLLMProvider) ClassifySkill(context.Context, string, llm.SkillClassificationRequest) (llm.Classification, error) {
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
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "как поживаешь сегодня",
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

func TestAgentSendsButtonsWhenSkillReturnsThem(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(skills.NewStartSkill())

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

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "/start",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || !strings.Contains(transport.sent[0], "openLight is ready.") {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}
	if len(transport.buttons) != 1 || len(transport.buttons[0]) != 2 {
		t.Fatalf("expected buttons for start response, got %#v", transport.buttons)
	}
	if transport.buttons[0][0][0].CallbackData != "enable docker" || transport.buttons[0][1][0].CallbackData != "enable auto-heal" {
		t.Fatalf("unexpected start buttons: %#v", transport.buttons)
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

type integrationUserAddSkill struct{}

func (integrationUserAddSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "user_add",
		Group:       skills.GroupAccounts,
		Description: "Add a user.",
	}
}

func (integrationUserAddSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: "User added: anya (jitsi)"}, nil
}

func TestAgentRedactsSensitiveUserAddDataInPersistence(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(integrationUserAddSkill{})

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

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "/user_add jitsi anya 123456",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	defer db.Close()

	var storedMessage string
	if err := db.QueryRowContext(ctx, `SELECT text FROM messages WHERE role = 'user' ORDER BY id DESC LIMIT 1`).Scan(&storedMessage); err != nil {
		t.Fatalf("load stored message: %v", err)
	}
	if strings.Contains(storedMessage, "123456") {
		t.Fatalf("expected redacted stored message, got %q", storedMessage)
	}
	if want := "/user_add jitsi anya [redacted]"; storedMessage != want {
		t.Fatalf("unexpected stored message: %q", storedMessage)
	}

	var inputText string
	var argsJSON string
	if err := db.QueryRowContext(ctx, `SELECT input_text, args_json FROM skill_calls ORDER BY id DESC LIMIT 1`).Scan(&inputText, &argsJSON); err != nil {
		t.Fatalf("load stored skill call: %v", err)
	}
	if strings.Contains(inputText, "123456") || strings.Contains(argsJSON, "123456") {
		t.Fatalf("expected redacted skill call data, got input=%q args=%q", inputText, argsJSON)
	}
	if !strings.Contains(argsJSON, `"password":"[redacted]"`) {
		t.Fatalf("expected redacted password in args json, got %q", argsJSON)
	}
}

type clarificationClassifier struct{}

func (clarificationClassifier) Classify(context.Context, string) (router.Decision, bool, error) {
	return router.Decision{
		Mode:                  router.ModeLLM,
		Confidence:            0.71,
		NeedsClarification:    true,
		ClarificationQuestion: "Which service should I restart?",
	}, true, nil
}

func TestAgentRepliesWithClarificationWithoutExecutingSkill(t *testing.T) {
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

	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, clarificationClassifier{}),
		registry,
		repo,
		nil,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "сделай что-нибудь с этим",
	}); err != nil {
		t.Fatalf("HandleMessage returned error: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "Which service should I restart?" {
		t.Fatalf("unexpected sent messages: %#v", transport.sent)
	}
}

type pendingClarificationClassifier struct {
	inputs []string
}

func (c *pendingClarificationClassifier) Classify(_ context.Context, text string) (router.Decision, bool, error) {
	c.inputs = append(c.inputs, text)
	return router.Decision{
		Mode:                  router.ModeLLM,
		Confidence:            0.71,
		NeedsClarification:    true,
		ClarificationQuestion: "Do you want me to show overall status?",
	}, true, nil
}

type integrationStatusSkill struct{}

func (integrationStatusSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "status",
		Group:       skills.GroupSystem,
		Description: "Show overall host status.",
	}
}

func (integrationStatusSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: "status ok"}, nil
}

func TestAgentUsesPendingClarificationContextOnFollowUp(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(integrationStatusSkill{})

	classifier := &pendingClarificationClassifier{}
	transport := &fakeTransport{}
	agent := NewAgent(
		transport,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, classifier),
		registry,
		repo,
		nil,
		nil,
		time.Second,
	)

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "что там по системе",
	}); err != nil {
		t.Fatalf("HandleMessage returned error on clarification step: %v", err)
	}

	if len(transport.sent) != 1 || transport.sent[0] != "Do you want me to show overall status?" {
		t.Fatalf("unexpected clarification sent messages: %#v", transport.sent)
	}

	setting, ok, err := repo.GetSetting(ctx, pendingClarificationKey(200))
	if err != nil {
		t.Fatalf("GetSetting returned error: %v", err)
	}
	if !ok || strings.TrimSpace(setting.Value) == "" {
		t.Fatalf("expected pending clarification to be stored, got ok=%v setting=%#v", ok, setting)
	}

	if err := agent.HandleMessage(ctx, telegram.IncomingMessage{
		ChatID: 200,
		UserID: 100,
		Text:   "да, сделай",
	}); err != nil {
		t.Fatalf("HandleMessage returned error on follow-up step: %v", err)
	}

	if len(transport.sent) != 2 || transport.sent[1] != "status ok" {
		t.Fatalf("unexpected sent messages after follow-up: %#v", transport.sent)
	}

	if len(classifier.inputs) != 2 {
		t.Fatalf("expected raw classifier calls only, got %#v", classifier.inputs)
	}

	setting, ok, err = repo.GetSetting(ctx, pendingClarificationKey(200))
	if err != nil {
		t.Fatalf("GetSetting returned error after follow-up: %v", err)
	}
	if !ok {
		t.Fatalf("expected pending clarification setting row to remain present")
	}
	if strings.TrimSpace(setting.Value) != "" {
		t.Fatalf("expected pending clarification to be cleared, got %#v", setting)
	}
}
