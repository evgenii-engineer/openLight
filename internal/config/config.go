package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultTelegramAPIBaseURL = "https://api.telegram.org"
const defaultOpenAIAPIBaseURL = "https://api.openai.com/v1"

type Config struct {
	Telegram  TelegramConfig  `yaml:"telegram"`
	Auth      AuthConfig      `yaml:"auth"`
	Storage   StorageConfig   `yaml:"storage"`
	Access    AccessConfig    `yaml:"access"`
	Accounts  AccountsConfig  `yaml:"accounts"`
	Files     FilesConfig     `yaml:"files"`
	Workbench WorkbenchConfig `yaml:"workbench"`
	Services  ServicesConfig  `yaml:"services"`
	Watch     WatchConfig     `yaml:"watch"`
	LLM       LLMConfig       `yaml:"llm"`
	Chat      ChatConfig      `yaml:"chat"`
	Notes     NotesConfig     `yaml:"notes"`
	Agent     AgentConfig     `yaml:"agent"`
	Log       LogConfig       `yaml:"log"`
}

type TelegramConfig struct {
	BotToken    string        `yaml:"bot_token"`
	APIBaseURL  string        `yaml:"api_base_url"`
	Mode        string        `yaml:"mode"`
	PollTimeout time.Duration `yaml:"poll_timeout"`
	Webhook     WebhookConfig `yaml:"webhook"`
}

type WebhookConfig struct {
	URL                string `yaml:"url"`
	ListenAddr         string `yaml:"listen_addr"`
	SecretToken        string `yaml:"secret_token"`
	DropPendingUpdates bool   `yaml:"drop_pending_updates"`
}

type AuthConfig struct {
	AllowedUserIDs []int64 `yaml:"allowed_user_ids"`
	AllowedChatIDs []int64 `yaml:"allowed_chat_ids"`
}

type StorageConfig struct {
	SQLitePath string `yaml:"sqlite_path"`
}

type AccessConfig struct {
	Hosts map[string]RemoteHostConfig `yaml:"hosts"`
}

type RemoteHostConfig struct {
	Address                 string `yaml:"address"`
	User                    string `yaml:"user"`
	Password                string `yaml:"password"`
	PasswordEnv             string `yaml:"password_env"`
	PrivateKeyPath          string `yaml:"private_key_path"`
	PrivateKeyPassphrase    string `yaml:"private_key_passphrase"`
	PrivateKeyPassphraseEnv string `yaml:"private_key_passphrase_env"`
	KnownHostsPath          string `yaml:"known_hosts_path"`
	InsecureIgnoreHostKey   bool   `yaml:"insecure_ignore_host_key"`
	Sudo                    bool   `yaml:"sudo"`
}

type AccountsConfig struct {
	Providers map[string]AccountProviderConfig `yaml:"providers"`
}

type AccountProviderConfig struct {
	Service       string            `yaml:"service"`
	Vars          map[string]string `yaml:"vars"`
	VarsEnv       map[string]string `yaml:"vars_env"`
	AddCommand    []string          `yaml:"add_command"`
	DeleteCommand []string          `yaml:"delete_command"`
	ListCommand   []string          `yaml:"list_command"`
}

type FilesConfig struct {
	Allowed      []string `yaml:"allowed"`
	MaxReadBytes int      `yaml:"max_read_bytes"`
	ListLimit    int      `yaml:"list_limit"`
}

type WorkbenchConfig struct {
	Enabled         bool     `yaml:"enabled"`
	WorkspaceDir    string   `yaml:"workspace_dir"`
	AllowedRuntimes []string `yaml:"allowed_runtimes"`
	AllowedFiles    []string `yaml:"allowed_files"`
	MaxOutputBytes  int      `yaml:"max_output_bytes"`
}

type ServicesConfig struct {
	Allowed     []string `yaml:"allowed"`
	LogLines    int      `yaml:"log_lines"`
	MaxLogChars int      `yaml:"max_log_chars"`
}

type WatchConfig struct {
	Enabled      bool          `yaml:"enabled"`
	PollInterval time.Duration `yaml:"poll_interval"`
	AskTTL       time.Duration `yaml:"ask_ttl"`
}

