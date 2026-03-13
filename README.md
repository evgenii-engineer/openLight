# openLight

Lightweight Telegram-first AI agent for Raspberry Pi, built in Go and designed to run with a local LLM.

`openLight` is a practical alternative to heavier agent frameworks like OpenClaw. It focuses on the simple local loop that people actually want on a Raspberry Pi: system checks, service control, notes, and lightweight chat through Telegram, without dragging in a full autonomous stack.

- Telegram-first interface
- Local LLM via Ollama
- Raspberry Pi friendly
- SQLite storage
- Small skill-based architecture

## Why It Exists

Most AI-agent frameworks are built for broad automation, multi-step planning, or cloud-heavy workflows. That is useful, but it is often too much for an edge device.

`openLight` takes the opposite approach:

- one machine
- one Telegram interface
- a small toolset
- optional local intelligence
- predictable behaviour first, LLM second

It is not trying to be a general autonomous platform. It is trying to be a useful bot you can actually run on a Raspberry Pi every day.

## Demo / Use Cases

Good fits:

- home server or homelab Raspberry Pi
- remote Telegram control for `tailscale`, `jellyfin`, `nginx`, and similar services
- private local assistant with Ollama on-device
- lightweight ops bot for status, logs, and restarts
- simple note capture and reminders without extra infrastructure

## Telegram Examples

```text
You: /skills
Bot: Available skills:

Chat
- chat: Talk to the local LLM in free-form mode.

Notes
- note_add: Add a short note to SQLite storage.
- note_list: List the latest saved notes.
- note_delete: Delete a saved note by id.
...
```

```text
You: service_list
Bot: Allowed services:
- tailscale: active
```

```text
You: note_add buy SSD
Bot: Saved note #1

You: note_list
Bot: Notes:
- #1 buy SSD

You: note_delete 1
Bot: Deleted note #1
```

```text
You: chat привет, как дела
Bot: Привет! Всё нормально. Чем помочь?
```

```text
You: restart tailscale
Bot: Service restarted: tailscale
```

## Quick Start

First, create your local config and fill in `configs/agent.rpi.yaml` with:

- `telegram.bot_token`
- `auth.allowed_user_ids`
- `auth.allowed_chat_ids`

By default the bot uses Telegram long polling with `telegram.mode: "polling"`.

Then bring the agent up in 3 commands:

```bash
make init-rpi-config
make deploy-rpi-all
ssh pi@raspberrypi.local "journalctl -u openlight-agent -f"
```

If you use your own Raspberry user or host, keep them in `Makefile.local` so you can still use the same commands.

## Skills / Commands

### Chat

- `/chat <message>`
- `chat <message>`
- plain text fallback when no tool matches

### Notes

- `note_add <text>`
- `note_list`
- `note_delete <id>`

### Services

- `service_list`
- `service_status [service]`
- `service_logs [service]`
- `service_restart <service>`
- natural language examples:
  - `restart tailscale`
  - `show jellyfin logs`

If only one service is whitelisted, `service_status` and `service_logs` can omit the service name.

### System

- `/status`
- `/cpu`
- `/memory`
- `/disk`
- `/uptime`
- `/hostname`
- `/ip`
- `/temperature`

### Core

- `/start`
- `/help`
- `/skills`
- `/ping`

## Architecture Overview

The runtime flow is:

`Telegram transport -> auth -> router -> skill execution -> optional LLM -> SQLite persistence`

High level components:

- transport: Telegram Bot API polling and replies
- auth: user/chat whitelist checks
- router: slash commands, explicit commands, rule-based parsing, optional LLM classifier
- skills: chat, notes, services, system metrics, meta commands
- llm: Ollama or generic HTTP provider
- storage: SQLite for messages, notes, skill calls, and settings

Full breakdown lives in [ARCHITECTURE.md](./ARCHITECTURE.md).

## Deploy To Raspberry Pi

The repo already includes:

- [Makefile](./Makefile)
- [deploy-rpi.sh](./scripts/deploy-rpi.sh)
- [deploy-rpi-config.sh](./scripts/deploy-rpi-config.sh)
- [deploy-rpi-service.sh](./scripts/deploy-rpi-service.sh)
- [openlight-agent.service](./deployments/systemd/openlight-agent.service)

Deploy layout:

- config on Pi: `/etc/openlight/agent.yaml`
- binary on Pi: `/home/<user>/openlight-agent`
- systemd unit: `/etc/systemd/system/openlight-agent.service`

Useful commands:

```bash
make build-rpi
make deploy-rpi-config
make deploy-rpi
make deploy-rpi-service
make deploy-rpi-all
```

## Why This Stack

### Why Not OpenClaw

OpenClaw is aimed at broader agent workflows. `openLight` is for the smaller, sharper problem:

- one Raspberry Pi
- one Telegram interface
- a small toolset
- low memory footprint
- predictable behaviour

If you want a compact operator bot, `openLight` is the simpler fit.

### Why Go

- small static binaries
- easy deployment to Raspberry Pi
- low runtime overhead
- good fit for long-running services
- straightforward concurrency and context-based cancellation

### Why Raspberry Pi

- cheap and available edge hardware
- good enough for a Telegram bot, SQLite, and a small local LLM
- ideal for private homelab and home server use

## Local LLM Setup

Example Ollama config:

```yaml
llm:
  enabled: true
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "qwen2.5:0.5b"
  execute_threshold: 0.80
  clarify_threshold: 0.60

chat:
  history_limit: 6
  history_chars: 900
  max_response_chars: 400
```

Templates:

- [agent.example.yaml](./configs/agent.example.yaml)
- [agent.rpi.ollama.example.yaml](./configs/agent.rpi.ollama.example.yaml)

## Telegram Webhooks

`openLight` supports both transport modes:

- `telegram.mode: "polling"` for the current simple `getUpdates` flow
- `telegram.mode: "webhook"` for inbound Telegram webhooks

Webhook mode needs a public `https://...` URL that Telegram can reach. Example:

```yaml
telegram:
  bot_token: "123456:replace-me"
  api_base_url: "https://api.telegram.org"
  mode: "webhook"
  poll_timeout: 25s
  webhook:
    url: "https://bot.example.com/openlight/webhook"
    listen_addr: ":8081"
    secret_token: "replace-me"
    drop_pending_updates: false
```

In webhook mode the agent will:

- start a local HTTP server on `telegram.webhook.listen_addr`
- call Telegram `setWebhook` on startup
- validate `X-Telegram-Bot-Api-Secret-Token` when `secret_token` is set

If you switch back to polling, the agent automatically calls `deleteWebhook` on startup so `getUpdates` works again.

## Build And Test

```bash
make build-rpi
GOCACHE=/tmp/go-build GOSUMDB=off go test ./...
```

## Roadmap

### v0.0.1

- Telegram bot transport
- whitelist auth
- SQLite persistence
- system metrics skills
- service skills
- notes add/list/delete
- rule-based routing
- Ollama chat and structured decision fallback
- Raspberry Pi deploy scripts and systemd unit

### Next

- richer structured decision routing for local LLMs
- better observability and runtime diagnostics
- web search skill
- safer shell and file-oriented tools
- richer service and host management skills

## Security Notes

- secrets live in config or environment, not in code
- service operations are restricted to `services.allowed`
- user/chat access is whitelist-based
- no uncontrolled shell execution for service commands

## License

This project is licensed under the MIT License.

## Contact

Author: Evgenii Isupov

GitHub: https://github.com/evgenii-engineer

For bugs or feature requests please open a GitHub issue.
