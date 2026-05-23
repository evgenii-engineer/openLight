package router_test

import (
	"context"
	"testing"

	"openlight/internal/router"
	"openlight/internal/skills"
)

type testSkill struct {
	name    string
	aliases []string
}

func (s testSkill) Definition() skills.Definition {
	return skills.Definition{Name: s.name, Aliases: s.aliases}
}

func (s testSkill) Execute(context.Context, skills.Input) (skills.Result, error) {
	return skills.Result{Text: s.name}, nil
}

type stubClassifier struct {
	decision router.Decision
	ok       bool
}

func (s stubClassifier) Classify(context.Context, string) (router.Decision, bool, error) {
	return s.decision, s.ok, nil
}

func TestRouterSlashCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/cpu")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "cpu" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterAliasMatch(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu", aliases: []string{"processor usage"}})

	decision, err := router.New(registry, nil).Route(context.Background(), "processor usage")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeAlias || decision.SkillName != "cpu" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterRuleBasedLogsParsing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_logs"})

	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "service before logs", input: "show jellyfin logs", want: "jellyfin"},
		{name: "service after logs", input: "show logs tailscale", want: "tailscale"},
		{name: "russian logs command", input: "покажи логи tailscale", want: "tailscale"},
		{name: "service alias after logs", input: "покажи логи tailscaled", want: "tailscaled"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decision, err := router.New(registry, nil).Route(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("route returned error: %v", err)
			}
			if decision.Mode != router.ModeRule || decision.SkillName != "service_logs" {
				t.Fatalf("unexpected decision: %#v", decision)
			}
			if got := decision.Args["service"]; got != tc.want {
				t.Fatalf("expected %q service, got %q", tc.want, got)
			}
		})
	}
}

func TestRouterRuleBasedRussianOverallStatusParsing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "status"})

	decision, err := router.New(registry, nil).Route(context.Background(), "покажи общий статус")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeRule || decision.SkillName != "status" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterDoesNotTreatGenericServicePhraseAsConcreteService(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_status"})
	registry.MustRegister(testSkill{name: "service_logs"})
	registry.MustRegister(testSkill{name: "service_restart"})

	cases := []string{
		"статус сервиса",
		"покажи логи сервиса",
		"перезапусти сервис",
	}

	for _, input := range cases {
		t.Run(input, func(t *testing.T) {
			decision, err := router.New(registry, nil).Route(context.Background(), input)
			if err != nil {
				t.Fatalf("route returned error: %v", err)
			}
			if decision.Matched() {
				t.Fatalf("expected no match for generic service phrase, got %#v", decision)
			}
		})
	}
}

func TestRouterUsesLLMFallback(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "skills"})

	decision, err := router.New(registry, stubClassifier{
		decision: router.Decision{
			Mode:      router.ModeLLM,
			SkillName: "skills",
			Args:      map[string]string{},
		},
		ok: true,
	}).Route(context.Background(), "what are you capable of?")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeLLM || decision.SkillName != "skills" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterChatSlashCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "chat"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/chat explain disk pressure")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "chat" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["text"] != "explain disk pressure" {
		t.Fatalf("unexpected chat args: %#v", decision.Args)
	}
}

func TestRouterExplicitNoteAddCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "note_add"})

	decision, err := router.New(registry, nil).Route(context.Background(), "note_add привет")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "note_add" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["text"] != "привет" {
		t.Fatalf("unexpected note args: %#v", decision.Args)
	}
}

func TestRouterSkillsCommandAcceptsTopic(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "skills"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/skills files")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "skills" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["topic"] != "files" {
		t.Fatalf("unexpected skills args: %#v", decision.Args)
	}
}

func TestRouterWatchAddCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "watch_add"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/watch add service nginx ask for 30s")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "watch_add" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if got := decision.Args["spec"]; got != "service nginx ask for 30s" {
		t.Fatalf("unexpected watch spec: %#v", decision.Args)
	}
}

func TestRouterEnableCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "watch_enable"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/enable docker")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "watch_enable" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if got := decision.Args["pack"]; got != "docker" {
		t.Fatalf("unexpected enable args: %#v", decision.Args)
	}
}

func TestRouterWatchHistoryCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "watch_history"})

	decision, err := router.New(registry, nil).Route(context.Background(), "watch history 12")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "watch_history" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if got := decision.Args["id"]; got != "12" {
		t.Fatalf("unexpected watch history args: %#v", decision.Args)
	}
}

func TestRouterRuleBasedRussianNoteAddParsing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "note_add"})

	decision, err := router.New(registry, nil).Route(context.Background(), "добавь заметку купить ssd")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeRule || decision.SkillName != "note_add" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["text"] != "купить ssd" {
		t.Fatalf("unexpected note args: %#v", decision.Args)
	}
}

func TestRouterExplicitTwoWordCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "service_logs"})

	decision, err := router.New(registry, nil).Route(context.Background(), "service logs tailscale")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "service_logs" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["service"] != "tailscale" {
		t.Fatalf("unexpected service args: %#v", decision.Args)
	}
}

func TestRouterExplicitNoteDeleteCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "note_delete"})

	decision, err := router.New(registry, nil).Route(context.Background(), "note_delete 2")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "note_delete" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["id"] != "2" {
		t.Fatalf("unexpected note delete args: %#v", decision.Args)
	}
}

func TestRouterRememberCommandRoutesToMemory(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "memory_add"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/remember that the Mac mini is primary")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.SkillName != "memory_add" || decision.Args["text"] != "that the Mac mini is primary" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterRuleBasedMemoryQueryParsing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "memory_list"})

	decision, err := router.New(registry, nil).Route(context.Background(), "what do you remember about my homelab")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeRule || decision.SkillName != "memory_list" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["query"] != "my homelab" {
		t.Fatalf("unexpected memory query args: %#v", decision.Args)
	}
}

func TestRouterFileSearchCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_search"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/file_search OPENAI_API_KEY in ./configs")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.SkillName != "file_search" || decision.Args["pattern"] != "OPENAI_API_KEY" || decision.Args["path"] != "./configs" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterBrowserCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "browser_title"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/browse title https://example.com")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.SkillName != "browser_title" || decision.Args["url"] != "https://example.com" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterExplicitUserAddCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "user_add"})

	decision, err := router.New(registry, nil).Route(context.Background(), "user_add jitsi anya 123456")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "user_add" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["provider"] != "jitsi" || decision.Args["username"] != "anya" || decision.Args["password"] != "123456" {
		t.Fatalf("unexpected user add args: %#v", decision.Args)
	}
}

func TestRouterExplicitUserDeleteCommandWithImplicitProvider(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "user_delete"})

	decision, err := router.New(registry, nil).Route(context.Background(), "user delete anya")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "user_delete" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["provider"] != "" || decision.Args["username"] != "anya" {
		t.Fatalf("unexpected user delete args: %#v", decision.Args)
	}
}

func TestRouterUsersCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "user_providers"})

	decision, err := router.New(registry, nil).Route(context.Background(), "/users")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeSlash || decision.SkillName != "user_providers" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterExplicitUserListCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "user_list"})

	decision, err := router.New(registry, nil).Route(context.Background(), "user list jitsi anya")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "user_list" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["provider"] != "jitsi" || decision.Args["pattern"] != "anya" {
		t.Fatalf("unexpected user list args: %#v", decision.Args)
	}
}

func TestRouterExplicitFileReadCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_read"})

	decision, err := router.New(registry, nil).Route(context.Background(), "read /etc/hostname")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "file_read" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["path"] != "/etc/hostname" {
		t.Fatalf("unexpected file args: %#v", decision.Args)
	}
}

func TestRouterExplicitFileWriteCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_write"})

	decision, err := router.New(registry, nil).Route(context.Background(), "write ./tmp/test.txt :: hello")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "file_write" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["path"] != "./tmp/test.txt" || decision.Args["content"] != "hello" {
		t.Fatalf("unexpected file args: %#v", decision.Args)
	}
}

func TestRouterExplicitFileReplaceCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "file_replace"})

	decision, err := router.New(registry, nil).Route(context.Background(), "replace 8080 with 8081 in ./config.yaml")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "file_replace" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["path"] != "./config.yaml" || decision.Args["find"] != "8080" || decision.Args["replace"] != "8081" {
		t.Fatalf("unexpected file args: %#v", decision.Args)
	}
}

func TestRouterRunCommandRoutesExecCode(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "exec_code"})

	decision, err := router.New(registry, nil).Route(context.Background(), "run python: print('hello')")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "exec_code" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["runtime"] != "python" || decision.Args["code"] != "print('hello')" {
		t.Fatalf("unexpected workbench args: %#v", decision.Args)
	}
}

func TestRouterRunCommandRoutesExecFile(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "exec_file"})

	decision, err := router.New(registry, nil).Route(context.Background(), "run /usr/bin/uptime")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "exec_file" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["path"] != "/usr/bin/uptime" {
		t.Fatalf("unexpected workbench args: %#v", decision.Args)
	}
}

func TestRouterWorkspaceCleanCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "workspace_clean"})

	decision, err := router.New(registry, nil).Route(context.Background(), "workspace_clean")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "workspace_clean" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
}

func TestRouterRuleBasedNoteDeleteParsing(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "note_delete"})

	decision, err := router.New(registry, nil).Route(context.Background(), "delete note 2")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode != router.ModeExplicit || decision.SkillName != "note_delete" {
		t.Fatalf("unexpected decision: %#v", decision)
	}
	if decision.Args["id"] != "2" {
		t.Fatalf("unexpected note delete args: %#v", decision.Args)
	}
}

