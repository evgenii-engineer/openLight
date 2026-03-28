# Architecture

`openLight` is a small self-hosted runtime for safe Telegram-based operations on personal infrastructure.

The core design is simple:

- deterministic routing first
- explicit allowlists for anything that can touch the host
- optional LLM fallback for classification and chat

This document is a technical map of how the runtime is assembled and how requests move through it.

## System at a glance

```text
Telegram Bot or CLI
  -> core.Agent
  -> auth.Authorizer
  -> router.Router
     -> deterministic route path
     -> optional LLM classifier
  -> skills.Registry
  -> storage.Repository (SQLite)

watch.Service runs alongside the agent:
  -> polls watches
  -> stores incidents
  -> sends proactive alerts
  -> reuses existing skills for alert actions
```

## Entry points

- `cmd/agent`: production runtime with Telegram polling or webhook transport
- `cmd/cli`: local transport for one-shot execution and smoke checks

Both binaries build the same runtime and execute the same skills through `core.Agent`.

Relevant files:

- [cmd/agent/main.go](./cmd/agent/main.go)
- [cmd/cli/main.go](./cmd/cli/main.go)
- [internal/core/agent.go](./internal/core/agent.go)

## Runtime assembly

`internal/app/runtime.go` wires the runtime in a fixed order:

1. load config
2. open SQLite and run embedded migrations
3. build the optional LLM provider
4. build the service manager and system provider
5. register skill modules
6. build the optional LLM classifier
7. return the shared runtime to `cmd/agent` or `cmd/cli`

Always-registered modules:

- `core`
- `system`
- `files`
- `services`
- `notes`
- `watch`

Conditionally registered modules:

- `accounts` when `accounts.providers` is configured
- `workbench` when `workbench.enabled` is true
- `chat` when LLM is enabled

Relevant files:

- [internal/app/runtime.go](./internal/app/runtime.go)
- [internal/config/config.go](./internal/config/config.go)
- [internal/skills](./internal/skills)

## Configuration model

`config.Load` combines three sources:

1. YAML file, if a path is provided
2. selected LLM profile, if `llm.profile` or `LLM_PROFILE` is set
3. env var overrides

Then it normalizes values and validates the final config before startup continues.

Examples of what config controls:

- Telegram mode and webhook settings
- auth allowlists
- SQLite path
- allowed services and file roots
- named SSH hosts
- watch timings
- LLM provider and thresholds
- optional account and workbench modules

Notes:

- `cmd/agent` resolves config from `-config`, then `OPENLIGHT_CONFIG`, then `/etc/openlight/agent.yaml`
- `cmd/cli` uses the passed `-config` path directly, or env/defaults when no file is passed

## Request lifecycle

For each incoming message, `core.Agent` does this:

1. ignore empty input
2. redact sensitive text before logging and persistence
3. save the user message in SQLite
4. enforce `allowed_user_ids` and `allowed_chat_ids`
5. let `watch.Service` consume alert-action callbacks or `yes` / `no` confirmations first
6. route the request
7. if a pending clarification exists for the chat, combine the original request with the follow-up and route again
8. if the router asks for clarification, save that pending clarification in SQLite settings and reply with the question
9. if nothing matched and `chat` is registered, fall back to `chat` for non-slash messages
10. execute the selected skill under `agent.request_timeout`
11. save the skill call
12. send the reply and save the assistant message

Pending clarification state is stored in the `settings` table and expires after 10 minutes.

Relevant files:

- [internal/core/agent.go](./internal/core/agent.go)
- [internal/storage/storage.go](./internal/storage/storage.go)

## Routing model

`router.Router` uses a layered pipeline:

1. slash commands such as `/status`
2. explicit command text such as `service tailscale`
3. direct registry identifiers and aliases
4. semantic rules
5. optional LLM classification
6. `chat` fallback in the agent when LLM is enabled

The deterministic path is intentionally strong. It handles:

- command parsing
- skill aliases
- common English and Russian variants through semantic normalization

Examples:

- `покажи логи tailscale` can normalize into `service_logs`
- `ram` and `memory` map to the same system skill
- `watch add`, `watch pause`, and similar subcommands are parsed before the LLM is involved

### LLM path

The LLM path is two-stage, not free-form execution:

1. classify the request into a skill group or chat
2. classify the concrete skill inside that group

The classifier uses:

- `execute_threshold`
- `clarify_threshold`
- per-layer input truncation and output-token limits
- allowlisted candidate groups, skills, services, and runtimes

If confidence is too low, the router either asks for clarification or returns no executable match.

The LLM never expands permissions. It can only choose among already registered skills and the already allowed service names, file roots, hosts, and runtimes.

Relevant files:

- [internal/router/router.go](./internal/router/router.go)
- [internal/router/rules/rules.go](./internal/router/rules/rules.go)
- [internal/router/semantic/normalize.go](./internal/router/semantic/normalize.go)
- [internal/router/llm/classifier.go](./internal/router/llm/classifier.go)

## Skill surface and safety boundaries

The registry is the execution boundary for the app. Every user-visible action goes through a skill definition.

Main skill groups:

- `core`: `start`, `help`, `skills`, `ping`
- `system`: host status and metrics
- `services`: list, status, restart, logs
- `watch`: add, list, pause, remove, history, test, enable packs
- `files`: list, read, write, replace
- `notes`: add, list, delete
- `chat`: free-form LLM chat
- `accounts`: optional provider-driven user management
- `workbench`: optional restricted code and file execution

