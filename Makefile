# openLight — developer infrastructure
#
# This Makefile is intentionally thin: it defines the default goal,
# loads shared variables, then includes one focused module per concern.
# Application logic does NOT live in Make. See ARCHITECTURE.md and
# docs/REGRESSION.md for the actual product surfaces (CLI / Telegram /
# API). Make exists here only for build, deploy, and dev shortcuts.
#
# Layout:
#   make/common.mk   shared variables (BIN_NAME, GOOS, SSH_*, etc.)
#   make/build.mk    go build / fmt / lint / clean
#   make/test.mk     test / smoke-cli / regression / host smoke
#   make/docker.mk   docker build/push and openlight-compose lifecycle
#   make/llm.mk      Ollama lifecycle + OCR / vision deps
#   make/deploy.mk   Raspberry Pi + Mac mini deploys, remote helpers
#   make/dev.mk      run-local, run-agent, host install-* deps
#   make/release.mk  release artifact bundle
#   make/help.mk     `make help`
#
# Personal overrides go in Makefile.local (gitignored).

.DEFAULT_GOAL := help

include make/common.mk
-include Makefile.local

include make/build.mk
include make/test.mk
include make/docker.mk
include make/llm.mk
include make/deploy.mk
include make/dev.mk
include make/release.mk
include make/help.mk
