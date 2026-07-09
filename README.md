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
Telegram (polling / webhook)   CLI
            \                 /
             ▼               ▼
           core.Agent
             │   auth + persist + (voice / image inbox)
             ▼
           router  (slash → explicit → shortcut → alias → rules → optional LLM)
             │
             ▼
           skill registry  ── system, services, files, browser, watch,
             │              notes, memory, chat, vision, ocr, network,
             │              visual_watch, accounts, workbench, mcp,
             │              external_skills
             ▼
           SQLite    local processes    SSH nodes    optional Ollama / OpenAI
                                                       (FAST + SMART profiles)
```

The router is **deterministic-first**: slash commands, explicit text,
normalized shortcuts, registry aliases, and semantic rules all run
before any LLM is consulted. When the LLM is enabled, it is a
**fallback classifier**, not the runtime. It can only pick from
already-registered skills and already-allowlisted services, file
roots, hosts, runtimes, and network targets. **Allowlisted execution**
is enforced in Go, inside each skill. See
[docs/ARCHITECTURE.md](./docs/ARCHITECTURE.md).

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

## Single binary, four subcommands

```
openlight agent     # run the Telegram bot (production)
openlight cli       # run a one-shot or interactive CLI session
openlight doctor    # validate config, allowlists, and dependencies
openlight skills    # list, validate, reload builtin + external skills
```

`openlight doctor` is the fast feedback loop. It loads your config,
exercises every probe (Telegram, Ollama, SSH nodes, SQLite write,
optional voice / browser / vision / ocr deps), and prints OK / WARN /
FAIL per check.

## What it can do today

- Check host status from Telegram: `/status`, `/cpu`, `/memory`,
  `/disk`, `/uptime`, `/hostname`, `/ip`, `/temperature`.
- Inspect, tail, and restart allowlisted services across local
  `systemd`, Docker Compose, Docker, and named SSH **nodes**.
- Create service, metric, port, and TLS-cert **watches**, then receive
  Telegram alerts with `Restart`, `Logs`, `Status`, and `Ignore`
  action buttons.
- Enable curated packs: `/enable docker`, `/enable system`,
  `/enable auto-heal`, `/enable tls`, `/enable homelab`, `/enable mac`,
  `/enable pi`.
- Keep durable notes (`/note`) and durable memory (`/remember`,
  `/memories`, `/forget`) in SQLite.
- Take voice notes on Telegram and transcribe them via local
  `whisper-cli` + `ffmpeg`.
- Optionally fetch page titles, text, and screenshots through a
  Playwright helper, and run periodic **visual watches** that diff
  screenshots over time.
- Optionally describe / OCR images that arrive in chat, and probe TCP
  ports, HTTP endpoints, TLS certs, and DNS records on allowlisted
  hosts.
- Optionally expose **MCP** (Model Context Protocol) tools and
  **external skills** (subprocess executables with a `skill.yaml`
  manifest) alongside the builtins.
- Run with local Ollama, deterministic-only, or remote providers like
  OpenAI — and split the workload across **FAST** (router/classifier)
  and **SMART** (chat, log explanations) LLM profiles.
- Reuse the same runtime from Telegram, the CLI, and smoke tests.

## Core vs. optional

openLight is split into a small **core** that defines its identity
and a set of **optional** modules that you turn on only if you need
them.

**Core (always registered):** `core`, `system`, `services`, `watch`,
`files`, `browser`, `notes`, `memory`. The `browser` skill is gated by
`browser.enabled` at call time but lives in the always-on set.

**Optional (off by default):** `chat`, `vision`, `ocr`,
`visual_watch`, `network`, `accounts`, `workbench`, `voice`, `mcp`,
`external_skills`.

If you never enable any optional module, openLight stays a
deterministic, no-LLM, no-extra-deps Telegram bot for safe service
ops. That is the default posture and the recommended starting point.
See [docs/SKILLS.md](./docs/SKILLS.md).

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

A **watch** is a background rule that polls and fires when a
condition holds longer than a threshold:

```
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch add port grafana.internal:3000 for 30s
/watch add cert example.com expires-in 14d cooldown 24h
/watch list
```

When the condition trips, you get a Telegram alert with action
buttons. The buttons reuse the same skill surface as manual commands
— one audit path for both. Curated packs (`/enable docker`,
`/enable tls`, `/enable mac`, `/enable pi`, ...) seed sensible
defaults idempotently. See [docs/WATCHES.md](./docs/WATCHES.md).

## Voice notes (local transcription, Russian-first)

Send a voice note to the Telegram bot and openLight transcribes it locally
with whisper.cpp, then routes the text through the **same** router, auth,
allowlists, and audit path as a typed message. Audio never leaves the
machine.

Pipeline: `Telegram voice note → ffmpeg → whisper.cpp (ru) → openLight router → skill`.

Needs `voice.enabled: true` in the config plus `ffmpeg`, `whisper-cli`, and a
model on the host (`make install-voice-deps`). Diagnose it with
`openlight doctor` — it checks the ffmpeg/whisper binaries and the STT model
file.

Config (see `configs/agent.example.yaml`):

```yaml
voice:
  enabled: true
  language: "ru"
  whisper_cli_path: "/opt/homebrew/bin/whisper-cli"
  model_path: "~/models/ggml-small.bin"
  ffmpeg_path: "/opt/homebrew/bin/ffmpeg"
  reply_with_transcript: true
