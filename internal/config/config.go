package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const defaultTelegramAPIBaseURL = "https://api.telegram.org"

type Config struct {
	TelegramBotToken string        `yaml:"telegram_bot_token"`
	AllowedUserIDs   []int64       `yaml:"allowed_user_ids"`
	AllowedChatIDs   []int64       `yaml:"allowed_chat_ids"`
	SQLitePath       string        `yaml:"sqlite_path"`
	AllowedServices  []string      `yaml:"allowed_services"`
	LLMEnabled       bool          `yaml:"llm_enabled"`
	LLMProvider      string        `yaml:"llm_provider"`
	LLMEndpoint      string        `yaml:"llm_endpoint"`
	LLMModel         string        `yaml:"llm_model"`
	LogLevel         string        `yaml:"log_level"`
	RequestTimeout   time.Duration `yaml:"request_timeout"`
	PollTimeout      time.Duration `yaml:"poll_timeout"`
	TelegramAPIBase  string        `yaml:"telegram_api_base_url"`
	ServiceLogLines  int           `yaml:"service_log_lines"`
	NotesListLimit   int           `yaml:"notes_list_limit"`
	ChatHistoryLimit int           `yaml:"chat_history_limit"`
	ChatHistoryChars int           `yaml:"chat_history_chars"`
	ChatMaxRespChars int           `yaml:"chat_max_response_chars"`
}

func Load(path string) (Config, error) {
	cfg := Config{
		LogLevel:         "info",
		RequestTimeout:   5 * time.Second,
		PollTimeout:      25 * time.Second,
		TelegramAPIBase:  defaultTelegramAPIBaseURL,
		ServiceLogLines:  100,
		NotesListLimit:   20,
		LLMProvider:      "generic",
		ChatHistoryLimit: 6,
		ChatHistoryChars: 900,
		ChatMaxRespChars: 400,
	}

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

func (c Config) Validate() error {
	switch {
	case strings.TrimSpace(c.TelegramBotToken) == "":
		return errors.New("TELEGRAM_BOT_TOKEN is required")
	case strings.TrimSpace(c.SQLitePath) == "":
		return errors.New("SQLITE_PATH is required")
	case len(c.AllowedUserIDs) == 0 && len(c.AllowedChatIDs) == 0:
		return errors.New("at least one of ALLOWED_USER_IDS or ALLOWED_CHAT_IDS is required")
	case c.LLMEnabled && strings.TrimSpace(c.LLMEndpoint) == "":
		return errors.New("LLM_ENDPOINT is required when LLM_ENABLED is true")
	case c.LLMEnabled && c.LLMProvider == "ollama" && strings.TrimSpace(c.LLMModel) == "":
		return errors.New("LLM_MODEL is required when LLM_PROVIDER is ollama")
	case c.LLMEnabled && c.LLMProvider != "generic" && c.LLMProvider != "ollama":
		return errors.New("LLM_PROVIDER must be one of: generic, ollama")
	case c.RequestTimeout <= 0:
		return errors.New("REQUEST_TIMEOUT must be greater than zero")
	case c.PollTimeout <= 0:
		return errors.New("poll_timeout must be greater than zero")
	case c.ServiceLogLines <= 0:
		return errors.New("service_log_lines must be greater than zero")
	case c.NotesListLimit <= 0:
		return errors.New("notes_list_limit must be greater than zero")
	case c.ChatHistoryLimit <= 0:
		return errors.New("chat_history_limit must be greater than zero")
	case c.ChatHistoryChars <= 0:
		return errors.New("chat_history_chars must be greater than zero")
	case c.ChatMaxRespChars <= 0:
		return errors.New("chat_max_response_chars must be greater than zero")
	}

	return nil
}

func overrideFromEnv(cfg *Config) {
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")); value != "" {
		cfg.TelegramBotToken = value
	}
	if value := parseInt64ListEnv("ALLOWED_USER_IDS"); len(value) > 0 {
		cfg.AllowedUserIDs = value
	}
	if value := parseInt64ListEnv("ALLOWED_CHAT_IDS"); len(value) > 0 {
		cfg.AllowedChatIDs = value
	}
	if value := strings.TrimSpace(os.Getenv("SQLITE_PATH")); value != "" {
		cfg.SQLitePath = value
	}
	if value := parseStringListEnv("ALLOWED_SERVICES"); len(value) > 0 {
		cfg.AllowedServices = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_ENABLED")); value != "" {
		cfg.LLMEnabled = parseBool(value)
	}
	if value := strings.TrimSpace(os.Getenv("LLM_PROVIDER")); value != "" {
		cfg.LLMProvider = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_ENDPOINT")); value != "" {
		cfg.LLMEndpoint = value
	}
	if value := strings.TrimSpace(os.Getenv("LLM_MODEL")); value != "" {
		cfg.LLMModel = value
	}
	if value := strings.TrimSpace(os.Getenv("LOG_LEVEL")); value != "" {
		cfg.LogLevel = value
	}
	if value := strings.TrimSpace(os.Getenv("REQUEST_TIMEOUT")); value != "" {
		cfg.RequestTimeout = parseDuration(value, cfg.RequestTimeout)
	}
	if value := strings.TrimSpace(os.Getenv("POLL_TIMEOUT")); value != "" {
		cfg.PollTimeout = parseDuration(value, cfg.PollTimeout)
	}
	if value := strings.TrimSpace(os.Getenv("TELEGRAM_API_BASE_URL")); value != "" {
		cfg.TelegramAPIBase = value
	}
	if value := strings.TrimSpace(os.Getenv("SERVICE_LOG_LINES")); value != "" {
		cfg.ServiceLogLines = parseInt(value, cfg.ServiceLogLines)
	}
	if value := strings.TrimSpace(os.Getenv("NOTES_LIST_LIMIT")); value != "" {
		cfg.NotesListLimit = parseInt(value, cfg.NotesListLimit)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_HISTORY_LIMIT")); value != "" {
		cfg.ChatHistoryLimit = parseInt(value, cfg.ChatHistoryLimit)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_HISTORY_CHARS")); value != "" {
		cfg.ChatHistoryChars = parseInt(value, cfg.ChatHistoryChars)
	}
	if value := strings.TrimSpace(os.Getenv("CHAT_MAX_RESPONSE_CHARS")); value != "" {
		cfg.ChatMaxRespChars = parseInt(value, cfg.ChatMaxRespChars)
	}
}

func normalize(cfg *Config) {
	cfg.TelegramBotToken = strings.TrimSpace(cfg.TelegramBotToken)
	cfg.SQLitePath = strings.TrimSpace(cfg.SQLitePath)
	cfg.LLMEndpoint = strings.TrimSpace(cfg.LLMEndpoint)
	cfg.LLMProvider = strings.ToLower(strings.TrimSpace(cfg.LLMProvider))
	cfg.LLMModel = strings.TrimSpace(cfg.LLMModel)
	cfg.LogLevel = strings.TrimSpace(cfg.LogLevel)
	cfg.TelegramAPIBase = strings.TrimRight(strings.TrimSpace(cfg.TelegramAPIBase), "/")
	cfg.AllowedServices = normalizeStrings(cfg.AllowedServices)
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
