# Watches

A **watch** is a background rule that polls some local or remote signal and
opens an **incident** the moment a configured condition stays true longer
than a threshold. Incidents become Telegram alerts with action buttons.

Watches are openLight's main proactive mechanism. Everything else (status,
logs, restart) is reactive — you ask, the bot answers. A watch lets the
bot speak first.

## Mental model

```
poll ──► condition ──► duration ──► incident ──► alert ──► action
```

- **poll**: the watch service evaluates every enabled watch on a fixed cadence
  (`watch.poll_interval`, default `15s`).
- **condition**: a watch fires when its predicate is true (service down, CPU
  > 90%, disk > 85%, etc).
- **duration**: a watch only counts as "tripped" if the condition holds for
  `for <duration>` (defaults vary by kind). Stops flapping.
- **incident**: an open SQLite row that links the watch, the time, and the
  reaction mode.
- **alert**: a Telegram message sent through the same transport as
  user-initiated commands.
- **action**: the user taps `Restart` / `Logs` / `Status` / `Ignore`. Action
  buttons reuse the existing skill surface — `Restart` invokes the same
  `service_restart` skill that a user could type directly. One audit path
  for both manual and automatic operations.

## Reaction modes

Every watch picks one:

- `notify` — send the alert. Don't act. Default for metric watches.
- `ask` — send the alert with action buttons. Wait `watch.ask_ttl` for the
  user to tap one. Default for service-down watches.
- `auto` — perform the action immediately. Use sparingly.

## Built-in watch kinds

| Kind                | Predicate                              | Default mode |
|---------------------|----------------------------------------|--------------|
| `service_down`      | service unit is not active             | `ask`        |
| `cpu_high`          | CPU usage > N% for D                   | `notify`     |
| `memory_high`       | memory usage > N% for D                | `notify`     |
| `disk_high`         | disk usage at path P > N% for D        | `notify`     |
| `temperature_high`  | sensor temp > N°C for D                | `notify`     |

Visual watches (page-screenshot diff) are a separate, optional subsystem;
see `visual_watch:` in config and the `visualwatch_*` skills.

## Adding a watch

From Telegram:

```
/watch add service tailscale ask for 30s cooldown 10m
/watch add cpu > 90% for 5m cooldown 15m
/watch add disk / > 85% for 10m
/watch list
/watch pause 7
/watch resume 7
/watch remove 7
/watch history
/watch test 7         # synthetic incident — exercises the alert path
```

`cooldown` is the minimum gap between two incidents for the same watch,
even if the condition keeps flapping. Defaults to `10m`.

## Packs

A **pack** is a one-shot way to seed a curated set of watches:

```
/enable system     # CPU > 90%, memory > 90%, disk / > 85%
/enable docker     # restart watches for declared docker/compose services
/enable auto-heal  # service_down watches in `auto` mode (use carefully)
```

Packs are idempotent. Re-running `/enable docker` on an already-seeded chat
updates existing watches in place; it does not create duplicates.

## Where watches live

- Specs and current state: `watches` table.
- Open / resolved / pending / expired / completed incidents: `watch_incidents`.
- Pack markers: `settings`.

The watch loop reuses the request timeout from `agent.request_timeout`. A
slow probe (e.g. SSH to a flaky node) cannot wedge the entire poller.

## What watches are NOT

- They are not metric storage. There is no time-series DB. A watch only
  remembers "in condition since Tn" — it does not graph history.
- They are not arbitrary cron. Conditions are predicates over local
  metrics or service state, not "run this command every 10 min."
- They are not multi-recipient. Alerts go to the chat that owns the watch.

If you find yourself wishing for any of those, you probably want
Prometheus + Alertmanager, not openLight. The point of openLight is the
last hop: turning an incident into a button-click action on Telegram.
