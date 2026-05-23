# Architecture

`openLight` is a small self-hosted runtime for safe Telegram-based
operations on personal infrastructure (Raspberry Pi, Mac mini, homelab,
small VPS).

The shape of the project, in one breath:

- **deterministic-first** routing — slash commands, aliases, and rules
  run before any LLM is consulted
- **local-first** persistence — one SQLite file; no daemons, no cloud
- **allowlisted execution** — every action goes through a registered
  skill, and every skill resolves only services, files, hosts, and
  runtimes that are explicitly declared in config
- **optional LLM** — when enabled, it picks among already-registered
  skills; it never expands the surface

This document maps how the runtime is wired and how a request travels
through it.

## System at a glance

```text
Telegram (polling / webhook)  CLI
            \                /
             v              v
           core.Agent
             |
             |  preprocess (voice / image inbox)
             |  auth
             |  persist user message
             |
             v
           router.Router
             |  1. slash command       (/status, /watch, /enable, ...)
             |  2. explicit text       ("service tailscale")
             |  3. normalized shortcut ("cpu", "status", "память")
             |  4. registry alias      (exact skill name / alias)
             |  5. registry generic    (first-word skill + remainder)
             |  6. semantic rules      ("покажи логи tailscale")
             |  7. optional LLM classifier (route → skill, two stages)
             v
           skills.Registry
             |  resolve skill, validate args
             v
           skill.Execute  ── replies, attachments, buttons
             |
             v
           storage.Repository (SQLite)

watch.Service             — background poller, opens incidents, sends alerts
visualwatch.Service       — periodic screenshot diffs, optional
mcp.Manager               — connects to configured MCP servers, optional
external.Loader           — scans roots, registers user-defined skills
```

## Entry points

There is a single binary, `openlight`, with these subcommands:

| Subcommand          | Purpose                                                                |
|---------------------|------------------------------------------------------------------------|
| `openlight agent`   | run the Telegram bot (polling or webhook)                              |
| `openlight cli`     | run a one-shot or interactive CLI session against the same runtime    |
| `openlight doctor`  | read-only health probe for config and dependencies                     |
| `openlight skills`  | `list`, `validate`, `reload` builtin + external skills                 |
| `openlight version` | print build version                                                    |

All subcommands build the same `runtime.Runtime` and execute the same
skills through `core.Agent`. They differ only in the transport that
drives the loop.

Relevant files:

- [cmd/openlight/main.go](../cmd/openlight/main.go)
- [cmd/openlight/agent.go](../cmd/openlight/agent.go)
- [cmd/openlight/cli.go](../cmd/openlight/cli.go)
- [cmd/openlight/doctor.go](../cmd/openlight/doctor.go)
- [cmd/openlight/skills.go](../cmd/openlight/skills.go)
- [internal/core/agent.go](../internal/core/agent.go)

## Runtime assembly

`internal/runtime/runtime.go` wires the runtime in a fixed order:

1. Load and validate config.
2. Open SQLite (`storage.sqlite_path`) and apply embedded migrations.
3. Optionally open a second SQLite file for memory if `memory.db_path`
   is set to a different path.
4. Prune old `messages` and `skill_calls` rows if
   `storage.retention_days > 0`.
5. Build the LLM providers when `llm.enabled = true`:
   - `SMART` provider (chat, final answers, log explanations)
   - `FAST` provider (route + skill classification)
   - If no dedicated `fast` profile is configured, the same `SMART`
     provider is reused for routing (`FastFallback = true`)
   - Schedule background warmup for the profiles listed in
     `llm.warmup.profiles`
6. Build the service manager (local + per-node), system provider,
   optional MCP manager, optional vision manager.
7. Register skill modules via `BuildRegistryWithWatch`.
8. Wire the LLM classifier (only when an LLM provider was built).
9. Return the shared `Runtime` to the agent / CLI / doctor entrypoint.

The set of registered modules:

**Always-on:** `core`, `system`, `files`, `browser`, `services`,
`memory`, `notes`, `watch`.

**Conditionally registered:**

