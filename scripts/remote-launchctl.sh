#!/usr/bin/env bash
# Thin wrapper around launchctl on the remote Mac mini.
# Usage: remote-launchctl.sh restart|stop
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${LAUNCH_AGENT:?LAUNCH_AGENT must be set}"

action="${1:-}"
case "${action}" in
  restart)
    ssh "${SSH_TARGET}" bash -se <<EOF
set -e
launchctl unload "${LAUNCH_AGENT}" 2>/dev/null || true
launchctl load "${LAUNCH_AGENT}"
EOF
    ;;
  stop)
    ssh "${SSH_TARGET}" "launchctl unload '${LAUNCH_AGENT}' 2>/dev/null || true"
    ;;
  *)
    echo "usage: $0 restart|stop" >&2
    exit 2
    ;;
esac
