package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultTelegramAPIBaseURL = "https://api.telegram.org"
const defaultOpenAIAPIBaseURL = "https://api.openai.com/v1"

type Config struct {
	Telegram TelegramConfig `yaml:"telegram"`
	Auth     AuthConfig     `yaml:"auth"`
	Storage  StorageConfig  `yaml:"storage"`
	Access   AccessConfig   `yaml:"access"`
	// Nodes is the canonical, top-level way to declare remote SSH targets.
	// Equivalent to access.hosts; both are accepted and merged at load time.
	Nodes       map[string]NodeConfig `yaml:"nodes"`
	Accounts    AccountsConfig        `yaml:"accounts"`
	Files       FilesConfig           `yaml:"files"`
	Filesystem  FilesConfig           `yaml:"filesystem"`
	Workbench   WorkbenchConfig       `yaml:"workbench"`
	Services    ServicesConfig        `yaml:"services"`
	Watch       WatchConfig           `yaml:"watch"`
	LLM         LLMConfig             `yaml:"llm"`
	Chat        ChatConfig            `yaml:"chat"`
	Notes       NotesConfig           `yaml:"notes"`
	Memory      MemoryConfig          `yaml:"memory"`
	Voice       VoiceConfig           `yaml:"voice"`
	Browser     BrowserConfig         `yaml:"browser"`
	Network     NetworkConfig         `yaml:"network"`
	// Node declares the role of this instance in a multi-node openLight
	// network. Leave empty for single-node (brain) mode.
	Node        NetworkNodeConfig     `yaml:"node"`
	MCP         MCPConfig             `yaml:"mcp"`
	External    ExternalSkillsConfig  `yaml:"external_skills"`
	Vision      VisionConfig          `yaml:"vision"`
	OCR         OCRConfig             `yaml:"ocr"`
	VisualWatch VisualWatchConfig     `yaml:"visual_watch"`
	Agent       AgentConfig           `yaml:"agent"`
	Log         LogConfig             `yaml:"log"`

	// Deprecations is populated at load time. It lists keys the user is
	// still using that have a preferred replacement. The runtime logs
	// these on startup and `openlight doctor` surfaces them as warnings.
	// Not exposed via YAML.
	Deprecations []string `yaml:"-"`
}

type TelegramConfig struct {
	BotToken    string        `yaml:"bot_token"`
	APIBaseURL  string        `yaml:"api_base_url"`
	Mode        string        `yaml:"mode"`
	PollTimeout time.Duration `yaml:"poll_timeout"`
	// DropPendingUpdates, when true (the default), discards the backlog of
	// updates Telegram queued while the agent was offline, so a restart starts
	// clean instead of replaying every message/command that piled up. In
	// polling mode this drops the queue on startup; in webhook mode it is also
	// honoured (alongside webhook.drop_pending_updates). Set to false to
	// process the backlog on restart.
	DropPendingUpdates *bool         `yaml:"drop_pending_updates"`
	Webhook            WebhookConfig `yaml:"webhook"`
}

// ShouldDropPendingUpdates reports whether the backlog of updates queued while
// the agent was offline should be discarded on startup. It defaults to true
// when unset so a restart starts clean.
func (c TelegramConfig) ShouldDropPendingUpdates() bool {
	if c.DropPendingUpdates == nil {
		return true
	}
	return *c.DropPendingUpdates
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
	// RetentionDays caps how long messages and skill_calls are kept in
	// SQLite. Zero (default) keeps everything, mirroring previous
	// behavior; any positive value triggers a prune at startup.
	RetentionDays int `yaml:"retention_days"`
}

type AccessConfig struct {
	Hosts map[string]RemoteHostConfig `yaml:"hosts"`
}

// NodeConfig is the canonical name for a remote SSH-reachable node.
// Internally it is the same shape as RemoteHostConfig; the alias keeps the
// older internal API (access.hosts) working without churn.
type NodeConfig = RemoteHostConfig

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
	Enabled            bool     `yaml:"enabled"`
	Allowed            []string `yaml:"allowed"`
	AllowedRoots       []string `yaml:"allowed_roots"`
	DefaultDir         string   `yaml:"default_dir"`
	MaxReadBytes       int      `yaml:"max_read_bytes"`
	ListLimit          int      `yaml:"list_limit"`
	AllowWrite         bool     `yaml:"allow_write"`
	RedactSecrets      bool     `yaml:"redact_secrets"`
	AllowSensitiveRead bool     `yaml:"allow_sensitive_read"`
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
	Enabled            bool                        `yaml:"enabled"`
	Profile            string                      `yaml:"profile"`
	Profiles           map[string]LLMProfileConfig `yaml:"profiles"`
	Provider           string                      `yaml:"provider"`
	Endpoint           string                      `yaml:"endpoint"`
	Model              string                      `yaml:"model"`
	APIKey             string                      `yaml:"api_key"`
	ExecuteThreshold   float64                     `yaml:"execute_threshold"`
	ClarifyThreshold   float64                     `yaml:"clarify_threshold"`
	DecisionInputChars int                         `yaml:"decision_input_chars"`
	DecisionNumPredict int                         `yaml:"decision_num_predict"`

	// KeepAlive is the top-level fallback for how long Ollama keeps a
	// model loaded after a request. Role-based defaults (smart=-1,
	// fast=10m, vision=5m) win when this is empty.
	KeepAlive string `yaml:"keep_alive"`

	// NumCtx caps the context window Ollama allocates for the model.
	// Zero leaves Ollama on its model default. Smaller values free
	// significant VRAM for the KV cache and matter most on shared-GPU
	// hosts (Mac mini) where FAST and SMART compete for memory.
	NumCtx int `yaml:"num_ctx"`

	// Warmup describes the always-warm policy executed on agent startup.
	// See LLMWarmupConfig.
	Warmup LLMWarmupConfig `yaml:"warmup"`
}

