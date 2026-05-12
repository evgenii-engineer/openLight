#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BIN_NAME="${BIN_NAME:-openlight}"
PKG="${PKG:-./cmd/openlight}"
PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
PI_DEST_DIR="${PI_DEST_DIR:-/home/${PI_USER}}"
PI_BIN_DIR="${PI_BIN_DIR:-${PI_DEST_DIR}/bin}"
PI_BIN_PATH="${PI_BIN_PATH:-${PI_BIN_DIR}/${BIN_NAME}}"

BUILD_DIR="${BUILD_DIR:-${ROOT_DIR}/build/linux-arm64}"
ARTIFACT="${ARTIFACT:-${BUILD_DIR}/${BIN_NAME}}"
REMOTE_PATH="${PI_BIN_PATH}"
REMOTE_TMP_PATH="${REMOTE_PATH}.new"

mkdir -p "${BUILD_DIR}"

echo "Building ${BIN_NAME} for Raspberry Pi..."
(
  cd "${ROOT_DIR}"
  GOOS=linux GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "${ARTIFACT}" "${PKG}"
)

echo "Ensuring remote bin directory ${PI_BIN_DIR}..."
ssh "${PI_USER}@${PI_HOST}" "mkdir -p '${PI_BIN_DIR}'"

echo "Uploading ${ARTIFACT} to ${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}..."
scp "${ARTIFACT}" "${PI_USER}@${PI_HOST}:${REMOTE_TMP_PATH}"

echo "Replacing remote binary..."
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  chmod +x '${REMOTE_TMP_PATH}'
  pkill -x '${BIN_NAME}' || true
  mv -f '${REMOTE_TMP_PATH}' '${REMOTE_PATH}'
"

echo "Deploy complete: ${PI_USER}@${PI_HOST}:${REMOTE_PATH}"
