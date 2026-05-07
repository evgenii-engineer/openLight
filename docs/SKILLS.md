# Skills

A **skill** is a single, named, deterministic operation the agent can
perform. Every user-visible action — `/status`, `/logs`, `/watch add`,
`/note` — is implemented as a skill. The skill is the safety boundary:
nothing happens that isn't a registered skill, and the LLM (when enabled)
can only choose among already-registered skills.

Skills are organised in two tiers.

## Core (always on)

These are the always-registered modules. They define what openLight *is*.
If you remove a core module, you lose the project's identity.

| Module     | Skills                                                       | What it owns                                |
|------------|--------------------------------------------------------------|---------------------------------------------|
| `core`     | `start`, `help`, `skills`, `ping`                            | bot self-identity                           |
| `system`   | `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, `temperature` | host + node metrics              |
| `services` | `services`, `service_status`, `service_logs`, `service_restart` | declared local + remote service ops      |
| `watch`    | `watch_add`, `watch_list`, `watch_pause`, `watch_resume`, `watch_remove`, `watch_history`, `watch_test`, `watch_enable_pack` | proactive alerting     |
| `files`    | `files_list`, `file_read`, `file_write`, `file_replace`, etc | safe local file read/write within allowlist |
| `notes`    | `note_add`, `notes`, `note_delete`                            | short operator notes                       |
| `memory`   | `memory_add`, `memory_list`, `memory_delete`                  | persistent key/value notes for the LLM     |

These map directly to "I want to operate my homelab from Telegram."

## Optional (off by default)

These ship in the same binary but only register when explicitly enabled in
config. They are **deliberately off-path**: they extend openLight beyond
the core homelab use case and are kept at the boundary so the project
doesn't drift into being a generic AI assistant.

| Module       | Enable with               | Why off by default                       |
|--------------|---------------------------|------------------------------------------|
| `chat`       | `llm.enabled: true`       | needs a model; not part of "infra ops"   |
| `accounts`   | `accounts.providers: ...` | runs explicit account-management commands inside already-allowed services |
| `workbench`  | `workbench.enabled: true` | runs allowlisted runtimes (`python`, `sh`) on allowlisted files |
| `browser`    | `browser.enabled: true`   | external network surface; needs Playwright |
| `vision`     | `vision.enabled: true`    | needs a VLM; large model footprint       |
| `ocr`        | `ocr.enabled: true`       | needs Tesseract installed                |
| `voice`      | `voice.enabled: true`     | needs ffmpeg + whisper-cpp               |
| `visual_watch` | `visual_watch.enabled: true` | layered on top of `browser` + `ocr`   |

If you never enable any of these, openLight stays a deterministic, no-LLM,
no-extra-deps Telegram bot for safe service ops. That is the default
posture and the recommended starting point.

## Where the registration happens

[internal/runtime/runtime.go](../internal/runtime/runtime.go) — search for
`BuildRegistryWithWatch`. The pattern is:

1. Always-on modules are appended unconditionally.
2. Optional modules are gated by their `enabled` flag.
3. `chat` is gated by the LLM provider being non-nil.

Each module is a small Go package under `internal/skills/<module>` that
implements the `skills.Module` interface. Adding a skill means writing
Go code, not editing config — by design.

## What skills are NOT

- They are not arbitrary tool calls. The LLM does not invent new skills;
  it can only classify into already-registered names.
- They are not plugins. There is no plugin loader, no shared object, no
  external skill registry. Skills are Go packages compiled into the
  binary.
- They are not exposed as a public API. `internal/skills` is private to
  the project. Third-party importers are not a use case.

This keeps the surface small, auditable, and predictable. If a skill does
not appear in `skills.Registry`, it cannot be invoked.
