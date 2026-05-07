#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

BIN_NAME="${BIN_NAME:-openlight}"
PKG="${PKG:-./cmd/openlight}"
SSH_USER="${SSH_USER:-$(whoami)}"
SSH_HOST="${SSH_HOST:-macmini.local}"
SSH_TARGET="${SSH_TARGET:-${SSH_USER}@${SSH_HOST}}"
RUNTIME_DIR="${RUNTIME_DIR:-/Users/${SSH_USER}/openlight}"

BUILD_DIR="${BUILD_DIR:-${ROOT_DIR}/build/darwin-arm64}"
ARTIFACT="${ARTIFACT:-${BUILD_DIR}/${BIN_NAME}}"
REMOTE_PATH="${REMOTE_PATH:-${RUNTIME_DIR}/bin/${BIN_NAME}}"
REMOTE_TMP_PATH="${REMOTE_PATH}.new"

mkdir -p "${BUILD_DIR}"

echo "Building ${BIN_NAME} for Mac mini..."
(
  cd "${ROOT_DIR}"
  GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 \
    go build -trimpath -ldflags="-s -w" -o "${ARTIFACT}" "${PKG}"
)

echo "Uploading ${ARTIFACT} to ${SSH_TARGET}:${REMOTE_TMP_PATH}..."
scp "${ARTIFACT}" "${SSH_TARGET}:${REMOTE_TMP_PATH}"

echo "Replacing remote binary..."
ssh "${SSH_TARGET}" "
  set -e
  mkdir -p '$(dirname "${REMOTE_PATH}")'
  chmod +x '${REMOTE_TMP_PATH}'
  mv '${REMOTE_TMP_PATH}' '${REMOTE_PATH}'
"

echo "Deploy complete: ${SSH_TARGET}:${REMOTE_PATH}"
