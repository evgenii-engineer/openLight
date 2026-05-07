# Nodes

A **node** is any machine openLight can reach over SSH to read status, tail
logs, or restart services. Your local machine is implicit; everything else
goes in the `nodes:` block.

```yaml
nodes:
  vps:
    address: "203.0.113.10:22"
    user: "root"
    password_env: "OPENLIGHT_VPS_PASSWORD"
    known_hosts_path: "/home/pi/.ssh/known_hosts"

  pi:
    address: "100.64.0.5:22"
    user: "pi"
    private_key_path: "/home/pi/.ssh/id_ed25519"
    known_hosts_path: "/home/pi/.ssh/known_hosts"
```

That's it. Nodes by themselves don't do anything — they become useful when
you reference them from a service target.

## Using a node in a service target

The `services.allowed` list says what openLight is permitted to operate on.
Anything not on this list is invisible to the bot, even with the LLM enabled.

```yaml
services:
  allowed:
    # local systemd service
    - tailscale

    # Compose service on a remote node
    - "matrix=node:vps:compose:/opt/matrix/docker-compose.yml:synapse"

    # Direct Docker container on a remote node
    - "jitsi=node:vps:docker:docker-jitsi-meet_web_1"

    # systemd unit on a remote node
    - "jvb=node:vps:systemd:jitsi-videobridge2"
```

Once that's in place, every service-skill (`status`, `logs`, `restart`) and
every watch (`/watch add service matrix ask`) operates against the right
backend on the right node, with the same Telegram UX as a local service.

## Authentication

A node config must specify exactly one of:

- `password` (plain text — discouraged)
- `password_env` (the env var the password lives in)
- `private_key_path` (with optional `private_key_passphrase` /
  `private_key_passphrase_env`)

`known_hosts_path` is required unless `insecure_ignore_host_key: true` is
set explicitly (don't).

`sudo: true` runs remote service commands under sudo. Only use it on nodes
where the user has passwordless sudo for the relevant systemctl/docker
commands.

## Validating

```bash
openlight doctor -config /etc/openlight/agent.yaml
```

For each configured node, doctor probes the SSH TCP port. It does not log
in — that requires real credentials and would side-effect against the host
— so a green line means "the network is reachable", not "auth works."

## Legacy: `access.hosts`

Older configs declared nodes under `access.hosts:`. That key is still
accepted and merged into `nodes:` at load time. New configs should use
`nodes:` directly.

## What nodes are NOT

- They are not arbitrary remote shell. openLight only runs the
  service-management commands you already declared in `services.allowed`.
- They are not a service-discovery layer. Adding a node doesn't enumerate
  its services automatically.
- They are not multi-tenant infra. Each openLight instance owns its node
  list and its own SSH credentials.

The mental model is closer to `~/.ssh/config` than to a cluster manager.
