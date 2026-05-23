# Skills

A **skill** is a single, named, deterministic operation the agent can
perform. Every user-visible action — `/status`, `/logs`, `/watch add`,
`/note`, `/remember`, the inline buttons on a watch alert — resolves to
a skill. The skill is the safety boundary: nothing happens that isn't a
registered skill, and the LLM (when enabled) can only choose among
already-registered skills.

Skills are organized into **skill groups** so the router can pick a
group first and then a concrete skill inside it. Groups also drive the
`/skills` listing and the Telegram `/menu` home screen.

## Built-in groups

These modules are always-on; they define what openLight *is*.
Removing one of them is not a supported configuration.

| Group      | Skills                                                                                  | What it owns                                       |
|------------|-----------------------------------------------------------------------------------------|----------------------------------------------------|
| `core`     | `start`, `help`, `skills`, `ping`                                                       | discovery and self-identity                        |
| `system`   | `status`, `cpu`, `memory`, `disk`, `uptime`, `hostname`, `ip`, `temperature`, `models`  | host metrics + LLM/Telegram health                 |
| `services` | `service_list`, `service_status`, `service_logs`, `service_restart`                     | allowlisted services on local + remote nodes       |
| `watch`    | `watch_add`, `watch_list`, `watch_pause`, `watch_remove`, `watch_history`, `watch_test`, `watch_enable` | proactive alerting + packs                |
| `files`    | `file_list`, `file_read`, `file_search`, `file_stat`, `file_write`, `file_replace`      | safe local file ops inside `files.allowed_roots`   |
| `browser`  | `browser_title`, `browser_text`, `browser_screenshot`, `browser_check`                  | read-only Playwright-backed page fetches           |
| `notes`    | `note_add`, `note_list`, `note_delete`                                                  | short operator notes                               |
| `memory`   | `memory_add`, `memory_list`, `memory_delete`                                            | durable facts and preferences (separable SQLite)   |

> `browser` is registered unconditionally, but its skills return a
> friendly "disabled" message until `browser.enabled = true` and a
> Node helper is configured. The same pattern applies to `files`,
> `memory`, `vision`, and `ocr` managers.

## Optional groups

These ship in the same binary but only register when explicitly enabled
in config. They are **deliberately off-path**: they extend openLight
beyond the core homelab use case and are kept at the boundary so the
project doesn't drift into being a generic AI assistant.

| Group           | Skills                                                              | Enable with                                | Notes                                              |
|-----------------|---------------------------------------------------------------------|--------------------------------------------|----------------------------------------------------|
| `chat`          | `chat`                                                              | `llm.enabled: true`                        | uses the SMART LLM profile                         |
| `vision`        | `vision_analyze`, `vision_compare`                                  | `vision.enabled: true`                     | needs a VLM (Ollama or OpenAI)                     |
| `ocr`           | `ocr_extract`                                                       | `ocr.enabled: true`                        | default backend is `tesseract`                     |
| `visual_watch`  | `visual_watch_add`, `visual_watch_list`, `visual_watch_remove`, `visual_watch_test` | `visual_watch.enabled: true`   | needs `browser`; uses `ocr` if enabled             |
| `network`       | `port_check`, `http_check`, `cert_check`, `dns_check`               | `network.enabled: true`                    | also unlocks `port_down` / `cert_expiring` watches |
| `accounts`      | `user_providers`, `user_add`, `user_list`, `user_delete`            | non-empty `accounts.providers`             | runs explicit commands inside an allowlisted service |
| `workbench`     | `exec_code`, `exec_file`, `workspace_clean`                         | `workbench.enabled: true`                  | only `workbench.allowed_runtimes` + `workbench.allowed_files` |
| `mcp`           | one skill per tool exposed by each MCP server                       | `mcp.enabled: true` + servers              | stdio JSON-RPC subprocesses                        |
| `external_skills` | one skill per discovered manifest                                 | non-empty `external_skills.roots`          | see [docs/skills/EXTERNAL.md](./skills/EXTERNAL.md) |
| voice pipeline  | (consumed inside `core.Agent`, not exposed as a skill)              | `voice.enabled: true`                      | needs `ffmpeg` + `whisper-cli`                     |

If you never enable any optional group, openLight stays a
deterministic, no-LLM, no-extra-deps Telegram bot for safe service
ops. That is the default posture and the recommended starting point.

## Routing behavior

Every skill carries metadata the router uses:

- `Name` — canonical identifier; what the registry matches on
- `Group` — which skill group owns it
- `Aliases` — alternate identifiers (e.g. `watches` → `watch_list`)
- `Usage`, `Examples` — surfaced in `/help <skill>`
- `Mutating` — flagged so the LLM stage prefers asking before running
- `Hidden` — kept out of `/skills` listings (used by `models`)

The router goes through deterministic stages before consulting any LLM:

1. **Slash command** (`/status`, `/watch add ...`, `/enable docker`).
2. **Explicit text** ("service tailscale", "note add ...").
3. **Normalized shortcut** — single-token lookup after `semantic.Normalize`,
   so `cpu`, `статус`, and `память` map straight to the right system
   skill.
