# openLight

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](./go.mod)
[![CI](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml/badge.svg)](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-green)](./LICENSE)

Safe Telegram ops for Raspberry Pi, homelabs, and small Linux hosts.

`openLight` is a lightweight self-hosted agent for checking box status, handling safe service actions, and receiving actionable alerts from Telegram. It exists for setups where a full agent framework is too heavy, but plain scripts and ad hoc bots are not enough.

- Deterministic-first routing. Slash commands, explicit commands, aliases, and semantic rules run before LLM fallback.
- Safe allowlisted operations. Files, services, runtimes, and remote hosts must be declared in config.
- Local Ollama by default. The bundled Docker path starts `openLight` with Ollama, but the same runtime can also run deterministic-only or with OpenAI.

## Good fit / Not a fit

Good fit:

- Raspberry Pi, homelabs, and small self-hosted Linux boxes
- Telegram-based status checks, alerts, and light operational actions
- Users who want a small codebase they can inspect and extend

Not a fit:

- browser agents
- arbitrary shell autonomy
- complex multi-agent orchestration

## What it can do today

Core use case: safe Telegram-based status checks, service actions, and alerts for self-hosted boxes.

- Check host status quickly from Telegram with `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, and `temperature`.
- Inspect, tail logs, and restart allowlisted services across local `systemd`, Docker Compose, Docker, and named SSH targets.
- Create service and metric watches, then receive Telegram alerts with `Restart`, `Logs`, `Status`, and `Ignore` actions.
- Enable built-in packs with `/enable docker`, `/enable system`, and `/enable auto-heal`.
- Run with local Ollama, deterministic-only mode, or remote providers such as OpenAI.
- Reuse the same runtime from Telegram and `cmd/cli` for local execution and smoke checks.

## Quick start

Recommended path: use the bundled installer. It resolves the latest tagged release, downloads `openlight-compose.yaml`, and starts `openLight` plus `Ollama` in `./openlight`.

```bash
export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
curl -fsSL https://raw.githubusercontent.com/evgenii-engineer/openLight/master/scripts/install.sh | bash
```

After it starts, open Telegram and try:

```text
/start
/status
/enable system
/chat explain load average
```

Typical first-minute flow with the bundled default stack:

```text
You: /status
openLight:
Hostname: <host>
CPU: <usage>
Memory: <used> / <total>

You: /enable system
openLight:
System pack enabled.
Created 3 watch(es), updated 0.
Defaults: CPU > 90%, Memory > 90%, Disk / > 85%.
System alerts will offer quick Status and Ignore actions.
```

With allowlisted services configured, a service alert looks like:

```text
You: /watch add service tailscale ask for 30s cooldown 10m
openLight:
Watch created:
#7 service/tailscale down

Later, if the service goes down:
openLight:
Alert #7
tailscale is down
[Restart] [Logs] [Status] [Ignore]
```

That is the core loop: define a safe watch once, then handle real incidents from Telegram.

If you want deterministic-only mode, set `LLM_ENABLED=false` before running the installer.

## Architecture overview

```text
Telegram or CLI
  -> auth checks
  -> router
  -> skill registry
  -> storage / watch service / optional LLM
```

- `cmd/agent` runs the Telegram bot in polling or webhook mode.
- `cmd/cli` runs the same runtime locally and adds one-shot execution plus smoke tests.
- `internal/app` wires storage, skills, the optional LLM provider, and the watch service.
- `internal/router` handles slash commands, explicit command text, semantic rules, and optional LLM classification.
- `internal/skills` contains the built-in modules: `system`, `services`, `files`, `notes`, `watch`, `chat`, `accounts`, and `workbench`.
- `internal/storage/sqlite` persists messages, skill calls, notes, watches, watch incidents, and settings.

## Run with Docker / Compose

If you already cloned the repo, the top-level [openlight-compose.yaml](./openlight-compose.yaml) is the same bundled stack used by the installer.

```bash
git clone https://github.com/evgenii-engineer/openLight.git
cd openLight

