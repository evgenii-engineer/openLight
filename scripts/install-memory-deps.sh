#!/usr/bin/env bash
# Long-term memory prerequisites: the SSD directory layout, Qdrant, and
# poppler's pdftotext.
#
# The embedding model is deliberately NOT pulled here — openLight pulls
# it itself on first start (memory.rag.embeddings.auto_pull), the same
# way the compose stack pulls the chat model. This script only covers
# what a process cannot provision for itself: directories owned by root
# and system packages.
#
# Idempotent by design, like install-ocr-deps.sh and
# install-vision-deps.sh: everything already in place is reported and
# skipped, so re-running on a configured machine is a no-op.
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

MEMORY_ROOT="${MEMORY_ROOT:-/mnt/openlight/memory}"
QDRANT_STORAGE="${QDRANT_STORAGE:-${MEMORY_ROOT}/qdrant}"
QDRANT_COMPOSE_FILE="${QDRANT_COMPOSE_FILE:-${ROOT_DIR}/deployments/docker/qdrant-compose.yaml}"
QDRANT_REST_ENDPOINT="${QDRANT_REST_ENDPOINT:-http://127.0.0.1:6333}"
QDRANT_WAIT_SECONDS="${QDRANT_WAIT_SECONDS:-60}"
BREW="${BREW:-brew}"

export PATH="/opt/homebrew/bin:/usr/local/bin:${PATH}"

# ---- 1. Directory layout on the SSD --------------------------------------
#
# The agent creates the RAW subdirectories itself, but it cannot create
# a directory under /mnt that it has no write access to. Doing it here,
# once, with the right owner, is the part that genuinely needs a human's
# privileges.

echo "==> memory root: ${MEMORY_ROOT}"
if [ -d "${MEMORY_ROOT}" ] && [ -w "${MEMORY_ROOT}" ]; then
  echo "    already present and writable"
else
  if mkdir -p "${MEMORY_ROOT}" 2>/dev/null; then
    echo "    created"
  else
    echo "    creating with sudo"
    sudo mkdir -p "${MEMORY_ROOT}"
    sudo chown -R "$(id -un):$(id -gn)" "${MEMORY_ROOT}"
  fi
fi
mkdir -p "${QDRANT_STORAGE}"
echo "    qdrant storage: ${QDRANT_STORAGE}"

# ---- 2. pdftotext --------------------------------------------------------
#
# Optional: without it the built-in PDF parser still handles ordinary
# text PDFs, it just cannot read scanned or CID-encoded ones. A missing
# package is therefore a warning, never a failure.

if command -v pdftotext >/dev/null 2>&1; then
  echo "==> pdftotext already installed: $(command -v pdftotext)"
elif command -v apt-get >/dev/null 2>&1; then
  echo "==> installing poppler-utils (pdftotext)"
  sudo apt-get update -qq
  sudo apt-get install -y poppler-utils
elif command -v "${BREW}" >/dev/null 2>&1; then
  echo "==> installing poppler (pdftotext)"
  "${BREW}" install poppler
else
  echo "==> no apt-get or brew; install poppler-utils manually for scanned-PDF support"
fi

# ---- 3. Qdrant -----------------------------------------------------------

if ! command -v docker >/dev/null 2>&1; then
  echo "==> docker not found; cannot start Qdrant automatically"
  echo "    install docker, then re-run this script (or set memory.rag.vector.provider: none)"
  exit 0
fi

if ! docker compose version >/dev/null 2>&1; then
  echo "==> 'docker compose' plugin not available; cannot start Qdrant automatically"
  exit 0
fi

if curl -fsS "${QDRANT_REST_ENDPOINT}/readyz" >/dev/null 2>&1 \
  || curl -fsS "${QDRANT_REST_ENDPOINT}/collections" >/dev/null 2>&1; then
  echo "==> qdrant already running at ${QDRANT_REST_ENDPOINT}"
else
  echo "==> starting qdrant (${QDRANT_COMPOSE_FILE})"
  QDRANT_STORAGE="${QDRANT_STORAGE}" docker compose -f "${QDRANT_COMPOSE_FILE}" up -d
fi

echo "==> waiting for qdrant (up to ${QDRANT_WAIT_SECONDS}s)"
deadline=$(( $(date +%s) + QDRANT_WAIT_SECONDS ))
until curl -fsS "${QDRANT_REST_ENDPOINT}/collections" >/dev/null 2>&1; do
  if [ "$(date +%s)" -ge "${deadline}" ]; then
    echo "    qdrant did not become ready in time; check: docker compose -f ${QDRANT_COMPOSE_FILE} logs"
    exit 1
  fi
  sleep 2
done

echo "==> qdrant ready: ${QDRANT_REST_ENDPOINT}"
echo
echo "Nothing else to provision by hand. Set memory.rag.enabled: true and restart"
echo "openLight — it pulls the embedding model and creates the collection itself."
