#!/usr/bin/env bash
# Hard health check on the remote Mac mini: Ollama API, launchd entry,
# process, CLI status. Exits non-zero on the first failure.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"
: "${CONFIG_REMOTE:?CONFIG_REMOTE must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
RUNTIME_DIR="${RUNTIME_DIR}"
CONFIG_REMOTE="${CONFIG_REMOTE}"

curl -fsS http://127.0.0.1:11434/api/tags >/dev/null
launchctl list | grep -q openlight
pgrep -f "openlight agent" >/dev/null
"\${RUNTIME_DIR}/bin/openlight" cli -config "\${CONFIG_REMOTE}" -exec "status" >/dev/null
EOF
