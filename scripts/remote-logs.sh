#!/usr/bin/env bash
# Tail the agent stdout/stderr logs on the remote Mac mini.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"

ssh "${SSH_TARGET}" \
  "touch '${RUNTIME_DIR}/logs/agent.out.log' '${RUNTIME_DIR}/logs/agent.err.log'; \
   tail -n 40 -f '${RUNTIME_DIR}/logs/agent.out.log' '${RUNTIME_DIR}/logs/agent.err.log'"
