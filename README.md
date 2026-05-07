# openLight

<p align="center">
  <img src="./docs/output.gif" width="500">
</p>

<p align="center">
  Synapse went down → Restart → Back online
</p>

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](./go.mod)
[![CI](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml/badge.svg)](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](./LICENSE)

**Lightweight local infrastructure agent for homelabs, Raspberry Pi, and personal servers — operated from Telegram.**

You declare which services, files, and SSH nodes are safe to touch.
openLight gives you a Telegram bot that checks status, tails logs,
restarts allowlisted services, and pages you when watches fire. One Go
binary, one YAML config, SQLite, optional local LLM. No frameworks.
No cloud. No hidden execution.

## Why

Plain shell scripts and ad-hoc bots stop scaling once you have more than
one host or more than one watch. Full agent frameworks are too heavy for
a Pi. openLight is the in-between: small enough to read end-to-end,
opinionated enough to be useful out of the box.

## Architecture in one diagram

```
Telegram / CLI
     │
     ▼
auth → router  (slash → command → alias → semantic → optional LLM)
     │
     ▼
skill registry  ── system, services, files, watch, notes, memory, chat
     │
     ▼
SQLite     local processes     SSH nodes     optional Ollama / OpenAI
```

The router is **deterministic-first**. Slash commands, explicit text, and
semantic rules run before the LLM is consulted. The LLM never bypasses
the Go-side allowlists — it can only choose among skills that are already
registered and resources that are already declared in your config.

## Quick start (60 seconds)

```bash
export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
curl -fsSL https://raw.githubusercontent.com/evgenii-engineer/openLight/master/scripts/install.sh | bash
```

This pulls the latest tagged release, drops `openlight-compose.yaml` into
`./openlight`, and starts the bot plus a local Ollama. Open Telegram and
try:

```
/start
/status
/enable system
/chat explain load average
```

For deterministic-only mode (no LLM at all), set `LLM_ENABLED=false` before
running the installer.

## Single binary, three subcommands

```
openlight agent     # run the Telegram bot (production)
openlight cli       # run a one-shot or interactive CLI session
openlight doctor    # validate config, allowlists, and dependencies
```

`openlight doctor` is the fast feedback loop. It loads your config,
exercises every probe (Telegram, Ollama, SSH nodes, SQLite write), and
prints OK / WARN / FAIL per check.

## What it can do today

- Check host status from Telegram: `/status`, `/cpu`, `/memory`, `/disk`,
  `/uptime`, `/hostname`, `/ip`, `/temperature`.
- Inspect, tail, and restart allowlisted services across local `systemd`,
  Docker Compose, Docker, and named SSH **nodes**.
- Create service and metric **watches**, then receive Telegram alerts with
  `Restart`, `Logs`, `Status`, and `Ignore` action buttons.
- Enable curated packs: `/enable docker`, `/enable system`, `/enable auto-heal`.
- Run with local Ollama, deterministic-only, or remote providers like OpenAI.
- Reuse the same runtime from Telegram, the CLI, and smoke tests.

## Core vs. optional

openLight is split into a small **core** that defines its identity and a
set of **optional** modules that you turn on only if you need them.

**Core (always on):** `core`, `system`, `services`, `watch`, `files`,
`notes`, `memory`.

**Optional (off by default):** `chat`, `accounts`, `workbench`, `browser`,
`vision`, `ocr`, `voice`, `visual_watch`.

If you never enable any optional module, openLight stays a deterministic,
no-LLM, no-extra-deps Telegram bot for safe service ops. That is the
default posture. See [docs/SKILLS.md](./docs/SKILLS.md).

## Nodes

A **node** is any machine openLight can reach over SSH. The local machine
is implicit; everything else goes in the `nodes:` block:

```yaml
nodes:
  vps:
    address: "203.0.113.10:22"
    user: "root"
    password_env: "OPENLIGHT_VPS_PASSWORD"
    known_hosts_path: "/home/pi/.ssh/known_hosts"

services:
  allowed:
    - tailscale
    - "matrix=node:vps:compose:/opt/matrix/docker-compose.yml:synapse"
```

Now `/status matrix`, `/logs matrix`, and `/restart matrix` work against
the right backend on the right node, with the same Telegram UX as a local
service. See [docs/NODES.md](./docs/NODES.md).

## Watches

A **watch** is a background rule that polls and fires when a condition
holds longer than a threshold:

```
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch list
```

When the condition trips, you get a Telegram alert with action buttons.
The buttons reuse the same skill surface as manual commands — one audit
path for both. See [docs/WATCHES.md](./docs/WATCHES.md).

## Run with Docker / Compose

If you cloned the repo, the top-level
[openlight-compose.yaml](./openlight-compose.yaml) is the same bundled
stack the installer uses:

```bash
git clone https://github.com/evgenii-engineer/openLight.git
cd openLight

export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
docker compose up -d
```

The image ships with a minimal embedded config that only sets the SQLite
path. Anything host-specific (file allowlists, services, nodes, webhook
mode, OpenAI) needs a mounted config:

```yaml
services:
  openlight:
    volumes:
      - ./data:/var/lib/openlight/data
      - ./agent.yaml:/etc/openlight/agent.yaml:ro
```

## Run locally

Prerequisites: Go 1.25+, a writable SQLite path, a Telegram bot token.

```bash
cp configs/agent.example.yaml ./agent.yaml
# edit ./agent.yaml

go run ./cmd/openlight agent -config ./agent.yaml
```

Other example configs:

