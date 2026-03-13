#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
PI_DEST_DIR="${PI_DEST_DIR:-/home/${PI_USER}}"
BIN_NAME="${BIN_NAME:-openlight-agent}"
SERVICE_NAME="${SERVICE_NAME:-openlight-agent}"

TEMPLATE_PATH="${ROOT_DIR}/deployments/systemd/openlight-agent.service"
LOCAL_TMP="$(mktemp)"
REMOTE_TMP_PATH="${PI_DEST_DIR}/${SERVICE_NAME}.service.new"
REMOTE_SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"

cleanup() {
  rm -f "${LOCAL_TMP}"
}
trap cleanup EXIT

sed \
  -e "s|^User=.*|User=${PI_USER}|" \
  -e "s|^Group=.*|Group=${PI_USER}|" \
  -e "s|^WorkingDirectory=.*|WorkingDirectory=${PI_DEST_DIR}|" \
  -e "s|^ExecStart=.*|ExecStart=${PI_DEST_DIR}/${BIN_NAME} -config /etc/openlight/agent.yaml|" \
  "${TEMPLATE_PATH}" > "${LOCAL_TMP}"

echo "Uploading systemd unit to ${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}..."
scp "${LOCAL_TMP}" "${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}"

echo "Installing and restarting ${SERVICE_NAME}.service..."
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  sudo install -m 644 '${REMOTE_TMP_PATH}' '${REMOTE_SERVICE_PATH}'
  rm -f '${REMOTE_TMP_PATH}'
  sudo systemctl daemon-reload
  sudo systemctl enable '${SERVICE_NAME}.service'
  sudo systemctl restart '${SERVICE_NAME}.service'
"

echo "Service deploy complete: ${REMOTE_SERVICE_PATH}"