export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
docker compose up -d
```

This stack starts:

- `openlight` from `ghcr.io/evgenii-engineer/openlight:latest`
- `ollama`
- `ollama-pull`, which pulls `qwen2.5:0.5b` by default

Notes for the bundled stack:

- It is aimed at the local Ollama path.
- It only mounts `./data` by default.
- The image ships with a minimal `/etc/openlight/agent.yaml` that only sets the SQLite path.
- The bundled Compose env expects `ALLOWED_USER_IDS` for the quick-start path.
- If you want host file access, host service access, remote SSH hosts, workbench, accounts, webhook mode, or a different provider setup, mount your own config file.

Example mount:

```yaml
services:
  openlight:
    volumes:
      - ./data:/var/lib/openlight/data
      - ./agent.yaml:/etc/openlight/agent.yaml:ro
```

For deterministic-only Docker usage:

```bash
export LLM_ENABLED=false
docker compose up -d
```

## Run locally

Prerequisites:

- Go `1.25+`
- a writable SQLite path
- a Telegram bot token

Start from the closest example config:

- [configs/agent.example.yaml](./configs/agent.example.yaml): deterministic baseline
- [configs/agent.rpi.ollama.example.yaml](./configs/agent.rpi.ollama.example.yaml): Raspberry Pi plus Ollama
- [configs/agent.openai.example.yaml](./configs/agent.openai.example.yaml): OpenAI-backed

Example local run:

```bash
cp configs/agent.example.yaml ./agent.yaml
# edit ./agent.yaml

go run ./cmd/agent -config ./agent.yaml
```

If you want local Ollama for the repo checkout:

```bash
make ollama-up
make ollama-pull
go run ./cmd/agent -config ./agent.yaml
```

The agent binary checks config in this order:

1. `-config`
2. `OPENLIGHT_CONFIG`
3. `/etc/openlight/agent.yaml`

For Raspberry Pi deployment, the repo includes build and deploy helpers:

```bash
cp configs/agent.rpi.ollama.example.yaml ./agent.rpi.yaml
# edit ./agent.rpi.yaml

make deploy-rpi-full PI_HOST=raspberrypi.local PI_USER=pi CONFIG_SRC=./agent.rpi.yaml
make smoke-rpi-cli-ollama PI_HOST=raspberrypi.local PI_USER=pi SMOKE_FLAGS='-smoke-all'
```

The systemd unit template is [deployments/systemd/openlight-agent.service](./deployments/systemd/openlight-agent.service).

## Configuration

Important config sections:

- `telegram`: bot token, polling or webhook mode, webhook URL and listen address.
- `auth`: allowed Telegram user IDs and chat IDs.
- `storage`: SQLite path.
- `services`: allowed service targets plus log limits.
- `files`: allowed file roots plus read and list limits.
- `access.hosts`: named SSH hosts for remote service targets.
- `watch`: background polling interval and ask TTL.
- `llm`: provider, endpoint, model, thresholds, and optional profiles.
- `accounts`: explicit account-provider commands executed inside already allowed services.
- `workbench`: optional runtimes, allowed files, and output limits.

Useful env overrides:

- `TELEGRAM_BOT_TOKEN`
- `ALLOWED_USER_IDS`
- `ALLOWED_CHAT_IDS`
- `SQLITE_PATH`
- `LLM_ENABLED`
- `LLM_PROVIDER`
- `LLM_ENDPOINT`
- `LLM_MODEL`
- `OPENAI_API_KEY`
- `LLM_PROFILE`
- `TELEGRAM_MODE`
- `TELEGRAM_WEBHOOK_URL`
- `TELEGRAM_WEBHOOK_LISTEN_ADDR`
- `TELEGRAM_WEBHOOK_SECRET_TOKEN`

Example service and remote-host config:

```yaml
access:
  hosts:
    vps:
      address: "203.0.113.10:22"
      user: "root"
      password_env: "OPENLIGHT_VPS_PASSWORD"
      known_hosts_path: "/home/pi/.ssh/known_hosts"

services:
  allowed:
    - tailscale
    - "matrix=compose:/home/pi/matrix/docker-compose.yml"
    - "web=host:vps:docker:docker-jitsi-meet_web_1"
