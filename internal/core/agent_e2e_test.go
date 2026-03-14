package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"openlight/internal/auth"
	basellm "openlight/internal/llm"
	"openlight/internal/router"
	routerllm "openlight/internal/router/llm"
	"openlight/internal/skills"
	chatskills "openlight/internal/skills/chat"
	"openlight/internal/skills/notes"
	systemskills "openlight/internal/skills/system"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
	"openlight/internal/telegram"
)

const (
	e2eOllamaEnabledEnv  = "OPENLIGHT_E2E_OLLAMA"
	e2eOllamaEndpointEnv = "OPENLIGHT_E2E_OLLAMA_ENDPOINT"
	e2eOllamaModelEnv    = "OPENLIGHT_E2E_OLLAMA_MODEL"
)

func TestAgentRunPollingEndToEndDeterministicNoteAdd(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	telegramAPI := newFakeTelegramAPI(t, "TOKEN", []fakeTelegramUpdate{
		{ID: 1, ChatID: 200, UserID: 100, Text: "/note buy milk"},
	})
	defer telegramAPI.Close()

	registry := skills.NewRegistry()
	registry.MustRegister(notes.NewAddSkill(repo))
	registry.MustRegister(notes.NewListSkill(repo, 10))
	registry.MustRegister(notes.NewDeleteSkill(repo))
	registry.MustRegister(skills.NewSkillsSkill(registry))
	registry.MustRegister(skills.NewHelpSkill(registry))

	bot := telegram.NewBot(telegram.Options{
		Token:       "TOKEN",
		BaseURL:     telegramAPI.URL(),
		Mode:        "polling",
		PollTimeout: 100 * time.Millisecond,
	})

	agent := NewAgent(
		bot,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, nil),
		registry,
		repo,
		nil,
		2*time.Second,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	reply := telegramAPI.waitForSentText(t)
	waitForRowCount(t, dbPath, "messages", 2, 3*time.Second)
	waitForRowCount(t, dbPath, "skill_calls", 1, 3*time.Second)
	cancel()

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("agent.Run returned error: %v", err)
	}

	if reply != "Saved note #1" {
		t.Fatalf("unexpected reply: %q", reply)
	}

	notesList, err := repo.ListNotes(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notesList) != 1 || notesList[0].Text != "buy milk" {
		t.Fatalf("unexpected notes: %#v", notesList)
	}

	assertRowCount(t, dbPath, "messages", 2)
	assertRowCount(t, dbPath, "skill_calls", 1)
}

func TestAgentRunPollingEndToEndWithRealOllamaResponds(t *testing.T) {
	t.Parallel()

	if testing.Short() {
		t.Skip("skipping real Ollama E2E in short mode")
	}

	endpoint, model := requireRealOllamaE2E(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	defer repo.Close()

	telegramAPI := newFakeTelegramAPI(t, "TOKEN", []fakeTelegramUpdate{
		{ID: 1, ChatID: 200, UserID: 100, Text: "просто коротко поздоровайся со мной по-русски"},
	})
	defer telegramAPI.Close()

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	provider := basellm.NewOllamaProvider(endpoint, model, 30*time.Second, logger.With("component", "llm"))
	registry := skills.NewRegistry()
	registry.MustRegister(chatskills.NewSkillWithOptions(provider, repo, chatskills.Options{
		HistoryLimit:     6,
		HistoryChars:     600,
		MaxResponseChars: 400,
	}))
	registry.MustRegister(skills.NewSkillsSkill(registry))
	registry.MustRegister(skills.NewHelpSkill(registry))

	classifier := routerllm.NewClassifier(provider, registry, routerllm.Options{
		ExecuteThreshold:         0.80,
		MutatingExecuteThreshold: 0.95,
		ClarifyThreshold:         0.60,
		InputChars:               160,
		NumPredict:               128,
	}, nil)

	bot := telegram.NewBot(telegram.Options{
		Token:       "TOKEN",
		BaseURL:     telegramAPI.URL(),
		Mode:        "polling",
		PollTimeout: 100 * time.Millisecond,
	})

	agent := NewAgent(
		bot,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, classifier),
		registry,
		repo,
		logger.With("component", "agent"),
		45*time.Second,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	reply := telegramAPI.waitForSentText(t)
	waitForRowCount(t, dbPath, "messages", 2, 5*time.Second)
	cancel()

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("agent.Run returned error: %v", err)
	}

	reply = strings.TrimSpace(reply)
	if reply == "" {
		t.Fatal("expected a non-empty reply from real Ollama")
	}
	t.Logf("real Ollama reply: %q", reply)
	if isFrameworkErrorReply(reply) {
		t.Fatalf("expected model reply, got framework error text: %q", reply)
	}

	assertRowCount(t, dbPath, "messages", 2)
}