- [configs/agent.rpi.ollama.example.yaml](./configs/agent.rpi.ollama.example.yaml) — Raspberry Pi + Ollama
- [configs/agent.macmini.example.yaml](./configs/agent.macmini.example.yaml) — Mac mini local-first
- [configs/agent.openai.example.yaml](./configs/agent.openai.example.yaml) — OpenAI-backed

`openlight agent` resolves config in this order: `-config`,
`OPENLIGHT_CONFIG`, `/etc/openlight/agent.yaml`.

For local Ollama in the repo checkout:

```bash
make llm-up
make llm-pull
go run ./cmd/openlight agent -config ./agent.yaml
```

## Deploy

Raspberry Pi:

```bash
cp configs/agent.rpi.ollama.example.yaml ./agent.rpi.yaml
make deploy-rpi-full PI_HOST=raspberrypi.local PI_USER=pi CONFIG_SRC=./agent.rpi.yaml
make smoke-rpi-cli-ollama PI_HOST=raspberrypi.local PI_USER=pi SMOKE_FLAGS='-smoke-all'
```

Mac mini (M1):

```bash
cp configs/agent.macmini.example.yaml configs/agent.macmini.yaml
make install-macmini-deps
make bootstrap-macmini SSH_HOST=100.x.y.z
make deploy-macmini SSH_HOST=100.x.y.z
make status-macmini SSH_HOST=100.x.y.z
```

Service templates: [systemd](./deployments/systemd/openlight-agent.service),
[launchd](./deployments/launchd/openlight-agent.plist).

## Configuration reference

Top-level keys:

```yaml
telegram:    # bot token, polling/webhook mode
auth:        # allowed_user_ids, allowed_chat_ids
storage:     # sqlite_path
nodes:       # named SSH-reachable machines (legacy: access.hosts)
services:    # allowed targets + log limits
watch:       # poll interval + ask TTL
filesystem:  # safe local file read/write (legacy: files)
llm:         # provider, endpoint, model, profiles
chat:        # history limits for chat skill
notes:       # list limit
memory:      # persistent kv notes for the LLM
agent:       # request_timeout
log:         # level
# Optional surfaces, off by default:
accounts:    # provider-driven user-management commands
workbench:   # restricted code/file execution
browser:     # browser skill (Playwright)
vision:      # VLM-backed image analysis
ocr:         # Tesseract OCR
voice:       # ffmpeg + whisper-cpp
visual_watch: # screenshot-diff watches (uses browser + ocr)
```

Useful env overrides: `TELEGRAM_BOT_TOKEN`, `ALLOWED_USER_IDS`,
`ALLOWED_CHAT_IDS`, `SQLITE_PATH`, `LLM_ENABLED`, `LLM_PROVIDER`,
`LLM_ENDPOINT`, `LLM_MODEL`, `OPENAI_API_KEY`, `LLM_PROFILE`,
`TELEGRAM_MODE`, `TELEGRAM_WEBHOOK_URL`, `TELEGRAM_WEBHOOK_LISTEN_ADDR`,
`TELEGRAM_WEBHOOK_SECRET_TOKEN`.

## Project structure

```
cmd/openlight/      single binary: agent, cli, doctor subcommands
internal/runtime/   wires storage, skills, optional LLM, watch
internal/router/    deterministic + optional LLM classification
internal/core/      request lifecycle (the Agent type)
internal/skills/    built-in skill modules
internal/watch/     watch rules, incidents, alert actions
internal/storage/   SQLite repository
internal/telegram/  bot transport + UI
configs/            example YAMLs
deployments/        systemd, launchd, docker
migrations/         embedded SQLite migrations
docs/               architecture, nodes, watches, skills
scripts/            install + remote-deploy helpers
```

## Good fit / Not a fit

**Good fit:**

- Raspberry Pi, homelabs, and small self-hosted Linux boxes
- Telegram-based status checks, alerts, and light operational actions
- A small codebase you can read end-to-end and extend in Go

**Not a fit:**

- generic AI assistant
- autonomous multi-agent platforms
- enterprise orchestration framework
- arbitrary shell autonomy
- a browser-automation framework

## Regression and smoke checks

| Level | When | Command | Covers |
|-------|------|---------|--------|
| P0 | every commit | `make test` | unit/router/skill/config/storage/LLM-safety |
| P0 smoke | every commit | `make smoke-cli` | local CLI flows against `configs/agent.test.yaml` |
| P1 | before release | `make regression` | `make test` + `make smoke-cli` |
| P2 | on a real host | `make smoke-macmini SSH_HOST=…` / `make smoke-rpi PI_HOST=…` | host deps, service manager, Telegram, Ollama |
| Manual | post-deploy | 5-min Telegram sanity | `/start`, `/skills`, `/status`, logs, watches |

CI runs `make test`. CI does not run P2 — those targets SSH into a real host.
The full matrix lives in [docs/REGRESSION.md](./docs/REGRESSION.md).

## Contributing

Small, focused contributions are the best fit here.

```bash
make test          # P0
make smoke-cli     # P0 deterministic CLI smoke
make regression    # P1: both, before release-shaped changes
make doctor        # validate ./configs/agent.example.yaml
```

For deeper details, see [docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md),
[docs/NODES.md](./docs/NODES.md), [docs/WATCHES.md](./docs/WATCHES.md),
[docs/SKILLS.md](./docs/SKILLS.md), and [CHANGELOG.md](./CHANGELOG.md).

## License

MIT. See [LICENSE](./LICENSE).
