# openLight

[![Go](https://img.shields.io/badge/Go-1.25+-00ADD8?logo=go&logoColor=white)](./go.mod) [![License](https://img.shields.io/badge/License-MIT-green)](./LICENSE) [![CI](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml/badge.svg)](https://github.com/evgenii-engineer/openLight/actions/workflows/ci.yml) [![Docker Publish](https://github.com/evgenii-engineer/openLight/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/evgenii-engineer/openLight/actions/workflows/docker-publish.yml) [![Ollama Tests](https://github.com/evgenii-engineer/openLight/actions/workflows/ollama-tests.yml/badge.svg)](https://github.com/evgenii-engineer/openLight/actions/workflows/ollama-tests.yml) [![Stars](https://img.shields.io/github/stars/evgenii-engineer/openLight?style=social)](https://github.com/evgenii-engineer/openLight/stargazers)

Run your own Telegram AI assistant locally in minutes.  
Built for Raspberry Pi and small Linux hosts. Local LLM by default. No heavy frameworks.

## Quick start

```bash
export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
curl -fsSL https://raw.githubusercontent.com/evgenii-engineer/openLight/master/scripts/install.sh | bash
```

Runs in about 1-2 minutes.

## After start

1. Open Telegram
2. Send `/start`
3. Try `/note buy milk` or `/status`

## What You Get

- Telegram bot as the primary interface
- Local Ollama-backed assistant in the default setup
- SQLite-backed notes and message history
- Allowlisted status, logs, and restart for services
- Self-hosted runtime you can inspect and extend

## Safety At A Glance

- No arbitrary shell access
- Allowlisted files and services only
- Deterministic routing first
- Optional LLM fallback
- Allowed Telegram users and chats only

## Example

```text
You: /note buy milk
openLight:
Saved note #1

You: /notes
openLight:
Notes:
- #1 buy milk
```

## Why openLight

Most AI frameworks are heavy and overengineered. `openLight` stays small, local-first, and easy to audit.

It is built for one job: Telegram plus local LLM plus notes and lightweight host control.

## Recommended Paths

- Recommended: Docker + Ollama
- Lightest: deterministic-only with `LLM_ENABLED=false`
- Advanced: OpenAI, generic HTTP LLMs, remote hosts, and extra service backends

## Run With Docker

Manual pinned path using the current stable release:

```bash
curl -fsSL https://raw.githubusercontent.com/evgenii-engineer/openLight/v0.0.3/openlight-compose.yaml -o openlight-compose.yaml
export TELEGRAM_BOT_TOKEN=123456:replace-me
export ALLOWED_USER_IDS=111111111
docker compose -f openlight-compose.yaml up -d
```

## Who It Is For

- For: Raspberry Pi, homelab, Telegram maintenance bot, local-first assistant
- Not for: autonomous browsing, arbitrary shell agenting, complex multi-agent orchestration

## What It Can Do

- Inspect the host with `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, and `temperature`
- List, inspect, tail logs, and restart allowlisted local services
- Target remote services over named SSH hosts
- Read, write, and replace text inside allowlisted file roots
- Store short notes in SQLite
- Optionally enable workbench tasks and account-provider flows
- Optionally use OpenAI or a generic HTTP LLM backend instead of local Ollama

Common English and Russian variants are normalized even when `llm.enabled: false`.

## More

- [Releases](https://github.com/evgenii-engineer/openLight/releases)
- [Changelog](./CHANGELOG.md)
- [Architecture](./ARCHITECTURE.md)
- [Configs](./configs/)
- [Release notes for v0.0.3](./docs/releases/v0.0.3.md)

## Developer Setup

```bash
make test
go run ./cmd/agent -config ./agent.yaml
go run ./cmd/cli -config ./agent.yaml -exec "ping"
go run ./cmd/cli -config ./agent.yaml -smoke
```

Optional Ollama end-to-end path:

```bash
make ollama-up
make ollama-pull
make test-e2e-ollama
make ollama-down
```

## Status

Early, but active.

- Tagged releases through `v0.0.3`
- CI on pushes and pull requests
- Multi-arch Docker images
- Tagged Ollama end-to-end coverage

## Contributing

Small, focused contributions are welcome.

Good fits:

- new explicit skills
- self-hosting docs and examples
- safer integrations for services and accounts
- Raspberry Pi, Docker, and smoke-test improvements

Before opening a PR, run `make test`.

## License

MIT. See [LICENSE](./LICENSE).
