#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MAC_USER="${MAC_USER:-$(whoami)}"
MAC_HOST="${MAC_HOST:-macmini.local}"
MAC_DEST_DIR="${MAC_DEST_DIR:-/Users/${MAC_USER}}"
BIN_NAME="${BIN_NAME:-openlight-agent}"
SERVICE_LABEL="${SERVICE_LABEL:-com.openlight.agent}"

TEMPLATE_PATH="${ROOT_DIR}/deployments/launchd/openlight-agent.plist"
LOCAL_TMP="$(mktemp)"
REMOTE_TMP_PATH="${MAC_DEST_DIR}/${SERVICE_LABEL}.plist.new"
REMOTE_SERVICE_PATH="/Library/LaunchDaemons/${SERVICE_LABEL}.plist"
REMOTE_LOG_DIR="${MAC_DEST_DIR}/Library/Logs"

cleanup() {
  rm -f "${LOCAL_TMP}"
}
trap cleanup EXIT

sed \
  -e "s|<string>com.openlight.agent</string>|<string>${SERVICE_LABEL}</string>|" \
  -e "0,|<string>/Users/macmini/openlight-agent</string>|s|<string>/Users/macmini/openlight-agent</string>|<string>${MAC_DEST_DIR}/${BIN_NAME}</string>|" \
  -e "s|<string>/Users/macmini</string>|<string>${MAC_DEST_DIR}</string>|" \
  -e "s|<string>macmini</string>|<string>${MAC_USER}</string>|" \
  -e "s|<string>/Users/macmini/Library/Logs/openlight-agent.log</string>|<string>${REMOTE_LOG_DIR}/openlight-agent.log</string>|" \
  -e "s|<string>/Users/macmini/Library/Logs/openlight-agent.error.log</string>|<string>${REMOTE_LOG_DIR}/openlight-agent.error.log</string>|" \
  "${TEMPLATE_PATH}" > "${LOCAL_TMP}"

echo "Uploading launchd plist to ${MAC_USER}@${MAC_HOST}:${REMOTE_TMP_PATH}..."
scp "${LOCAL_TMP}" "${MAC_USER}@${MAC_HOST}:${REMOTE_TMP_PATH}"

echo "Installing and restarting ${SERVICE_LABEL}..."
ssh "${MAC_USER}@${MAC_HOST}" "
  set -e
  mkdir -p '${REMOTE_LOG_DIR}'
  sudo install -m 644 -o root -g wheel '${REMOTE_TMP_PATH}' '${REMOTE_SERVICE_PATH}'
  rm -f '${REMOTE_TMP_PATH}'
  sudo launchctl bootout system '${REMOTE_SERVICE_PATH}' 2>/dev/null || true
  sudo launchctl bootstrap system '${REMOTE_SERVICE_PATH}'
  sudo launchctl enable system/${SERVICE_LABEL}
  sudo launchctl kickstart -k system/${SERVICE_LABEL}
"

echo "Service deploy complete: ${REMOTE_SERVICE_PATH}"
