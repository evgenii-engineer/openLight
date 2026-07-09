package localmod

import (
	"context"
	"log/slog"
	"os"
	"strings"
)

// Env var names driving the loader.
const (
	EnvEnabled = "OPENLIGHT_LOCAL_MODULES_ENABLED"
	EnvPath    = "OPENLIGHT_LOCAL_MODULES_PATH"
	EnvModules = "OPENLIGHT_LOCAL_MODULES"
)

// Deps carries the host capabilities the loader needs to build an AppContext.
// The host fills this in once, at startup.
type Deps struct {
	Logger        *slog.Logger
	Env           EnvReader // defaults to OSEnv() when nil
	Telegram      TelegramSender
	Commands      CommandRegistry
	StorageDir    string
	DefaultChatID int64
}

// Load activates enabled local modules. It is safe to call unconditionally:
//
//   - EnvEnabled unset/false        -> no-op, openLight starts as before.
//   - EnvPath missing on disk       -> warning only, keeps going.
//   - a listed module not compiled  -> warning, keeps going.
//   - a module panics/errors in     -> logged, other modules still load, host
//     Register                         is never crashed.
//
// runCtx bounds every scheduled job the modules register; cancel it to stop
// them. Load returns after all Register calls complete (jobs run in the
// background).
func Load(runCtx context.Context, deps Deps) {
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger = logger.With("component", "localmod")

	env := deps.Env
	if env == nil {
		env = OSEnv()
	}

	if !env.Bool(EnvEnabled) {
		logger.Debug("local modules disabled", "env", EnvEnabled)
		return
	}

	// The path does not drive Go imports (modules are compiled in), but it is
	// the base location modules may read config/state from and it lets us warn
	// early on an obvious misconfiguration.
	path := env.GetDefault(EnvPath, "./local_modules")
	if info, err := os.Stat(path); err != nil {
		logger.Warn("local modules path not found, continuing", "path", path, "error", err)
	} else if !info.IsDir() {
		logger.Warn("local modules path is not a directory, continuing", "path", path)
	}

	names := parseModuleList(env.Get(EnvModules))
	if len(names) == 0 {
		logger.Warn("local modules enabled but none listed", "env", EnvModules)
		return
	}

	available := registered()
	scheduler := newScheduler(runCtx, logger)

	for _, name := range names {
		module, ok := available[name]
		if !ok {
			logger.Error("local module listed but not compiled in; add its blank-import to cmd/openlight/localmodules_local.go",
				"module", name, "compiled_in", RegisteredNames())
			continue
		}
		loadOne(logger, scheduler, deps, env, module)
	}
}

// loadOne registers a single module with full panic/error containment.
func loadOne(logger *slog.Logger, scheduler Scheduler, deps Deps, env EnvReader, module Module) {
	name := module.Name()
	appCtx := &AppContext{
		Logger:        logger.With("module", name),
		Env:           env,
		Scheduler:     scheduler,
		Telegram:      deps.Telegram,
		Commands:      deps.Commands,
		StorageDir:    deps.StorageDir,
		DefaultChatID: deps.DefaultChatID,
	}

	defer func() {
		if r := recover(); r != nil {
			logger.Error("local module panicked during register (recovered)", "module", name, "panic", r)
		}
	}()

	if err := module.Register(appCtx); err != nil {
		logger.Error("local module failed to register", "module", name, "error", err)
		return
	}
	logger.Info("local module registered", "module", name)
}

// parseModuleList splits a comma-separated list, trimming blanks.
func parseModuleList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if name := strings.TrimSpace(p); name != "" {
			out = append(out, name)
		}
	}
	return out
}