func TestAgentRunPollingEndToEndWithRealOllamaNoteAdd(t *testing.T) {
	t.Parallel()

	reply, repo, dbPath := runRealOllamaSkillScenario(t, "добавь заметку купить ssd",
		func(_ basellm.Provider, repo storage.Repository) *skills.Registry {
			registry := skills.NewRegistry()
			registry.MustRegister(notes.NewAddSkill(repo))
			return registry
		},
		routerllm.Options{
			ExecuteThreshold:         0.80,
			MutatingExecuteThreshold: 0.90,
			ClarifyThreshold:         0.60,
			InputChars:               160,
			NumPredict:               128,
		},
	)

	if reply != "Saved note #1" {
		t.Fatalf("unexpected note_add reply: %q", reply)
	}

	notesList, err := repo.ListNotes(context.Background(), 10)
	if err != nil {
		t.Fatalf("ListNotes returned error: %v", err)
	}
	if len(notesList) != 1 || notesList[0].Text != "купить ssd" {
		t.Fatalf("unexpected notes: %#v", notesList)
	}

	assertRowCount(t, dbPath, "messages", 2)
	assertRowCount(t, dbPath, "skill_calls", 1)
}

func TestAgentRunPollingEndToEndWithRealOllamaStatus(t *testing.T) {
	t.Parallel()

	reply, _, dbPath := runRealOllamaSkillScenario(t, "покажи общий статус",
		func(_ basellm.Provider, _ storage.Repository) *skills.Registry {
			registry := skills.NewRegistry()
			registry.MustRegister(systemskills.NewStatusSkill(stubSystemProvider{}))
			return registry
		},
		routerllm.Options{
			ExecuteThreshold:         0.80,
			MutatingExecuteThreshold: 0.95,
			ClarifyThreshold:         0.60,
			InputChars:               160,
			NumPredict:               128,
		},
	)

	for _, expected := range []string{
		"Hostname: raspberry",
		"CPU: 12.5%",
		"Memory: 3.0 GiB used / 8.0 GiB total",
		"Disk: 16.0 GiB free / 64.0 GiB total",
		"Uptime: 1d 2h 3m 4s",
		"Temperature: 58.5C",
	} {
		if !strings.Contains(reply, expected) {
			t.Fatalf("expected status reply to contain %q, got %q", expected, reply)
		}
	}

	assertRowCount(t, dbPath, "messages", 2)
	assertRowCount(t, dbPath, "skill_calls", 1)
}

func TestAgentRunPollingEndToEndWithRealOllamaMemory(t *testing.T) {
	t.Parallel()

	reply, _, dbPath := runRealOllamaSkillScenario(t, "мемори",
		func(_ basellm.Provider, _ storage.Repository) *skills.Registry {
			registry := skills.NewRegistry()
			registry.MustRegister(systemskills.NewMemorySkill(stubSystemProvider{}))
			return registry
		},
		routerllm.Options{
			ExecuteThreshold:         0.80,
			MutatingExecuteThreshold: 0.95,
			ClarifyThreshold:         0.60,
			InputChars:               160,
			NumPredict:               128,
		},
	)

	expected := "Memory usage: 3.0 GiB used / 8.0 GiB total (5.0 GiB free)"
	if reply != expected {
		t.Fatalf("unexpected memory reply: %q", reply)
	}

	assertRowCount(t, dbPath, "messages", 2)
	assertRowCount(t, dbPath, "skill_calls", 1)
}