```

Polling is the default Telegram mode. Webhook mode is supported through `telegram.mode: webhook` and `telegram.webhook.*`.

## Skills, routing, providers

Routing order:

1. slash commands
2. explicit command text such as `service tailscale`
3. skill names and aliases
4. semantic rules
5. optional LLM route and skill classification
6. `chat` fallback when LLM is enabled

The LLM never bypasses the Go-side allowlists. Files, services, remote hosts, accounts, and workbench access still have to be explicitly configured.

Built-in LLM providers:

- `generic`
- `ollama`
- `openai`

You can keep multiple LLM profiles in one config file and switch with `LLM_PROFILE`:

```yaml
llm:
  enabled: true
  profile: "ollama"
  profiles:
    ollama:
      provider: "ollama"
      endpoint: "http://127.0.0.1:11434"
      model: "qwen2.5:0.5b"
    openai:
      provider: "openai"
      endpoint: "https://api.openai.com/v1"
      model: "gpt-4o-mini"
```

For OpenAI, set `OPENAI_API_KEY` or provide `llm.api_key` in your config.

Then switch without editing the file:

```bash
LLM_PROFILE=openai go run ./cmd/agent -config ./agent.yaml
```

## Example workflows and commands

Basic Telegram session:

```text
/start
/skills
/status
/services
/service tailscale
/logs tailscale
/restart tailscale
```

Watch setup:

```text
/enable docker
/enable system
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch list
/watch history
```

Files and notes:

```text
/files
/read /tmp/openlight/example.txt
/write /tmp/openlight/example.txt :: hello
/replace hello with hi in /tmp/openlight/example.txt
/note rotate backups
/notes
```

Local CLI:

```bash
go run ./cmd/cli -config ./agent.yaml -exec "status"
go run ./cmd/cli -config ./agent.yaml -exec "watch list"
go run ./cmd/cli -config ./agent.yaml -smoke
go run ./cmd/cli -config ./agent.yaml -smoke-all
```

## Advanced capabilities

- SQLite-backed notes, watches, incidents, messages, and skill-call history
- Allowlisted file read, write, and replace operations
- Optional account-provider flows executed through already allowed services
- Optional workbench runtime for restricted code and file execution
- Polling and webhook Telegram modes
- Multiple LLM profiles switched with `LLM_PROFILE`

## Project structure

- [cmd/agent](./cmd/agent): Telegram runtime
- [cmd/cli](./cmd/cli): local runner and smoke harness
- [internal/app](./internal/app): runtime wiring
- [internal/router](./internal/router): deterministic routing and optional LLM classifier
- [internal/skills](./internal/skills): built-in modules
- [internal/watch](./internal/watch): watch rules, incidents, and alert actions
- [internal/storage/sqlite](./internal/storage/sqlite): SQLite storage
- [configs](./configs): example configs
- [deployments/docker](./deployments/docker): Docker stack files
- [deployments/systemd](./deployments/systemd): systemd unit template
- [scripts](./scripts): install and Raspberry Pi deploy helpers
- [migrations](./migrations): embedded SQLite migrations

## Current limitations

- Telegram is the primary interface. The CLI is mainly for local execution and smoke tests.
- Local service control is Linux-oriented and assumes `systemd`, Docker Compose, Docker, or configured SSH targets.
- Metric watches currently support `notify` only. `ask` and `auto` restart flows apply to service-down watches.
- Running inside Docker does not automatically expose host services or files. You need an explicit config plus the right mounts or sockets.
- The bundled Docker path is optimized for local Ollama. If you want OpenAI or another remote provider in Docker, use a mounted config and, if needed, extend the Compose environment.

## Contributing

Small, focused contributions are the best fit here.

Before opening a PR:

```bash
make test
```

Optional real Ollama end-to-end run:

```bash
make ollama-up
make ollama-pull
make test-e2e-ollama
make ollama-down
```

For deeper project details, see [ARCHITECTURE.md](./ARCHITECTURE.md) and [CHANGELOG.md](./CHANGELOG.md).

## License

MIT. See [LICENSE](./LICENSE).
