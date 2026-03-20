# Changelog

This file tracks released tags and summarizes what each release added or changed.

## Unreleased

### What changed

- Added `scripts/install.sh`, a one-command Docker installer that resolves the latest tagged release assets by default and supports pinning through `OPENLIGHT_REF`.
- Reworked the README into a more aggressive landing page with a stronger quick start, earlier safety positioning, a clearer default path, and a tighter Telegram-plus-local-LLM focus.
- Added a lightweight Telegram proof asset for the README and a release-notes draft for `v0.0.3`.

## v0.0.3 - 2026-03-20

Compared with `v0.0.2`, this release turns `openLight` into a container-distributed and more operations-oriented agent, while keeping the Raspberry Pi path intact.

### What shipped

- Added remote service backends over named SSH hosts, so service status, restart, and logs can target not only local `systemd` units, but also remote `systemd`, Docker Compose services, and direct Docker containers.
- Added compose-backed and direct Docker service support, including fallback to legacy `docker-compose` when the newer `docker compose` subcommand is unavailable.
- Added account providers for explicit user-management flows inside allowed services, covering add, delete, and list operations for integrations such as Jitsi Prosody and Synapse-style admin APIs.
- Added a local CLI binary that reuses the same runtime, router, storage, auth rules, and skills as the Telegram agent.
- Added a smoke-test harness and Raspberry Pi smoke flows so deployments can be checked end-to-end without sending live Telegram messages.
- Added a first-class Docker image with a multi-stage `Dockerfile`, `.dockerignore`, GHCR publishing workflow, and `make docker-build`, `make docker-buildx`, and `make docker-push` targets.
- Added an embedded minimal container config so the Docker image can start from env vars plus a data volume, with automatic config discovery through `OPENLIGHT_CONFIG` or `/etc/openlight/agent.yaml`.
- Added a bundled `openlight` + `ollama` Compose stack that starts a local Ollama instance and pulls the default model `qwen2.5:0.5b`.
- Added GitHub Actions workflows for release-tag Docker publishing, release-tag Ollama end-to-end tests, and a lightweight PR CI workflow with `go test ./...` and a Docker build smoke check.
- Added README badges and a one-line Docker install path so the current build and release status are visible directly from the repository front page.
- Added broader test coverage for SSH execution, compose fallback behavior, account providers, CLI transport, CLI smoke flows, Telegram client behavior, redaction, and agent integrations.

### Upgrade notes

- If you want anonymous `docker pull` from GHCR, make the package public once in GitHub package settings after the first publish.
- The bundled Docker quick start now assumes local Ollama by default. Set `LLM_ENABLED=false` for deterministic-only mode, or override `LLM_PROVIDER`, `LLM_ENDPOINT`, and `LLM_MODEL` for another backend.
- Running inside Docker does not give local host access automatically. For real host management, mount an explicit config with allowlists or use named `access.hosts` over SSH.

## v0.0.2 - 2026-03-15

Compared with `v0.0.1`, this release broadened the skill surface, added native OpenAI support, and redesigned the routing path around a deterministic-first, two-stage LLM classifier.

### What shipped

- Added native `openai` provider support alongside `ollama` and `generic`, including a provider factory registry, dedicated OpenAI implementation, function-calling based skill selection, schemas, prompts, and tests.
- Added a dedicated OpenAI example config so hosted LLM routing and chat can be enabled without starting from the generic provider path.
- Added safe file-management skills for listing, reading, writing, and replacing text inside explicit allowlisted roots.
- Added restricted workbench skills for temporary code execution and allowlisted file execution, with runtime and file allowlists plus output limits.
- Redesigned routing into a deterministic-first flow followed by two LLM stages: route classification first, then concrete skill selection inside the chosen group.
- Added stronger semantic normalization and routing rules so common English and Russian variants map more reliably onto built-in skills.
- Expanded skill metadata, grouping, module registration, discovery, and help output so the registry exposes a clearer contract to both humans and the LLM layer.
- Added richer unit, integration, and end-to-end coverage across config parsing, router behavior, provider selection, OpenAI/Ollama integrations, and full agent flows.
- Added an Ollama Docker Compose setup and `make` helpers for starting Ollama locally, pulling a default model, and running a real Ollama end-to-end test.
- Reworked the docs: the README, architecture guide, Raspberry Pi setup path, and latency snapshot were all expanded to reflect the new routing model and deployment flows.

### Upgrade notes

- Existing configs should add the new `files.*` and `workbench.*` sections.
- `llm.mutating_execute_threshold` and `LLM_MUTATING_EXECUTE_THRESHOLD` were removed; route-stage confidence is now the single execution gate for tool groups.
- If you run a custom `generic` or `ollama`-compatible backend, update the skill-classification response shape to use `skill`, `arguments`, `needs_clarification`, and `clarification_question` without a separate skill-confidence field.
- For OpenAI deployments, prefer `OPENAI_API_KEY` over storing `llm.api_key` in a tracked file.

## v0.0.1 - 2026-03-13

This was the first tagged release of `openLight`: a Telegram-first Raspberry Pi agent with SQLite persistence, deterministic tool routing, and optional LLM fallback.

### What shipped

- Added the core Telegram agent runtime with auth checks, message persistence, skill execution, and SQLite-backed state.
- Added built-in system skills for status, CPU, memory, disk, uptime, hostname, IP, and temperature.
- Added built-in service-management skills for listing, status, logs, and restart of explicitly allowed local services.
- Added built-in notes skills for saving, listing, and deleting short notes from SQLite.
- Added built-in chat and meta skills, including `chat`, `start`, `help`, `skills`, and `ping`.
- Added deterministic routing with optional LLM fallback through Ollama or a generic HTTP provider.
- Added Telegram webhook support in addition to the default polling transport.
- Added Raspberry Pi deployment assets, including example configs, deploy scripts, a local run script, and a `systemd` service unit.
- Added baseline automated coverage for config parsing, LLM classification, router behavior, Telegram transport, services, notes, auth, and SQLite storage.

### Upgrade notes

- `v0.0.1` established the original config shape for Telegram, auth, storage, services, and optional LLM routing.