type LLMConfig struct {
	Enabled            bool    `yaml:"enabled"`
	Provider           string  `yaml:"provider"`
	Endpoint           string  `yaml:"endpoint"`
	Model              string  `yaml:"model"`
	APIKey             string  `yaml:"api_key"`
	ExecuteThreshold   float64 `yaml:"execute_threshold"`
	ClarifyThreshold   float64 `yaml:"clarify_threshold"`
	DecisionInputChars int     `yaml:"decision_input_chars"`
	DecisionNumPredict int     `yaml:"decision_num_predict"`
}

type ChatConfig struct {
	HistoryLimit     int `yaml:"history_limit"`
	HistoryChars     int `yaml:"history_chars"`
	MaxResponseChars int `yaml:"max_response_chars"`
}

type NotesConfig struct {
	ListLimit int `yaml:"list_limit"`
}

type AgentConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

func Load(path string) (Config, error) {
	cfg := defaultConfig()

	if path != "" {
		content, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config file: %w", err)
		}

		if err := yaml.Unmarshal(content, &cfg); err != nil {
			return Config{}, fmt.Errorf("parse config file: %w", err)
		}
	}

	overrideFromEnv(&cfg)
	normalize(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

func defaultConfig() Config {
	return Config{
		Telegram: TelegramConfig{
			APIBaseURL:  defaultTelegramAPIBaseURL,
			Mode:        "polling",
			PollTimeout: 25 * time.Second,
			Webhook: WebhookConfig{
				ListenAddr: ":8081",
			},
		},
		Files: FilesConfig{
			MaxReadBytes: 4096,
			ListLimit:    40,
		},
		Workbench: WorkbenchConfig{
			WorkspaceDir:   "/tmp/openlight",
			MaxOutputBytes: 8192,
		},
		Services: ServicesConfig{
			LogLines:    100,
			MaxLogChars: 3000,
		},
		Watch: WatchConfig{
			Enabled:      true,
			PollInterval: 15 * time.Second,
			AskTTL:       10 * time.Minute,
		},
		LLM: LLMConfig{
			Provider:           "generic",
			ExecuteThreshold:   0.80,
			ClarifyThreshold:   0.60,
			DecisionInputChars: 160,
			DecisionNumPredict: 128,
		},
		Chat: ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     900,
			MaxResponseChars: 400,
		},
		Notes: NotesConfig{
			ListLimit: 20,
		},
		Agent: AgentConfig{
			RequestTimeout: 5 * time.Second,
		},
		Log: LogConfig{
			Level: "info",
		},
	}
}

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.Telegram.BotToken) == "":
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	case c.Telegram.Mode != "polling" && c.Telegram.Mode != "webhook":
		return errors.New("telegram.mode must be one of: polling, webhook")
	case strings.TrimSpace(c.Storage.SQLitePath) == "":
		return errors.New("SQLITE_PATH is required")
	case len(c.Auth.AllowedUserIDs) == 0 && len(c.Auth.AllowedChatIDs) == 0:
		return errors.New("at least one of ALLOWED_USER_IDS or ALLOWED_CHAT_IDS is required")
	case c.LLM.Enabled && strings.TrimSpace(c.LLM.Provider) == "":
		return errors.New("LLM_PROVIDER is required when LLM_ENABLED is true")
	case c.LLM.ExecuteThreshold <= 0 || c.LLM.ExecuteThreshold > 1:
		return errors.New("llm.execute_threshold must be greater than zero and less than or equal to one")
	case c.LLM.ClarifyThreshold <= 0 || c.LLM.ClarifyThreshold >= c.LLM.ExecuteThreshold:
		return errors.New("llm.clarify_threshold must be greater than zero and less than llm.execute_threshold")
	case c.LLM.DecisionInputChars <= 0:
		return errors.New("llm.decision_input_chars must be greater than zero")
	case c.LLM.DecisionNumPredict <= 0:
		return errors.New("llm.decision_num_predict must be greater than zero")
	case c.Agent.RequestTimeout <= 0:
		return errors.New("agent.request_timeout must be greater than zero")
	case c.Telegram.PollTimeout <= 0:
		return errors.New("telegram.poll_timeout must be greater than zero")
	case c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.Webhook.URL) == "":
		return errors.New("telegram.webhook.url is required when telegram.mode is webhook")
	case c.Telegram.Mode == "webhook" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Telegram.Webhook.URL)), "https://"):
		return errors.New("telegram.webhook.url must start with https://")
	case c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.Webhook.ListenAddr) == "":
		return errors.New("telegram.webhook.listen_addr is required when telegram.mode is webhook")
	case c.Files.MaxReadBytes <= 0:
		return errors.New("files.max_read_bytes must be greater than zero")
	case c.Files.ListLimit <= 0:
		return errors.New("files.list_limit must be greater than zero")
	case c.Workbench.Enabled && strings.TrimSpace(c.Workbench.WorkspaceDir) == "":
		return errors.New("workbench.workspace_dir is required when workbench.enabled is true")
	case c.Workbench.MaxOutputBytes <= 0:
		return errors.New("workbench.max_output_bytes must be greater than zero")
	case c.Services.LogLines <= 0:
		return errors.New("services.log_lines must be greater than zero")
	case c.Services.MaxLogChars <= 0:
		return errors.New("services.max_log_chars must be greater than zero")
	case c.Watch.PollInterval <= 0:
		return errors.New("watch.poll_interval must be greater than zero")
	case c.Watch.AskTTL <= 0:
		return errors.New("watch.ask_ttl must be greater than zero")
	case c.Notes.ListLimit <= 0:
		return errors.New("notes.list_limit must be greater than zero")
	case c.Chat.HistoryLimit <= 0:
		return errors.New("chat.history_limit must be greater than zero")
	case c.Chat.HistoryChars <= 0:
		return errors.New("chat.history_chars must be greater than zero")
	case c.Chat.MaxResponseChars <= 0:
		return errors.New("chat.max_response_chars must be greater than zero")
	}

	for name, host := range c.Access.Hosts {
		if name == "" {
			return errors.New("access.hosts keys must not be empty")
		}
		if strings.TrimSpace(host.Address) == "" {
			return fmt.Errorf("access.hosts.%s.address is required", name)
		}
		if strings.TrimSpace(host.User) == "" {
			return fmt.Errorf("access.hosts.%s.user is required", name)
		}
		if strings.TrimSpace(host.Password) == "" &&
			strings.TrimSpace(host.PasswordEnv) == "" &&
			strings.TrimSpace(host.PrivateKeyPath) == "" {
			return fmt.Errorf("access.hosts.%s must set password, password_env, or private_key_path", name)
		}
		if !host.InsecureIgnoreHostKey && strings.TrimSpace(host.KnownHostsPath) == "" {
			return fmt.Errorf("access.hosts.%s.known_hosts_path is required unless insecure_ignore_host_key is true", name)
		}
	}

	for name, provider := range c.Accounts.Providers {
		if name == "" {
			return errors.New("accounts.providers keys must not be empty")
		}
		if strings.TrimSpace(provider.Service) == "" {
			return fmt.Errorf("accounts.providers.%s.service is required", name)
		}
		for key, value := range provider.Vars {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return fmt.Errorf("accounts.providers.%s.vars must not contain empty keys or values", name)
			}
		}
		for key, value := range provider.VarsEnv {
			if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
				return fmt.Errorf("accounts.providers.%s.vars_env must not contain empty keys or values", name)
			}
		}
		if len(provider.AddCommand) == 0 && len(provider.DeleteCommand) == 0 && len(provider.ListCommand) == 0 {
			return fmt.Errorf("accounts.providers.%s must set add_command, delete_command, or list_command", name)
		}
	}

	return nil
}

