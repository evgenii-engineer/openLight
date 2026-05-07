#!/usr/bin/env bash
# Vision (Ollama + a small VLM) for vision_analyze, vision_compare, and
# the visual-watch alert summaries. Skips brew install when an ollama
# binary is already on PATH (the desktop app puts one at
# /opt/homebrew/bin/ollama and brew refuses to link over it). The pull
# step is also skipped when the model already shows up in `ollama list`,
# so re-running this script after the first machine setup is a no-op.
set -euo pipefail

BREW="${BREW:-brew}"
OLLAMA_VISION_MODEL="${OLLAMA_VISION_MODEL:-qwen2.5vl:3b}"
OLLAMA_ENDPOINT="${OLLAMA_ENDPOINT:-http://127.0.0.1:11434}"

export PATH="/opt/homebrew/bin:/usr/local/bin:${PATH}"

if command -v ollama >/dev/null 2>&1; then
  echo "ollama already installed: $(command -v ollama)"
else
  "${BREW}" install ollama
fi

if ! command -v ollama >/dev/null 2>&1; then
  echo "ollama binary still not on PATH; cannot pull ${OLLAMA_VISION_MODEL}"
  exit 0
fi

if ! curl -fsS "${OLLAMA_ENDPOINT}/api/tags" >/dev/null 2>&1; then
  echo "ollama daemon not reachable at ${OLLAMA_ENDPOINT}"
  echo "start it (Ollama.app, 'brew services start ollama', or 'ollama serve')"
  echo "then run: ollama pull ${OLLAMA_VISION_MODEL}"
  exit 0
fi

if ollama list 2>/dev/null | awk 'NR>1 {print $1}' | grep -qx "${OLLAMA_VISION_MODEL}"; then
  echo "vision model ${OLLAMA_VISION_MODEL} already pulled"
  exit 0
fi

echo "pulling vision model ${OLLAMA_VISION_MODEL}"
ollama pull "${OLLAMA_VISION_MODEL}"
