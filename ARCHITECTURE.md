# Architecture

This is a short reference for how `openLight` is wired today.

## Design Constraints

`openLight` is intentionally narrow:

- Telegram-first interface
- deterministic routing before any LLM fallback
- explicit skills instead of general shell autonomy
- one Go binary plus one YAML config
- SQLite for persistence and lightweight runtime state
- deployment that fits Raspberry Pi and other small Linux hosts

## Request Lifecycle

For each incoming Telegram message, the runtime does the following:

1. receive the update through polling or webhook mode
2. check user and chat allowlists
3. store the message in SQLite
4. try deterministic routing first
5. optionally fall back to LLM route classification, then LLM skill classification
6. validate the selected route, selected skill, and required arguments
7. execute the selected skill
8. store the result and send the reply

If routing needs clarification, the pending question and original request are stored in SQLite settings so the next user reply can resume the same decision flow.

## Routing Model

Routing is layered and deterministic-first.

Priority order:

1. slash commands like `/cpu`
2. explicit commands such as `note_add hello`
3. exact skill names and aliases from the registry
4. semantic rules such as `restart tailscale`
5. optional LLM fallback
6. `chat` fallback when the `chat` skill is registered

### Deterministic Layer

The deterministic path bypasses the LLM entirely.
It handles slash commands, explicit command text, aliases, and a small semantic normalization layer for common English and Russian variants.

Examples:

- `read /tmp/openlight/app.conf`
- `replace 8080 with 8081 in /tmp/openlight/app.conf`
- `logs tailscale`
- `note buy milk`

### LLM Layer

LLM support is optional.
When `llm.enabled: false`, routing stays deterministic-only and the `chat` skill is not registered.

When enabled, the fallback is split into two steps:

1. route classification: choose `chat`, `unknown`, or one visible skill group
2. skill classification: choose one visible skill inside that group and extract minimal arguments

Common English and Russian semantic variants still resolve in the deterministic layer.
The LLM path is only used after deterministic routing has no match.

The Go side remains authoritative:

- it validates that the chosen skill belongs to the selected group
- it checks required arguments
- it uses route-stage confidence as the execution gate for `chat` and tool groups
- it treats the skill stage as skill selection, argument extraction, and clarification only
- it never lets the LLM bypass Go-side allowlists or argument validation

## Main Runtime Pieces

| Component | Responsibility | Key files |
| --- | --- | --- |
| Telegram transport | receive updates and send replies | [internal/telegram/client.go](./internal/telegram/client.go) |
| Auth | allowlist checks for users and chats | [internal/auth/auth.go](./internal/auth/auth.go) |
| Core agent | orchestration, persistence, clarification flow, execution | [internal/core/agent.go](./internal/core/agent.go) |
| Skill registry | skill metadata, aliases, visible groups, discovery | [internal/skills/registry.go](./internal/skills/registry.go) |
| Storage | messages, notes, skill calls, settings | [internal/storage/sqlite/sqlite.go](./internal/storage/sqlite/sqlite.go) |
| Router | deterministic rules plus optional classifier integration | [internal/router/router.go](./internal/router/router.go), [internal/router/llm/classifier.go](./internal/router/llm/classifier.go) |
| LLM provider layer | provider abstraction and factories | [internal/llm/provider.go](./internal/llm/provider.go), [internal/llm/factory.go](./internal/llm/factory.go) |

## Skill System

Skills are the unit of executable behavior.
Each skill exposes:

- `Definition()`
- `Execute(...)`

`Definition` carries the metadata used by help output, deterministic routing, and LLM catalogs:

- name
- group
- description
- aliases
- usage
- examples
- mutating flag
- hidden flag

Built-in groups:

- `core`
- `system`
- `files`
- `services`
- `notes`
- `workbench` when `workbench.enabled: true`
- `chat` when `llm.enabled: true`

Startup registers skills through modules rather than wiring each skill individually in `main.go`.

Relevant files:

- [internal/skills/module.go](./internal/skills/module.go)
- [internal/skills/core_module.go](./internal/skills/core_module.go)
- [internal/skills/files/module.go](./internal/skills/files/module.go)
- [internal/skills/system/module.go](./internal/skills/system/module.go)
- [internal/skills/services/module.go](./internal/skills/services/module.go)
- [internal/skills/notes/module.go](./internal/skills/notes/module.go)
- [internal/skills/workbench/module.go](./internal/skills/workbench/module.go)
- [internal/skills/chat/module.go](./internal/skills/chat/module.go)

## Safety Boundaries

`openLight` is opinionated about what the bot is allowed to do:

- file access is limited to `files.allowed`
- remote access is limited to named SSH hosts in `access.hosts`
- service actions are limited to `services.allowed`
- workbench execution is limited to one workspace, allowlisted runtimes, and exact allowlisted files
- the runtime does not expose unrestricted shell access
- the LLM cannot bypass Go-side validation

## LLM Providers

The repository currently includes:

- generic HTTP provider
- Ollama provider
- OpenAI Responses API provider

Provider wiring is done through a factory registry so new providers can be added without editing the main startup path.

Relevant files:

- [internal/llm/provider.go](./internal/llm/provider.go)
- [internal/llm/factory.go](./internal/llm/factory.go)
- [internal/llm/ollama.go](./internal/llm/ollama.go)
- [internal/llm/openai.go](./internal/llm/openai.go)
- [internal/llm/openai_tools.go](./internal/llm/openai_tools.go)

## Deployment Shape

The repository includes:

- Raspberry Pi build and deploy helpers in [Makefile](./Makefile)
- a local run helper in [scripts/run-local.sh](./scripts/run-local.sh)
- Raspberry Pi deploy scripts in [scripts](./scripts)
- a systemd unit in [deployments/systemd/openlight-agent.service](./deployments/systemd/openlight-agent.service)
- a local Ollama Compose file in [deployments/docker/ollama-compose.yaml](./deployments/docker/ollama-compose.yaml)

## Extension Points

The main extension surfaces are intentionally small:

- implement `skills.Skill` for one new executable behavior
- bundle related skills with `skills.Module`
- implement `llm.Provider` for a new provider
- register it through `llm.ProviderFactory`

The best reference for extension work is the existing built-in modules under [internal/skills](./internal/skills) and providers under [internal/llm](./internal/llm).
