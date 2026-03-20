# Architecture

`openLight` is a small self-hosted control plane for personal infrastructure.
Core idea: deterministic skills first, optional LLM fallback second.

## Entry Points

- `cmd/agent`: production runtime with Telegram polling or webhook transport
- `cmd/cli`: local execution, one-shot commands, and smoke checks

Both binaries build the same runtime and use the same `core.Agent`.

## Runtime

`internal/app/runtime.go` wires the app:

1. load config
2. open SQLite
3. build optional LLM provider
4. register skill modules
5. build optional LLM classifier

Registered groups depend on config:

- always: `core`, `system`, `files`, `services`, `notes`
- when configured: `accounts`, `workbench`
- when LLM is enabled: `chat`

## Request Flow

For every incoming message:

1. save message
2. check allowlists
3. route request
4. ask for clarification if needed
5. execute skill
6. save skill call and reply
7. send reply

Pending clarification state is stored in SQLite settings.

## Routing

Priority order:

1. slash commands
2. explicit command text
3. skill names and aliases
4. semantic rules
5. optional LLM route + skill classification
6. `chat` fallback when available

The LLM never bypasses Go-side validation.
Files, services, hosts, accounts, and workbench access stay allowlisted.

## Execution Surface

- `files`: only paths from `files.allowed`
- `services`: only targets from `services.allowed`
- `access.hosts`: named remote hosts for SSH-backed service actions
- `accounts`: provider commands executed through already allowed services
- `workbench`: one workspace, allowlisted runtimes, allowlisted files

Service targets can point to:

- local `systemd`
- Docker Compose services
- Docker containers
- the same backends on named remote hosts

## Persistence

SQLite stores:

- messages
- notes
- skill calls
- settings

Default container path: `/var/lib/openlight/data/agent.db`.

## LLM

Built-in providers:

- `generic`
- `ollama`
- `openai`

When LLM is disabled, routing is deterministic only and `chat` is not registered.

## Deploy

Supported paths:

- native Linux / Raspberry Pi
- Docker image from `Dockerfile`
- bundled Docker + Ollama stack from `deployments/docker/openlight-compose.yaml`

The container image includes a minimal `/etc/openlight/agent.yaml` with only the SQLite path.
Everything sensitive or host-specific is expected to come from env vars or a mounted config.

## CI / Release

- `ci.yml`: tests + Docker build smoke check on `push` and `pull_request`
- `docker-publish.yml`: multi-arch GHCR publish on `v*` tags or manual run
- `ollama-tests.yml`: real Ollama E2E on `v*` tags or manual run

## Main Files

- [internal/app/runtime.go](./internal/app/runtime.go)
- [internal/core/agent.go](./internal/core/agent.go)
- [internal/router/router.go](./internal/router/router.go)
- [internal/router/llm/classifier.go](./internal/router/llm/classifier.go)
- [internal/skills](./internal/skills)
- [internal/storage/sqlite/sqlite.go](./internal/storage/sqlite/sqlite.go)
- [deployments/docker/openlight-compose.yaml](./deployments/docker/openlight-compose.yaml)