| Module          | Gated by                          |
|-----------------|-----------------------------------|
| `chat`          | `llm.enabled = true`              |
| `vision`        | `vision.enabled = true`           |
| `ocr`           | `ocr.enabled = true`              |
| `visual_watch`  | `visual_watch.enabled = true`     |
| `network`       | `network.enabled = true`          |
| `accounts`      | non-empty `accounts.providers`    |
| `workbench`     | `workbench.enabled = true`        |
| `mcp`           | `mcp.enabled` + non-empty servers |
| `external`      | non-empty `external_skills.roots` |
| `voice`         | `voice.enabled = true` (consumed inside `core.Agent`, not as a module) |

`browser` is always registered, but skill calls return a friendly
"disabled" message unless `browser.enabled = true`. The same pattern
applies to `files`, `memory`, and `vision`/`ocr` managers — they refuse
work cleanly when disabled.

The **core** module always registers last among builtins; the
**external** module registers after that. This ordering enforces a
hard invariant: a manifest on disk cannot shadow a builtin skill name.

Relevant files:

- [internal/runtime/runtime.go](../internal/runtime/runtime.go)
- [internal/config/config.go](../internal/config/config.go)
- [internal/skills](../internal/skills)

## Configuration model

`config.Load` combines three sources in order:

1. YAML file (if a path is given).
2. The selected LLM profile, if `llm.profile` or `LLM_PROFILE` is set —
   profile fields override the corresponding top-level `llm.*` fields.
3. Environment variable overrides.

After the merge the config is normalized (paths expanded, durations
defaulted, deprecated aliases coalesced) and then validated. Validation
errors abort startup.

Some keys have legacy aliases that are still accepted and merged at load
time:

| Legacy        | Canonical | Notes                                         |
|---------------|-----------|-----------------------------------------------|
| `access.hosts`| `nodes`   | both accepted; `nodes` is preferred           |
| `filesystem`  | `files`   | both accepted; `files` is preferred           |

Each load also fills `cfg.Deprecations` with one-line messages for any
legacy keys it noticed; the agent logs them on startup and `openlight
doctor` surfaces them as warnings.

Config search order for `openlight agent`:

1. `-config <path>` flag
2. `OPENLIGHT_CONFIG` env var
3. `/etc/openlight/agent.yaml`

`openlight cli` uses the passed `-config` path directly, or env /
defaults when no file is passed.

## Request lifecycle

For each incoming message, `core.Agent.HandleMessage` does this:

1. Start the Telegram "typing" indicator.
2. Pre-process attachments:
   - Voice notes go through `voice.Processor` (download → ffmpeg →
     whisper-cli → transcript). The transcript becomes the message text.
   - Photos go through `core.ImageInbox`, which routes the caption to
     either `vision_analyze` or `ocr_extract`. The skill result is
     returned directly.
3. Persist the user message (after `utils.RedactSensitiveText`).
4. Enforce `auth.allowed_user_ids` and `auth.allowed_chat_ids`.
5. Run the Telegram UI pipeline (only on the Telegram transport):
   callback queries from inline buttons, reply-keyboard taps, the
   `/menu` home screen, and any in-flight session form. Either the UI
   fully answers the message or it lets the call fall through.
6. Watch consumes alert-action callbacks and `yes` / `no` confirmations
   for pending incidents first.
7. Route the request. If a pending clarification exists for the chat
   and the new text looks like a follow-up, the agent composes
   "previous request + clarification question + user answer" and routes
   again.
8. If the router asks for clarification, persist the pending question
   in the `settings` table (10-minute TTL) and reply with it.
9. If nothing matched and `chat` is registered, fall back to `chat` for
   non-slash messages.
10. Execute the selected skill under `agent.request_timeout`. Persist
    a `skill_calls` row (name, args, status, duration, error).
11. Send the reply (text + optional photo + optional inline buttons).
12. Persist the assistant message.

Pending clarifications expire after 10 minutes.

Relevant files:

- [internal/core/agent.go](../internal/core/agent.go)
- [internal/core/image_inbox.go](../internal/core/image_inbox.go)
- [internal/voice/voice.go](../internal/voice/voice.go)
- [internal/telegram/ui](../internal/telegram/ui)

