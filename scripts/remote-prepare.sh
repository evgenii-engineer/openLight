#!/usr/bin/env bash
# Create the remote directory layout used by deploy-macmini and friends.
# Idempotent: every mkdir uses -p.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${USER_HOME:?USER_HOME must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"
: "${BROWSER_AGENT_REMOTE_DIR:?BROWSER_AGENT_REMOTE_DIR must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
mkdir -p \
  "${USER_HOME}" \
  "${PROJECT_DIR}" \
  "${PROJECT_DIR}/data" \
  "${PROJECT_DIR}/data/browser-artifacts" \
  "${BROWSER_AGENT_REMOTE_DIR}" \
  "${RUNTIME_DIR}/bin" \
  "${RUNTIME_DIR}/data" \
  "${RUNTIME_DIR}/workspace" \
  "${RUNTIME_DIR}/scripts" \
  "${RUNTIME_DIR}/logs" \
  "${USER_HOME}/Library/LaunchAgents"
EOF