func runRealOllamaSkillScenario(t *testing.T, input string, buildRegistry func(basellm.Provider, storage.Repository) *skills.Registry, options routerllm.Options) (string, storage.Repository, string) {
	t.Helper()

	if testing.Short() {
		t.Skip("skipping real Ollama E2E in short mode")
	}

	endpoint, model := requireRealOllamaE2E(t)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dbPath := filepath.Join(t.TempDir(), "agent.db")
	repo, err := sqlite.New(ctx, dbPath, nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	telegramAPI := newFakeTelegramAPI(t, "TOKEN", []fakeTelegramUpdate{
		{ID: 1, ChatID: 200, UserID: 100, Text: input},
	})
	t.Cleanup(telegramAPI.Close)

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	provider := basellm.NewOllamaProvider(endpoint, model, 30*time.Second, logger.With("component", "llm"))
	registry := buildRegistry(provider, repo)

	classifier := routerllm.NewClassifier(provider, registry, options, logger.With("component", "router-llm"))

	bot := telegram.NewBot(telegram.Options{
		Token:       "TOKEN",
		BaseURL:     telegramAPI.URL(),
		Mode:        "polling",
		PollTimeout: 100 * time.Millisecond,
	})

	agent := NewAgent(
		bot,
		auth.New([]int64{100}, []int64{200}),
		router.New(registry, classifier),
		registry,
		repo,
		logger.With("component", "agent"),
		45*time.Second,
	)

	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx)
	}()

	reply := strings.TrimSpace(telegramAPI.waitForSentText(t))
	waitForRowCount(t, dbPath, "messages", 2, 5*time.Second)
	waitForRowCount(t, dbPath, "skill_calls", 1, 5*time.Second)
	cancel()

	err = <-errCh
	if err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("agent.Run returned error: %v", err)
	}

	if reply == "" {
		t.Fatal("expected a non-empty agent reply")
	}
	if isFrameworkErrorReply(reply) {
		t.Fatalf("expected skill reply, got framework error text: %q", reply)
	}

	return reply, repo, dbPath
}