// LLMWarmupConfig pins specific LLM profiles into Ollama memory on startup
// so users never wait on a cold model load. The warmup request also
// transmits an explicit keep_alive value so the chosen profiles stay
// resident until evicted by another model or until Ollama restarts.
type LLMWarmupConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Profiles  []string `yaml:"profiles"`
	KeepAlive any      `yaml:"keep_alive"`
	Prompt    string   `yaml:"prompt"`
}

// Includes reports whether the named profile is in the warmup list.
func (w LLMWarmupConfig) Includes(name string) bool {
	if !w.Enabled {
		return false
	}
	name = strings.ToLower(strings.TrimSpace(name))
	if name == "" {
		return false
	}
	for _, candidate := range w.Profiles {
		if strings.ToLower(strings.TrimSpace(candidate)) == name {
			return true
		}
	}
	return false
}

// KeepAliveString normalizes the YAML value of warmup.keep_alive. Accepts
// integers like -1 (number form in YAML) and strings like "30m" or "-1".
// Empty/nil returns "" so callers fall back to a sensible default.
func (w LLMWarmupConfig) KeepAliveString() string {
	switch v := w.KeepAlive.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(v)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case uint64:
		return strconv.FormatUint(v, 10)
	case float64:
		// YAML often decodes plain integers as float64. Trim the decimals
		// when the value is a whole number ("-1" not "-1.000000").
		if v == float64(int64(v)) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return fmt.Sprint(v)
	}
}

// PromptOrDefault returns the warmup prompt or a sensible fallback so the
// Ollama request always has a non-empty body.
func (w LLMWarmupConfig) PromptOrDefault() string {
	if p := strings.TrimSpace(w.Prompt); p != "" {
		return p
	}
	return "warmup"
}

type LLMProfileConfig struct {
	Provider           string  `yaml:"provider"`
	Endpoint           string  `yaml:"endpoint"`
	Model              string  `yaml:"model"`
	APIKey             string  `yaml:"api_key"`
	ExecuteThreshold   float64 `yaml:"execute_threshold"`
	ClarifyThreshold   float64 `yaml:"clarify_threshold"`
	DecisionInputChars int     `yaml:"decision_input_chars"`
	DecisionNumPredict int     `yaml:"decision_num_predict"`
	// KeepAlive overrides llm.keep_alive for this profile only. Useful for
	// pinning SMART forever while letting FAST evict normally.
	KeepAlive string `yaml:"keep_alive"`
	// NumCtx overrides llm.num_ctx for this profile only. Zero inherits
	// the top-level value (which itself may be zero = Ollama default).
	NumCtx int `yaml:"num_ctx"`
	// Think enables the model's reasoning mode (e.g. Gemma 4). Set to true
	// for the "deep" profile; leave false (default) for fast/smart.
	Think bool `yaml:"think"`
}

// HasProfile reports whether a profile with the given name is configured.
// Names are matched case-insensitively after trimming whitespace.
func (c LLMConfig) HasProfile(name string) bool {
	key := strings.ToLower(strings.TrimSpace(name))
	if key == "" || len(c.Profiles) == 0 {
		return false
	}
	_, ok := c.Profiles[key]
	return ok
}

// ResolveProfile returns an effective LLMProfileConfig for the given profile
// name. Top-level llm.* fields act as defaults; any non-empty profile field
// overrides them. When the named profile is missing the top-level values are
// returned unchanged — this is how openLight stays backward compatible with
// single-model configs.
func (c LLMConfig) ResolveProfile(name string) LLMProfileConfig {
	base := LLMProfileConfig{
		Provider:           c.Provider,
		Endpoint:           c.Endpoint,
		Model:              c.Model,
		APIKey:             c.APIKey,
		ExecuteThreshold:   c.ExecuteThreshold,
		ClarifyThreshold:   c.ClarifyThreshold,
		DecisionInputChars: c.DecisionInputChars,
		DecisionNumPredict: c.DecisionNumPredict,
		KeepAlive:          c.KeepAlive,
		NumCtx:             c.NumCtx,
	}
	if !c.HasProfile(name) {
		return base
	}
	profile := c.Profiles[strings.ToLower(strings.TrimSpace(name))]
	if v := strings.TrimSpace(profile.Provider); v != "" {
		base.Provider = v
	}
	if v := strings.TrimSpace(profile.Endpoint); v != "" {
		base.Endpoint = v
	}
	if v := strings.TrimSpace(profile.Model); v != "" {
		base.Model = v
	}
	if v := strings.TrimSpace(profile.APIKey); v != "" {
		base.APIKey = v
	}
	if profile.ExecuteThreshold > 0 {
		base.ExecuteThreshold = profile.ExecuteThreshold
	}
	if profile.ClarifyThreshold > 0 {
		base.ClarifyThreshold = profile.ClarifyThreshold
	}
	if profile.DecisionInputChars > 0 {
		base.DecisionInputChars = profile.DecisionInputChars
	}
	if profile.DecisionNumPredict > 0 {
		base.DecisionNumPredict = profile.DecisionNumPredict
	}
	if v := strings.TrimSpace(profile.KeepAlive); v != "" {
		base.KeepAlive = v
	}
	if profile.NumCtx > 0 {
		base.NumCtx = profile.NumCtx
	}
	if profile.Think {
		base.Think = true
	}
	if strings.TrimSpace(base.KeepAlive) == "" {
		base.KeepAlive = defaultKeepAliveForRole(name)
	}
	return base
}

// defaultKeepAliveForRole returns the default keep_alive for a profile
// role:
//   - fast   → "-1"  (router classifier runs on every request; pin it)
//   - smart  → "10m" (big model; evict during idle to free VRAM)
//   - vision → "5m"  (rarely called; release VRAM aggressively)
//
// Unrecognized profile names fall back to "10m".
func defaultKeepAliveForRole(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "fast":
		return "-1"
	case "smart":
		return "10m"
	case "deep":
		return "10m"
	case "vision":
		return "5m"
	default:
		return "10m"
	}
}