```

With `reply_with_transcript: true` the bot first echoes what it heard, then
acts on it. Audio is never persisted — the temp file is deleted right after
transcription.

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
storage:     # sqlite_path, retention_days
nodes:       # named SSH-reachable machines (legacy alias: access.hosts)
services:    # allowed targets + log limits
files:       # safe local file read/write (legacy alias: filesystem)
watch:       # poll interval + ask TTL
llm:         # provider, endpoint, model, profiles (fast/smart/vision), warmup
chat:        # history limits for chat skill
notes:       # list limit
memory:      # durable kv memory; optional separate sqlite file
agent:       # request_timeout
log:         # level
# Optional surfaces, off by default:
accounts:      # provider-driven user-management commands
workbench:     # restricted code/file execution
browser:       # browser skill (Playwright helper, Node.js)
vision:        # VLM-backed image analysis
ocr:           # Tesseract OCR (default)
voice:         # ffmpeg + whisper-cli for Telegram voice notes
visual_watch:  # screenshot-diff watches (uses browser + optional ocr)
network:       # TCP / HTTP / TLS / DNS probes
mcp:           # Model Context Protocol server tools
external_skills: # user-defined subprocess skills (skill.yaml)
```

Useful env overrides: `TELEGRAM_BOT_TOKEN`, `ALLOWED_USER_IDS`,
`ALLOWED_CHAT_IDS`, `SQLITE_PATH`, `LLM_ENABLED`, `LLM_PROVIDER`,
`LLM_ENDPOINT`, `LLM_MODEL`, `OPENAI_API_KEY`, `LLM_PROFILE`,
`TELEGRAM_MODE`, `TELEGRAM_WEBHOOK_URL`,
`TELEGRAM_WEBHOOK_LISTEN_ADDR`, `TELEGRAM_WEBHOOK_SECRET_TOKEN`,
`MEMORY_ENABLED`, `MEMORY_DB_PATH`, `BROWSER_ENABLED`,
`BROWSER_ALLOWED_DOMAINS`, `VOICE_ENABLED`, `VOICE_WHISPER_CLI_PATH`,
`VISION_ENABLED`, `VISION_PROVIDER`, `VISION_MODEL`, `OCR_ENABLED`,
`VISUAL_WATCH_ENABLED`, `WORKBENCH_ENABLED`.

## Local private modules

openLight can be extended with **local private modules** — machine-specific code
that lives on your box but **never enters this repository**. Core openLight stays
universal; your private logic (custom alerts, cost monitoring, business metrics,
integrations you can't open-source) lives under `local_modules/`, which is
gitignored.

A module gets a small, stable extension surface (`localmod.AppContext`): a
scoped logger, an env/config reader, a scheduler (interval + daily jobs), the
Telegram sender, the command registry, and a private storage directory. It never
receives the whole internal app object.

**Why it's safe**

- Nothing runs unless `OPENLIGHT_LOCAL_MODULES_ENABLED=true`.
- A module that's listed but not compiled in produces a warning, not a crash.
- A module that errors or panics during registration is contained and logged —
  a broken private module never brings down openLight core.
- `local_modules/` and the local build hook (`cmd/openlight/localmodules_local.go`)
  are gitignored. Core has no knowledge of any specific module.

**Enable one**

```bash
# 1. Copy the example (or your own module) into local_modules/
cp -r local_modules.example/example_module local_modules/example_module

# 2. Copy the build hook and point its blank imports at your module(s)
cp local_modules.example/localmodules_local.go.example \
   cmd/openlight/localmodules_local.go

# 3. Rebuild
go build ./cmd/openlight
```

Then enable it either via **environment** or via the **`local_modules:` block in
`agent.yaml`** — whichever fits your setup. Real environment variables take
precedence over config-file values, so you can keep everything in the config and
override individual keys at runtime.

```bash
# via environment
export OPENLIGHT_LOCAL_MODULES_ENABLED=true
export OPENLIGHT_LOCAL_MODULES_PATH=./local_modules
export OPENLIGHT_LOCAL_MODULES=example_module   # comma-separated for several
```

```yaml
# via agent.yaml (settings is an opaque passthrough; core never reads its keys)
local_modules:
  enabled: true
  path: ./local_modules
  modules: [example_module]
  settings:
    EXAMPLE_SETTING: "value"
```

Because Go is statically compiled, "loading a module" means the gitignored
`cmd/openlight/localmodules_local.go` blank-imports it (baking it into the
binary via `init()` self-registration); the env vars above then decide which
compiled-in modules actually activate. Fresh clones with no `local_modules/`
build and run exactly as before.

See [`local_modules.example/example_module/README.md`](local_modules.example/example_module/README.md)
for a complete walkthrough.

## Project structure

```
cmd/openlight/        single binary: agent, cli, doctor, skills subcommands
internal/runtime/     wires storage, skills, optional LLM, watch, visual_watch, MCP
internal/core/        request lifecycle (Agent), image inbox, voice handoff
internal/router/      deterministic + optional LLM classification
internal/skills/      built-in skill modules + external skill loader
internal/watch/       watch rules, incidents, alert actions, packs
internal/visualwatch/ periodic screenshot diff + keyword scan
internal/mcp/         stdio JSON-RPC client for Model Context Protocol servers
internal/voice/       voice-note pipeline (ffmpeg + whisper-cli)
internal/storage/     SQLite repository + embedded migrations
internal/telegram/    bot transport + UI (callbacks, sessions, keyboards)
configs/              example YAMLs
deployments/          systemd, launchd, docker
migrations/           embedded SQLite migrations
docs/                 architecture, nodes, watches, skills
scripts/              install + remote-deploy helpers (Pi, Mac mini)
tools/browser-agent/  Node.js Playwright helper invoked by the browser skill
testdata/skills/      example external skill (echo)
internal/localmod/    local private module loader + AppContext extension point
local_modules.example/ committable example module + build-hook template
local_modules/        your private modules (gitignored, not committed)
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