func requireRealOllamaE2E(t *testing.T) (string, string) {
	t.Helper()

	if !parseBoolEnv(os.Getenv(e2eOllamaEnabledEnv)) {
		t.Skipf("set %s=1 to run the real Ollama E2E smoke test", e2eOllamaEnabledEnv)
	}

	endpoint := strings.TrimSpace(os.Getenv(e2eOllamaEndpointEnv))
	if endpoint == "" {
		endpoint = "http://127.0.0.1:11434"
	}

	model := strings.TrimSpace(os.Getenv(e2eOllamaModelEnv))
	if model == "" {
		model = "qwen2.5:0.5b"
	}

	client := &http.Client{Timeout: 3 * time.Second}
	response, err := client.Get(strings.TrimRight(endpoint, "/") + "/api/tags")
	if err != nil {
		t.Fatalf("ollama preflight failed for %s: %v", endpoint, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= http.StatusBadRequest {
		t.Fatalf("ollama preflight returned status %d for %s", response.StatusCode, endpoint)
	}

	return endpoint, model
}

func parseBoolEnv(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func isFrameworkErrorReply(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if lower == "" {
		return true
	}

	for _, disallowed := range []string{
		"access denied",
		"internal error",
		"invalid arguments",
		"request timed out",
		"skill not found",
	} {
		if strings.Contains(lower, disallowed) {
			return true
		}
	}

	return false
}

type stubSystemProvider struct{}

func (stubSystemProvider) CPUUsage(context.Context) (float64, error) {
	return 12.5, nil
}

func (stubSystemProvider) MemoryStats(context.Context) (systemskills.MemoryStats, error) {
	const gib = 1024 * 1024 * 1024
	return systemskills.MemoryStats{
		Total:     8 * gib,
		Available: 5 * gib,
		Used:      3 * gib,
	}, nil
}

func (stubSystemProvider) DiskStats(context.Context, string) (systemskills.DiskStats, error) {
	const gib = 1024 * 1024 * 1024
	return systemskills.DiskStats{
		Path:  "/",
		Total: 64 * gib,
		Free:  16 * gib,
		Used:  48 * gib,
	}, nil
}

func (stubSystemProvider) Uptime(context.Context) (time.Duration, error) {
	return 26*time.Hour + 3*time.Minute + 4*time.Second, nil
}

func (stubSystemProvider) Hostname(context.Context) (string, error) {
	return "raspberry", nil
}

func (stubSystemProvider) IPAddresses(context.Context) ([]string, error) {
	return []string{"192.168.1.82"}, nil
}

func (stubSystemProvider) Temperature(context.Context) (float64, error) {
	return 58.5, nil
}

type fakeTelegramUpdate struct {
	ID     int64
	ChatID int64
	UserID int64
	Text   string
}

type fakeTelegramAPI struct {
	t      *testing.T
	token  string
	server *httptest.Server

	mu        sync.Mutex
	updates   []fakeTelegramUpdate
	sentTexts []string
	sentCh    chan string
}

func newFakeTelegramAPI(t *testing.T, token string, updates []fakeTelegramUpdate) *fakeTelegramAPI {
	t.Helper()

	api := &fakeTelegramAPI{
		t:       t,
		token:   token,
		updates: append([]fakeTelegramUpdate(nil), updates...),
		sentCh:  make(chan string, 4),
	}
	api.server = httptest.NewServer(http.HandlerFunc(api.handle))
	return api
}

func (f *fakeTelegramAPI) URL() string {
	return f.server.URL
}

func (f *fakeTelegramAPI) Close() {
	f.server.Close()
}

func (f *fakeTelegramAPI) waitForSentText(t *testing.T) string {
	t.Helper()

	select {
	case text := <-f.sentCh:
		return text
	case <-time.After(45 * time.Second):
		t.Fatal("timed out waiting for telegram sendMessage")
		return ""
	}
}

func (f *fakeTelegramAPI) handle(w http.ResponseWriter, r *http.Request) {
	f.t.Helper()

	expectedPrefix := "/bot" + f.token + "/"
	if !strings.HasPrefix(r.URL.Path, expectedPrefix) {
		f.t.Fatalf("unexpected telegram path: %s", r.URL.Path)
	}

	method := strings.TrimPrefix(r.URL.Path, expectedPrefix)
	switch method {
	case "deleteWebhook":
		writeJSON(w, map[string]any{"ok": true, "result": true})
	case "getUpdates":
		f.handleGetUpdates(w)
	case "sendMessage":
		f.handleSendMessage(w, r)
	default:
		f.t.Fatalf("unexpected telegram method: %s", method)
	}
}

func (f *fakeTelegramAPI) handleGetUpdates(w http.ResponseWriter) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if len(f.updates) == 0 {
		writeJSON(w, map[string]any{"ok": true, "result": []any{}})
		return
	}

	update := f.updates[0]
	f.updates = f.updates[1:]

	writeJSON(w, map[string]any{
		"ok": true,
		"result": []map[string]any{
			{
				"update_id": update.ID,
				"message": map[string]any{
					"message_id": update.ID * 10,
					"text":       update.Text,
					"chat":       map[string]any{"id": update.ChatID},
					"from":       map[string]any{"id": update.UserID},
				},
			},
		},
	})
}

func (f *fakeTelegramAPI) handleSendMessage(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		ChatID int64  `json:"chat_id"`
		Text   string `json:"text"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		f.t.Fatalf("decode sendMessage payload: %v", err)
	}
	if payload.ChatID == 0 || strings.TrimSpace(payload.Text) == "" {
		f.t.Fatalf("unexpected sendMessage payload: %#v", payload)
	}

	f.mu.Lock()
	f.sentTexts = append(f.sentTexts, payload.Text)
	f.mu.Unlock()

	select {
	case f.sentCh <- payload.Text:
	default:
	}

	writeJSON(w, map[string]any{"ok": true, "result": map[string]any{"message_id": 999}})
}

func writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		panic(fmt.Sprintf("encode json response: %v", err))
	}
}

func assertRowCount(t *testing.T, dbPath, table string, want int) {
	t.Helper()

	switch table {
	case "messages", "skill_calls":
	default:
		t.Fatalf("unsupported table for count assertion: %s", table)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("sql.Open returned error: %v", err)
	}
	defer db.Close()

	var got int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if err := db.QueryRow(query).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("expected %d rows in %s, got %d", want, table, got)
	}
}

func waitForRowCount(t *testing.T, dbPath, table string, want int, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for {
		got, err := rowCount(dbPath, table)
		if err == nil && got >= want {
			return
		}
		if time.Now().After(deadline) {
			if err != nil {
				t.Fatalf("timed out waiting for %s row count: %v", table, err)
			}
			t.Fatalf("timed out waiting for %d rows in %s; got %d", want, table, got)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func rowCount(dbPath, table string) (int, error) {
	switch table {
	case "messages", "skill_calls":
	default:
		return 0, fmt.Errorf("unsupported table for count assertion: %s", table)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return 0, err
	}
	defer db.Close()

	var got int
	query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
	if err := db.QueryRow(query).Scan(&got); err != nil {
		return 0, err
	}

	return got, nil
}