func overrideFromEnv(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); value != "" {
		cfg.Telegram.BotToken = value
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_MODE")); value != "" {
		cfg.Telegram.Mode = value
	}
	if value := parseInt64ListEnv("ALLOWED_USER_IDS"); len(value) > 0 {
		cfg.Auth.AllowedUserIDs = value
	}
	if value := parseInt64ListEnv("ALLOWED_CHAT_IDS"); len(value) > 0 {
		cfg.Auth.AllowedChatIDs = value
	}
	if value := strings.TrimSpace(os.Getenv("SQLITE_PATH")); value != "" {
		cfg.Storage.SQLitePath = value
	}
	if value := parseStringListEnv("ALLOWED_SERVICES"); value != nil {
		cfg.Services.Allowed = value
	}
	if value := parseStringListEnv("ALLOWED_FILE_ROOTS"); value != nil {
		cfg.Files.Allowed = value
	}
	if value := strings.TrimSpace(os.Getenv("WORKBENCH_ENABLED")); value != "" {
		cfg.Workbench.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("WORKBENCH_DIR")); value != "" {
		cfg.Workbench.WorkspaceDir = value
	}
	if value := parseStringListEnv("WORKBENCH_ALLOWED_RUNTIMES"); value != nil {
		cfg.Workbench.AllowedRuntimes = value
	}
	if value := parseStringListEnv("WORKBENCH_ALLOWED_FILES"); value != nil {
		cfg.Workbench.AllowedFiles = value
	}
	if value := strings.TrimSpace(os.Getenv("WORKBENCH_MAX_OUTPUT_BYTES")); value != "" {
		cfg.Workbench.MaxOutputBytes = parseInt(value, cfg.Workbench.MaxOutputBytes)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_ENABLED")); value != "" {
		cfg.LLM.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_PROVIDER")); value != "" {
		cfg.LLM.Provider = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_ENDPOINT")); value != "" {
		cfg.LLM.Endpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_MODEL")); value != "" {
		cfg.LLM.Model = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENAI_API_KEY")); value != "" {
		cfg.LLM.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_EXECUTE_THRESHOLD")); value != "" {
		cfg.LLM.ExecuteThreshold = parseFloat(value, cfg.LLM.ExecuteThreshold)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_CLARIFY_THRESHOLD")); value != "" {
		cfg.LLM.ClarifyThreshold = parseFloat(value, cfg.LLM.ClarifyThreshold)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_DECISION_INPUT_CHARS")); value != "" {
		cfg.LLM.DecisionInputChars = parseInt(value, cfg.LLM.DecisionInputChars)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_DECISION_NUM_PREDICT")); value != "" {
		cfg.LLM.DecisionNumPredict = parseInt(value, cfg.LLM.DecisionNumPredict)
	}
	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		cfg.Log.Level = value
	}
	if value := strings.TrimSpace(os.Getenv("REQUEST_TIMEOUT")); value != "" {
		cfg.Agent.RequestTimeout = parseDuration(value, cfg.Agent.RequestTimeout)
	}
	if value := strings.TrimSpace(os.Getenv("POLL_TIMEOUT")); value != "" {
		cfg.Telegram.PollTimeout = parseDuration(value, cfg.Telegram.PollTimeout)
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_URL")); value != "" {
		cfg.Telegram.Webhook.URL = value
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_LISTEN_ADDR")); value != "" {
		cfg.Telegram.Webhook.ListenAddr = value
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_SECRET_TOKEN")); value != "" {
		cfg.Telegram.Webhook.SecretToken = value
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_WEBHOOK_DROP_PENDING_UPDATES")); value != "" {
		cfg.Telegram.Webhook.DropPendingUpdates = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE_URL")); value != "" {
		cfg.Telegram.APIBaseURL = value
	}
	if value := strings.TrimSpace(os.Getenv("SERVICE_LOG_LINES")); value != "" {
		cfg.Services.LogLines = parseInt(value, cfg.Services.LogLines)
	}
	if value := strings.TrimSpace(os.Getenv("SERVICE_MAX_LOG_CHARS")); value != "" {
		cfg.Services.MaxLogChars = parseInt(value, cfg.Services.MaxLogChars)
	}
	if value := strings.TrimSpace(os.Getenv("WATCH_ENABLED")); value != "" {
		cfg.Watch.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("WATCH_POLL_INTERVAL")); value != "" {
		cfg.Watch.PollInterval = parseDuration(value, cfg.Watch.PollInterval)
	}
	if value := strings.TrimSpace(os.Getenv("WATCH_ASK_TTL")); value != "" {
		cfg.Watch.AskTTL = parseDuration(value, cfg.Watch.AskTTL)
	}
	if value := strings.TrimSpace(os.Getenv("FILE_MAX_READ_BYTES")); value != "" {
		cfg.Files.MaxReadBytes = parseInt(value, cfg.Files.MaxReadBytes)
	}
	if value := strings.TrimSpace(os.Getenv("FILE_LIST_LIMIT")); value != "" {
		cfg.Files.ListLimit = parseInt(value, cfg.Files.ListLimit)
	}
	if value := strings.TrimSpace(os.Getenv("NOTES_LIST_LIMIT")); value != "" {
		cfg.Notes.ListLimit = parseInt(value, cfg.Notes.ListLimit)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_HISTORY_LIMIT")); value != "" {
		cfg.Chat.HistoryLimit = parseInt(value, cfg.Chat.HistoryLimit)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_HISTORY_CHARS")); value != "" {
		cfg.Chat.HistoryChars = parseInt(value, cfg.Chat.HistoryChars)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_MAX_RESPONSE_CHARS")); value != "" {
		cfg.Chat.MaxResponseChars = parseInt(value, cfg.Chat.MaxResponseChars)
	}
}