Safety boundaries are enforced in Go, not delegated to the LLM:

- `files` can only touch paths under `files.allowed`
- `services` can only touch targets from `services.allowed`
- `access.hosts` defines the only named SSH targets available to remote service specs
- `accounts` execute explicit provider commands only through already allowed services
- `workbench` is limited to one workspace plus allowlisted runtimes and files

Service targets can resolve to:

- local `systemd`
- Docker Compose services
- Docker containers
- the same backends on named remote hosts via `host:<name>:...`

## Watch subsystem

`watch.Service` is both a background poller and an action handler for incidents.

Supported watch kinds:

- `service_down`
- `cpu_high`
- `memory_high`
- `disk_high`
- `temperature_high`

Supported reaction modes:

- `notify`
- `ask`
- `auto`

### Polling flow

Each watch cycle does this:

1. expire stale pending incidents
2. load enabled watches
3. evaluate each watch under `request_timeout`
4. update watch state such as `condition_since`, `last_checked_at`, and `last_triggered_at`
5. open a new incident when the condition stays true for the required duration and cooldown has elapsed
6. send an alert through the configured notifier
7. mark incidents resolved and send a recovery message when the condition clears

### Alert actions

For service-down incidents, alert actions can include:

- `Restart`
- `Logs`
- `Status`
- `Ignore`

For metric incidents, alerts offer quick status and ignore actions.

Important detail: watch actions reuse the existing skill surface. For example, a restart action internally calls the same `service_restart` skill that a user could invoke directly. This keeps one validation and auditing path for both manual and automatic operations.

When the transport supports buttons, alerts use inline buttons. Otherwise the runtime falls back to text confirmations like `yes <id>` or `no <id>`.

Relevant files:

- [internal/watch/service.go](./internal/watch/service.go)
- [internal/watch/spec.go](./internal/watch/spec.go)
- [internal/watch/actions.go](./internal/watch/actions.go)
- [internal/watch/packs.go](./internal/watch/packs.go)

## Persistence model

SQLite is the only built-in persistence layer.

Stored entities:

- messages
- skill calls
- notes
- settings
- watches
- watch incidents

What those tables are used for:

- `messages`: user and assistant chat history
- `skill_calls`: execution audit trail with status and duration
- `notes`: short operator notes
- `settings`: pending clarification state and watch-pack markers
- `watches`: configured rules and current watch state
- `watch_incidents`: open, resolved, pending, expired, or completed incidents

The repository uses `modernc.org/sqlite`, keeps a single open connection, and applies embedded migrations on startup.

Default container database path:

- `/var/lib/openlight/data/agent.db`

Relevant files:

- [internal/storage/storage.go](./internal/storage/storage.go)
- [internal/storage/sqlite/sqlite.go](./internal/storage/sqlite/sqlite.go)
- [migrations/0001_init.sql](./migrations/0001_init.sql)
- [migrations/0002_watch.sql](./migrations/0002_watch.sql)

## LLM integration

Built-in providers:

- `generic`
- `ollama`
- `openai`

The provider interface supports four operations:

- route classification
- skill classification
- summarization
- chat

When LLM is disabled:

- no provider is constructed
- no classifier is attached to the router
- the `chat` module is not registered

This means the runtime still works in deterministic-only mode for commands, rules, watches, services, files, and notes.

Relevant files:

- [internal/llm/factory.go](./internal/llm/factory.go)
- [internal/llm/ollama.go](./internal/llm/ollama.go)
- [internal/llm/openai.go](./internal/llm/openai.go)

## Deployment model

Supported deployment paths:

- native Linux or Raspberry Pi
- Docker image from [Dockerfile](./Dockerfile)
- bundled Compose stack from [openlight-compose.yaml](./openlight-compose.yaml)
- same bundled stack under [deployments/docker/openlight-compose.yaml](./deployments/docker/openlight-compose.yaml)

The two Compose files are currently identical. The top-level file is the simpler entrypoint for users.

Container layout:

- binary: `/usr/local/bin/openlight-agent`
- config dir: `/etc/openlight`
- data dir: `/var/lib/openlight/data`
- webhook port: `8081`

The image ships with a minimal embedded config that only sets the SQLite path. Telegram credentials, allowlists, and any host-specific execution surface must come from env vars or a mounted config file.

Important deployment constraint:

Running `openLight` inside Docker does not automatically grant host file access or host service control. Those capabilities still require explicit config and, when needed, the correct mounts, sockets, or remote SSH targets.

Relevant files:

- [Dockerfile](./Dockerfile)
- [openlight-compose.yaml](./openlight-compose.yaml)
- [deployments/systemd/openlight-agent.service](./deployments/systemd/openlight-agent.service)
- [scripts/install.sh](./scripts/install.sh)
- [scripts/deploy-rpi.sh](./scripts/deploy-rpi.sh)

## Code map

If you want to read the code in roughly the right order:

1. [cmd/agent/main.go](./cmd/agent/main.go)
2. [internal/app/runtime.go](./internal/app/runtime.go)
3. [internal/core/agent.go](./internal/core/agent.go)
4. [internal/router/router.go](./internal/router/router.go)
5. [internal/router/llm/classifier.go](./internal/router/llm/classifier.go)
6. [internal/skills](./internal/skills)
7. [internal/watch/service.go](./internal/watch/service.go)
8. [internal/storage/sqlite/sqlite.go](./internal/storage/sqlite/sqlite.go)

Related docs:

- [README.md](./README.md)
- [CHANGELOG.md](./CHANGELOG.md)
