# example_module

A minimal, committable template for an **openLight local private module**.

Local modules let you extend openLight with private, machine-specific code that
**never lands in the main repository**. Core openLight stays universal; your
private logic lives under `local_modules/` (which is gitignored).

## What it demonstrates

- `localmod.Register(&Module{})` from `init()` — self-registration so a blank
  import compiles the module into the binary.
- `Register(ctx *localmod.AppContext)` — the wiring entrypoint (equivalent to
  the `register(app_context)` shape).
- Registering a Telegram command: `/example_ping` → replies `pong`.
- Registering a scheduled job: a 6-hour heartbeat log.

## How to use it

1. Copy this directory into `local_modules/`:

   ```bash
   mkdir -p local_modules
   cp -r local_modules.example/example_module local_modules/example_module
   ```

   (For a real module, rename it and change `moduleName` in `module.go`.)

2. Copy the build hook so the binary compiles your module in:

   ```bash
   cp local_modules.example/localmodules_local.go.example \
      cmd/openlight/localmodules_local.go
   ```

   Then edit `cmd/openlight/localmodules_local.go` so its blank imports point at
   your module(s). Both this file and `local_modules/` are gitignored.

3. Rebuild:

   ```bash
   go build ./cmd/openlight
   ```

4. Enable it via environment variables (e.g. in `.env`):

   ```bash
   OPENLIGHT_LOCAL_MODULES_ENABLED=true
   OPENLIGHT_LOCAL_MODULES_PATH=./local_modules
   OPENLIGHT_LOCAL_MODULES=example_module
   ```

5. Start openLight and send `/example_ping` in Telegram → it replies `pong`.

## Safety guarantees

- If `OPENLIGHT_LOCAL_MODULES_ENABLED` is unset/false, nothing here runs.
- If the module errors or panics during registration, the loader logs it and
  openLight continues — a broken private module never crashes core.
- Never put real secrets or private business logic in `local_modules.example/`.
  Keep those in `local_modules/`.