type ChatConfig struct {
	HistoryLimit     int `yaml:"history_limit"`
	HistoryChars     int `yaml:"history_chars"`
	MaxResponseChars int `yaml:"max_response_chars"`
}

type NotesConfig struct {
	ListLimit int `yaml:"list_limit"`
}

type MemoryConfig struct {
	Enabled   bool   `yaml:"enabled"`
	DBPath    string `yaml:"db_path"`
	ListLimit int    `yaml:"list_limit"`
}

// VoiceConfig configures the Telegram voice-note transcription path: when a
// voice note arrives in chat, ffmpeg converts it and whisper transcribes it.
type VoiceConfig struct {
	Enabled             bool   `yaml:"enabled"`
	Provider            string `yaml:"provider"`
	Language            string `yaml:"language"`
	WhisperCLIPath      string `yaml:"whisper_cli_path"`
	ModelPath           string `yaml:"model_path"`
	FFmpegPath          string `yaml:"ffmpeg_path"`
	ReplyWithTranscript bool   `yaml:"reply_with_transcript"`
}

// STTLanguage returns the language whisper should decode incoming voice notes
// with, from voice.language.
func (v VoiceConfig) STTLanguage() string {
	return strings.TrimSpace(v.Language)
}

type BrowserConfig struct {
	Enabled             bool     `yaml:"enabled"`
	NodePath            string   `yaml:"node_path"`
	HelperPath          string   `yaml:"helper_path"`
	AllowedDomains      []string `yaml:"allowed_domains"`
	AllowAllDomains     bool     `yaml:"allow_all_domains"`
	AllowPrivateNetwork bool     `yaml:"allow_private_network"`
	ArtifactsDir        string   `yaml:"artifacts_dir"`
	TimeoutSeconds      int      `yaml:"timeout_seconds"`
}

type NetworkConfig struct {
	Enabled bool          `yaml:"enabled"`
	Allowed []string      `yaml:"allowed"`
	Timeout time.Duration `yaml:"timeout"`
}

type MCPConfig struct {
	Enabled bool                       `yaml:"enabled"`
	Servers map[string]MCPServerConfig `yaml:"servers"`
}

type MCPServerConfig struct {
	Command      []string          `yaml:"command"`
	Env          map[string]string `yaml:"env"`
	EnvFrom      map[string]string `yaml:"env_from"`
	AllowedTools []string          `yaml:"allowed_tools"`
}

// ExternalSkillsConfig governs loading of user-defined skills declared
// on disk. Each root is scanned at startup; every immediate subdirectory
// that contains a `skill.yaml` is registered alongside the builtin
// skills. Roots that don't exist are silently skipped so operators can
// safely point at `~/.openlight/skills` whether they've created it yet
// or not.
//
// Enabled defaults to true (via normalize) whenever at least one root
// is configured, so the simplest config is just:
//
//	external_skills:
//	  roots:
//	    - ~/.openlight/skills
type ExternalSkillsConfig struct {
	Enabled bool     `yaml:"enabled"`
	Roots   []string `yaml:"roots"`
}

type VisionConfig struct {
	Enabled          bool          `yaml:"enabled"`
	Provider         string        `yaml:"provider"`
	Endpoint         string        `yaml:"endpoint"`
	Model            string        `yaml:"model"`
	APIKey           string        `yaml:"api_key"`
	MaxImageSizeMB   int           `yaml:"max_image_size_mb"`
	Timeout          time.Duration `yaml:"timeout"`
	DefaultPrompt    string        `yaml:"default_prompt"`
	MaxResponseChars int           `yaml:"max_response_chars"`
}

type OCRConfig struct {
	Enabled        bool          `yaml:"enabled"`
	Provider       string        `yaml:"provider"`
	BinaryPath     string        `yaml:"binary_path"`
	Languages      []string      `yaml:"languages"`
	Timeout        time.Duration `yaml:"timeout"`
	MaxImageSizeMB int           `yaml:"max_image_size_mb"`
}

type VisualWatchConfig struct {
	Enabled          bool          `yaml:"enabled"`
	BaselinesDir     string        `yaml:"baselines_dir"`
	PollInterval     time.Duration `yaml:"poll_interval"`
	DefaultInterval  time.Duration `yaml:"default_interval"`
	DefaultThreshold float64       `yaml:"default_threshold"`
	DefaultCooldown  time.Duration `yaml:"default_cooldown"`
	RequestTimeout   time.Duration `yaml:"request_timeout"`
}

type AgentConfig struct {
	RequestTimeout time.Duration `yaml:"request_timeout"`
}

type LogConfig struct {
	Level string `yaml:"level"`
}

