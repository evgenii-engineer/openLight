package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadNestedConfig(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"
  api_base_url: "https://api.telegram.org"
  mode: "webhook"
  poll_timeout: 30s
  webhook:
    url: "https://bot.example.com/openlight/webhook"
    listen_addr: ":8081"
    secret_token: "secret"
    drop_pending_updates: true

auth:
  allowed_user_ids: [1]
  allowed_chat_ids: [2]

storage:
  sqlite_path: "./agent.db"

files:
  allowed: ["/tmp/openlight", "/home/pi/scripts", "/tmp/openlight"]
  max_read_bytes: 8192
  list_limit: 25

workbench:
  enabled: true
  workspace_dir: "/tmp/openlight"
  allowed_runtimes: ["python", "bash", "python"]
  allowed_files: ["/usr/bin/uptime", "/usr/bin/uptime"]
  max_output_bytes: 16384

services:
  allowed: ["tailscale", "docker", "tailscale"]
  log_lines: 42
  max_log_chars: 1234

llm:
  enabled: true
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "qwen2.5:0.5b"
  execute_threshold: 0.9
  clarify_threshold: 0.7
  decision_input_chars: 120
  decision_num_predict: 48

chat:
  history_limit: 4
  history_chars: 300
  max_response_chars: 200

notes:
  list_limit: 9

agent:
  request_timeout: 15s

log:
  level: "debug"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Telegram.BotToken != "token" {
		t.Fatalf("unexpected bot token: %q", cfg.Telegram.BotToken)
	}
	if cfg.Telegram.PollTimeout != 30*time.Second {
		t.Fatalf("unexpected poll timeout: %v", cfg.Telegram.PollTimeout)
	}
	if cfg.Telegram.Mode != "webhook" {
		t.Fatalf("unexpected telegram mode: %q", cfg.Telegram.Mode)
	}
	if cfg.Telegram.Webhook.URL != "https://bot.example.com/openlight/webhook" {
		t.Fatalf("unexpected webhook url: %q", cfg.Telegram.Webhook.URL)
	}
	if cfg.Telegram.Webhook.ListenAddr != ":8081" || cfg.Telegram.Webhook.SecretToken != "secret" || !cfg.Telegram.Webhook.DropPendingUpdates {
		t.Fatalf("unexpected webhook config: %#v", cfg.Telegram.Webhook)
	}
	if cfg.Storage.SQLitePath != "./agent.db" {
		t.Fatalf("unexpected sqlite path: %q", cfg.Storage.SQLitePath)
	}
	if got := cfg.Files.Allowed; len(got) != 2 || got[0] != "/tmp/openlight" || got[1] != "/home/pi/scripts" {
		t.Fatalf("unexpected allowed file roots: %#v", got)
	}
	if cfg.Files.MaxReadBytes != 8192 || cfg.Files.ListLimit != 25 {
		t.Fatalf("unexpected files config: %#v", cfg.Files)
	}
	if !cfg.Workbench.Enabled {
		t.Fatal("expected workbench to be enabled")
	}
	if cfg.Workbench.WorkspaceDir != "/tmp/openlight" {
		t.Fatalf("unexpected workbench dir: %q", cfg.Workbench.WorkspaceDir)
	}
	if got := cfg.Workbench.AllowedRuntimes; len(got) != 2 || got[0] != "python" || got[1] != "bash" {
		t.Fatalf("unexpected allowed runtimes: %#v", got)
	}
	if got := cfg.Workbench.AllowedFiles; len(got) != 1 || got[0] != "/usr/bin/uptime" {
		t.Fatalf("unexpected allowed workbench files: %#v", got)
	}
	if cfg.Workbench.MaxOutputBytes != 16384 {
		t.Fatalf("unexpected workbench config: %#v", cfg.Workbench)
	}
	if got := cfg.Services.Allowed; len(got) != 2 || got[0] != "tailscale" || got[1] != "docker" {
		t.Fatalf("unexpected allowed services: %#v", got)
	}
	if cfg.Services.LogLines != 42 || cfg.Services.MaxLogChars != 1234 {
		t.Fatalf("unexpected services config: %#v", cfg.Services)
	}
	if !cfg.LLM.Enabled || cfg.LLM.Provider != "ollama" || cfg.LLM.Model != "qwen2.5:0.5b" {
		t.Fatalf("unexpected llm config: %#v", cfg.LLM)
	}
	if cfg.LLM.ExecuteThreshold != 0.9 || cfg.LLM.ClarifyThreshold != 0.7 {
		t.Fatalf("unexpected llm thresholds: %#v", cfg.LLM)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("unexpected llm api key: %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.DecisionInputChars != 120 || cfg.LLM.DecisionNumPredict != 48 {
		t.Fatalf("unexpected llm decision budget: %#v", cfg.LLM)
	}
	if cfg.Chat.HistoryLimit != 4 || cfg.Chat.HistoryChars != 300 || cfg.Chat.MaxResponseChars != 200 {
		t.Fatalf("unexpected chat config: %#v", cfg.Chat)
	}
	if cfg.Notes.ListLimit != 9 {
		t.Fatalf("unexpected notes limit: %d", cfg.Notes.ListLimit)
	}
	if cfg.Agent.RequestTimeout != 15*time.Second {
		t.Fatalf("unexpected request timeout: %v", cfg.Agent.RequestTimeout)
	}
	if cfg.Log.Level != "debug" {
		t.Fatalf("unexpected log level: %q", cfg.Log.Level)
	}
}

func TestLoadAppliesNestedDefaults(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.Telegram.APIBaseURL != defaultTelegramAPIBaseURL {
		t.Fatalf("unexpected telegram api base url: %q", cfg.Telegram.APIBaseURL)
	}
	if cfg.Telegram.PollTimeout != 25*time.Second {
		t.Fatalf("unexpected poll timeout: %v", cfg.Telegram.PollTimeout)
	}
	if cfg.Telegram.Mode != "polling" {
		t.Fatalf("unexpected telegram mode: %q", cfg.Telegram.Mode)
	}
	if cfg.Telegram.Webhook.ListenAddr != ":8081" {
		t.Fatalf("unexpected webhook listen addr default: %q", cfg.Telegram.Webhook.ListenAddr)
	}
	if cfg.Workbench.WorkspaceDir != "/tmp/openlight" {
		t.Fatalf("unexpected workbench dir default: %q", cfg.Workbench.WorkspaceDir)
	}
	if cfg.Workbench.MaxOutputBytes != 8192 {
		t.Fatalf("unexpected workbench defaults: %#v", cfg.Workbench)
	}
	if cfg.Services.LogLines != 100 {
		t.Fatalf("unexpected service log lines: %d", cfg.Services.LogLines)
	}
	if cfg.Services.MaxLogChars != 3000 {
		t.Fatalf("unexpected service max log chars: %d", cfg.Services.MaxLogChars)
	}
	if cfg.Files.MaxReadBytes != 4096 || cfg.Files.ListLimit != 40 {
		t.Fatalf("unexpected files defaults: %#v", cfg.Files)
	}
	if cfg.LLM.Provider != "generic" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.ExecuteThreshold != 0.80 || cfg.LLM.ClarifyThreshold != 0.60 {
		t.Fatalf("unexpected llm thresholds: %#v", cfg.LLM)
	}
	if cfg.Chat.HistoryLimit != 6 || cfg.Chat.HistoryChars != 900 || cfg.Chat.MaxResponseChars != 400 {
		t.Fatalf("unexpected chat defaults: %#v", cfg.Chat)
	}
	if cfg.Notes.ListLimit != 20 {
		t.Fatalf("unexpected notes list limit: %d", cfg.Notes.ListLimit)
	}
	if cfg.Agent.RequestTimeout != 5*time.Second {
		t.Fatalf("unexpected request timeout: %v", cfg.Agent.RequestTimeout)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("unexpected log level: %q", cfg.Log.Level)
	}
}

func TestLoadOpenAIConfig(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

llm:
  enabled: true
  provider: "openai"
  model: "gpt-4o-mini"
  api_key: "sk-test"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Fatalf("unexpected llm api key: %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Endpoint != defaultOpenAIAPIBaseURL {
		t.Fatalf("unexpected llm endpoint default: %q", cfg.LLM.Endpoint)
	}
}

func TestLoadPreservesComposeServicePath(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

services:
  allowed:
    - "synapse=compose:/home/damk/Matrix/docker-compose.yml"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got := cfg.Services.Allowed; len(got) != 1 || got[0] != "synapse=compose:/home/damk/Matrix/docker-compose.yml" {
		t.Fatalf("unexpected allowed services: %#v", got)
	}
}

func TestLoadNormalizesRemoteHosts(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

access:
  hosts:
    VPS:
      address: "203.0.113.10"
      user: "root"
      password_env: "OPENLIGHT_VPS_PASSWORD"
      known_hosts_path: "/home/pi/.ssh/known_hosts"
      sudo: true

services:
  allowed:
    - "jitsi-web=host:VPS:compose:/opt/Jitsi/docker-compose.yml:web"
    - "jitsi-jvb=host:VPS:docker:docker-jitsi-meet_jvb_1"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	host, ok := cfg.Access.Hosts["vps"]
	if !ok {
		t.Fatalf("expected normalized host key, got %#v", cfg.Access.Hosts)
	}
	if host.Address != "203.0.113.10:22" {
		t.Fatalf("unexpected normalized host address: %q", host.Address)
	}
	if host.User != "root" || host.PasswordEnv != "OPENLIGHT_VPS_PASSWORD" || host.KnownHostsPath != "/home/pi/.ssh/known_hosts" || !host.Sudo {
		t.Fatalf("unexpected remote host config: %#v", host)
	}
	if got := cfg.Services.Allowed; len(got) != 2 || got[0] != "jitsi-web=host:vps:compose:/opt/Jitsi/docker-compose.yml:web" || got[1] != "jitsi-jvb=host:vps:docker:docker-jitsi-meet_jvb_1" {
		t.Fatalf("unexpected remote services config: %#v", got)
	}
}

func TestLoadNormalizesAccountProviders(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

accounts:
  providers:
    JITSI:
      service: "JITSI-PROSODY"
      add_command: [" prosodyctl ", " register ", " {username} ", "meet.jitsi", " {password} "]
      delete_command: [" prosodyctl ", " unregister ", " {username} ", " meet.jitsi "]
      list_command: [" prosodyctl ", " shell ", " user ", " list ", " meet.jitsi ", " {pattern} "]
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	provider, ok := cfg.Accounts.Providers["jitsi"]
	if !ok {
		t.Fatalf("expected normalized provider key, got %#v", cfg.Accounts.Providers)
	}
	if provider.Service != "jitsi-prosody" {
		t.Fatalf("unexpected normalized provider service: %q", provider.Service)
	}
	if got := provider.AddCommand; len(got) != 5 || got[0] != "prosodyctl" || got[1] != "register" || got[2] != "{username}" || got[3] != "meet.jitsi" || got[4] != "{password}" {
		t.Fatalf("unexpected add command: %#v", got)
	}
	if got := provider.DeleteCommand; len(got) != 4 || got[0] != "prosodyctl" || got[1] != "unregister" || got[2] != "{username}" || got[3] != "meet.jitsi" {
		t.Fatalf("unexpected delete command: %#v", got)
	}
	if got := provider.ListCommand; len(got) != 6 || got[0] != "prosodyctl" || got[1] != "shell" || got[2] != "user" || got[3] != "list" || got[4] != "meet.jitsi" || got[5] != "{pattern}" {
		t.Fatalf("unexpected list command: %#v", got)
	}
}

func TestLoadRejectsRemoteHostWithoutHostKeyPolicy(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

access:
  hosts:
    vps:
      address: "203.0.113.10"
      user: "root"
      password: "secret"
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected invalid remote host config to fail")
	}
}