func normalize(cfg *Config) {
	cfg.Telegram.BotToken = strings.TrimSpace(cfg.Telegram.BotToken)
	cfg.Telegram.APIBaseURL = strings.TrimRight(strings.TrimSpace(cfg.Telegram.APIBaseURL), "/")
	if cfg.Telegram.APIBaseURL == "" {
		cfg.Telegram.APIBaseURL = defaultTelegramAPIBaseURL
	}
	cfg.Telegram.Mode = strings.ToLower(strings.TrimSpace(cfg.Telegram.Mode))
	if cfg.Telegram.Mode == "" {
		cfg.Telegram.Mode = "polling"
	}
	cfg.Telegram.Webhook.URL = strings.TrimSpace(cfg.Telegram.Webhook.URL)
	cfg.Telegram.Webhook.ListenAddr = strings.TrimSpace(cfg.Telegram.Webhook.ListenAddr)
	if cfg.Telegram.Webhook.ListenAddr == "" {
		cfg.Telegram.Webhook.ListenAddr = ":8081"
	}
	cfg.Telegram.Webhook.SecretToken = strings.TrimSpace(cfg.Telegram.Webhook.SecretToken)

	cfg.Storage.SQLitePath = strings.TrimSpace(cfg.Storage.SQLitePath)
	cfg.Access.Hosts = normalizeRemoteHosts(cfg.Access.Hosts)
	cfg.Accounts.Providers = normalizeAccountProviders(cfg.Accounts.Providers)
	cfg.Files.Allowed = normalizePaths(cfg.Files.Allowed)
	cfg.Workbench.WorkspaceDir = strings.TrimSpace(cfg.Workbench.WorkspaceDir)
	if cfg.Workbench.WorkspaceDir == "" {
		cfg.Workbench.WorkspaceDir = "/tmp/openlight"
	}
	cfg.Workbench.AllowedRuntimes = normalizeStrings(cfg.Workbench.AllowedRuntimes)
	cfg.Workbench.AllowedFiles = normalizePaths(cfg.Workbench.AllowedFiles)
	cfg.Services.Allowed = normalizeServiceSpecs(cfg.Services.Allowed)
	if cfg.Watch.PollInterval <= 0 {
		cfg.Watch.PollInterval = 15 * time.Second
	}
	if cfg.Watch.AskTTL <= 0 {
		cfg.Watch.AskTTL = 10 * time.Minute
	}

	cfg.LLM.Provider = strings.ToLower(strings.TrimSpace(cfg.LLM.Provider))
	if cfg.LLM.Provider == "" {
		cfg.LLM.Provider = "generic"
	}
	cfg.LLM.Endpoint = strings.TrimSpace(cfg.LLM.Endpoint)
	if cfg.LLM.Provider == "openai" && cfg.LLM.Endpoint == "" {
		cfg.LLM.Endpoint = defaultOpenAIAPIBaseURL
	}
	cfg.LLM.Model = strings.TrimSpace(cfg.LLM.Model)
	cfg.LLM.APIKey = strings.TrimSpace(cfg.LLM.APIKey)

	cfg.Log.Level = strings.TrimSpace(cfg.Log.Level)
	if cfg.Log.Level == "" {
		cfg.Log.Level = "info"
	}
}

