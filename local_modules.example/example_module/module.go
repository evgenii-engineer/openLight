// Package examplemodule is a committable, dependency-free template for an
// openLight local private module. Copy this directory to
// local_modules/<your_module>/ and adapt it. See README.md in this directory.
//
// It demonstrates the three things a module typically does:
//
//  1. Self-register with the loader from init() (so a blank import compiles it
//     in).
//  2. Register a Telegram command (a skill), here /example_ping.
//  3. Register a scheduled background job, here a harmless heartbeat log.
package examplemodule

import (
	"context"
	"time"

	"openlight/internal/localmod"
	"openlight/internal/skills"
)

// moduleName is the identifier used in OPENLIGHT_LOCAL_MODULES.
const moduleName = "example_module"

func init() {
	// The class-based shape from the design doc: a struct implementing Module.
	localmod.Register(&Module{})
}

// Module implements localmod.Module.
type Module struct{}

func (m *Module) Name() string { return moduleName }

// Register wires the module into the host. Equivalent to register(app_context).
func (m *Module) Register(ctx *localmod.AppContext) error {
	ctx.Logger.Info("example_module registering")

	// 1) A Telegram command.
	if err := ctx.Commands.Register(&pingSkill{}); err != nil {
		return err
	}

	// 2) A scheduled job. Runs every 6 hours; logs a heartbeat. Real modules
	// would send a Telegram message or collect metrics here.
	ctx.Scheduler.Every("example_heartbeat", 6*time.Hour, func(jobCtx context.Context) {
		ctx.Logger.Info("example_module heartbeat", "storage_dir", ctx.StorageDir)
	})

	return nil
}

// pingSkill is a trivial /example_ping command.
type pingSkill struct{}

func (pingSkill) Definition() skills.Definition {
	return skills.Definition{
		Name:        "example_ping",
		Description: "Example local module health check",
	}
}

func (pingSkill) Execute(ctx context.Context, input skills.Input) (skills.Result, error) {
	return skills.Result{Text: "🏓 example_module: pong"}, nil
}
