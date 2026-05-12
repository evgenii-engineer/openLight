#!/usr/bin/env bash
# Print or tail the agent stdout log on the remote Mac mini.
#
# Defaults to a one-shot snapshot of agent.out.log (last $LINES lines).
# Set FOLLOW=1 for `tail -f`. Set WITH_ERR=1 to also include agent.err.log.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${RUNTIME_DIR:?RUNTIME_DIR must be set}"

LINES="${LINES:-200}"
FOLLOW="${FOLLOW:-0}"
WITH_ERR="${WITH_ERR:-0}"

if [ "${FOLLOW}" = "1" ] || [ "${FOLLOW}" = "true" ]; then
  tail_args="-n ${LINES} -f"
else
  tail_args="-n ${LINES}"
fi

files="'${RUNTIME_DIR}/logs/agent.out.log'"
touch_files="'${RUNTIME_DIR}/logs/agent.out.log'"
if [ "${WITH_ERR}" = "1" ] || [ "${WITH_ERR}" = "true" ]; then
  files="${files} '${RUNTIME_DIR}/logs/agent.err.log'"
  touch_files="${touch_files} '${RUNTIME_DIR}/logs/agent.err.log'"
fi

ssh "${SSH_TARGET}" \
  "touch ${touch_files}; tail ${tail_args} ${files}"