func normalizeStrings(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.ToLower(strings.TrimSpace(value))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func normalizeServiceSpecs(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, ok := normalizeServiceSpec(value)
		if !ok {
			continue
		}
		if _, exists := seen[normalized]; exists {
			continue
		}
		seen[normalized] = struct{}{}
		result = append(result, normalized)
	}
	return result
}

func normalizeServiceSpec(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "", false
	}

	name, spec, hasAlias := strings.Cut(trimmed, "=")
	if !hasAlias {
		return strings.ToLower(trimmed), true
	}

	name = strings.ToLower(strings.TrimSpace(name))
	spec = strings.TrimSpace(spec)
	if name == "" || spec == "" {
		return "", false
	}

	normalizedSpec, ok := normalizeServiceBackendSpec(spec)
	if !ok {
		return "", false
	}

	return name + "=" + normalizedSpec, true
}

func normalizeServiceBackendSpec(spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return "", false
	}

	lowerSpec := strings.ToLower(spec)
	if strings.HasPrefix(lowerSpec, "host:") {
		rest := strings.TrimSpace(spec[len("host:"):])
		idx := strings.Index(rest, ":")
		if idx <= 0 || idx == len(rest)-1 {
			return "", false
		}

		host := strings.ToLower(strings.TrimSpace(rest[:idx]))
		innerSpec := strings.TrimSpace(rest[idx+1:])
		if host == "" || innerSpec == "" {
			return "", false
		}

		normalizedInner, ok := normalizeServiceBackendSpec(innerSpec)
		if !ok {
			return "", false
		}
		return "host:" + host + ":" + normalizedInner, true
	}

	switch {
	case strings.HasPrefix(lowerSpec, "compose:"):
		return "compose:" + strings.TrimSpace(spec[len("compose:"):]), true
	case strings.HasPrefix(lowerSpec, "docker:"):
		return "docker:" + strings.TrimSpace(spec[len("docker:"):]), true
	case strings.HasPrefix(lowerSpec, "systemd:"):
		return "systemd:" + strings.ToLower(strings.TrimSpace(spec[len("systemd:"):])), true
	default:
		return strings.ToLower(spec), true
	}
}

