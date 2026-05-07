#!/usr/bin/env bash
# Install host-side dependencies (ffmpeg, whisper, node, tesseract,
# ollama, browser-agent npm deps + Playwright, optional vision model)
# on the remote Mac mini.
#
# Idempotent: every step skips its work if already done. Run after
# `push-browser-agent.sh` so the npm prefix exists.
set -euo pipefail

: "${SSH_TARGET:?SSH_TARGET must be set}"
: "${PROJECT_DIR:?PROJECT_DIR must be set}"
: "${BROWSER_AGENT_DIR:?BROWSER_AGENT_DIR must be set}"
: "${BREW:?BREW must be set}"
: "${NPM:?NPM must be set}"
: "${NPX:?NPX must be set}"
: "${PLAYWRIGHT_BROWSER:?PLAYWRIGHT_BROWSER must be set}"
: "${TESSERACT_LANG_PACK:?TESSERACT_LANG_PACK must be set}"
: "${OLLAMA_VISION_MODEL:?OLLAMA_VISION_MODEL must be set}"

ssh "${SSH_TARGET}" bash -se <<EOF
set -e
export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:\${PATH}"

PROJECT_DIR="${PROJECT_DIR}"
BROWSER_AGENT_DIR="${BROWSER_AGENT_DIR}"
BREW_BIN="\$(command -v ${BREW} || true)"
if [ -z "\${BREW_BIN}" ]; then
  echo "brew not found on remote host"
  exit 1
fi
if [ ! -d "\${PROJECT_DIR}/\${BROWSER_AGENT_DIR}" ]; then
  echo "browser agent directory not found: \${PROJECT_DIR}/\${BROWSER_AGENT_DIR}"
  exit 1
fi

"\${BREW_BIN}" install ffmpeg whisper-cpp node

if command -v tesseract >/dev/null 2>&1; then
  echo "tesseract already installed: \$(command -v tesseract)"
else
  "\${BREW_BIN}" install tesseract ${TESSERACT_LANG_PACK}
fi

if command -v ollama >/dev/null 2>&1; then
  echo "ollama already installed: \$(command -v ollama)"
else
  "\${BREW_BIN}" install ollama
  "\${BREW_BIN}" services start ollama >/dev/null 2>&1 || true
fi

NPM_BIN="\$(command -v ${NPM} || true)"
NPX_BIN="\$(command -v ${NPX} || true)"
if [ -z "\${NPM_BIN}" ] || [ -z "\${NPX_BIN}" ]; then
  echo "npm or npx not found after installing node"
  exit 1
fi

"\${NPM_BIN}" --prefix "\${PROJECT_DIR}/\${BROWSER_AGENT_DIR}" install
"\${NPX_BIN}" --prefix "\${PROJECT_DIR}/\${BROWSER_AGENT_DIR}" playwright install ${PLAYWRIGHT_BROWSER}

OLLAMA_BIN="\$(command -v ollama || true)"
if [ -z "\${OLLAMA_BIN}" ]; then
  echo "ollama binary still not on PATH; skip vision model pull"
elif ! curl -fsS http://127.0.0.1:11434/api/tags >/dev/null 2>&1; then
  echo "ollama daemon not reachable; pull ${OLLAMA_VISION_MODEL} manually after it starts"
elif "\${OLLAMA_BIN}" list 2>/dev/null | awk 'NR>1 {print \$1}' | grep -qx "${OLLAMA_VISION_MODEL}"; then
  echo "vision model ${OLLAMA_VISION_MODEL} already pulled"
else
  echo "pulling vision model ${OLLAMA_VISION_MODEL}"
  "\${OLLAMA_BIN}" pull ${OLLAMA_VISION_MODEL} || true
fi
EOF
