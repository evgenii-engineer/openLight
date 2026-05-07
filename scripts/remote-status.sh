#!/usr/bin/env bash
# Print a concise picture of the agent on the remote Mac mini:
# launchd entries, processes, last log lines, CLI status output.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"
: "${CONFIG_REMOTE:?CONFIG_REMOTE must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set +e
RUNTIME_DIR="${RUNTIME_DIR}"
CONFIG_REMOTE="${CONFIG_REMOTE}"

echo "launchctl:"
launchctl list | grep openlight || true
echo

echo "processes:"
pgrep -fl openlight-agent || true
echo

echo "stderr (last 40):"
tail -n 40 "\${RUNTIME_DIR}/logs/agent.err.log" 2>/dev/null || true
echo

echo "stdout (last 40):"
tail -n 40 "\${RUNTIME_DIR}/logs/agent.out.log" 2>/dev/null || true
echo

echo "cli status:"
"\${RUNTIME_DIR}/bin/openlight-cli" -config "\${CONFIG_REMOTE}" -exec "status" || true
EOF