func normalizeRemoteHosts(values map[string]RemoteHostConfig) map[string]RemoteHostConfig {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]RemoteHostConfig, len(values))
	for name, host := range values {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		if normalizedName == "" {
			continue
		}

		host.Address = normalizeSSHAddress(host.Address)
		host.User = strings.TrimSpace(host.User)
		host.Password = strings.TrimSpace(host.Password)
		host.PasswordEnv = strings.TrimSpace(host.PasswordEnv)
		host.PrivateKeyPath = strings.TrimSpace(host.PrivateKeyPath)
		host.PrivateKeyPassphrase = strings.TrimSpace(host.PrivateKeyPassphrase)
		host.PrivateKeyPassphraseEnv = strings.TrimSpace(host.PrivateKeyPassphraseEnv)
		host.KnownHostsPath = strings.TrimSpace(host.KnownHostsPath)

		result[normalizedName] = host
	}

	return result
}

func normalizeAccountProviders(values map[string]AccountProviderConfig) map[string]AccountProviderConfig {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]AccountProviderConfig, len(values))
	for name, provider := range values {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		if normalizedName == "" {
			continue
		}

		provider.Service = strings.ToLower(strings.TrimSpace(provider.Service))
		provider.Vars = normalizeCommandVariables(provider.Vars)
		provider.VarsEnv = normalizeCommandVariables(provider.VarsEnv)
		provider.AddCommand = normalizeCommandTemplate(provider.AddCommand)
		provider.DeleteCommand = normalizeCommandTemplate(provider.DeleteCommand)
		provider.ListCommand = normalizeCommandTemplate(provider.ListCommand)

		result[normalizedName] = provider
	}
	return result
}

func normalizeCommandTemplate(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func normalizeCommandVariables(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]string, len(values))
	for key, value := range values {
		normalizedKey := strings.TrimSpace(key)
		normalizedValue := strings.TrimSpace(value)
		if normalizedKey == "" || normalizedValue == "" {
			continue
		}
		result[normalizedKey] = normalizedValue
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

func normalizeSSHAddress(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(value); err == nil {
		return value
	}
	if strings.HasPrefix(value, "[") && strings.HasSuffix(value, "]") {
		value = strings.TrimPrefix(strings.TrimSuffix(value, "]"), "[")
	}
	return net.JoinHostPort(value, "22")
}

func normalizePaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	return result
}

func parseInt64ListEnv(key string) []int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	return parseInt64List(value)
}

func parseStringListEnv(key string) []string {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return nil
	}

	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}

	return result
}

func parseInt64List(value string) []int64 {
	parts := strings.Split(value, ",")
	result := make([]int64, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed == "" {
			continue
		}
		parsed, err := strconv.ParseInt(trimmed, 10, 64)
		if err == nil {
			result = append(result, parsed)
		}
	}
	return result
}

func parseBool(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func parseDuration(value string, fallback time.Duration) time.Duration {
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseInt(value string, fallback int) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseFloat(value string, fallback float64) float64 {
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}
