# Watches

A **watch** is a background rule that polls some local or remote
signal and opens an **incident** the moment a configured condition
stays true longer than a threshold. Incidents become Telegram alerts
with action buttons.

Watches are openLight's proactive mechanism. Everything else (status,
logs, restart) is reactive — you ask, the bot answers. A watch lets
the bot speak first.

## Mental model

```
poll ──► condition ──► duration ──► incident ──► alert ──► action
```

- **poll**: the watch service evaluates every enabled watch on a fixed
  cadence (`watch.poll_interval`, default `15s`).
- **condition**: a watch fires when its predicate is true (service is
  down, CPU > 90%, port unreachable, cert expires soon).
- **duration**: the condition only counts as "tripped" if it holds for
  `for <duration>`. Defaults vary by kind. This stops flapping.
- **incident**: a SQLite row that links the watch, the time, and the
  reaction mode.
- **alert**: a Telegram message (text + inline buttons when supported).
- **action**: the user taps `Restart` / `Logs` / `Status` / `Ignore`.
  Buttons reuse the existing skill surface — tapping `Restart` invokes
  the same `service_restart` skill a user could type directly. One
  audit path for both manual and automatic operations.

## Reaction modes

Every service-down watch picks one:

- `notify` — send the alert; do not act.
- `ask` — send the alert with action buttons. Wait `watch.ask_ttl`
  (default `10m`) for the user to tap one.
- `auto` — perform the action immediately. Use sparingly.

Metric, port, and cert watches currently support `notify` only. The
parser accepts `notify` explicitly and rejects `ask` / `auto` on
metric watches.

## Built-in watch kinds

| Kind                  | Predicate                                          | Default mode | Default duration |
|-----------------------|----------------------------------------------------|--------------|------------------|
| `service_down`        | allowlisted service is not active                  | `notify`     | `30s`            |
| `cpu_high`            | CPU usage > N% for D                               | `notify`     | `5m`             |
| `memory_high`         | memory usage > N% for D                            | `notify`     | `5m`             |
| `disk_high`           | disk usage at path P > N% for D                    | `notify`     | `3m`             |
| `temperature_high`    | sensor temp > N°C for D                            | `notify`     | `5m`             |
| `port_down`           | TCP `host:port` unreachable                        | `notify`     | `30s`            |
| `cert_expiring_soon`  | TLS cert at `host[:port]` within N days of expiry  | `notify`     | `1m`             |

`port_down` and `cert_expiring_soon` require `network.enabled = true`
with the relevant targets listed in `network.allowed`.

Visual watches (periodic screenshot diff + keyword scan) are a
separate, optional subsystem; see the
[Visual watches](#visual-watches) section below.

## Spec grammar

`watch_add` parses each spec as one of these shapes:

```text
service <name> [notify|ask|auto] [for <duration>] [cooldown <duration>] [restart]
cpu        [>] N%  [for <duration>] [cooldown <duration>]
memory     [>] N%  [for <duration>] [cooldown <duration>]
disk [<path>] [>] N%  [for <duration>] [cooldown <duration>]
temperature [>] N    [for <duration>] [cooldown <duration>]
port  <host:port>   [for <duration>] [cooldown <duration>] [notify|ask]
cert  <host[:port]> [expires-in Nd]  [for <duration>] [cooldown <duration>] [notify|ask]
```

Filler words are tolerated (`is`, `if`, `when`, `then`, `goes`,
`becomes`, `stays`, `remains`) so an LLM-produced rule can normalize
into the deterministic format.

## Adding a watch

From Telegram:

```text
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch add disk / > 85% for 10m
/watch add port grafana.internal:3000 for 30s
/watch add cert example.com expires-in 14d cooldown 24h
/watch list
/watch pause 7                  # toggles between enabled and paused
/watch remove 7                 # /watch delete 7 is an alias
/watch history                  # all
/watch history 7                # one watch's incidents
/watch test 7                   # synthetic incident — exercises the alert path
```

`cooldown` is the minimum gap between two incidents for the same watch,
even if the condition keeps flapping. Defaults vary by kind (`10m` for
service-down and port, `15m` for metrics, `24h` for cert).

## Packs

A **pack** is a one-shot way to seed a curated set of watches for the
current chat. Packs are **idempotent**: re-running `/enable docker` on
an already-seeded chat updates existing watches in place and never
creates duplicates.

| Pack         | What it creates                                                                          |
|--------------|------------------------------------------------------------------------------------------|
| `docker`     | `service ... ask for 30s cooldown 10m` for every allowlisted Docker / Compose service    |
| `system`     | `cpu > 90% for 5m`, `memory > 90% for 5m`, `disk / > 85% for 3m`                         |
| `auto-heal`  | `service ... auto for 30s cooldown 10m` for every allowlisted service                    |
| `tls`        | `cert host:443 expires-in 14d cooldown 24h` for every allowlisted network target on 443  |
| `homelab`    | `system` pack + one `port host:port` per explicit `host:port` in `network.allowed`       |
| `mac`        | `system` pack tuned for Mac mini (no temperature; SMC requires root)                     |
| `pi`         | `system` pack tuned for Raspberry Pi (lower disk threshold, explicit temperature)        |

Aliases the parser accepts: `autoheal`/`auto-healing` → `auto-heal`;
`macmini`/`mac-mini`/`macos` → `mac`; `raspberrypi`/`raspberry-pi`/`rpi`
→ `pi`; `ssl`/`certs`/`certificates` → `tls`.

## Visual watches

When `visual_watch.enabled = true` (and `browser.enabled = true`), a
parallel poller — `visualwatch.Service` — periodically screenshots
allowlisted URLs, diffs them against a stored baseline, and notifies
the chat when something changes visually or when configured keywords
appear / disappear.

Skills:

```text
visual_watch_add https://status.example.com interval=10m threshold=0.15 cooldown=30m \
    keywords=down,outage notify=both
visual_watch_list
visual_watch_test 1
visual_watch_remove 1
```

Notification modes:

- `change` — visual diff above `threshold` (fraction of changed pixels, default `0.15`)
- `keywords` — keyword match in OCR'd text (falls back to page HTML text)
- `both` — fire on either signal

Baseline screenshots are stored under `visual_watch.baselines_dir`
(default `./data/visual-watch`).

## Where watches live

- Specs + current state: `watches` table.
- Incidents (open / resolved / pending / expired / completed):
  `watch_incidents`.
- Pack markers: `settings` (`pack:<chat>:<name>` = `enabled`).
- Visual watch specs + baseline paths: `visual_watches`.

The watch loop reuses `agent.request_timeout` per probe. A slow probe
(e.g. SSH to a flaky node) cannot wedge the entire poller.

## What watches are NOT

- They are not metric storage. There is no time-series DB. A watch
  only remembers "in condition since Tn" — it does not graph history.
- They are not arbitrary cron. Conditions are predicates over local
  metrics, service state, or allowlisted network targets, not "run
  this command every 10 min."
- They are not multi-recipient. Alerts go to the chat that owns the
  watch.

If you find yourself wishing for any of those, you probably want
Prometheus + Alertmanager, not openLight. The point of openLight is
the last hop: turning an incident into a button-click action on
Telegram.
