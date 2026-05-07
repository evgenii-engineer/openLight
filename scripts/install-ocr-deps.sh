#!/usr/bin/env bash
# OCR (Tesseract) for the ocr_extract skill and visual-watch keyword
# matching. tesseract-lang bundles multilingual data; override
# TESSERACT_LANG_PACK to install just specific languages
# (e.g. tesseract-lang-eng-rus). Skips installation when the binary
# is already on PATH (e.g. installed via the desktop app or a system
# package), so re-runs on configured machines stay no-ops.
set -euo pipefail

BREW="${BREW:-brew}"
TESSERACT_LANG_PACK="${TESSERACT_LANG_PACK:-tesseract-lang}"

export PATH="/opt/homebrew/bin:/usr/local/bin:${PATH}"

if command -v tesseract >/dev/null 2>&1; then
  echo "tesseract already installed: $(command -v tesseract)"
  exit 0
fi

"${BREW}" install tesseract "${TESSERACT_LANG_PACK}"
