#!/usr/bin/env bash
# Deploy the MHS35 display dashboard to the Raspberry Pi.
# Uses the same PI_USER / PI_HOST / PI_DEST_DIR / PI_BIN_DIR variables
# that deploy-rpi.sh and deploy-rpi-service.sh use (exported by common.mk).
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
PI_DEST_DIR="${PI_DEST_DIR:-/home/${PI_USER}}"
PI_BIN_DIR="${PI_BIN_DIR:-${PI_DEST_DIR}/bin}"
SERVICE_NAME="openlight-display"

SCRIPT_SRC="${ROOT_DIR}/scripts/display-dashboard.py"
SCRIPT_DEST="${PI_DEST_DIR}/openlight/scripts/display-dashboard.py"

TEMPLATE_PATH="${ROOT_DIR}/deployments/systemd/${SERVICE_NAME}.service"
REMOTE_SERVICE_PATH="/etc/systemd/system/${SERVICE_NAME}.service"
REMOTE_TMP="${PI_DEST_DIR}/${SERVICE_NAME}.service.new"

LOCAL_TMP="$(mktemp)"
cleanup() { rm -f "${LOCAL_TMP}"; }
trap cleanup EXIT

# Patch user/group/paths in the unit template so it matches the live Pi layout.
sed \
  -e "s|^User=.*|User=${PI_USER}|" \
  -e "s|^Group=.*|Group=${PI_USER}|" \
  -e "s|^WorkingDirectory=.*|WorkingDirectory=${PI_DEST_DIR}|" \
  -e "s|ExecStart=.*|ExecStart=/usr/bin/python3 ${SCRIPT_DEST}|" \
  -e "s|OPENLIGHT_BIN=.*|OPENLIGHT_BIN=${PI_BIN_DIR}/openlight|" \
  "${TEMPLATE_PATH}" > "${LOCAL_TMP}"

echo "Ensuring python3-pil is installed on ${PI_HOST}..."
ssh "${PI_USER}@${PI_HOST}" "dpkg -s python3-pil &>/dev/null || sudo apt-get install -y python3-pil"

echo "Ensuring remote scripts directory ${PI_USER}@${PI_HOST}:${PI_DEST_DIR}/openlight/scripts..."
ssh "${PI_USER}@${PI_HOST}" "mkdir -p '${PI_DEST_DIR}/openlight/scripts'"

echo "Uploading display-dashboard.py..."
scp "${SCRIPT_SRC}" "${PI_USER}@${PI_HOST}:${SCRIPT_DEST}"

echo "Uploading systemd unit..."
scp "${LOCAL_TMP}" "${PI_USER}@${PI_HOST}:${REMOTE_TMP}"

echo "Installing and restarting ${SERVICE_NAME}.service..."
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  sudo install -m 644 '${REMOTE_TMP}' '${REMOTE_SERVICE_PATH}'
  rm -f '${REMOTE_TMP}'
  sudo systemctl daemon-reload
  sudo systemctl enable '${SERVICE_NAME}.service'
  sudo systemctl restart '${SERVICE_NAME}.service'
"

echo "Display deploy complete: ${PI_USER}@${PI_HOST}"