4. **Registry alias** — exact match against a skill `Name` or `Alias`.
5. **Registry generic** — first word resolves to a skill; the rest of
   the message becomes the `text` argument. This is how external
   skills with free-form arguments are reached.
6. **Semantic rules** — hardcoded patterns like "покажи логи tailscale"
   → `service_logs`.
7. **LLM classifier** (optional) — two-stage: pick the group, then pick
   the skill and extract arguments. Confidence thresholds:
   `execute_threshold` (default `0.80`) auto-runs; `clarify_threshold`
   (default `0.60`) asks the user to confirm; below that, no match.

If nothing matched and `chat` is registered, the agent falls back to
`chat` for any non-slash message.

## Example commands

The examples below are the actual command patterns the deterministic
router accepts. Anything below the slash path can also be typed without
the leading `/`.

```text
/start
/help watch_add
/skills services

/status                       # overall host status + LLM/Telegram health
/cpu  /memory  /disk  /uptime
/ip   /hostname               # /host is an alias for /hostname
/temperature                  # /temp is an alias

/services
/status tailscale             # equivalent: /service tailscale, /service status tailscale
/logs tailscale               # equivalent: /log tailscale, /service logs tailscale
/restart tailscale

/watch                        # equivalent to /watch list
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch add disk / > 85% for 10m
/watch add port grafana.internal:3000 for 30s
/watch add cert example.com expires-in 14d cooldown 24h
/watch list
/watch pause 7                # toggles enabled/disabled
/watch remove 7
/watch history 7              # /watch incidents 7 also works
/watch test 7                 # synthetic incident — exercises the alert path

/enable docker
/enable system
/enable auto-heal
/enable tls
/enable homelab
/enable mac
/enable pi

/files /home/pi/scripts       # /file list, /list files all work
/read /etc/hostname           # /show, /cat are aliases
/write /tmp/note.txt::hello world
/replace /tmp/note.txt::old=>new
/grep TODO in /home/pi/scripts
/metadata /etc/hostname

/note take out the trash
/notes
/note delete 3

/remember Synapse listens on 8008 inside docker
/memories                     # all
/memories synapse             # full-text search
/forget 12                    # by id or by ref text

/chat why is /var so full?
/ask explain load average

# Visual + image (optional groups)
/browse title https://example.com
/browse text https://example.com
/browse screenshot https://example.com
/browse check https://example.com::Welcome

# Accounts (optional, configured providers only)
/users                        # /accounts, /user providers all work
/user list jitsi
/user add jitsi alice s3cr3t
/user delete jitsi alice

# Workbench (optional, allowlisted runtimes only)
/run python::print("hello")
/exec file /usr/bin/uptime
/workspace clean

# Network probes (optional, allowlisted targets only)
port_check raspberrypi.local:22
http_check https://example.com expect=ok
cert_check example.com:443
dns_check example.com

# Visual watch (optional, requires browser)
visual_watch_add https://status.example.com interval=10m keywords=down notify=both
visual_watch_list
visual_watch_test 1
visual_watch_remove 1
```

## Semantic matching

`semantic.Normalize` does light, deterministic normalization before
the alias / shortcut / rule stages run:

- lowercase, trim, collapse whitespace
- strip trailing punctuation
- map common Russian variants onto the English skill vocabulary
  (`проц`/`процессор` → `cpu`, `память` → `memory`, `статус` → `status`,
  `перезапусти` → `restart`, etc.)

This is intentionally narrow — no fuzzy matching, no embeddings, no
LLM call. The point is that frequent operator phrasings resolve at the
deterministic layer so latency stays predictable.

## Watches and actions

Watch alerts reuse the regular skill surface. When a service-down
incident fires and the user taps `Restart`, the callback hits the same
`service_restart` skill that a manual command would. That keeps one
auditing path: every executed action lands in `skill_calls` with the
same args and the same allowlist enforcement, whether a human or a
watch triggered it.

See [docs/WATCHES.md](./WATCHES.md) for the watch model, action modes
(`notify` / `ask` / `auto`), and pack contents.

## Where the registration happens

[internal/runtime/runtime.go](../internal/runtime/runtime.go) — search
for `BuildRegistryWithWatch`. The pattern is:

1. Always-on modules are appended unconditionally.
2. Optional modules are gated by their `enabled` flag or non-empty
   config block.
3. `chat` is gated by the LLM provider being non-nil.
4. The `external` module registers last so a manifest on disk can
   never shadow a builtin skill name.

Each module is a small Go package under `internal/skills/<module>`
that implements `skills.Module`. Adding a builtin skill means writing
Go code, not editing config — by design. Adding a third-party skill is
the job of [external skills](./skills/EXTERNAL.md): drop a
`skill.yaml` + executable into a configured root.

## What skills are NOT

- They are not arbitrary tool calls. The LLM cannot invent a skill; it
  can only classify into a name that's already in the registry.
- Builtins are not plugins. There is no Go plugin loader. Builtins
  are compiled into the binary.
- They are not a public API. `internal/skills` is private to the
  project. Third-party importers are not a use case — third-party
  *skills* are, via the external loader.

This keeps the surface small, auditable, and predictable. If a skill
does not appear in `skills.Registry`, it cannot be invoked.