func TestRouterDoesNotTreatSentenceAsNoArgCommand(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "note_list"})

	decision, err := router.New(registry, nil).Route(context.Background(), "notes are useful")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Matched() {
		t.Fatalf("expected no match for ordinary sentence, got %#v", decision)
	}
}

// failOnCallClassifier fails the enclosing test if Classify is invoked. Used
// to assert that deterministic inputs (slash, explicit, alias, rule) never
// fall through to the FAST classifier.
type failOnCallClassifier struct {
	t *testing.T
}

func (c failOnCallClassifier) Classify(context.Context, string) (router.Decision, bool, error) {
	c.t.Helper()
	c.t.Fatal("classifier must not be invoked for deterministic inputs")
	return router.Decision{}, false, nil
}

func TestRouterDeterministicInputsDoNotCallClassifier(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	for _, s := range []skills.Skill{
		testSkill{name: "skills"},
		testSkill{name: "status"},
		testSkill{name: "service_status"},
		testSkill{name: "service_logs"},
		testSkill{name: "watch_list"},
		testSkill{name: "models", aliases: []string{"llm status"}},
	} {
		registry.MustRegister(s)
	}

	cases := []struct {
		input     string
		wantSkill string
		wantSvc   string
	}{
		{input: "skills", wantSkill: "skills"},
		{input: "system status", wantSkill: "status"},
		{input: "watch list", wantSkill: "watch_list"},
		{input: "/service_status openlight-agent", wantSkill: "service_status", wantSvc: "openlight-agent"},
		{input: "/logs openlight-agent", wantSkill: "service_logs", wantSvc: "openlight-agent"},
		{input: "llm status", wantSkill: "models"},
	}

	r := router.New(registry, failOnCallClassifier{t: t})
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			decision, err := r.Route(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("route returned error: %v", err)
			}
			if decision.SkillName != tc.wantSkill {
				t.Fatalf("expected skill %q, got %#v", tc.wantSkill, decision)
			}
			if decision.Mode == router.ModeLLM || decision.Mode == router.ModeUnknown {
				t.Fatalf("expected deterministic mode (slash/explicit/alias/rule), got %q", decision.Mode)
			}
			if tc.wantSvc != "" && decision.Args["service"] != tc.wantSvc {
				t.Fatalf("expected service arg %q, got %q", tc.wantSvc, decision.Args["service"])
			}
		})
	}
}

// TestRouterNormalizedShortcutBypassesClassifier asserts that single
// normalized tokens (English or Russian after semantic rewriting) reach the
// matching skill without invoking the LLM classifier. This is the fast path
// targeted at common operational queries on the Mac mini build.
func TestRouterNormalizedShortcutBypassesClassifier(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	for _, name := range []string{"cpu", "memory", "disk", "uptime", "status", "temperature", "hostname", "service_list", "note_list"} {
		registry.MustRegister(testSkill{name: name})
	}

	cases := []struct {
		input     string
		wantSkill string
	}{
		{input: "проц", wantSkill: "cpu"},
		{input: "Память", wantSkill: "memory"},
		{input: "диск", wantSkill: "disk"},
		{input: "аптайм", wantSkill: "uptime"},
		{input: "статус", wantSkill: "status"},
		{input: "температура", wantSkill: "temperature"},
		{input: "хост", wantSkill: "hostname"},
		{input: "сервисы", wantSkill: "service_list"},
		{input: "заметки", wantSkill: "note_list"},
	}

	r := router.New(registry, failOnCallClassifier{t: t})
	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			decision, err := r.Route(context.Background(), tc.input)
			if err != nil {
				t.Fatalf("route returned error: %v", err)
			}
			if decision.SkillName != tc.wantSkill {
				t.Fatalf("expected skill %q, got %#v", tc.wantSkill, decision)
			}
			if decision.Mode != router.ModeShortcut && decision.Mode != router.ModeExplicit {
				t.Fatalf("expected deterministic shortcut/explicit mode, got %q", decision.Mode)
			}
		})
	}
}

// TestRouterNormalizedShortcutSkipsMultiToken keeps multi-token input out of
// the shortcut path so qualifiers ("cpu of nginx", "memory please") still go
// through the rules / LLM pipeline that can use the surrounding context.
func TestRouterNormalizedShortcutSkipsMultiToken(t *testing.T) {
	t.Parallel()

	registry := skills.NewRegistry()
	registry.MustRegister(testSkill{name: "cpu"})

	classifier := stubClassifier{
		decision: router.Decision{Mode: router.ModeLLM, SkillName: "cpu", Args: map[string]string{}},
		ok:       true,
	}

	decision, err := router.New(registry, classifier).Route(context.Background(), "memory please thanks")
	if err != nil {
		t.Fatalf("route returned error: %v", err)
	}
	if decision.Mode == router.ModeShortcut {
		t.Fatalf("multi-token input must not hit the shortcut path: %#v", decision)
	}
}
