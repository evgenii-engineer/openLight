package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// validationNegativeCases exercises Config.Validate() through Load() against
// scenarios listed in docs/REGRESSION.md. Every case must either fail with a
// non-nil error and a recognizable message fragment, or assert successful load
// when noted. Add new rows here rather than scattering one-off tests across
// files.
func TestLoadValidationNegativeCases(t *testing.T) {
	cases := []struct {
		name        string
		yaml        string
		errContains string
	}{
		{
			name: "missing telegram bot token fails",
			yaml: `
telegram:
  bot_token: ""
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
`,
			errContains: "TELEGRAM_BOT_TOKEN",
		},
		{
			name: "missing both auth allowlists fails",
			yaml: `
telegram:
  bot_token: "token"
auth: {}
storage:
  sqlite_path: "./agent.db"
`,
			errContains: "ALLOWED_USER_IDS",
		},
		{
			name: "invalid telegram mode fails",
			yaml: `
telegram:
  bot_token: "token"
  mode: "smoke-signals"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
`,
			errContains: "telegram.mode",
		},
		{
			name: "webhook mode without url fails",
			yaml: `
telegram:
  bot_token: "token"
  mode: "webhook"
  webhook:
    listen_addr: ":8081"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
`,
			errContains: "telegram.webhook.url is required",
		},
		{
			name: "webhook url not https fails",
			yaml: `
telegram:
  bot_token: "token"
  mode: "webhook"
  webhook:
    url: "http://example.com/openlight"
    listen_addr: ":8081"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
`,
			errContains: "must start with https://",
		},
		{
			name: "missing storage sqlite path fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: ""
`,
			errContains: "SQLITE_PATH",
		},
		{
			name: "llm execute_threshold above 1 fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
llm:
  enabled: true
  provider: "ollama"
  execute_threshold: 1.5
  clarify_threshold: 0.6
  decision_input_chars: 160
  decision_num_predict: 48
`,
			errContains: "execute_threshold",
		},
		{
			name: "llm clarify_threshold above execute fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
llm:
  enabled: true
  provider: "ollama"
  execute_threshold: 0.5
  clarify_threshold: 0.7
  decision_input_chars: 160
  decision_num_predict: 48
`,
			errContains: "clarify_threshold",
		},
		{
			name: "llm num_ctx negative top-level fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
llm:
  enabled: true
  provider: "ollama"
  execute_threshold: 0.8
  clarify_threshold: 0.6
  decision_input_chars: 160
  decision_num_predict: 48
  num_ctx: -1
`,
			errContains: "llm.num_ctx",
		},
		{
			name: "llm profile num_ctx negative fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
llm:
  enabled: true
  provider: "ollama"
  execute_threshold: 0.8
  clarify_threshold: 0.6
  decision_input_chars: 160
  decision_num_predict: 48
  profiles:
    fast:
      model: "qwen2.5:1.5b"
      num_ctx: -2
`,
			errContains: "num_ctx",
		},
		{
			name: "files max_read_bytes zero fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
files:
  max_read_bytes: 0
  list_limit: 10
`,
			errContains: "files.max_read_bytes",
		},
		{
			name: "browser enabled without domains and without allow_all fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
browser:
  enabled: true
  timeout_seconds: 20
`,
			errContains: "browser.allowed_domains",
		},
		{
			name: "voice enabled without model_path fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
voice:
  enabled: true
  provider: "whisper_cli"
  whisper_cli_path: "/opt/homebrew/bin/whisper-cli"
`,
			errContains: "voice.model_path",
		},
		{
			name: "remote host without auth method fails",
			yaml: `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
access:
  hosts:
    vps:
      address: "203.0.113.10:22"
      user: "root"
      known_hosts_path: "/tmp/known_hosts"
`,
			errContains: "password",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clearConfigEnv(t)

			path := filepath.Join(t.TempDir(), "agent.yaml")
			writeConfig(t, path, tc.yaml)

			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.errContains)
			}
			if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
				t.Fatalf("expected error to contain %q, got: %v", tc.errContains, err)
			}
		})
	}
}

func TestLoadValidationAllowsBrowserAllowAllDomains(t *testing.T) {
	clearConfigEnv(t)

	path := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, path, `
telegram:
  bot_token: "token"
auth:
  allowed_user_ids: [1]
storage:
  sqlite_path: "./agent.db"
browser:
  enabled: true
  allow_all_domains: true
  timeout_seconds: 20
`)

	if _, err := Load(path); err != nil {
		t.Fatalf("expected allow_all_domains to satisfy validation, got: %v", err)
	}
}
