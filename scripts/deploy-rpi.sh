#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BIN_NAME="${BIN_NAME:-openlight-agent}"
PKG="${PKG:-./cmd/agent}"
PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
PI_DEST_DIR="${PI_DEST_DIR:-/home/${PI_USER}}"

BUILD_DIR="${BUILD_DIR:-${ROOT_DIR}/build/linux-arm64}"
ARTIFACT="${ARTIFACT:-${BUILD_DIR}/${BIN_NAME}}"
REMOTE_PATH="${PI_DEST_DIR}/${BIN_NAME}"
REMOTE_TMP_PATH="${REMOTE_PATH}.new"

mkdir -p "${BUILD_DIR}"

echo "Building ${BIN_NAME} for Raspberry Pi..."
(
  cd "${ROOT_DIR}"
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "${ARTIFACT}" "${PKG}"
)

echo "Uploading ${ARTIFACT} to ${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}..."
scp "${ARTIFACT}" "${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}"

echo "Replacing remote binary..."
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  chmod +x '${REMOTE_TMP_PATH}'
  pkill -x '${BIN_NAME}' || true
  mv '${REMOTE_TMP_PATH}' '${REMOTE_PATH}'
"

echo "Deploy complete: ${PI_USER}@${PI_HOST}:${REMOTE_PATH}"
