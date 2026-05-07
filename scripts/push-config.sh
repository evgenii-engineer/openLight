#!/usr/bin/env bash
# Upload CONFIG_LOCAL to CONFIG_REMOTE, taking a timestamped backup of the
# previous remote config first. Final mode is 600.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${CONFIG_LOCAL:?CONFIG_LOCAL must be set}"
: "${CONFIG_REMOTE:?CONFIG_REMOTE must be set}"

if [ ! -f "${CONFIG_LOCAL}" ]; then
  echo "missing local config: ${CONFIG_LOCAL}" >&2
  exit 1
fi

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
PROJECT_DIR="${PROJECT_DIR}"
CONFIG_REMOTE="${CONFIG_REMOTE}"

if [ ! -d "\${PROJECT_DIR}" ]; then
  echo "remote project dir missing: \${PROJECT_DIR}. run make remote-prepare first"
  exit 1
fi
mkdir -p "\$(dirname "\${CONFIG_REMOTE}")"
if [ -f "\${CONFIG_REMOTE}" ]; then
  backup="\${CONFIG_REMOTE}.bak.\$(date +%Y%m%d-%H%M%S)"
  cp "\${CONFIG_REMOTE}" "\${backup}"
  chmod 600 "\${backup}"
fi
EOF

rsync -az "${CONFIG_LOCAL}" "${SSH_TARGET}:${CONFIG_REMOTE}"
ssh "${SSH_TARGET}" "chmod 600 '${CONFIG_REMOTE}'"
