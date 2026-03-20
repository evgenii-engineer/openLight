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
