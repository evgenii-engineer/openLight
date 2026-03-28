package cli

import (
	"bytes"
	"context"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"openlight/internal/app"
	"openlight/internal/config"
	"openlight/internal/models"
	"openlight/internal/router"
	"openlight/internal/skills"
	"openlight/internal/storage"
	"openlight/internal/storage/sqlite"
)

func TestParseSmokeNoteID(t *testing.T) {
	t.Parallel()

	got, err := parseSmokeNoteID("Saved note #42")
	if err != nil {
		t.Fatalf("parseSmokeNoteID returned error: %v", err)
	}
	if got != "42" {
		t.Fatalf("unexpected note id: %q", got)
	}
}

func TestParseSmokeWatchID(t *testing.T) {
	t.Parallel()

	got, err := parseSmokeWatchID("Watch created:\n#7 memory high\nKind: memory_high")
	if err != nil {
		t.Fatalf("parseSmokeWatchID returned error: %v", err)
	}
	if got != "7" {
		t.Fatalf("unexpected watch id: %q", got)
	}
}

func TestSmokeReportRenderTable(t *testing.T) {
	t.Parallel()

	report := SmokeReport{
		Rows: []SmokeRow{
			{Check: "core.ping", Command: "ping", Status: SmokePass, Duration: 25 * time.Millisecond, Summary: "pong"},
			{Check: "chat.chat", Command: "chat reply with exactly SMOKE_CHAT_OK", Status: SmokePass, Duration: 900 * time.Millisecond, Summary: "SMOKE_CHAT_OK"},
			{Check: "services.restart", Command: "restart tailscale", Status: SmokeSkip, Summary: "skipped for safety"},
		},
	}

	text := report.RenderTable()
	for _, fragment := range []string{
		"| Check",
		"core.ping",
		"PASS",
		"SKIP",
		"Result: PASS | pass=2 fail=0 skip=1",
		"Totals: 3/3 completed without failure",
		"Latency: checks=2 min=25ms p50=900ms p95=900ms max=900ms",
		"Latency LLM: checks=1 min=900ms p50=900ms p95=900ms max=900ms",
		"Slowest: chat.chat=900ms | core.ping=25ms",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in rendered table, got:\n%s", fragment, text)
		}
	}
}

func TestSmokeReportLatencySummary(t *testing.T) {
	t.Parallel()

	report := SmokeReport{
		Rows: []SmokeRow{
			{Check: "core.ping", Status: SmokePass, Duration: 10 * time.Millisecond},
			{Check: "system.status", Status: SmokePass, Duration: 20 * time.Millisecond},
			{Check: "chat.chat", Status: SmokePass, Duration: 300 * time.Millisecond},
			{Check: "llm.route_status", Status: SmokeFail, Duration: 600 * time.Millisecond},
			{Check: "services.restart", Status: SmokeSkip},
		},
	}

	stats, ok := report.LatencySummary()
	if !ok {
		t.Fatal("expected latency summary")
	}
	if stats.Count != 4 || stats.Min != 10*time.Millisecond || stats.P50 != 300*time.Millisecond || stats.P95 != 600*time.Millisecond || stats.Max != 600*time.Millisecond {
		t.Fatalf("unexpected latency summary: %#v", stats)
	}

	llmStats, ok := report.LLMLatencySummary()
	if !ok {
		t.Fatal("expected llm latency summary")
	}
	if llmStats.Count != 2 || llmStats.Min != 300*time.Millisecond || llmStats.P50 != 600*time.Millisecond || llmStats.P95 != 600*time.Millisecond || llmStats.Max != 600*time.Millisecond {
		t.Fatalf("unexpected llm latency summary: %#v", llmStats)
	}
}

