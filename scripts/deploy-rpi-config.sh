#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
CONFIG_SRC="${CONFIG_SRC:-${ROOT_DIR}/configs/agent.rpi.yaml}"
REMOTE_CONFIG_PATH="${REMOTE_CONFIG_PATH:-/etc/openlight/agent.yaml}"
REMOTE_TMP_PATH="/home/${PI_USER}/$(basename "${REMOTE_CONFIG_PATH}").new"

if [[ ! -f "${CONFIG_SRC}" ]]; then
  echo "Config file not found: ${CONFIG_SRC}"
  echo "Create it from configs/agent.rpi.ollama.example.yaml first."
  exit 1
fi

echo "Uploading config ${CONFIG_SRC} to ${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}..."
scp "${CONFIG_SRC}" "${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}"

echo "Installing remote config to ${REMOTE_CONFIG_PATH}..."
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  sudo mkdir -p '$(dirname "${REMOTE_CONFIG_PATH}")'
  sudo install -m 600 -o '${PI_USER}' -g '${PI_USER}' '${REMOTE_TMP_PATH}' '${REMOTE_CONFIG_PATH}'
  rm -f '${REMOTE_TMP_PATH}'
"

echo "Config deploy complete: ${PI_USER}@${PI_HOST}:${REMOTE_CONFIG_PATH}"
