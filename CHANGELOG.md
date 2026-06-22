# Changelog

This file tracks released tags and summarizes what each release added or changed.

## v0.3.0 - 2026-06-22

Compared with `v0.2.0`, this release introduces an edge/brain network topology
that lets Raspberry Pi edge nodes offload LLM inference, voice transcription,
and skill execution to a central brain node over a lightweight HTTP API, while
keeping the full single-node path intact for deployments that don't need it.
Voice handling is tightened and the status command is wired through a new hooks
layer.

### What shipped

- Added an edge/brain network architecture: a `brain` node (Mac mini or server)
  exposes an HTTP API (`internal/brain/server.go`); `edge` nodes (Raspberry Pi)
  can route LLM calls, voice transcription, and skill invocations to it via new
  `remote` providers (`internal/llm/remote.go`, `internal/voice/remote.go`,
  `internal/skills/remote.go`). New example configs
  (`configs/agent.brain.example.yaml`, `configs/agent.edge.example.yaml`)
  document the topology end-to-end.
- Added a `display` skill and a Python display dashboard
  (`scripts/display-dashboard.py`) for Raspberry Pi nodes with a screen, plus
  a deploy script (`scripts/deploy-rpi-display.sh`) and systemd unit
  (`deployments/systemd/openlight-display.service`).
- Added a `think` skill (`internal/skills/chat/think_skill.go`) that exposes
  an explicit reasoning step through the normal routing pipeline.
- Improved voice handling: extended config validation for `whisper_path`,
  `model_path`, and `language`; tightened the Telegram voice-message client;
  added Russian-language routing tests; expanded `openlight doctor` voice
  probes.
- Fixed the `/status` command by wiring it through a new runtime hooks layer
  (`internal/runtime/`), replacing the previous direct call path.

### Upgrade notes

- Existing `v0.2.0` configs work unchanged. The edge/brain topology is entirely
  opt-in: copy `configs/agent.brain.example.yaml` to the brain node and
  `configs/agent.edge.example.yaml` to each edge node, fill in the shared
  token, and restart.
- The display dashboard requires Python 3 and its dependencies on the Pi.
  Run `scripts/deploy-rpi-display.sh` to install them; the script is
  idempotent.
- The `think` skill is registered automatically alongside other chat skills;
  no config change is needed to enable it.

## v0.2.0 - 2026-05-23

Compared with `v0.1.0`, this release widens the safe surface around the
deterministic-first runtime. The Telegram bot picks up an inline-button
home menu and multi-step skill input sessions. Voice notes and images
become first-class inputs. Optional modules grow to cover vision, OCR,
network probes, durable memory, visual watches, MCP servers, and
user-defined external skills. Routing splits into FAST + SMART LLM
profiles with always-warm policy so a Raspberry Pi or Mac mini can keep
classification cheap while reasoning calls hit a larger local model
only when needed. Allowlisted execution and the deterministic-first
routing pipeline are unchanged — the LLM is still optional and never
expands the surface on its own.

### What shipped

- Consolidated the legacy `cmd/agent` and `cmd/cli` binaries into a single `openlight` binary with `agent`, `cli`, `doctor`, and `skills` (`list` / `validate` / `reload`) subcommands.
- Added a generic registry stage to the deterministic router so the first word of any message can resolve to a registered skill (builtin or external) with the remainder treated as the `text` argument; the existing slash, explicit text, normalized shortcut, alias, and semantic-rule stages still run first.
- Added FAST + SMART LLM profiles with role-aware provider construction: the router classifier runs on FAST, while `chat`, log explanations, and final answers run on SMART. When no `fast` profile is defined, the SMART provider serves both roles (`FastFallback`).
- Added `llm.warmup` policy that pre-loads listed profiles in the background at startup with exponential backoff (5s → 5m, up to 8 attempts), keeping the configured `keep_alive` so Ollama does not unload the model between requests.
- Added a Telegram UI layer: inline home menu (`/menu`, `/home`), per-group submenus, callback router for action buttons, persistent quick-action keyboard, and multi-step skill input sessions so mutating skills can collect fields one at a time.
- Added a Telegram voice-message pipeline: download, `ffmpeg` resample, `whisper-cli` transcription, transcript-aware reply, and full mockable interfaces. Configured via the `voice:` block.
- Added a core image inbox that dispatches photos and image documents to either `vision_analyze` (default) or `ocr_extract` (when the caption looks like an OCR ask) and returns the skill result through the normal reply path.
- Added optional `vision` skills (`vision_analyze`, `vision_compare`) with pluggable providers (Ollama VLM, OpenAI), default prompt, and image-size limits.
- Added optional `ocr` skills (`ocr_extract`) with a Tesseract provider by default and configurable languages.
- Added a `visual_watch` subsystem and skills (`visual_watch_add`, `_list`, `_test`, `_remove`) that periodically screenshot allowlisted URLs, diff against a stored baseline, and optionally scan OCR / HTML text for keywords. Stores baselines under `visual_watch.baselines_dir`.
- Added optional `network` skills (`port_check`, `http_check`, `cert_check`, `dns_check`) plus two new watch kinds — `port_down` and `cert_expiring_soon` — that the watch service evaluates through the same network manager.
- Added a durable memory subsystem (`memory_add`, `memory_list`, `memory_delete`) backed by a new `memories` SQLite table, with `/remember`, `/memories`, `/forget` slash commands; supports a separate SQLite file via `memory.db_path`.
- Added Model Context Protocol (MCP) integration: configured stdio servers register their tools as skills in the `mcp` group through the same routing pipeline as builtins; an `allowed_tools` per-server gate is supported.
- Added an external-skills loader: drop a `skill.yaml` plus an executable into a directory under `external_skills.roots` and the runtime spawns it per invocation with a strict JSON v1 protocol. Builtins always win on duplicate names. A complete example lives under `testdata/skills/echo`.
- Added `openlight skills list` / `validate` / `reload` subcommands for inspecting builtin + external skills and validating manifest changes before restarting the agent.
- Added new watch packs: `/enable tls`, `/enable homelab`, `/enable mac`, `/enable pi`, alongside the existing `docker`, `system`, and `auto-heal`. All packs remain idempotent.
- Added Mac mini (`darwin/arm64`) support: an example config, a Darwin-specific system provider (CPU, memory, swap, memory pressure, uptime), a `launchd` plist template, and `deploy-macmini` / `bootstrap-macmini` make targets and scripts.
- Added storage retention (`storage.retention_days`): a positive value triggers a one-shot prune of old `messages` and `skill_calls` rows on startup. New SQLite indexes (migration `0005_message_retention.sql`) keep the prune cheap on Pi-class hardware.
- Added a `/status` and a `/models` skill that report Ollama loaded-model status, last LLM latency (per FAST/SMART role), Telegram connectivity, and configured profiles. Hooks are wired at runtime build time.
- Expanded `openlight doctor` with probes for Telegram, Ollama, nodes, filesystem, watches, voice, browser, vision, OCR, workbench, visual watch, and a security-warnings pass that flags inline passwords, host-key bypass, and similar foot-guns.
- Reorganized the `Makefile` into per-area files under `make/` (`build.mk`, `common.mk`, `deploy.mk`, `dev.mk`, `docker.mk`, `help.mk`, `llm.mk`, `release.mk`, `test.mk`) so `make help` stays readable.
- Reworked documentation under `docs/`: `ARCHITECTURE.md`, `SKILLS.md`, `WATCHES.md`, `NODES.md`, and a new `docs/skills/EXTERNAL.md`, plus a regression matrix in `docs/REGRESSION.md`.

