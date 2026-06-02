package config

import (
	"os"
	"path/filepath"
	"strings"
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
  enabled: true
  allowed: ["/tmp/openlight", "/home/pi/scripts", "/tmp/openlight"]
  allow_write: true
  redact_secrets: false
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

watch:
  enabled: true
  poll_interval: 45s
  ask_ttl: 20m

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

memory:
  enabled: true
  db_path: "./memory.db"
  list_limit: 12

voice:
  enabled: true
  provider: "whisper_cli"
  whisper_cli_path: "/opt/homebrew/bin/whisper-cli"
  model_path: "~/models/ggml-small.bin"
  ffmpeg_path: "/opt/homebrew/bin/ffmpeg"
  reply_with_transcript: true

browser:
  enabled: true
  node_path: "/opt/homebrew/bin/node"
  helper_path: "./tools/browser-agent/index.mjs"
  allowed_domains: ["example.com", "github.com"]
  allow_all_domains: true
  allow_private_network: false
  artifacts_dir: "./data/browser"
  timeout_seconds: 18

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
	if !cfg.Files.Enabled || !cfg.Files.AllowWrite || cfg.Files.RedactSecrets || cfg.Files.MaxReadBytes != 8192 || cfg.Files.ListLimit != 25 {
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
	if !cfg.Watch.Enabled || cfg.Watch.PollInterval != 45*time.Second || cfg.Watch.AskTTL != 20*time.Minute {
		t.Fatalf("unexpected watch config: %#v", cfg.Watch)
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
	if !cfg.Memory.Enabled || cfg.Memory.DBPath != "./memory.db" || cfg.Memory.ListLimit != 12 {
		t.Fatalf("unexpected memory config: %#v", cfg.Memory)
	}
	wantModel := "~/models/ggml-small.bin"
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		wantModel = filepath.Join(home, "models/ggml-small.bin")
	}
	if !cfg.Voice.Enabled || cfg.Voice.Provider != "whisper_cli" || cfg.Voice.ModelPath != wantModel || !cfg.Voice.ReplyWithTranscript {
		t.Fatalf("unexpected voice config: %#v", cfg.Voice)
	}
	if !cfg.Browser.Enabled || !cfg.Browser.AllowAllDomains || cfg.Browser.NodePath != "/opt/homebrew/bin/node" || cfg.Browser.TimeoutSeconds != 18 {
		t.Fatalf("unexpected browser config: %#v", cfg.Browser)
	}
	if got := cfg.Browser.AllowedDomains; len(got) != 2 || got[0] != "example.com" || got[1] != "github.com" {
		t.Fatalf("unexpected allowed browser domains: %#v", got)
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
	if !cfg.Telegram.ShouldDropPendingUpdates() {
		t.Fatalf("expected drop_pending_updates to default to true")
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
	if !cfg.Watch.Enabled || cfg.Watch.PollInterval != 15*time.Second || cfg.Watch.AskTTL != 10*time.Minute {
		t.Fatalf("unexpected watch defaults: %#v", cfg.Watch)
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
	if !cfg.Memory.Enabled || cfg.Memory.ListLimit != 20 {
		t.Fatalf("unexpected memory defaults: %#v", cfg.Memory)
	}
	if cfg.Voice.Provider != "whisper_cli" || cfg.Voice.WhisperCLIPath != "whisper-cli" || cfg.Voice.FFmpegPath != "ffmpeg" {
		t.Fatalf("unexpected voice defaults: %#v", cfg.Voice)
	}
	if cfg.Browser.NodePath != "node" || cfg.Browser.TimeoutSeconds != 20 {
		t.Fatalf("unexpected browser defaults: %#v", cfg.Browser)
	}
	if cfg.Agent.RequestTimeout != 5*time.Second {
		t.Fatalf("unexpected request timeout: %v", cfg.Agent.RequestTimeout)
	}
	if cfg.Log.Level != "info" {
		t.Fatalf("unexpected log level: %q", cfg.Log.Level)
	}
}

func TestLoadDropPendingUpdatesExplicitFalse(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"
  drop_pending_updates: false

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.Telegram.ShouldDropPendingUpdates() {
		t.Fatalf("expected drop_pending_updates=false to be honoured")
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

func TestLoadBrowserAllowAllDomainsWithoutList(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

browser:
  enabled: true
  allow_all_domains: true
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.Browser.Enabled || !cfg.Browser.AllowAllDomains {
		t.Fatalf("unexpected browser config: %#v", cfg.Browser)
	}
	if len(cfg.Browser.AllowedDomains) != 0 {
		t.Fatalf("expected allowed domains to stay empty, got %#v", cfg.Browser.AllowedDomains)
	}
}

func TestLoadAppliesLLMProfileFromConfig(t *testing.T) {
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
  profile: "ollama"
  execute_threshold: 0.80
  clarify_threshold: 0.60
  decision_input_chars: 160
  decision_num_predict: 48
  profiles:
    ollama:
      provider: "ollama"
      endpoint: "http://127.0.0.1:11434"
      model: "qwen2.5:0.5b"
      decision_num_predict: 128
    openai:
      provider: "openai"
      model: "gpt-4o-mini"
      api_key: "sk-test"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.LLM.Profile != "ollama" {
		t.Fatalf("unexpected llm profile: %q", cfg.LLM.Profile)
	}
	if cfg.LLM.Provider != "ollama" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Endpoint != "http://127.0.0.1:11434" {
		t.Fatalf("unexpected llm endpoint: %q", cfg.LLM.Endpoint)
	}
	if cfg.LLM.Model != "qwen2.5:0.5b" {
		t.Fatalf("unexpected llm model: %q", cfg.LLM.Model)
	}
	if cfg.LLM.DecisionNumPredict != 128 {
		t.Fatalf("unexpected llm decision_num_predict: %d", cfg.LLM.DecisionNumPredict)
	}
}

func TestLoadAppliesLLMProfileFromEnv(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLM_PROFILE", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-from-env")

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
  profile: "ollama"
  execute_threshold: 0.80
  clarify_threshold: 0.60
  decision_input_chars: 160
  decision_num_predict: 48
  profiles:
    ollama:
      provider: "ollama"
      endpoint: "http://127.0.0.1:11434"
      model: "qwen2.5:0.5b"
    openai:
      provider: "openai"
      model: "gpt-4o-mini"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if cfg.LLM.Profile != "openai" {
		t.Fatalf("unexpected llm profile: %q", cfg.LLM.Profile)
	}
	if cfg.LLM.Provider != "openai" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected llm model: %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "sk-from-env" {
		t.Fatalf("unexpected llm api key: %q", cfg.LLM.APIKey)
	}
	if cfg.LLM.Endpoint != defaultOpenAIAPIBaseURL {
		t.Fatalf("unexpected llm endpoint default: %q", cfg.LLM.Endpoint)
	}
}

func TestLoadAcceptsMatchingDirectLLMProviderFromEnvProfile(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLM_PROFILE", "openai")

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

	if cfg.LLM.Profile != "openai" {
		t.Fatalf("unexpected llm profile: %q", cfg.LLM.Profile)
	}
	if cfg.LLM.Provider != "openai" {
		t.Fatalf("unexpected llm provider: %q", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Fatalf("unexpected llm model: %q", cfg.LLM.Model)
	}
	if cfg.LLM.APIKey != "sk-test" {
		t.Fatalf("unexpected llm api key: %q", cfg.LLM.APIKey)
	}
}

func TestLoadRejectsMismatchedDirectLLMProviderFromEnvProfile(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("LLM_PROFILE", "ollama")

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

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected mismatched direct llm provider to fail")
	}
	if !strings.Contains(err.Error(), `llm profile "ollama" requested but llm.profiles is not configured; current direct provider is "openai"`) {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoadRejectsUnknownLLMProfile(t *testing.T) {
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
  profile: "missing"
  profiles:
    ollama:
      provider: "ollama"
      endpoint: "http://127.0.0.1:11434"
      model: "qwen2.5:0.5b"
`)

	_, err := Load(configPath)
	if err == nil {
		t.Fatal("expected missing llm profile to fail")
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
    - "synapse=compose:/home/pi/Matrix/docker-compose.yml"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if got := cfg.Services.Allowed; len(got) != 1 || got[0] != "synapse=compose:/home/pi/Matrix/docker-compose.yml" {
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

func TestLoadAcceptsNodesAlias(t *testing.T) {
	clearConfigEnv(t)

	configPath := filepath.Join(t.TempDir(), "agent.yaml")
	writeConfig(t, configPath, `
telegram:
  bot_token: "token"

auth:
  allowed_user_ids: [1]

storage:
  sqlite_path: "./agent.db"

nodes:
  vps:
    address: "203.0.113.10"
    user: "root"
    password_env: "OPENLIGHT_VPS_PASSWORD"
    known_hosts_path: "/home/pi/.ssh/known_hosts"

services:
  allowed:
    - "matrix=node:vps:compose:/opt/matrix/docker-compose.yml:synapse"
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	host, ok := cfg.Access.Hosts["vps"]
	if !ok {
		t.Fatalf("expected nodes entry to merge into access.hosts, got %#v", cfg.Access.Hosts)
	}
	if host.Address != "203.0.113.10:22" {
		t.Fatalf("unexpected node address: %q", host.Address)
	}
	if got := cfg.Services.Allowed; len(got) != 1 || got[0] != "matrix=host:vps:compose:/opt/matrix/docker-compose.yml:synapse" {
		t.Fatalf("expected node: prefix to normalize to host:, got %#v", got)
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
      vars:
        server_name: " meet.jitsi "
      vars_env:
        admin_token: " OPENLIGHT_ADMIN_TOKEN "
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
	if provider.Vars["server_name"] != "meet.jitsi" {
		t.Fatalf("unexpected normalized provider vars: %#v", provider.Vars)
	}
	if provider.VarsEnv["admin_token"] != "OPENLIGHT_ADMIN_TOKEN" {
		t.Fatalf("unexpected normalized provider vars_env: %#v", provider.VarsEnv)
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
		"LLM_PROFILE",
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
		"WATCH_ENABLED",
		"WATCH_POLL_INTERVAL",
		"WATCH_ASK_TTL",
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

func TestLLMConfigResolveProfileInheritsTopLevel(t *testing.T) {
	t.Parallel()

	cfg := LLMConfig{
		Provider:           "ollama",
		Endpoint:           "http://127.0.0.1:11434",
		Model:              "gemma3-12b-8k",
		ExecuteThreshold:   0.8,
		ClarifyThreshold:   0.6,
		DecisionInputChars: 160,
		DecisionNumPredict: 48,
		Profiles: map[string]LLMProfileConfig{
			"fast": {
				Model:              "gemma3:4b-4k",
				DecisionNumPredict: 64,
			},
			"smart": {
				Model:              "gemma3-12b-8k",
				DecisionNumPredict: 256,
			},
		},
	}

	if !cfg.HasProfile("fast") || !cfg.HasProfile("smart") {
		t.Fatalf("HasProfile should report both profiles")
	}

	fast := cfg.ResolveProfile("fast")
	if fast.Provider != "ollama" || fast.Endpoint != "http://127.0.0.1:11434" {
		t.Fatalf("fast profile should inherit provider/endpoint from top-level: %#v", fast)
	}
	if fast.Model != "gemma3:4b-4k" {
		t.Fatalf("fast.Model = %q, want gemma3:4b-4k", fast.Model)
	}
	if fast.DecisionNumPredict != 64 {
		t.Fatalf("fast.DecisionNumPredict = %d, want 64", fast.DecisionNumPredict)
	}
	if fast.DecisionInputChars != 160 {
		t.Fatalf("fast.DecisionInputChars should inherit (160), got %d", fast.DecisionInputChars)
	}

	smart := cfg.ResolveProfile("smart")
	if smart.Model != "gemma3-12b-8k" {
		t.Fatalf("smart.Model = %q, want gemma3-12b-8k", smart.Model)
	}
	if smart.DecisionNumPredict != 256 {
		t.Fatalf("smart.DecisionNumPredict = %d, want 256", smart.DecisionNumPredict)
	}
}

func TestLLMConfigKeepAliveInheritsAndOverrides(t *testing.T) {
	t.Parallel()

	cfg := LLMConfig{
		Provider:  "ollama",
		Endpoint:  "http://127.0.0.1:11434",
		Model:     "gemma3-12b-8k",
		KeepAlive: "30m",
		Profiles: map[string]LLMProfileConfig{
			"fast": {
				Model: "qwen2.5:1.5b",
			},
			"smart": {
				Model:     "gemma3-12b-8k",
				KeepAlive: "-1",
			},
		},
	}

	fast := cfg.ResolveProfile("fast")
	if fast.KeepAlive != "30m" {
		t.Fatalf("fast should inherit top-level keep_alive=30m, got %q", fast.KeepAlive)
	}

	smart := cfg.ResolveProfile("smart")
	if smart.KeepAlive != "-1" {
		t.Fatalf("smart should override keep_alive to -1, got %q", smart.KeepAlive)
	}
}

func TestLLMWarmupConfigIncludesAndKeepAlive(t *testing.T) {
	t.Parallel()

	w := LLMWarmupConfig{Enabled: true, Profiles: []string{"smart"}, KeepAlive: -1}
	if !w.Includes("smart") {
		t.Fatalf("expected smart to be in warmup list")
	}
	if w.Includes("fast") {
		t.Fatalf("fast should not be in warmup list")
	}
	if w.KeepAliveString() != "-1" {
		t.Fatalf("KeepAliveString for int -1 should be %q, got %q", "-1", w.KeepAliveString())
	}

	w.KeepAlive = "30m"
	if w.KeepAliveString() != "30m" {
		t.Fatalf("KeepAliveString string passthrough failed: %q", w.KeepAliveString())
	}

	w.KeepAlive = float64(-1) // YAML decodes plain integers as float64 in some paths
	if w.KeepAliveString() != "-1" {
		t.Fatalf("KeepAliveString for float64 -1 should be %q, got %q", "-1", w.KeepAliveString())
	}

	w.Enabled = false
	if w.Includes("smart") {
		t.Fatalf("disabled warmup should match nothing")
	}
}

func TestLoadWarmupDefaultsApplied(t *testing.T) {
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
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "gemma3-12b-8k"
`)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if !cfg.LLM.Warmup.Enabled {
		t.Fatalf("default warmup should be enabled")
	}
	if !cfg.LLM.Warmup.Includes("smart") {
		t.Fatalf("default warmup should include smart, got %v", cfg.LLM.Warmup.Profiles)
	}
	if cfg.LLM.Warmup.KeepAliveString() != "-1" {
		t.Fatalf("default warmup keep_alive should be -1, got %q", cfg.LLM.Warmup.KeepAliveString())
	}
	if cfg.LLM.Warmup.PromptOrDefault() != "warmup" {
		t.Fatalf("default warmup prompt should be warmup, got %q", cfg.LLM.Warmup.PromptOrDefault())
	}
}

func TestLoadWarmupExplicitIntKeepAlive(t *testing.T) {
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
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "gemma3-12b-8k"
  warmup:
    enabled: true
    profiles: ["smart"]
    keep_alive: -1
    prompt: "warmup"
`)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.LLM.Warmup.KeepAliveString() != "-1" {
		t.Fatalf("expected keep_alive=-1, got %q", cfg.LLM.Warmup.KeepAliveString())
	}
}

func TestLoadWarmupDisabled(t *testing.T) {
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
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "gemma3-12b-8k"
  warmup:
    enabled: false
`)
	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if cfg.LLM.Warmup.Enabled {
		t.Fatalf("warmup should be disabled")
	}
	if cfg.LLM.Warmup.Includes("smart") {
		t.Fatalf("disabled warmup must match nothing")
	}
}

func TestRoleBasedKeepAliveDefaults(t *testing.T) {
	t.Parallel()

	cfg := LLMConfig{
		Provider: "ollama",
		Model:    "gemma3-12b-8k",
		Profiles: map[string]LLMProfileConfig{
			"smart":  {Model: "gemma3-12b-8k"},
			"fast":   {Model: "qwen2.5:1.5b"},
			"vision": {Model: "qwen2.5vl:3b"},
			"misc":   {Model: "foo"},
		},
	}

	cases := map[string]string{
		"fast":   "-1",
		"smart":  "10m",
		"vision": "5m",
		"misc":   "10m",
	}
	for role, want := range cases {
		got := cfg.ResolveProfile(role).KeepAlive
		if got != want {
			t.Errorf("role %q default keep_alive = %q, want %q", role, got, want)
		}
	}

	// Explicit profile-level override wins.
	cfg.Profiles["smart"] = LLMProfileConfig{Model: "gemma3-12b-8k", KeepAlive: "24h"}
	if got := cfg.ResolveProfile("smart").KeepAlive; got != "24h" {
		t.Fatalf("explicit profile keep_alive should win, got %q", got)
	}
}

func TestLLMConfigResolveNumCtx(t *testing.T) {
	t.Parallel()

	cfg := LLMConfig{
		Provider: "ollama",
		Model:    "gemma3-12b-8k",
		NumCtx:   2048,
		Profiles: map[string]LLMProfileConfig{
			"fast":   {Model: "qwen2.5:1.5b", NumCtx: 1024},
			"smart":  {Model: "gemma3-12b-8k"}, // inherits top-level
			"vision": {Model: "qwen2.5vl:3b", NumCtx: 0},
		},
	}

	cases := map[string]int{
		"fast":   1024, // profile override
		"smart":  2048, // top-level inherited
		"vision": 2048, // explicit zero == inherit
	}
	for role, want := range cases {
		if got := cfg.ResolveProfile(role).NumCtx; got != want {
			t.Errorf("role %q num_ctx = %d, want %d", role, got, want)
		}
	}

	// Missing profile falls back to top-level.
	if got := cfg.ResolveProfile("missing").NumCtx; got != 2048 {
		t.Fatalf("missing profile should inherit top-level num_ctx, got %d", got)
	}

	// Top-level zero leaves resolved value at zero (Ollama default).
	cfgZero := LLMConfig{Provider: "ollama", Model: "x"}
	if got := cfgZero.ResolveProfile("fast").NumCtx; got != 0 {
		t.Fatalf("top-level zero should resolve to zero, got %d", got)
	}
}

func TestLLMConfigResolveMissingProfileFallsBackToTopLevel(t *testing.T) {
	t.Parallel()

	cfg := LLMConfig{
		Provider: "ollama",
		Endpoint: "http://127.0.0.1:11434",
		Model:    "gemma3-12b-8k",
	}

	if cfg.HasProfile("fast") {
		t.Fatalf("HasProfile should return false when profiles are not configured")
	}

	resolved := cfg.ResolveProfile("fast")
	if resolved.Provider != "ollama" || resolved.Model != "gemma3-12b-8k" {
		t.Fatalf("missing profile should fall back to top-level llm fields: %#v", resolved)
	}
}

func TestLoadAcceptsFastSmartProfilesWithoutSelection(t *testing.T) {
	// Validates the new two-tier shape: both `fast` and `smart` profiles
	// declared simultaneously, no `llm.profile` selection. Each profile
	// may omit `provider` and inherit it from the top-level.
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
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "gemma3-12b-8k"
  profiles:
    fast:
      model: "gemma3:4b-4k"
      decision_num_predict: 64
    smart:
      model: "gemma3-12b-8k"
      decision_num_predict: 256
`)

	cfg, err := Load(configPath)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}

	if !cfg.LLM.HasProfile("fast") || !cfg.LLM.HasProfile("smart") {
		t.Fatalf("expected both fast and smart profiles to be loaded")
	}

	fast := cfg.LLM.ResolveProfile("fast")
	if fast.Provider != "ollama" || fast.Model != "gemma3:4b-4k" || fast.DecisionNumPredict != 64 {
		t.Fatalf("unexpected fast profile resolution: %#v", fast)
	}

	smart := cfg.LLM.ResolveProfile("smart")
	if smart.Provider != "ollama" || smart.Model != "gemma3-12b-8k" || smart.DecisionNumPredict != 256 {
		t.Fatalf("unexpected smart profile resolution: %#v", smart)
	}
}