func TestSmokeProgressOutput(t *testing.T) {
	t.Parallel()

	var buffer bytes.Buffer
	writeSmokeProgressStart(&buffer, "core.ping", "ping")
	writeSmokeProgressDone(&buffer, SmokeRow{
		Check:    "core.ping",
		Command:  "ping",
		Status:   SmokePass,
		Duration: 25 * time.Millisecond,
		Summary:  "pong",
	})

	text := buffer.String()
	for _, fragment := range []string{
		"[RUN ] core.ping | ping",
		"[PASS] core.ping | 25ms | pong",
	} {
		if !strings.Contains(text, fragment) {
			t.Fatalf("expected %q in progress output, got:\n%s", fragment, text)
		}
	}
}

func TestAddLLMFallbackExecCheckUsesClassifierDecision(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	registry := skills.NewRegistry()
	registry.MustRegister(smokeTestSkill{
		name:     "llm_only_memory",
		group:    skills.GroupSystem,
		response: "Memory usage: 42%",
	})

	runtime := app.Runtime{
		Registry: registry,
		Classifier: smokeStubClassifier{
			decision: router.Decision{
				Mode:       router.ModeLLM,
				SkillName:  "llm_only_memory",
				Args:       map[string]string{},
				Confidence: 0.91,
			},
			ok: true,
		},
		Repository: repo,
	}

	harness := NewHarness(config.Config{
		Auth: config.AuthConfig{
			AllowedUserIDs: []int64{1},
		},
		Agent: config.AgentConfig{
			RequestTimeout: time.Second,
		},
	}, runtime, 1, 1)

	var report SmokeReport
	addLLMFallbackExecCheck(&report, runtime, harness, io.Discard, ctx, "llm.fallback.memory", expectDecisionSkill("llm_only_memory", nil), expectContains("Memory usage:"),
		"How much RAM is being used right now?",
	)

	if len(report.Rows) != 1 {
		t.Fatalf("expected 1 smoke row, got %d", len(report.Rows))
	}
	if report.Rows[0].Status != SmokePass {
		t.Fatalf("expected fallback check to pass, got %#v", report.Rows[0])
	}
}

func TestPreferredRuntime(t *testing.T) {
	t.Parallel()

	if got := preferredRuntime([]string{"python", "sh"}); got != "sh" {
		t.Fatalf("expected sh to be preferred, got %q", got)
	}
	if got := preferredRuntime([]string{"node"}); got != "node" {
		t.Fatalf("expected node fallback, got %q", got)
	}
}

func TestAllAccountProvidersSorted(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Accounts: config.AccountsConfig{
			Providers: map[string]config.AccountProviderConfig{
				"synapse": {},
				"jitsi":   {},
			},
		},
	}

	got := allAccountProviders(cfg)
	want := []string{"jitsi", "synapse"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected providers: %#v", got)
	}
}

func TestAllDockerPackTargets(t *testing.T) {
	t.Parallel()

	cfg := config.Config{
		Services: config.ServicesConfig{
			Allowed: []string{
				"tailscale",
				"api=docker:demo-api",
				"web=compose:/srv/demo/docker-compose.yml:web",
				"backup=host:pi:docker:backup-container",
				"nginx=systemd:nginx",
			},
		},
	}

	got := allDockerPackTargets(cfg)
	want := []string{"api", "backup", "web"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected docker pack targets: %#v", got)
	}
}

func TestSmokeAccountUsername(t *testing.T) {
	t.Parallel()

	if got := smokeAccountUsername("smoke_123", "matrix-admin"); got != "smoke_123_matrix_admin" {
		t.Fatalf("unexpected scoped username: %q", got)
	}
}

func TestExpectContainsAllFold(t *testing.T) {
	t.Parallel()

	validate := expectContainsAllFold("alert #", "memory")
	if err := validate("Alert #2: Memory is 17.2%"); err != nil {
		t.Fatalf("expectContainsAllFold returned error: %v", err)
	}
}

