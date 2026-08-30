#!/usr/bin/env bash
# Provision long-term memory on the Raspberry Pi: SSD directories,
# pdftotext, and the Qdrant container.
#
# Runs as part of deploy-rpi-all, so it must be a no-op when memory is
# switched off. It reads memory.rag.enabled out of the config that
# deploy-rpi-config.sh is about to push and skips everything when the
# feature is disabled — a deploy of an unrelated change must never
# install a system package or start a container.
#
# Override the decision with MEMORY_PROVISION:
#   auto    (default) follow memory.rag.enabled in the config
#   always  provision regardless
#   never   skip regardless
#
# The embedding model is not touched here: openLight pulls it onto the
# brain node itself on first start.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

PI_USER="${PI_USER:-pi}"
PI_HOST="${PI_HOST:-raspberrypi.local}"
PI_DEST_DIR="${PI_DEST_DIR:-/home/${PI_USER}}"
CONFIG_SRC="${CONFIG_SRC:-${ROOT_DIR}/configs/agent.rpi.yaml}"
MEMORY_PROVISION="${MEMORY_PROVISION:-auto}"
MEMORY_ROOT="${MEMORY_ROOT:-/mnt/openlight/memory}"
QDRANT_STORAGE="${QDRANT_STORAGE:-${MEMORY_ROOT}/qdrant}"

REMOTE_DIR="${PI_DEST_DIR}/openlight"
REMOTE_COMPOSE="${REMOTE_DIR}/deployments/docker/qdrant-compose.yaml"
REMOTE_SCRIPT="${REMOTE_DIR}/scripts/install-memory-deps.sh"

# memory_rag_value reads a scalar out of the memory.rag block: either a
# direct child (memory.rag.enabled) or one level deeper
# (memory.rag.storage.root).
#
# It walks the block structure rather than grepping for the key name —
# "enabled:" appears half a dozen times in a full config, and
# memory.enabled (the older manual-/remember switch) sits two lines away
# from the one we care about.
#
# Assumes the project's own 2-space YAML style, which every configs/*.yaml
# uses. Anything exotic reads as absent; use MEMORY_PROVISION=always and
# an explicit MEMORY_ROOT in that case.
#
#   memory_rag_value <file> <key>            -> memory.rag.<key>
#   memory_rag_value <file> <section> <key>  -> memory.rag.<section>.<key>
memory_rag_value() {
  awk -v section="${3:+$2}" -v key="${3:-$2}" '
    function clean(line, prefix,   value) {
      value = line
      sub(prefix, "", value)
      sub(/[[:space:]]*#.*$/, "", value)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      gsub(/^["'"'"']|["'"'"']$/, "", value)
      return value
    }
    # A new top-level key ends whatever block we were in.
    /^[^[:space:]#]/ { in_memory = ($0 ~ /^memory:[[:space:]]*$/); in_rag = 0; in_section = 0; next }
    # A new second-level key ends the rag block.
    in_memory && /^  [^[:space:]#]/ { in_rag = ($0 ~ /^  rag:[[:space:]]*$/); in_section = 0; next }
    # A new third-level key ends the section inside rag.
    in_rag && /^    [^[:space:]#]/ {
      if (section != "" && $0 ~ "^    " section ":[[:space:]]*$") { in_section = 1; next }
      in_section = 0
      if (section == "" && $0 ~ "^    " key ":") { print clean($0, "^    " key ":"); exit }
      next
    }
    in_section && $0 ~ "^      " key ":" { print clean($0, "^      " key ":"); exit }
  ' "$1"
}

case "${MEMORY_PROVISION}" in
  never)
    echo "Memory provisioning skipped (MEMORY_PROVISION=never)."
    exit 0
    ;;
  always)
    echo "Memory provisioning forced (MEMORY_PROVISION=always)."
    ;;
  auto)
    if [[ ! -f "${CONFIG_SRC}" ]]; then
      echo "Memory provisioning skipped: no config at ${CONFIG_SRC}."
      exit 0
    fi
    if [[ "$(memory_rag_value "${CONFIG_SRC}" enabled | tr '[:upper:]' '[:lower:]')" != "true" ]]; then
      echo "Memory provisioning skipped: memory.rag.enabled is not true in ${CONFIG_SRC}."
      echo "  Set it to true (or run with MEMORY_PROVISION=always) to install Qdrant on the Pi."
      exit 0
    fi
    ;;
  *)
    echo "unknown MEMORY_PROVISION=${MEMORY_PROVISION}; expected auto|always|never" >&2
    exit 2
    ;;
esac

# The agent obeys memory.rag.storage.root, so provisioning has to follow
# it. Creating the directory somewhere else would leave the agent writing
# to an unprovisioned path — most likely the SD card instead of the SSD.
if [[ -f "${CONFIG_SRC}" ]]; then
  config_root="$(memory_rag_value "${CONFIG_SRC}" storage root)"
  if [[ -n "${config_root}" && "${config_root}" != "${MEMORY_ROOT}" ]]; then
    echo "Using memory root from ${CONFIG_SRC}: ${config_root} (make default was ${MEMORY_ROOT})"
    MEMORY_ROOT="${config_root}"
    QDRANT_STORAGE="${MEMORY_ROOT}/qdrant"
  fi
fi

echo "Memory root: ${MEMORY_ROOT}"
echo "Uploading memory provisioning files to ${PI_USER}@${PI_HOST}..."
ssh "${PI_USER}@${PI_HOST}" "mkdir -p '${REMOTE_DIR}/deployments/docker' '${REMOTE_DIR}/scripts'"
scp "${ROOT_DIR}/deployments/docker/qdrant-compose.yaml" "${PI_USER}@${PI_HOST}:${REMOTE_COMPOSE}"
scp "${ROOT_DIR}/scripts/install-memory-deps.sh" "${PI_USER}@${PI_HOST}:${REMOTE_SCRIPT}"

echo "Provisioning memory on ${PI_HOST}..."
# shellcheck disable=SC2029  # the remote command is intentionally expanded locally
ssh "${PI_USER}@${PI_HOST}" "
  set -e
  chmod +x '${REMOTE_SCRIPT}'
  MEMORY_ROOT='${MEMORY_ROOT}' \
  QDRANT_STORAGE='${QDRANT_STORAGE}' \
  QDRANT_COMPOSE_FILE='${REMOTE_COMPOSE}' \
    '${REMOTE_SCRIPT}'
"

echo "Memory provisioned on ${PI_USER}@${PI_HOST}."