// NetworkNodeConfig declares the role of this openLight instance in a
// multi-node network. When NodeRole is "edge" all LLM inference is
// forwarded to the brain node at BrainURL; no local model is used.
// When NodeRole is "brain" (or empty) the instance runs LLM locally and
// optionally exposes the brain API on ListenAddr.
type NetworkNodeConfig struct {
	// NodeID is a human-readable identifier for this node (e.g. "raspberry-pi-5").
	NodeID string `yaml:"node_id"`
	// NodeRole is "brain" or "edge". Defaults to "brain" when empty.
	NodeRole string `yaml:"node_role"`
	// BrainURL is the HTTP base URL of the brain node (e.g.
	// "http://openclaw-m1:8787"). Required when NodeRole is "edge".
	BrainURL string `yaml:"brain_url"`
	// LLMMode controls how LLM inference is routed. "remote_only" means
	// the node never calls a local model, even as a fallback.
	// Defaults to "remote_only" on edge nodes and "local" on brain nodes.
	LLMMode string `yaml:"llm_mode"`
	// LocalLLMEnabled, when false, prevents the node from initialising any
	// local LLM provider. Automatically false on edge nodes.
	LocalLLMEnabled *bool `yaml:"local_llm_enabled"`
	// ListenAddr is the TCP address the brain API server listens on (e.g.
	// ":8787"). Used only when NodeRole is "brain". Empty disables the API.
	ListenAddr string `yaml:"listen_addr"`
	// RemoteSkills is the list of skill names (as defined on the brain) that
	// edge nodes should fetch and expose as remote proxies. The brain must
	// have those skills registered; the edge fetches their definitions from
	// GET {BrainURL}/skills at startup and executes them via
	// POST {BrainURL}/skills/invoke.
	// Set to ["*"] or leave empty with RemoteAllSkills:true to fetch all.
	RemoteSkills []string `yaml:"remote_skills"`
	// RemoteAllSkills, when true, registers all skills available on the brain
	// that are not already registered locally. Equivalent to remote_skills: ["*"].
	RemoteAllSkills bool `yaml:"remote_all_skills"`
}

// IsEdge reports whether this node is configured as an edge node.
func (n NetworkNodeConfig) IsEdge() bool {
	return strings.EqualFold(strings.TrimSpace(n.NodeRole), "edge")
}

// IsBrain reports whether this node is configured as (or defaults to) a brain node.
func (n NetworkNodeConfig) IsBrain() bool {
	role := strings.ToLower(strings.TrimSpace(n.NodeRole))
	return role == "brain" || role == ""
}

// IsExplicitBrain reports whether this node's role is explicitly set to "brain".
func (n NetworkNodeConfig) IsExplicitBrain() bool {
	return strings.ToLower(strings.TrimSpace(n.NodeRole)) == "brain"
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

	collectDeprecations(&cfg)
	normalize(&cfg)

	if err := applySelectedLLMProfile(&cfg, os.Getenv("LLM_PROFILE")); err != nil {
		return Config{}, err
	}

	overrideFromEnv(&cfg)
	normalize(&cfg)

	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}

	return cfg, nil
}

