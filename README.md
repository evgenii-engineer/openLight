# openLight

`openLight` is a lightweight Telegram-first agent for Raspberry Pi.

It is built for the practical case where frameworks like OpenClaw are simply too heavy: one small machine, a few useful tools, optional local LLM help, and predictable behaviour.

## What It Is

`openLight` is a small Go service that runs on Raspberry Pi, talks through a Telegram bot, stores state in SQLite, and exposes a focused set of operational skills:

- inspect machine health
- check and manage whitelisted services
- save and delete notes
- talk to a local LLM through Ollama

The project is intentionally narrow. It is not a full autonomous agent platform. It is a practical operator loop for a single box.

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

- `telegram_bot_token`
- `allowed_user_ids`
- `allowed_chat_ids`

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
llm_enabled: true
llm_provider: "ollama"
llm_endpoint: "http://127.0.0.1:11434"
llm_model: "qwen2.5:0.5b"
chat_history_limit: 6
chat_history_chars: 900
chat_max_response_chars: 400
```

Templates:

- [agent.example.yaml](./configs/agent.example.yaml)
- [agent.rpi.ollama.example.yaml](./configs/agent.rpi.ollama.example.yaml)

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
- Ollama chat and intent fallback
- Raspberry Pi deploy scripts and systemd unit

### Next

- structured tool calling for LLM decisions
- better observability and runtime diagnostics
- web search skill
- safer shell and file-oriented tools
- richer service and host management skills

## Security Notes

- secrets live in config or environment, not in code
- service operations are restricted to `allowed_services`
- user/chat access is whitelist-based
- no uncontrolled shell execution for service commands

## License

This project is licensed under the MIT License.

## Contact

Author: Evgenii Isupov

GitHub: https://github.com/evgenii-engineer

For bugs or feature requests please open a GitHub issue.
