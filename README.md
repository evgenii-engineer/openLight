# openLight

![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)
![License](https://img.shields.io/badge/License-MIT-green)
![Target](https://img.shields.io/badge/Target-Raspberry%20Pi-red)

Telegram-first local agent for Raspberry Pi.

Deterministic host skills, SQLite state, and just enough LLM.

`openLight` is a small Go runtime for a very specific job: expose a narrow set of useful host skills through Telegram, keep routing deterministic, and use an LLM only where natural language actually helps.

[Architecture](./ARCHITECTURE.md) · [Config Templates](./configs) · [Systemd Unit](./deployments/systemd/openlight-agent.service) · [Docker Compose for Ollama](./deployments/docker/ollama-compose.yaml)

## At A Glance

| Area | Choice |
| --- | --- |
| Language | Go |
| Runtime | single binary |
| Storage | SQLite |
| Interface | Telegram |
| LLM providers | Ollama, OpenAI, generic HTTP |
| Routing model | deterministic first, LLM fallback |
| Deployment target | Raspberry Pi and small Linux hosts |
| Process model | systemd-friendly |

## Why openLight

Most agent projects start with autonomy and then try to add safety back in.  
`openLight` does the opposite:

- deterministic routing first
- LLM fallback second
- one small binary
- SQLite persistence
- Raspberry Pi-friendly deployment
- narrow, auditable skills instead of general shell access

This makes it a good fit for:

- Raspberry Pi home servers
- Telegram-based status and maintenance bots
- local-first assistants with Ollama
- simple remote ops with OpenAI as a fallback provider

## Design Position

| openLight | General-purpose agent stacks |
| --- | --- |
| Telegram-first host assistant | broad multi-tool autonomy |
| deterministic routing first | often LLM-first orchestration |
| explicit skills and groups | generic tool surfaces |
| single Go binary | larger runtime stacks |
| Raspberry Pi-friendly deploy | server or dev-machine oriented |
| SQLite state and simple ops | broader platform concerns |

## Highlights

- `system` skills: `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, `temperature`
- `services` skills: `service_list`, `service_status`, `service_logs`, `service_restart`
- `notes` skills: `note_add`, `note_list`, `note_delete`
- `chat` mode: free-form fallback when no tool matches
- two-stage LLM routing: route to group, then choose one concrete skill
- local or remote LLM providers: Ollama, OpenAI, or generic HTTP
- polling and webhook Telegram transports
- Raspberry Pi deploy scripts and systemd unit included

## What It Feels Like

```text
You: покажи общий статус
Bot: Hostname: raspberry
CPU: 2.0%
Memory: 1.9 GiB used / 7.9 GiB total
Disk: 864.1 GiB free / 916.3 GiB total
Uptime: 2d 4h 11m
Temperature: 58.7C
```

```text
You: покажи логи tailscale
Bot: Logs for tailscale:
...
```

```text
You: добавь заметку купить ssd
Bot: Saved note #3
```

```text
You: привет, как дела
Bot: Привет. Чем помочь?
```

## Runtime Model

The core idea is simple:

`deterministic routing first, LLM second`

```mermaid
flowchart TD
    A[Telegram message] --> B[Auth + persistence]
    B --> C[Deterministic routing]
    C --> D{Matched?}
    D -- yes --> E[Execute skill]
    D -- no --> F[LLM route classifier]
    F --> G{chat or group}
    G -- chat --> H[Chat skill]
    G -- group --> I[LLM skill classifier]
    I --> J[Validate]
    J --> E
    H --> K[Reply]
    E --> K
```

What the LLM does here:

- decide `chat` vs one skill group
- choose one concrete skill in that group
- extract minimal arguments

What the LLM does not do:

- execute commands
- plan long tool chains
- bypass validation
- access arbitrary shell tools

For the full breakdown, see [ARCHITECTURE.md](./ARCHITECTURE.md).

## Quick Start

1. Initialize a config:

```bash
make init-rpi-config
```

2. Fill in:

- `telegram.bot_token`
- `auth.allowed_user_ids`
- `auth.allowed_chat_ids`

3. Pick an LLM provider:

- `ollama` for local inference
- `openai` for hosted inference
- `generic` for a custom HTTP adapter

4. Deploy:

```bash
make deploy-rpi-all
ssh pi@raspberrypi.local "journalctl -u openlight-agent -f"
```

Config templates:

- [configs/agent.example.yaml](./configs/agent.example.yaml)
- [configs/agent.openai.example.yaml](./configs/agent.openai.example.yaml)
- [configs/agent.rpi.ollama.example.yaml](./configs/agent.rpi.ollama.example.yaml)

## LLM Setup

Ollama example:

```yaml
llm:
  enabled: true
  provider: "ollama"
  endpoint: "http://127.0.0.1:11434"
  model: "qwen2.5:0.5b"
  execute_threshold: 0.80
  mutating_execute_threshold: 0.95
  clarify_threshold: 0.60
  decision_input_chars: 160
  decision_num_predict: 128

chat:
  history_limit: 6
  history_chars: 900
  max_response_chars: 400
```

OpenAI example:

```yaml
llm:
  enabled: true
  provider: "openai"
  endpoint: "https://api.openai.com/v1"
  model: "gpt-4o-mini"
  api_key: ""
  execute_threshold: 0.80
  mutating_execute_threshold: 0.95
  clarify_threshold: 0.60
  decision_input_chars: 160
  decision_num_predict: 128
```

Notes:

- `chat.*` affects only free-form chat
- `llm.decision_*` affects only structured routing
- the same `llm.model` is used for route classification and skill classification
- `OPENAI_API_KEY` can be used instead of `llm.api_key`

## Telegram Modes

`openLight` supports:

- `telegram.mode: "polling"`
- `telegram.mode: "webhook"`

Webhook mode needs a public `https://...` URL that Telegram can reach.

Example:

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

## Built-In Skills

| Group | Skills |
| --- | --- |
| `system` | `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, `temperature` |
| `services` | `service_list`, `service_status`, `service_logs`, `service_restart` |
| `notes` | `note_add`, `note_list`, `note_delete` |
| `core` | `start`, `help`, `skills`, `ping` |
| `chat` | free-form LLM conversation |

## Local Ollama

Local Ollama compose lives in [deployments/docker/ollama-compose.yaml](./deployments/docker/ollama-compose.yaml).

```bash
make ollama-up
make ollama-pull
curl http://127.0.0.1:11434/api/generate \
  -d '{"model":"qwen2.5:0.5b","prompt":"reply with ok","stream":false}'
```

## Build, Test, Deploy

Build:

```bash
make build-rpi
```

Run tests:

```bash
GOCACHE=/tmp/go-build GOSUMDB=off go test ./...
```

Run real Ollama smoke tests:

```bash
make ollama-up
make ollama-pull
make test-e2e-ollama
make ollama-down
```

Deploy helpers:

- [Makefile](./Makefile)
- [scripts/deploy-rpi.sh](./scripts/deploy-rpi.sh)
- [scripts/deploy-rpi-config.sh](./scripts/deploy-rpi-config.sh)
- [scripts/deploy-rpi-service.sh](./scripts/deploy-rpi-service.sh)

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

## Extending

`openLight` is designed to grow in two directions:

- new LLM providers through [internal/llm/factory.go](./internal/llm/factory.go)
- new skills and modules through [internal/skills/module.go](./internal/skills/module.go)

The practical extension guide lives in [ARCHITECTURE.md](./ARCHITECTURE.md).

## Security Notes

- Telegram access is controlled by user/chat whitelist checks
- service management is limited to explicitly allowed services
- there is no general shell execution path in the bot runtime

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

## License

MIT. See [LICENSE](./LICENSE).
