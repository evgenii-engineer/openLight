#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MAC_USER="${MAC_USER:-$(whoami)}"
MAC_HOST="${MAC_HOST:-macmini.local}"
CONFIG_SRC="${CONFIG_SRC:-${ROOT_DIR}/configs/agent.macmini.yaml}"
REMOTE_CONFIG_PATH="${REMOTE_CONFIG_PATH:-/etc/openlight/agent.yaml}"
REMOTE_TMP_PATH="/Users/${MAC_USER}/$(basename "${REMOTE_CONFIG_PATH}").new"

if [[ ! -f "${CONFIG_SRC}" ]]; then
  echo "Config file not found: ${CONFIG_SRC}"
  echo "Create it from configs/agent.macmini.example.yaml first."
  exit 1
fi

echo "Uploading config ${CONFIG_SRC} to ${MAC_USER}@${MAC_HOST}:${REMOTE_TMP_PATH}..."
scp "${CONFIG_SRC}" "${MAC_USER}@${MAC_HOST}:${REMOTE_TMP_PATH}"

echo "Installing remote config to ${REMOTE_CONFIG_PATH}..."
ssh "${MAC_USER}@${MAC_HOST}" "
  set -e
  remote_group=\$(id -gn '${MAC_USER}')
  sudo mkdir -p '$(dirname "${REMOTE_CONFIG_PATH}")'
  sudo install -m 600 -o '${MAC_USER}' -g \"\${remote_group}\" '${REMOTE_TMP_PATH}' '${REMOTE_CONFIG_PATH}'
  rm -f '${REMOTE_TMP_PATH}'
"

echo "Config deploy complete: ${MAC_USER}@${MAC_HOST}:${REMOTE_CONFIG_PATH}"