// collectDeprecations records which legacy keys the user still has set in
// their YAML so the doctor and runtime can warn on them. It runs BEFORE
// normalize() so the legacy field is still distinguishable from its
// canonical equivalent. The aliases continue to work for now.
func collectDeprecations(cfg *Config) {
	if cfg.Filesystem.Enabled || len(cfg.Filesystem.Allowed) > 0 || len(cfg.Filesystem.AllowedRoots) > 0 {
		cfg.Deprecations = append(cfg.Deprecations,
			"`filesystem:` is deprecated; rename to `files:` (the alias still works for now but will be removed in a future release)")
	}
	if len(cfg.Access.Hosts) > 0 {
		cfg.Deprecations = append(cfg.Deprecations,
			"`access.hosts:` is deprecated; declare remote SSH targets under top-level `nodes:` instead")
	}
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
			MaxReadBytes:  4096,
			ListLimit:     40,
			RedactSecrets: true,
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
			DecisionNumPredict: 48,
			Warmup: LLMWarmupConfig{
				Enabled:   true,
				Profiles:  []string{"smart"},
				KeepAlive: -1,
				Prompt:    "warmup",
			},
		},
		Chat: ChatConfig{
			HistoryLimit:     6,
			HistoryChars:     900,
			MaxResponseChars: 400,
		},
		Notes: NotesConfig{
			ListLimit: 20,
		},
		Memory: MemoryConfig{
			Enabled:   true,
			ListLimit: 20,
		},
		Voice: VoiceConfig{
			Provider:       "whisper_cli",
			Language:       "ru",
			WhisperCLIPath: "whisper-cli",
			FFmpegPath:     "ffmpeg",
		},
		Browser: BrowserConfig{
			NodePath:       "node",
			HelperPath:     "./tools/browser-agent/index.mjs",
			ArtifactsDir:   "./data/browser-artifacts",
			TimeoutSeconds: 20,
		},
		Vision: VisionConfig{
			Provider:         "ollama",
			Model:            "qwen2.5vl:3b",
			MaxImageSizeMB:   10,
			Timeout:          30 * time.Second,
			DefaultPrompt:    "Describe this image in concise plain English.",
			MaxResponseChars: 1500,
		},
		OCR: OCRConfig{
			Provider:       "tesseract",
			BinaryPath:     "tesseract",
			Languages:      []string{"eng"},
			Timeout:        20 * time.Second,
			MaxImageSizeMB: 10,
		},
		VisualWatch: VisualWatchConfig{
			BaselinesDir:     "./data/visual-watch",
			PollInterval:     30 * time.Second,
			DefaultInterval:  15 * time.Minute,
			DefaultThreshold: 0.15,
			DefaultCooldown:  30 * time.Minute,
			RequestTimeout:   60 * time.Second,
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
	telegramEnabled := strings.TrimSpace(c.Telegram.BotToken) != ""
	switch {
	case !telegramEnabled && c.Node.IsExplicitBrain():
		// Brain-only node: Telegram is optional. The agent process is not
		// started; only the brain API server runs.
	case !telegramEnabled:
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	case telegramEnabled && c.Telegram.Mode != "polling" && c.Telegram.Mode != "webhook":
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
	case c.LLM.NumCtx < 0:
		return errors.New("llm.num_ctx must not be negative")
	case c.Node.IsEdge() && strings.TrimSpace(c.Node.BrainURL) == "":
		return errors.New("node.brain_url is required when node.node_role is edge")
	case c.Node.IsEdge() && c.Node.LLMMode == "local":
		return errors.New("node.llm_mode cannot be local when node.node_role is edge")
	case c.Agent.RequestTimeout <= 0:
		return errors.New("agent.request_timeout must be greater than zero")
	case telegramEnabled && c.Telegram.PollTimeout <= 0:
		return errors.New("telegram.poll_timeout must be greater than zero")
	case telegramEnabled && c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.Webhook.URL) == "":
		return errors.New("telegram.webhook.url is required when telegram.mode is webhook")
	case c.Telegram.Mode == "webhook" && !strings.HasPrefix(strings.ToLower(strings.TrimSpace(c.Telegram.Webhook.URL)), "https://"):
		return errors.New("telegram.webhook.url must start with https://")
	case c.Telegram.Mode == "webhook" && strings.TrimSpace(c.Telegram.Webhook.ListenAddr) == "":
		return errors.New("telegram.webhook.listen_addr is required when telegram.mode is webhook")
	case c.Files.MaxReadBytes <= 0:
		return errors.New("files.max_read_bytes must be greater than zero")
	case c.Files.ListLimit <= 0:
		return errors.New("files.list_limit must be greater than zero")
	case c.Memory.ListLimit <= 0:
		return errors.New("memory.list_limit must be greater than zero")
	case c.Voice.Enabled && strings.TrimSpace(c.Voice.Provider) == "":
		return errors.New("voice.provider is required when voice.enabled is true")
	case c.Voice.Enabled && strings.EqualFold(strings.TrimSpace(c.Voice.Provider), "whisper_cli") && strings.TrimSpace(c.Voice.ModelPath) == "" && !c.Node.IsEdge():
		return errors.New("voice.model_path is required when voice.enabled is true")
	case c.Browser.TimeoutSeconds <= 0:
		return errors.New("browser.timeout_seconds must be greater than zero")
	case c.Browser.Enabled && !c.Browser.AllowAllDomains && len(c.Browser.AllowedDomains) == 0:
		return errors.New("browser.allowed_domains must not be empty when browser.enabled is true unless browser.allow_all_domains is true")
	case c.Vision.Enabled && strings.TrimSpace(c.Vision.Provider) == "":
		return errors.New("vision.provider is required when vision.enabled is true")
	case c.Vision.Enabled && c.Vision.MaxImageSizeMB <= 0:
		return errors.New("vision.max_image_size_mb must be greater than zero when vision.enabled is true")
	case c.Vision.Enabled && c.Vision.Timeout <= 0:
		return errors.New("vision.timeout must be greater than zero when vision.enabled is true")
	case c.OCR.Enabled && strings.TrimSpace(c.OCR.Provider) == "":
		return errors.New("ocr.provider is required when ocr.enabled is true")
	case c.OCR.Enabled && c.OCR.Timeout <= 0:
		return errors.New("ocr.timeout must be greater than zero when ocr.enabled is true")
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

	for name, profile := range c.LLM.Profiles {
		if name == "" {
			return errors.New("llm.profiles keys must not be empty")
		}
		// A profile may omit `provider` and inherit it from the top-level
		// llm.provider — this is how fast/smart profiles share a single
		// Ollama endpoint with different model names. We still require
		// *some* provider to be resolvable.
		if strings.TrimSpace(profile.Provider) == "" && strings.TrimSpace(c.LLM.Provider) == "" {
			return fmt.Errorf("llm.profiles.%s.provider is required when llm.provider is empty", name)
		}
		if profile.ExecuteThreshold < 0 || profile.ExecuteThreshold > 1 {
			return fmt.Errorf("llm.profiles.%s.execute_threshold must be between zero and one", name)
		}
		if profile.ClarifyThreshold < 0 || profile.ClarifyThreshold >= 1 {
			return fmt.Errorf("llm.profiles.%s.clarify_threshold must be between zero and one", name)
		}
		if profile.DecisionInputChars < 0 {
			return fmt.Errorf("llm.profiles.%s.decision_input_chars must not be negative", name)
		}
		if profile.DecisionNumPredict < 0 {
			return fmt.Errorf("llm.profiles.%s.decision_num_predict must not be negative", name)
		}
		if profile.NumCtx < 0 {
			return fmt.Errorf("llm.profiles.%s.num_ctx must not be negative", name)
		}
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
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_DROP_PENDING_UPDATES")); value != "" {
		v := parseBool(value)
		cfg.Telegram.DropPendingUpdates = &v
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
	if value := strings.TrimSpace(os.Getenv("FILESYSTEM_ENABLED")); value != "" {
		cfg.Files.Enabled = parseBool(value)
	}
	if value := parseStringListEnv("FILESYSTEM_ALLOWED_ROOTS"); value != nil {
		cfg.Files.AllowedRoots = value
	}
	if value := strings.TrimSpace(os.Getenv("FILESYSTEM_ALLOW_WRITE")); value != "" {
		cfg.Files.AllowWrite = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("FILESYSTEM_REDACT_SECRETS")); value != "" {
		cfg.Files.RedactSecrets = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("FILESYSTEM_ALLOW_SENSITIVE_READ")); value != "" {
		cfg.Files.AllowSensitiveRead = parseBool(value)
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
	if value := strings.TrimSpace(os.Getenv("MEMORY_ENABLED")); value != "" {
		cfg.Memory.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("MEMORY_DB_PATH")); value != "" {
		cfg.Memory.DBPath = value
	}
	if value := strings.TrimSpace(os.Getenv("MEMORY_LIST_LIMIT")); value != "" {
		cfg.Memory.ListLimit = parseInt(value, cfg.Memory.ListLimit)
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_ENABLED")); value != "" {
		cfg.Voice.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_PROVIDER")); value != "" {
		cfg.Voice.Provider = value
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_WHISPER_CLI_PATH")); value != "" {
		cfg.Voice.WhisperCLIPath = value
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_MODEL_PATH")); value != "" {
		cfg.Voice.ModelPath = value
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_FFMPEG_PATH")); value != "" {
		cfg.Voice.FFmpegPath = value
	}
	if value := strings.TrimSpace(os.Getenv("VOICE_REPLY_WITH_TRANSCRIPT")); value != "" {
		cfg.Voice.ReplyWithTranscript = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_ENABLED")); value != "" {
		cfg.Browser.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_NODE_PATH")); value != "" {
		cfg.Browser.NodePath = value
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_HELPER_PATH")); value != "" {
		cfg.Browser.HelperPath = value
	}
	if value := parseStringListEnv("BROWSER_ALLOWED_DOMAINS"); value != nil {
		cfg.Browser.AllowedDomains = value
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_ALLOW_ALL_DOMAINS")); value != "" {
		cfg.Browser.AllowAllDomains = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_ALLOW_PRIVATE_NETWORK")); value != "" {
		cfg.Browser.AllowPrivateNetwork = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_ARTIFACTS_DIR")); value != "" {
		cfg.Browser.ArtifactsDir = value
	}
	if value := strings.TrimSpace(os.Getenv("BROWSER_TIMEOUT_SECONDS")); value != "" {
		cfg.Browser.TimeoutSeconds = parseInt(value, cfg.Browser.TimeoutSeconds)
	}
	if value := strings.TrimSpace(os.Getenv("VISION_ENABLED")); value != "" {
		cfg.Vision.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("VISION_PROVIDER")); value != "" {
		cfg.Vision.Provider = value
	}
	if value := strings.TrimSpace(os.Getenv("VISION_ENDPOINT")); value != "" {
		cfg.Vision.Endpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("VISION_MODEL")); value != "" {
		cfg.Vision.Model = value
	}
	if value := strings.TrimSpace(os.Getenv("VISION_API_KEY")); value != "" {
		cfg.Vision.APIKey = value
	}
	if value := strings.TrimSpace(os.Getenv("VISION_TIMEOUT")); value != "" {
		cfg.Vision.Timeout = parseDuration(value, cfg.Vision.Timeout)
	}
	if value := strings.TrimSpace(os.Getenv("VISION_MAX_IMAGE_MB")); value != "" {
		cfg.Vision.MaxImageSizeMB = parseInt(value, cfg.Vision.MaxImageSizeMB)
	}
	if value := strings.TrimSpace(os.Getenv("OCR_ENABLED")); value != "" {
		cfg.OCR.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("OCR_PROVIDER")); value != "" {
		cfg.OCR.Provider = value
	}
	if value := strings.TrimSpace(os.Getenv("OCR_BINARY_PATH")); value != "" {
		cfg.OCR.BinaryPath = value
	}
	if value := parseStringListEnv("OCR_LANGUAGES"); value != nil {
		cfg.OCR.Languages = value
	}
	if value := strings.TrimSpace(os.Getenv("OCR_TIMEOUT")); value != "" {
		cfg.OCR.Timeout = parseDuration(value, cfg.OCR.Timeout)
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_ENABLED")); value != "" {
		cfg.VisualWatch.Enabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_BASELINES_DIR")); value != "" {
		cfg.VisualWatch.BaselinesDir = value
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_POLL_INTERVAL")); value != "" {
		cfg.VisualWatch.PollInterval = parseDuration(value, cfg.VisualWatch.PollInterval)
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_DEFAULT_INTERVAL")); value != "" {
		cfg.VisualWatch.DefaultInterval = parseDuration(value, cfg.VisualWatch.DefaultInterval)
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_DEFAULT_THRESHOLD")); value != "" {
		cfg.VisualWatch.DefaultThreshold = parseFloat(value, cfg.VisualWatch.DefaultThreshold)
	}
	if value := strings.TrimSpace(os.Getenv("VISUAL_WATCH_DEFAULT_COOLDOWN")); value != "" {
		cfg.VisualWatch.DefaultCooldown = parseDuration(value, cfg.VisualWatch.DefaultCooldown)
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
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_NODE_ID")); value != "" {
		cfg.Node.NodeID = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_NODE_ROLE")); value != "" {
		cfg.Node.NodeRole = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_BRAIN_URL")); value != "" {
		cfg.Node.BrainURL = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_LLM_MODE")); value != "" {
		cfg.Node.LLMMode = value
	}
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_LOCAL_LLM_ENABLED")); value != "" {
		v := parseBool(value)
		cfg.Node.LocalLLMEnabled = &v
	}
	if value := strings.TrimSpace(os.Getenv("OPENLIGHT_BRAIN_LISTEN_ADDR")); value != "" {
		cfg.Node.ListenAddr = value
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
	cfg.Access.Hosts = mergeNodeMaps(cfg.Access.Hosts, cfg.Nodes)
	cfg.Nodes = nil
	cfg.Access.Hosts = normalizeRemoteHosts(cfg.Access.Hosts)
	cfg.Accounts.Providers = normalizeAccountProviders(cfg.Accounts.Providers)
	cfg.Files = mergeFilesConfig(cfg.Files, cfg.Filesystem)
	cfg.Files.Enabled = normalizeFilesEnabled(cfg.Files)
	cfg.Files.Allowed = normalizePaths(cfg.Files.Allowed)
	cfg.Files.AllowedRoots = normalizePaths(cfg.Files.AllowedRoots)
	if len(cfg.Files.AllowedRoots) > 0 {
		cfg.Files.Allowed = cfg.Files.AllowedRoots
	}
	if !cfg.Files.RedactSecrets {
		cfg.Files.AllowSensitiveRead = true
	}
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
	cfg.Memory.DBPath = strings.TrimSpace(cfg.Memory.DBPath)
	if cfg.Memory.ListLimit <= 0 {
		cfg.Memory.ListLimit = 20
	}
	cfg.Voice.Provider = strings.ToLower(strings.TrimSpace(cfg.Voice.Provider))
	if cfg.Voice.Provider == "" {
		cfg.Voice.Provider = "whisper_cli"
	}
	cfg.Voice.WhisperCLIPath = expandHomePath(strings.TrimSpace(cfg.Voice.WhisperCLIPath))
	if cfg.Voice.WhisperCLIPath == "" {
		cfg.Voice.WhisperCLIPath = "whisper-cli"
	}
	// Expand a leading "~" because these paths are passed to exec (no shell), so
	// the shell would never expand them and whisper/ffmpeg would not find them.
	cfg.Voice.ModelPath = expandHomePath(strings.TrimSpace(cfg.Voice.ModelPath))
	cfg.Voice.FFmpegPath = expandHomePath(strings.TrimSpace(cfg.Voice.FFmpegPath))
	if cfg.Voice.FFmpegPath == "" {
		cfg.Voice.FFmpegPath = "ffmpeg"
	}
	cfg.Voice.Language = strings.ToLower(strings.TrimSpace(cfg.Voice.Language))
	if cfg.Voice.Language == "" {
		cfg.Voice.Language = "ru"
	}
	cfg.Browser.NodePath = strings.TrimSpace(cfg.Browser.NodePath)
	if cfg.Browser.NodePath == "" {
		cfg.Browser.NodePath = "node"
	}
	cfg.Browser.HelperPath = strings.TrimSpace(cfg.Browser.HelperPath)
	if cfg.Browser.HelperPath == "" {
		cfg.Browser.HelperPath = "./tools/browser-agent/index.mjs"
	}
	cfg.Browser.AllowedDomains = normalizeStrings(cfg.Browser.AllowedDomains)
	cfg.Browser.ArtifactsDir = strings.TrimSpace(cfg.Browser.ArtifactsDir)
	if cfg.Browser.ArtifactsDir == "" {
		cfg.Browser.ArtifactsDir = "./data/browser-artifacts"
	}
	if cfg.Browser.TimeoutSeconds <= 0 {
		cfg.Browser.TimeoutSeconds = 20
	}

	cfg.Vision.Provider = strings.ToLower(strings.TrimSpace(cfg.Vision.Provider))
	if cfg.Vision.Provider == "" {
		cfg.Vision.Provider = "ollama"
	}
	cfg.Vision.Endpoint = strings.TrimSpace(cfg.Vision.Endpoint)
	cfg.Vision.Model = strings.TrimSpace(cfg.Vision.Model)
	cfg.Vision.APIKey = strings.TrimSpace(cfg.Vision.APIKey)
	cfg.Vision.DefaultPrompt = strings.TrimSpace(cfg.Vision.DefaultPrompt)
	if cfg.Vision.DefaultPrompt == "" {
		cfg.Vision.DefaultPrompt = "Describe this image in concise plain English."
	}
	if cfg.Vision.MaxImageSizeMB <= 0 {
		cfg.Vision.MaxImageSizeMB = 10
	}
	if cfg.Vision.Timeout <= 0 {
		cfg.Vision.Timeout = 30 * time.Second
	}
	if cfg.Vision.MaxResponseChars <= 0 {
		cfg.Vision.MaxResponseChars = 1500
	}

	cfg.OCR.Provider = strings.ToLower(strings.TrimSpace(cfg.OCR.Provider))
	if cfg.OCR.Provider == "" {
		cfg.OCR.Provider = "tesseract"
	}
	cfg.OCR.BinaryPath = strings.TrimSpace(cfg.OCR.BinaryPath)
	if cfg.OCR.BinaryPath == "" {
		switch cfg.OCR.Provider {
		case "tesseract":
			cfg.OCR.BinaryPath = "tesseract"
		case "apple_vision":
			cfg.OCR.BinaryPath = "shortcuts"
		}
	}
	cfg.OCR.Languages = normalizeStrings(cfg.OCR.Languages)
	if len(cfg.OCR.Languages) == 0 && cfg.OCR.Provider == "tesseract" {
		cfg.OCR.Languages = []string{"eng"}
	}
	if cfg.OCR.Timeout <= 0 {
		cfg.OCR.Timeout = 20 * time.Second
	}
	if cfg.OCR.MaxImageSizeMB <= 0 {
		cfg.OCR.MaxImageSizeMB = 10
	}

	cfg.VisualWatch.BaselinesDir = strings.TrimSpace(cfg.VisualWatch.BaselinesDir)
	if cfg.VisualWatch.BaselinesDir == "" {
		cfg.VisualWatch.BaselinesDir = "./data/visual-watch"
	}
	if cfg.VisualWatch.PollInterval <= 0 {
		cfg.VisualWatch.PollInterval = 30 * time.Second
	}
	if cfg.VisualWatch.DefaultInterval <= 0 {
		cfg.VisualWatch.DefaultInterval = 15 * time.Minute
	}
	if cfg.VisualWatch.DefaultThreshold <= 0 {
		cfg.VisualWatch.DefaultThreshold = 0.15
	}
	if cfg.VisualWatch.DefaultCooldown <= 0 {
		cfg.VisualWatch.DefaultCooldown = 30 * time.Minute
	}
	if cfg.VisualWatch.RequestTimeout <= 0 {
		cfg.VisualWatch.RequestTimeout = 60 * time.Second
	}

	cfg.Node.NodeID = strings.TrimSpace(cfg.Node.NodeID)
	cfg.Node.NodeRole = strings.ToLower(strings.TrimSpace(cfg.Node.NodeRole))
	cfg.Node.BrainURL = strings.TrimRight(strings.TrimSpace(cfg.Node.BrainURL), "/")
	cfg.Node.LLMMode = strings.ToLower(strings.TrimSpace(cfg.Node.LLMMode))
	if cfg.Node.IsEdge() {
		// Edge nodes must never use a local LLM. Enforce defaults.
		if cfg.Node.LLMMode == "" {
			cfg.Node.LLMMode = "remote_only"
		}
		if cfg.Node.LocalLLMEnabled == nil {
			f := false
			cfg.Node.LocalLLMEnabled = &f
		}
	}
	if cfg.Node.IsBrain() {
		if cfg.Node.LLMMode == "" {
			cfg.Node.LLMMode = "local"
		}
		if cfg.Node.ListenAddr == "" && strings.TrimSpace(cfg.Node.NodeRole) == "brain" {
			cfg.Node.ListenAddr = ":8787"
		}
	}

	cfg.External.Roots = normalizeStrings(cfg.External.Roots)
	if len(cfg.External.Roots) > 0 && !cfg.External.Enabled {
		// Make the common case ("declare a root, get external skills")
		// just work. Operators who want to keep the roots configured
		// but disable loading can set `enabled: false` explicitly —
		// the YAML decoder distinguishes unset from false only via the
		// pointer dance we deliberately don't do here, so the rule is:
		// roots present implies enabled.
		cfg.External.Enabled = true
	}

	cfg.LLM.Profile = strings.ToLower(strings.TrimSpace(cfg.LLM.Profile))
	cfg.LLM.Profiles = normalizeLLMProfiles(cfg.LLM.Profiles)
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

func applySelectedLLMProfile(cfg *Config, requestedProfile string) error {
	if cfg == nil {
		return nil
	}

	profileName := strings.ToLower(strings.TrimSpace(requestedProfile))
	if profileName == "" {
		profileName = cfg.LLM.Profile
	}
	cfg.LLM.Profile = profileName

	if profileName == "" {
		return nil
	}

	if len(cfg.LLM.Profiles) == 0 {
		if cfg.LLM.Provider == profileName {
			return nil
		}
		return fmt.Errorf("llm profile %q requested but llm.profiles is not configured; current direct provider is %q", profileName, cfg.LLM.Provider)
	}

	profile, ok := cfg.LLM.Profiles[profileName]
	if !ok {
		return fmt.Errorf("unknown llm profile: %s", profileName)
	}

	if strings.TrimSpace(profile.Provider) != "" {
		cfg.LLM.Provider = profile.Provider
	}
	if strings.TrimSpace(profile.Endpoint) != "" {
		cfg.LLM.Endpoint = profile.Endpoint
	}
	if strings.TrimSpace(profile.Model) != "" {
		cfg.LLM.Model = profile.Model
	}
	if strings.TrimSpace(profile.APIKey) != "" {
		cfg.LLM.APIKey = profile.APIKey
	}
	if profile.ExecuteThreshold > 0 {
		cfg.LLM.ExecuteThreshold = profile.ExecuteThreshold
	}
	if profile.ClarifyThreshold > 0 {
		cfg.LLM.ClarifyThreshold = profile.ClarifyThreshold
	}
	if profile.DecisionInputChars > 0 {
		cfg.LLM.DecisionInputChars = profile.DecisionInputChars
	}
	if profile.DecisionNumPredict > 0 {
		cfg.LLM.DecisionNumPredict = profile.DecisionNumPredict
	}

	return nil
}

// expandHomePath replaces a leading "~" (alone or "~/...") with the current
// user's home directory. Paths are otherwise returned unchanged. Used for
// exec'd tool paths where no shell is involved to expand the tilde.
func expandHomePath(path string) string {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return path
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == "~" {
		return home
	}
	return filepath.Join(home, path[2:])
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
	// "node:" and "host:" are equivalent; node: is the canonical spelling,
	// host: is kept for back-compat with older configs.
	for _, prefix := range []string{"node:", "host:"} {
		if !strings.HasPrefix(lowerSpec, prefix) {
			continue
		}
		rest := strings.TrimSpace(spec[len(prefix):])
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

// mergeNodeMaps combines entries from `nodes:` (canonical) and `access.hosts:`
// (legacy) into a single map. Entries declared under `nodes:` win on key
// collision, so users can graduate to the new spelling without removing the
// old block first.
func mergeNodeMaps(legacy, canonical map[string]RemoteHostConfig) map[string]RemoteHostConfig {
	if len(legacy) == 0 && len(canonical) == 0 {
		return nil
	}
	merged := make(map[string]RemoteHostConfig, len(legacy)+len(canonical))
	for name, host := range legacy {
		merged[name] = host
	}
	for name, host := range canonical {
		merged[name] = host
	}
	return merged
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

func normalizeLLMProfiles(values map[string]LLMProfileConfig) map[string]LLMProfileConfig {
	if len(values) == 0 {
		return nil
	}

	result := make(map[string]LLMProfileConfig, len(values))
	for name, profile := range values {
		normalizedName := strings.ToLower(strings.TrimSpace(name))
		if normalizedName == "" {
			continue
		}

		profile.Provider = strings.ToLower(strings.TrimSpace(profile.Provider))
		profile.Endpoint = strings.TrimSpace(profile.Endpoint)
		profile.Model = strings.TrimSpace(profile.Model)
		profile.APIKey = strings.TrimSpace(profile.APIKey)

		result[normalizedName] = profile
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

func mergeFilesConfig(base FilesConfig, override FilesConfig) FilesConfig {
	if override.Enabled {
		base.Enabled = true
	}
	if len(override.Allowed) > 0 {
		base.Allowed = override.Allowed
	}
	if len(override.AllowedRoots) > 0 {
		base.AllowedRoots = override.AllowedRoots
	}
	if override.MaxReadBytes > 0 {
		base.MaxReadBytes = override.MaxReadBytes
	}
	if override.ListLimit > 0 {
		base.ListLimit = override.ListLimit
	}
	if override.AllowWrite {
		base.AllowWrite = true
	}
	if override.RedactSecrets {
		base.RedactSecrets = true
	}
	if override.AllowSensitiveRead {
		base.AllowSensitiveRead = true
	}
	return base
}

func normalizeFilesEnabled(cfg FilesConfig) bool {
	if cfg.Enabled {
		return true
	}
	return len(cfg.Allowed) > 0 || len(cfg.AllowedRoots) > 0
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
