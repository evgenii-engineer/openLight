package localmod

import (
	"context"
	"log/slog"

	"openlight/internal/skills"
)

// TelegramSender is the narrow slice of the Telegram transport that modules are
// allowed to touch. *telegram.Bot satisfies it. Keeping this an interface means
// the extension point does not hand modules the whole bot, and tests can supply
// a fake.
type TelegramSender interface {
	SendText(ctx context.Context, chatID int64, text string) error
	SendPhoto(ctx context.Context, chatID int64, path, caption string) error
}

// CommandRegistry is the command/skill registration surface. *skills.Registry
// satisfies it. Registering a skill makes it available as a Telegram command.
type CommandRegistry interface {
	Register(skill skills.Skill) error
}

// EnvReader reads configuration from the process environment. Modules read
// their own secrets from env; the reader exists so tests can inject values and
// so we have one place to add redaction/logging later if needed.
type EnvReader interface {
	Get(key string) string
	// GetDefault returns fallback when the key is unset or blank.
	GetDefault(key, fallback string) string
	// Bool parses truthy values ("1", "true", "yes", "on"), case-insensitive.
	Bool(key string) bool
}

// AppContext is the stable surface passed to every local module's Register. It
// intentionally exposes capabilities, not the internal app object:
//
//   - Logger:      scoped structured logger
//   - Env:         environment/config reader
//   - Scheduler:   register interval and daily jobs, tied to the run context
//   - Telegram:    send messages
//   - Commands:    register Telegram commands (skills)
//   - StorageDir:  directory the module may write private state into
//   - DefaultChatID: first configured chat id, a convenience for modules that
//     do not define their own chat id (0 when none configured)
//
// There is no event bus field because openLight does not have one yet; add it
// here when it exists.
type AppContext struct {
	Logger        *slog.Logger
	Env           EnvReader
	Scheduler     Scheduler
	Telegram      TelegramSender
	Commands      CommandRegistry
	StorageDir    string
	DefaultChatID int64
}
