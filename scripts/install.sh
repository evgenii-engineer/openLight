#!/usr/bin/env bash
set -euo pipefail

fail() {
  echo "openLight install error: $*" >&2
  exit 1
}

require_command() {
  local name="$1"
  command -v "$name" >/dev/null 2>&1 || fail "required command not found: $name"
}

require_command curl
require_command docker

docker compose version >/dev/null 2>&1 || fail "docker compose plugin is required"

if [[ -z "${TELEGRAM_BOT_TOKEN:-}" ]]; then
  fail "TELEGRAM_BOT_TOKEN is required"
fi

if [[ -z "${ALLOWED_USER_IDS:-}" && -z "${ALLOWED_CHAT_IDS:-}" ]]; then
  fail "set ALLOWED_USER_IDS or ALLOWED_CHAT_IDS before running the installer"
fi

REPO_OWNER="${OPENLIGHT_REPO_OWNER:-evgenii-engineer}"
REPO_NAME="${OPENLIGHT_REPO_NAME:-openLight}"
GITHUB_API_BASE="${OPENLIGHT_GITHUB_API_BASE:-https://api.github.com/repos/$REPO_OWNER/$REPO_NAME}"
OPENLIGHT_REF="${OPENLIGHT_REF:-}"
INSTALL_DIR="${OPENLIGHT_DIR:-$PWD/openlight}"
COMPOSE_FILE="$INSTALL_DIR/openlight-compose.yaml"

resolve_ref() {
  if [[ -n "$OPENLIGHT_REF" ]]; then
    printf '%s\n' "$OPENLIGHT_REF"
    return
  fi

  local latest
  latest="$(
    curl -fsSL "$GITHUB_API_BASE/releases/latest" \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n 1
  )"

  if [[ -n "$latest" ]]; then
    printf '%s\n' "$latest"
    return
  fi

  fail "could not resolve latest release tag; set OPENLIGHT_REF manually"
}

REF="$(resolve_ref)"
RAW_BASE="${OPENLIGHT_RAW_BASE:-https://raw.githubusercontent.com/$REPO_OWNER/$REPO_NAME/$REF}"
COMPOSE_URL="${OPENLIGHT_COMPOSE_URL:-$RAW_BASE/openlight-compose.yaml}"

mkdir -p "$INSTALL_DIR/data"

echo "Using openLight ref: $REF"
echo "Downloading bundled Docker stack into $INSTALL_DIR ..."
curl -fsSL "$COMPOSE_URL" -o "$COMPOSE_FILE"

echo "Starting openLight ..."
(
  cd "$INSTALL_DIR"
  docker compose -f "$COMPOSE_FILE" up -d
)

cat <<EOF

openLight is starting.

Location:
- stack: $COMPOSE_FILE
- data: $INSTALL_DIR/data

Next steps:
- open Telegram and send /skills to your bot
- view logs: cd "$INSTALL_DIR" && docker compose -f openlight-compose.yaml logs -f openlight
- stop: cd "$INSTALL_DIR" && docker compose -f openlight-compose.yaml down

Tip:
- set LLM_ENABLED=false before install if you want deterministic-only mode
- set OPENLIGHT_REF=<tag> before install if you want to pin a specific release

EOF