### Upgrade notes

- Existing v0.1.0 configs keep working unchanged. New optional surfaces (`vision`, `ocr`, `voice`, `visual_watch`, `network`, `mcp`, `external_skills`, `workbench`, `accounts`) are gated by their own `enabled` flag (or non-empty servers / roots / providers map) and stay off by default.
- `access.hosts:` is still accepted and merged into `nodes:` at load time. `filesystem:` is still accepted as an alias for `files:`. The agent logs a one-line deprecation note when either legacy key is in use, and `openlight doctor` surfaces the same note as a warning.
- Older deployments that ran `cmd/agent` or `cmd/cli` as separate binaries should point their service unit at `openlight agent` / `openlight cli` instead. The bundled systemd unit, launchd plist, and Dockerfile already use the single binary.
- `llm.warmup` is enabled by default with `profiles: ["smart"]` and `keep_alive: -1`. Set `llm.warmup.enabled: false` to skip background model loads at startup.
- `memory.enabled` defaults to `true` and shares the main SQLite file. Set `memory.db_path` to split it into a dedicated file. No data is migrated out of `notes` — memories and notes are separate stores.
- Metric watches still support `notify` only; `ask` / `auto` apply to service-down watches.

## v0.1.0 - 2026-04-12

Compared with `v0.0.3`, this release turns `openLight` from a Telegram control surface into a Telegram-first monitoring loop: persistent watches, alert actions, built-in monitoring packs, a one-command installer, and faster, more resilient LLM routing.

### What shipped

- Added a new watch subsystem with SQLite-backed watch rules and incidents, background polling, `/watch add|list|pause|remove|history|test` commands, and local CPU, memory, disk, and temperature probes.
- Added service-down alert workflows with `notify`, `ask`, and `auto` reaction modes, Telegram inline-button actions for `Restart`, `Logs`, `Status`, and `Ignore`, plus allowlisted auto-restart flows for services and containers.
- Added `/enable docker`, `/enable system`, and `/enable auto-heal` packs that create or refresh watch rules with opinionated defaults for containers, host metrics, and one-shot service recovery.
- Added `scripts/install.sh`, a one-command Docker installer that resolves the latest tagged release assets by default, downloads the published Compose stack, and supports pinning through `OPENLIGHT_REF`.
- Added a top-level `openlight-compose.yaml` release asset so the tagged Docker plus Ollama stack can be started directly from the repository root.
- Improved LLM routing with lower-latency route and skill limits, heuristic rescue before and after model calls, better argument extraction, more reliable Ollama and OpenAI parsing, and profile switching through `llm.profile` or `LLM_PROFILE`.
- Improved the CLI smoke flows with streamed progress output, richer pass/fail summaries and latency stats, and dedicated Ollama/OpenAI profile runs for Raspberry Pi deployment checks.
- Expanded automated coverage across watch parsing and execution, SQLite watch persistence, Telegram callback handling, LLM routing heuristics, provider parsing, config profile selection, and end-to-end agent flows.
- Reworked the README and architecture docs around the monitoring workflow, the installer path, and the Docker plus Ollama quick start, and added a Telegram demo GIF.

### Upgrade notes

- Existing SQLite databases pick up the new `0002_watch.sql` migration automatically on startup; the agent now persists watches, incidents, and pack markers in addition to prior state.
- Existing configs can keep working without new fields, but `watch.enabled`, `watch.poll_interval`, and `watch.ask_ttl` are now first-class settings with defaults.
- The example configs now default `llm.decision_num_predict` to `48` instead of `128` to trim routing latency; raise it again if your chosen model needs longer classifier outputs.
- You can now keep multiple LLM backends in one config and switch between them with `llm.profile` or `LLM_PROFILE` instead of editing provider fields in place.
- Metric watches currently support `notify` only; `ask` and `auto` apply to service-down watches.

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
