# External skills

External skills let operators extend openLight with their own commands
without touching the Go runtime. A skill is a directory containing a
`skill.yaml` manifest and an executable. The runtime spawns the
executable per request, writes one line of JSON to its stdin, reads one
line of JSON from its stdout, and treats stderr as logs.

The design borrows from Hashicorp's plugin philosophy, Terraform
providers, and the UNIX process model. It is deliberately NOT a generic
AI plugin framework: there are no shared libraries, no dynamic Go
plugins, no in-process RPC, and no shared memory.

## Quick start

1. Create a directory anywhere the agent can read it. The conventional
   roots are `~/.openlight/skills` and `/etc/openlight/skills`.
2. Drop a `skill.yaml` and an executable in it.
3. Add the root to your config:

   ```yaml
   external_skills:
     roots:
       - ~/.openlight/skills
   ```

4. Validate before restarting:

   ```sh
   openlight skills validate ~/.openlight/skills/myskill
   openlight skills reload
   ```

5. Restart the agent. The skill appears in `/skills` and in
   `openlight skills list` alongside the builtins.

A complete example lives at
[`testdata/skills/echo`](../../testdata/skills/echo) — ~10 lines of
Python that read the request envelope from stdin and echo the user's
text back. Try it:

```text
> echo hello world
hello world
```

The example also short-circuits `ping` → `pong` to show how a skill can
branch on input, but note that **the builtin `ping` skill resolves
first**, so typing `ping` in the agent will hit the Go implementation
rather than this Python one. To exercise the external code path
directly, invoke it from the shell:

```sh
echo '{"api_version":"v1","input":{"text":"ping"},"skill":{"name":"echo"},"context":{}}' \
  | python3 testdata/skills/echo/run.py
# {"ok": true, "message": "pong"}
```

## Manifest

`skill.yaml` is parsed into the [`external.Manifest`](../../internal/skills/external/manifest.go)
struct. Required fields: `api_version`, `name`, `description`, and
exactly one of `entrypoint.command` or `entrypoint.path`. Everything
else is optional.

```yaml
api_version: v1                # only "v1" is currently accepted

name: weather                  # lowercased; must be unique in the registry
description: Weather forecast  # one-line summary surfaced in /skills
version: 1.0.0                 # informational; logged on load

group: network                 # optional; falls back to "other"

aliases: [forecast, rain]      # extra identifiers users can type
triggers: [weather, forecast]  # routing hints surfaced to the LLM
examples: [weather tomorrow]   # rendered in /help

capabilities:                  # advisory labels; future-ready for matching
  - weather.current
  - weather.forecast

entrypoint:
  command: python3             # absolute path or anything on PATH
  args: [run.py]
  env:                         # static env merged onto the parent's
    PYTHONUNBUFFERED: "1"

# Alternatively:
# entrypoint:
#   path: run.sh               # resolved relative to the skill directory

permissions:                   # declarative; informational in v1, future
  network:                     # versions will enforce these
    allow:
      - api.open-meteo.com:443
  filesystem:
    read:
      - /srv/media
    write: []
  shell: false

timeout: 5s                    # defaults to 5s, hard-clamped to 2m
mutating: false                # if true, agent treats it as state-changing
hidden: false                  # if true, omitted from /skills listings
```

## Request / response protocol

The runtime sends one line of JSON to stdin per invocation:

```json
{
  "api_version": "v1",
  "request_id": "req_a1b2c3d4e5f60718",
  "skill": {"name": "weather", "version": "1.0.0"},
  "input": {
    "text": "weather tomorrow",
    "args": {"topic": "weather"}
  },
  "context": {
    "user_id": "42",
    "chat_id": "99",
    "source": "cli"
  }
}
```

The skill writes one line of JSON to stdout:

```json
{
  "ok": true,
  "message": "Tomorrow: 18°C in Lisbon",
  "data": {"temperature": 18},
  "buttons": [
    {"text": "Refresh", "action": "refresh"}
  ]
}
```

- `ok` is required. `false` plus a non-empty `error` surfaces as a
  user-visible failure and is logged like any other skill error.
- `message` is the text the user sees. Empty is allowed.
- `data` is opaque to the runtime; it's reserved for richer transports
  later. Skills can omit it.
- `buttons` are reduced to a single Telegram-style row in v1. The
  `action` is delivered back through the agent's normal callback
  routing, so a skill cannot inject arbitrary callbacks.

Unknown response fields are rejected — the schema is strict so additions
require an `api_version` bump.

## Runtime guarantees

- **Timeout**: every invocation has a deadline (`timeout` field, default
  5s, max 2 minutes). The runtime kills the process on expiry and
  returns a user-facing timeout error.
- **Isolation**: skills cannot reach any internal Go API. They get
  stdin, stdout, stderr, exit code, and the environment the manifest
  declares — nothing else.
- **stderr is logs only**. The runtime never parses stderr as protocol
  output. Use it for debug printlns; lines are surfaced in the audit
  feed at `info` level.
- **JSON validation**: malformed, empty, or extra-field responses are
  rejected before they reach the user.
- **Audit**: each call logs a `request_id`, elapsed milliseconds, and
  the resolved skill name. External skills appear in the same audit
  stream as builtins.

## CLI helpers

```sh
openlight skills list                       # all skills, marked builtin/external
openlight skills list --external-only       # just the external ones
openlight skills validate <dir>             # parse one manifest and report
openlight skills validate                   # sweep every configured root
openlight skills reload                     # preview a fresh scan; print diff
```

`openlight skills reload` does not signal a running agent — process
reload is a separate concern. Use it to confirm a directory edit is
valid, then restart the agent (or your supervisor) to pick it up.

## Authoring notes

- A skill is just a program. Go, Python, Bash, Node.js, Rust — anything
  the kernel can exec. There is no SDK to depend on.
- Duplicate names lose: when two manifests share a `name`, the one
  found in the earlier-listed root wins and the later one is logged as
  a duplicate.
- Builtin names always win over external names — external skills are
  registered last so an on-disk `system.yaml` cannot accidentally
  override the real `system` skill.
- Permissions are declarative in v1 and recorded for audit/UX. Future
  versions will enforce them via OS-level sandboxing. Declare them now
  so your manifest stays valid as the runtime tightens.
