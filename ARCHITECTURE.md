# Architecture

This document describes how `openLight` is wired internally.

## Goals

The project is built around a small set of constraints:

- lightweight enough for Raspberry Pi
- Telegram-first interface
- tool-first execution model
- optional local LLM
- predictable behaviour over broad autonomy

## Runtime Flow

The main runtime path is:

`Telegram transport -> auth -> router -> skill execution -> optional LLM -> storage`

In practice:

1. Telegram polling receives a message.
2. The agent persists the incoming message to SQLite.
3. Auth checks user ID and chat ID against whitelists.
4. Router selects a skill using a fixed priority order.
5. The selected skill executes.
6. Skill result and metadata are persisted.
7. A text reply is sent back to Telegram.

## Routing Priority

Routing is intentionally deterministic.

Priority order:

1. slash commands like `/cpu`
2. explicit commands without slash like `note_add hello`
3. alias matches
4. rule-based parsing like `restart tailscale`
5. optional LLM classifier fallback
6. chat fallback when no skill matches

Relevant files:

- [router.go](./internal/router/router.go)
- [rules.go](./internal/router/rules/rules.go)
- [classifier.go](./internal/router/llm/classifier.go)

## Main Components

### Transport

Telegram transport handles Bot API polling and reply sending.

File:

- [client.go](./internal/telegram/client.go)

### Auth

Auth is a simple whitelist check for:

- allowed user IDs
- allowed chat IDs

File:

- [auth.go](./internal/auth/auth.go)

### Core Agent

The core agent coordinates:

- persistence of incoming and outgoing messages
- router decisions
- skill execution with timeouts
- user-friendly error mapping

File:

- [agent.go](./internal/core/agent.go)

### Skills

Skills are the main unit of behaviour. Each skill exposes:

- name
- description
- aliases
- usage
- execution handler

Files:

- [skill.go](./internal/skills/skill.go)
- [registry.go](./internal/skills/registry.go)

Skill groups currently implemented:

- system metrics
- service management
- notes
- chat
- meta commands like `start`, `help`, `skills`, `ping`

### LLM Layer

LLM support is optional.

Two providers exist:

- generic HTTP provider
- Ollama provider

Files:

- [provider.go](./internal/llm/provider.go)
- [ollama.go](./internal/llm/ollama.go)

LLM is used in two ways:

- classifier fallback for intent parsing
- free-form chat skill

### Storage

SQLite is the persistence layer.

Stored entities:

- `messages`
- `skill_calls`
- `notes`
- `settings`

Files:

- [storage.go](./internal/storage/storage.go)
- [sqlite.go](./internal/storage/sqlite/sqlite.go)
- [0001_init.sql](./migrations/0001_init.sql)

## Service Management Model

Service operations are intentionally constrained:

- only whitelisted services are allowed
- no arbitrary shell execution
- `systemctl` and `journalctl` are called directly
- `tailscale` is normalized to `tailscaled`
- `restart` can retry with `sudo -n` when systemd permissions require it

Files:

- [manager.go](./internal/skills/services/manager.go)
- [skills.go](./internal/skills/services/skills.go)

## Chat Model

The chat skill is tuned for small local models:

- short system prompt
- short retained history
- history filtering for command noise
- trimmed responses
- reset history on simple greetings

File:

- [skill.go](./internal/skills/chat/skill.go)

## Process Model

The application starts in [main.go](./cmd/agent/main.go), where it wires together:

- config
- logger
- SQLite repository
- LLM provider
- skill registry
- router
- Telegram transport
- core agent

Graceful shutdown is handled with `context` and OS signals.

## Deployment Model

The project includes a Raspberry Pi deployment flow:

- build ARM64 binary
- upload config
- upload binary
- install or update systemd service

Files:

- [Makefile](./Makefile)
- [deploy-rpi.sh](./scripts/deploy-rpi.sh)
- [deploy-rpi-config.sh](./scripts/deploy-rpi-config.sh)
- [deploy-rpi-service.sh](./scripts/deploy-rpi-service.sh)
- [openlight-agent.service](./deployments/systemd/openlight-agent.service)

## Extension Model

Adding a new skill is meant to stay simple:

1. add a new Go file in the relevant skill package
2. implement the `skills.Skill` interface
3. register it in [main.go](./cmd/agent/main.go)

Core routing and storage do not need to change unless the new capability truly needs new infrastructure.
