#!/usr/bin/env bash
# Build the openlight binary on the remote host using its Go toolchain.
# Used when we want the binary built from the remote git checkout
# instead of cross-compiling locally and shipping it.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
PROJECT_DIR="${PROJECT_DIR}"
RUNTIME_DIR="${RUNTIME_DIR}"

cd "\${PROJECT_DIR}"
test -f go.mod || {
  echo "remote source checkout not found: \${PROJECT_DIR}"
  echo "use make deploy-macmini to upload local darwin/arm64 binaries instead"
  exit 1
}
go mod download
CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "\${RUNTIME_DIR}/bin/openlight" ./cmd/openlight
test -x "\${RUNTIME_DIR}/bin/openlight"
EOF
