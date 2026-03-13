#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  echo "TELEGRAM_BOT_TOKEN must be set"
  exit 1
fi

export ALLOWED_USER_IDS="${ALLOWED_USER_IDS:-111111111}"
export ALLOWED_CHAT_IDS="${ALLOWED_CHAT_IDS:-111111111}"
export SQLITE_PATH="${SQLITE_PATH:-$ROOT_DIR/data/agent.db}"
export ALLOWED_SERVICES="${ALLOWED_SERVICES:-tailscale,jellyfin}"
export LLM_ENABLED="${LLM_ENABLED:-false}"
export LOG_LEVEL="${LOG_LEVEL:-info}"
export REQUEST_TIMEOUT="${REQUEST_TIMEOUT:-5s}"
export POLL_TIMEOUT="${POLL_TIMEOUT:-25s}"

mkdir -p "$ROOT_DIR/data"
exec go run ./cmd/agent