## Routing model

`router.Router.Route` is a layered, fail-fast pipeline. Each stage
returns a `Decision{Mode, SkillName, Args, Confidence,
NeedsClarification, ClarificationQuestion}`. The first stage that
matches wins; the LLM is only consulted when every deterministic stage
came up empty.

Stages, in order:

1. **Slash commands** (`ModeSlash`) — `/status`, `/watch add ...`,
   `/enable docker`, `/chat ...`, `/remember ...`, etc.
2. **Explicit text** (`ModeExplicit`) — `service tailscale`, `note add
   ...`, `watch list`. Tries 1–3 leading words against the same command
   table the slash router uses.
3. **Normalized shortcut** (`ModeShortcut`) — a single-token lookup
   against the already-normalized text. `semantic.Normalize` collapses
   case, punctuation, and common Russian variants (`проц` → `cpu`,
   `память` → `memory`, `статус` → `status`), so `статус` typed alone
   maps straight to the `status` skill.
4. **Registry alias** (`ModeAlias`) — exact match against any registered
   skill name or declared alias.
5. **Registry generic** (`ModeAlias`) — if the first word resolves to a
   registered skill, route to it with the remainder as the `text`
   argument. This is the path external skills with free-form arguments
   take.
6. **Semantic rules** (`ModeRule`) — hardcoded patterns under
   `internal/router/rules` for cross-language phrasings ("покажи логи
   tailscale" → `service_logs`).
7. **LLM classifier** (`ModeLLM`) — only when `classifier != nil`.

If every stage falls through, the result is `ModeUnknown`. For
non-slash text with `chat` registered, `core.Agent` then routes to the
`chat` skill so the LLM can answer conversationally.

### LLM path

The LLM classifier is two-stage and never free-form:

1. **Route classification**: pick a skill group (or `chat`, or
   `unknown`) from a closed list of group keys.
2. **Skill classification**: pick a concrete skill inside the chosen
   group from a closed list of candidate skills, and extract arguments.

Both stages report a confidence and may flag
`needs_clarification=true`. The router uses two thresholds:

| Threshold              | Default | Behavior                                                  |
|------------------------|---------|-----------------------------------------------------------|
| `execute_threshold`    | `0.80`  | At or above: execute the matched skill                    |
| `clarify_threshold`    | `0.60`  | At or above (but below execute): ask the user to confirm  |
| below `clarify_threshold` | —    | No match; falls back to `chat` for non-slash messages     |

The classifier runs on the **FAST** LLM profile (low-latency, JSON
output). The `chat` skill, log summarization, and final answers run on
the **SMART** profile. With no dedicated `fast` profile, both roles
share one provider.

The LLM never expands permissions. It can only pick from already
registered skills and the already-allowed service names, file roots,
hosts, runtimes, and account providers. Allowlists are enforced in Go,
inside each skill.

Relevant files:

- [internal/router/router.go](../internal/router/router.go)
- [internal/router/rules/rules.go](../internal/router/rules/rules.go)
- [internal/router/semantic/normalize.go](../internal/router/semantic/normalize.go)
- [internal/router/llm/classifier.go](../internal/router/llm/classifier.go)

## Skill model and safety boundaries

The registry is the execution boundary for the whole app. Every
user-visible action — `/status`, `/logs`, `/watch add`, `/note`,
`/remember`, an inline button on an alert — resolves to a registered
skill. The skill is also where allowlists are enforced.

Built-in skill groups (`internal/skills/groups.go`):

| Group          | What it owns                                                     |
|----------------|------------------------------------------------------------------|
| `core`         | `start`, `help`, `skills`, `ping`                                |
| `system`       | `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, `temperature`, `models` |
| `services`     | `service_list`, `service_status`, `service_logs`, `service_restart` |
| `watch`        | `watch_add`, `watch_list`, `watch_pause`, `watch_remove`, `watch_history`, `watch_test`, `watch_enable` |
| `files`        | `file_list`, `file_read`, `file_search`, `file_stat`, `file_write`, `file_replace` |
| `notes`        | `note_add`, `note_list`, `note_delete`                           |
| `memory`       | `memory_add`, `memory_list`, `memory_delete`                     |
| `browser`      | `browser_title`, `browser_text`, `browser_screenshot`, `browser_check` |
| `network`      | `port_check`, `http_check`, `cert_check`, `dns_check`            |
| `vision`       | `vision_analyze`, `vision_compare`                               |
| `ocr`          | `ocr_extract`                                                    |
| `visual_watch` | `visual_watch_add`, `visual_watch_list`, `visual_watch_remove`, `visual_watch_test` |
| `chat`         | `chat`                                                           |
| `accounts`     | `user_providers`, `user_add`, `user_list`, `user_delete`         |
| `workbench`    | `exec_code`, `exec_file`, `workspace_clean`                      |
| `mcp`          | one skill per tool exposed by each configured MCP server         |

Boundaries enforced inside Go (never delegated to the LLM):

- `files` only reads/writes paths under `files.allowed_roots`, refuses
  parent-traversal, and redacts secret-shaped lines when
  `files.redact_secrets = true`.
- `services` only operates on entries in `services.allowed`. A service
  spec can resolve to local `systemd`, Docker Compose, Docker
  container, or any of those backends on a named `node:<name>:...`.
- `nodes` (a.k.a. legacy `access.hosts`) defines the only SSH targets
  that remote service specs can reference.
- `accounts` runs explicit command templates inside already-allowed
  services.
- `workbench` is limited to one workspace directory plus the runtimes
  in `workbench.allowed_runtimes` and the files in
  `workbench.allowed_files`.
- `browser` only opens domains in `browser.allowed_domains` (or any
  public domain if `browser.allow_all_domains = true`) and refuses
  private-network targets unless `browser.allow_private_network = true`.
- `network` only probes targets in `network.allowed`.
- `mcp` only exposes tools listed in `mcp.servers.<name>.allowed_tools`
  (when set).
- `external` skills are subprocess-isolated: they get stdin/stdout/
  stderr and the env their manifest declares — no internal Go API.

## Watch subsystem

`watch.Service` is both a background poller and an action handler for
incidents. Polling cadence is `watch.poll_interval` (default `15s`).

Supported watch kinds (`internal/models`):

| Kind                  | What it watches                                  |
|-----------------------|--------------------------------------------------|
| `service_down`        | an allowlisted service is not active             |
| `cpu_high`            | CPU usage > N% for a duration                    |
| `memory_high`         | memory usage > N% for a duration                 |
| `disk_high`           | disk usage at a path > N% for a duration         |
| `temperature_high`    | sensor temp > N°C for a duration                 |
| `port_down`           | TCP host:port unreachable (requires `network`)   |
| `cert_expiring_soon`  | TLS cert within N days of expiry (requires `network`) |

Reaction modes:

- `notify` — send the alert; do not act. Default for metric watches.
- `ask` — send the alert with action buttons; wait up to
  `watch.ask_ttl` for a tap. Default for service-down watches.
- `auto` — perform the configured action immediately. Use sparingly.

### Polling cycle

1. Expire stale pending incidents.
2. Load enabled watches.
3. Evaluate each watch under `agent.request_timeout`. A slow probe
   cannot wedge the poller.
4. Update watch state (`condition_since`, `last_checked_at`,
   `last_triggered_at`).
5. Open a new incident when the condition holds for the required
   duration **and** cooldown has elapsed.
6. Send the alert through the bot transport.
7. Mark incidents resolved and send a recovery message when the
   condition clears.

### Alert actions

For service-down incidents, alerts offer `Restart`, `Logs`, `Status`,
`Ignore`. For metric incidents, alerts offer quick `Status` and
`Ignore`. Action buttons go through `core.Agent` and reuse the exact
same skills a user could type. One audit path for both manual and
automatic operations.

When the transport doesn't support inline buttons (CLI, older clients),
the alert text includes a `yes <id>` / `no <id>` fallback.

### Packs

A pack is a one-shot way to seed a curated set of watches in one
chat. Packs are idempotent: re-running `/enable docker` updates
existing watches in place instead of creating duplicates.

| Pack       | What it creates                                                                   |
|------------|-----------------------------------------------------------------------------------|
| `docker`   | `service ... ask` for every allowlisted Docker / Compose service                  |
| `system`   | `cpu > 90%`, `memory > 90%`, `disk / > 85%`                                       |
| `auto-heal`| `service ... auto` for every allowlisted service (use carefully)                  |
| `tls`      | `cert host:443 expires-in 14d` for every allowlisted network target               |
| `homelab`  | system pack + one `port host:port` watch per explicit `host:port` in `network.allowed` |
| `mac`      | system pack tuned for Mac mini (looser disk, no temperature — SMC needs root)     |
| `pi`       | system pack tuned for Raspberry Pi (lower disk threshold, explicit temperature)   |

Relevant files:

- [internal/watch/service.go](../internal/watch/service.go)
- [internal/watch/spec.go](../internal/watch/spec.go)
- [internal/watch/packs.go](../internal/watch/packs.go)
- [internal/visualwatch/service.go](../internal/visualwatch/service.go)

## Persistence model

SQLite (`modernc.org/sqlite`) is the only built-in persistence layer.
The repository keeps one open connection and applies embedded
migrations on startup.

Stored entities:

| Table              | Used for                                                       |
|--------------------|----------------------------------------------------------------|
| `messages`         | user + assistant chat history                                  |
| `skill_calls`      | execution audit trail (name, args, status, duration)           |
| `notes`            | short operator notes                                           |
| `memories`         | durable facts/preferences/tags (separate DB if `memory.db_path` is set) |
| `settings`         | pending clarifications, watch-pack markers, UI session state   |
| `watches`          | watch rules + current state                                    |
| `watch_incidents`  | open, resolved, pending, expired, completed incidents          |
| `visual_watches`   | visual watch specs, baseline paths, last-checked, cooldown     |

Migrations:

- `0001_init.sql` — messages, skill_calls, notes, settings
- `0002_watch.sql` — watches, watch_incidents
- `0003_memory.sql` — memories with kind/tags/source
- `0004_visual_watch.sql` — visual_watches
- `0005_message_retention.sql` — indexes for fast retention pruning

If `storage.retention_days > 0`, `Repository.PruneOlderThan` runs at
startup and deletes `messages` / `skill_calls` rows older than the
cutoff.

Default container database path: `/var/lib/openlight/data/agent.db`.

Relevant files:

- [internal/storage/storage.go](../internal/storage/storage.go)
- [internal/storage/sqlite/sqlite.go](../internal/storage/sqlite/sqlite.go)
- [migrations/](../migrations)

## LLM integration

Built-in providers (`internal/llm`):

- `generic` — any OpenAI-compatible HTTP endpoint
- `ollama` — local Ollama with explicit `keep_alive` control
- `openai` — official OpenAI API

The provider interface covers four operations: route classification,
skill classification, summarization, and chat.

### Profiles and roles

`llm.profiles` lets one config carry multiple model definitions and
select between them with `llm.profile` or `LLM_PROFILE`. Recognized
profile names today:

- `fast` — used by the router classifier (route + skill JSON output)
- `smart` — used by `chat`, log explanations, final answers
- `vision` — used by the vision manager when `vision.enabled = true`

Selection rules at startup:

- `SMART` is always resolved (falls back to top-level `llm.*` when no
  `profiles.smart` is defined).
- `FAST` is resolved when `profiles.fast` exists; otherwise the same
  `SMART` provider serves both roles (`FastFallback = true`).
- Warmup: every name in `llm.warmup.profiles` is loaded in the
  background with retries (5s → 5m, up to 8 attempts), keeping the
  configured `keep_alive` so Ollama doesn't unload the model.

When `llm.enabled = false`, no provider is built, no classifier is
attached, `chat` is not registered, and `vision`/`ocr` warmup paths
are skipped. The runtime stays fully functional in deterministic-only
mode.

Relevant files:

- [internal/llm/factory.go](../internal/llm/factory.go)
- [internal/llm/ollama.go](../internal/llm/ollama.go)
- [internal/llm/openai.go](../internal/llm/openai.go)
- [internal/llm/prompts.go](../internal/llm/prompts.go)

## Optional add-ons

These ship in the same binary, register only when enabled, and are
deliberately off-path so the project doesn't drift into being a
generic AI assistant.

| Subsystem      | Owns                                            | Notable deps                       |
|----------------|-------------------------------------------------|------------------------------------|
| `voice`        | Telegram voice notes → transcript               | `whisper-cli`, `ffmpeg`            |
| `browser`      | Page title / text / screenshot / contains-text  | Node.js, Playwright (via helper)   |
| `vision`       | Describe / compare images                       | local Ollama VLM or OpenAI         |
| `ocr`          | Extract text from images                        | `tesseract` (default)              |
| `visual_watch` | Periodic screenshot diff + keyword check        | `browser`, optional `ocr`          |
| `network`      | TCP / HTTP / TLS / DNS probes                   | none                               |
| `accounts`     | Provider-driven user management (Synapse, Jitsi, ...) | inside an already-allowed service |
| `workbench`    | Restricted code / file execution                | declared runtimes only             |
| `mcp`          | Tools from configured Model Context Protocol servers | stdio JSON-RPC subprocesses    |
| `external_skills` | User-defined subprocess skills               | none — anything `exec`-able        |

See [docs/SKILLS.md](./SKILLS.md), [docs/WATCHES.md](./WATCHES.md),
[docs/NODES.md](./NODES.md), and
[docs/skills/EXTERNAL.md](./skills/EXTERNAL.md) for details.

## Deployment model

Supported paths:

- native Linux / Raspberry Pi (systemd unit at
  [deployments/systemd/openlight-agent.service](../deployments/systemd/openlight-agent.service))
- macOS / Mac mini (launchd plist at
  [deployments/launchd/openlight-agent.plist](../deployments/launchd/openlight-agent.plist))
- Docker image ([Dockerfile](../Dockerfile))
- bundled Compose stack ([openlight-compose.yaml](../openlight-compose.yaml))

Container layout:

- binary: `/usr/local/bin/openlight`
- config dir: `/etc/openlight`
- data dir: `/var/lib/openlight/data`
- webhook port: `8081`

The image ships with a minimal embedded config that only sets the
SQLite path. Telegram credentials, allowlists, and any host-specific
execution surface come from env vars or a mounted config file.

Running inside Docker does **not** automatically grant host file or
service control. Those capabilities still require explicit config and,
when needed, the right mounts, sockets, or remote SSH targets.

Relevant files:

- [Dockerfile](../Dockerfile)
- [openlight-compose.yaml](../openlight-compose.yaml)
- [scripts/install.sh](../scripts/install.sh)
- [scripts/deploy-rpi.sh](../scripts/deploy-rpi.sh)
- [scripts/deploy-macmini.sh](../scripts/deploy-macmini.sh)

## Code map

If you want to read the code in roughly the right order:

1. [cmd/openlight/main.go](../cmd/openlight/main.go)
2. [cmd/openlight/agent.go](../cmd/openlight/agent.go)
3. [internal/runtime/runtime.go](../internal/runtime/runtime.go)
4. [internal/core/agent.go](../internal/core/agent.go)
5. [internal/router/router.go](../internal/router/router.go)
6. [internal/router/llm/classifier.go](../internal/router/llm/classifier.go)
7. [internal/skills](../internal/skills)
8. [internal/watch/service.go](../internal/watch/service.go)
9. [internal/visualwatch/service.go](../internal/visualwatch/service.go)
10. [internal/storage/sqlite/sqlite.go](../internal/storage/sqlite/sqlite.go)

Related docs:

- [README.md](../README.md)
- [CHANGELOG.md](../CHANGELOG.md)
- [docs/SKILLS.md](./SKILLS.md)
- [docs/WATCHES.md](./WATCHES.md)
- [docs/NODES.md](./NODES.md)
- [docs/skills/EXTERNAL.md](./skills/EXTERNAL.md)