func TestLoadAllowsCustomLLMProviderNames(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

llm:
  enabled: true
  provider: "my-provider"
  endpoint: "http://127.0.0.1:8080"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.LLM.Provider != "my-provider" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Endpoint != "http://127.0.0.1:8080" {
		t.Fatalf("unexpected llm endpoint: %q", cfg.LLM.Endpoint)
	}
}

func TestLoadRejectsInvalidWorkbenchConfig(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

workbench:
  enabled: true
  max_output_bytes: 0
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected invalid workbench config to fail")
	}
}

func clearConfigEnv(t *testing.T) {
	t.Helper()

	for _, key := range []string{
		"TELEGRAM_BOT_TOKEN",
		"TELEGRAM_MODE",
		"ALLOWED_USER_IDS",
		"ALLOWED_CHAT_IDS",
		"SQLITE_PATH",
		"ALLOWED_SERVICES",
		"WORKBENCH_ENABLED",
		"WORKBENCH_DIR",
		"WORKBENCH_ALLOWED_RUNTIMES",
		"WORKBENCH_ALLOWED_FILES",
		"WORKBENCH_MAX_OUTPUT_BYTES",
		"LLM_ENABLED",
		"LLM_PROVIDER",
		"LLM_ENDPOINT",
		"LLM_MODEL",
		"OPENAI_API_KEY",
		"LLM_EXECUTE_THRESHOLD",
		"LLM_CLARIFY_THRESHOLD",
		"LLM_DECISION_INPUT_CHARS",
		"LLM_DECISION_NUM_PREDICT",
		"LOG_LEVEL",
		"REQUEST_TIMEOUT",
		"POLL_TIMEOUT",
		"TELEGRAM_WEBHOOK_URL",
		"TELEGRAM_WEBHOOK_LISTEN_ADDR",
		"TELEGRAM_WEBHOOK_SECRET_TOKEN",
		"TELEGRAM_WEBHOOK_DROP_PENDING_UPDATES",
		"TELEGRAM_API_BASE_URL",
		"SERVICE_LOG_LINES",
		"SERVICE_MAX_LOG_CHARS",
		"NOTES_LIST_LIMIT",
		"CHAT_HISTORY_LIMIT",
		"CHAT_HISTORY_CHARS",
		"CHAT_MAX_RESPONSE_CHARS",
	} {
		t.Setenv(key, "")
	}
}

func writeConfig(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
}
