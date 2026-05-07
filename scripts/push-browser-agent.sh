#!/usr/bin/env bash
# Sync the small browser-agent helper (index.mjs + package.json) onto the
# remote host. Playwright install happens separately on the host.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${BROWSER_AGENT_REMOTE_DIR:?BROWSER_AGENT_REMOTE_DIR must be set}"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

rsync -az \
  "${ROOT_DIR}/tools/browser-agent/index.mjs" \
  "${ROOT_DIR}/tools/browser-agent/package.json" \
  "${SSH_TARGET}:${BROWSER_AGENT_REMOTE_DIR}/"