func TestSmokePackSnapshotRestore(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo, err := sqlite.New(ctx, filepath.Join(t.TempDir(), "agent.db"), nil)
	if err != nil {
		t.Fatalf("sqlite.New returned error: %v", err)
	}
	t.Cleanup(func() {
		_ = repo.Close()
	})

	original, err := repo.CreateWatch(ctx, models.Watch{
		TelegramUserID:  11,
		TelegramChatID:  7,
		Name:            "memory custom",
		Kind:            models.WatchKindMemoryHigh,
		Target:          "memory",
		Threshold:       70,
		Duration:        2 * time.Minute,
		ReactionMode:    models.WatchReactionNotify,
		ActionType:      models.WatchActionNone,
		Cooldown:        9 * time.Minute,
		Enabled:         false,
		IncidentState:   models.WatchIncidentStateClear,
		ConditionSince:  time.Unix(10, 0).UTC(),
		LastTriggeredAt: time.Unix(20, 0).UTC(),
		LastCheckedAt:   time.Unix(30, 0).UTC(),
	})
	if err != nil {
		t.Fatalf("CreateWatch returned error: %v", err)
	}

	keys := []smokeWatchKey{
		{Kind: models.WatchKindCPUHigh, Target: "cpu"},
		{Kind: models.WatchKindMemoryHigh, Target: "memory"},
	}
	snapshot, err := captureSmokePackSnapshot(ctx, repo, 7, keys, []string{packSettingKey(7, "system")})
	if err != nil {
		t.Fatalf("captureSmokePackSnapshot returned error: %v", err)
	}

	original.Name = "memory pack"
	original.Threshold = 90
	original.Duration = 5 * time.Minute
	original.Cooldown = 15 * time.Minute
	original.Enabled = true
	original.ConditionSince = time.Time{}
	original.LastTriggeredAt = time.Time{}
	original.LastCheckedAt = time.Time{}
	if err := repo.UpdateWatch(ctx, original); err != nil {
		t.Fatalf("UpdateWatch returned error: %v", err)
	}

	if _, err := repo.CreateWatch(ctx, models.Watch{
		TelegramUserID: 1,
		TelegramChatID: 7,
		Name:           "cpu pack",
		Kind:           models.WatchKindCPUHigh,
		Target:         "cpu",
		Threshold:      90,
		Duration:       5 * time.Minute,
		ReactionMode:   models.WatchReactionNotify,
		ActionType:     models.WatchActionNone,
		Cooldown:       15 * time.Minute,
		Enabled:        true,
		IncidentState:  models.WatchIncidentStateClear,
	}); err != nil {
		t.Fatalf("CreateWatch cpu returned error: %v", err)
	}

	if err := repo.SetSetting(ctx, packSettingKey(7, "system"), "enabled"); err != nil {
		t.Fatalf("SetSetting returned error: %v", err)
	}

	if err := snapshot.restore(ctx, repo); err != nil {
		t.Fatalf("restore returned error: %v", err)
	}

	watches, err := repo.ListWatches(ctx, storage.WatchListOptions{ChatID: 7})
	if err != nil {
		t.Fatalf("ListWatches returned error: %v", err)
	}
	if len(watches) != 1 {
		t.Fatalf("expected one restored watch, got %#v", watches)
	}
	if !smokeWatchEquals(watches[0], snapshot.Watches[watches[0].ID]) {
		t.Fatalf("watch was not restored: got %#v want %#v", watches[0], snapshot.Watches[watches[0].ID])
	}

	_, ok, err := repo.GetSetting(ctx, packSettingKey(7, "system"))
	if err != nil {
		t.Fatalf("GetSetting returned error: %v", err)
	}
	if ok {
		t.Fatal("expected pack setting to be removed after restore")
	}
}

type smokeStubClassifier struct {
	decision router.Decision
	ok       bool
}

func (s smokeStubClassifier) Classify(context.Context, string) (router.Decision, bool, error) {
	return s.decision, s.ok, nil
}

type smokeTestSkill struct {
	name     string
	group    skills.Group
	response string
}

func (s smokeTestSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:  s.name,
		Group: s.group,
	}
}

func (s smokeTestSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: s.response}, nil
}
