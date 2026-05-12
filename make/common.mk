# make/common.mk
#
# Shared variables for the openLight Make build.
# Anything reused across two or more module files lives here so individual
# .mk files stay focused on targets, not configuration.
#
# Override locally by editing Makefile.local (gitignored) or by passing
# `VAR=value` on the command line.

# ---- Go build ------------------------------------------------------------

BIN_NAME      ?= openlight
PKG           ?= ./cmd/openlight

GOOS          ?= linux
GOARCH        ?= arm64
CGO_ENABLED   ?= 0

BUILD_DIR     ?= build/$(GOOS)-$(GOARCH)
OUTPUT        ?= $(BUILD_DIR)/$(BIN_NAME)

GO_LDFLAGS    ?= -s -w
GO_BUILD_FLAGS ?= -trimpath -ldflags="$(GO_LDFLAGS)"

# ---- Versioning ----------------------------------------------------------

BUILD_DATE    ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
VCS_REF       ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)

# ---- Docker --------------------------------------------------------------

DOCKER_IMAGE      ?= ghcr.io/evgenii-engineer/openlight
DOCKER_TAG        ?= dev
DOCKERFILE        ?= Dockerfile
DOCKER_PLATFORM   ?= linux/$(GOARCH)
DOCKER_PLATFORMS  ?= linux/amd64,linux/arm64
IMAGE_REF         ?= $(DOCKER_IMAGE):$(DOCKER_TAG)

OPENLIGHT_COMPOSE_FILE ?= openlight-compose.yaml
OLLAMA_COMPOSE_FILE    ?= deployments/docker/ollama-compose.yaml

# ---- LLM / Ollama --------------------------------------------------------

OLLAMA_ENDPOINT      ?= http://127.0.0.1:11434
OLLAMA_MODEL         ?= qwen2.5:0.5b
OLLAMA_VISION_MODEL  ?= qwen2.5vl:3b

# ---- Host tooling --------------------------------------------------------

BREW                 ?= brew
NPM                  ?= npm
NPX                  ?= npx
BROWSER_AGENT_DIR    ?= tools/browser-agent
PLAYWRIGHT_BROWSER   ?= chromium
TESSERACT_LANG_PACK  ?= tesseract-lang

# ---- Raspberry Pi --------------------------------------------------------

PI_USER       ?= pi
PI_HOST       ?= raspberrypi.local
PI_DEST_DIR   ?= /home/$(PI_USER)
PI_BIN_DIR    ?= $(PI_DEST_DIR)/bin
PI_BIN_PATH   ?= $(PI_BIN_DIR)/$(BIN_NAME)

# ---- Mac mini / generic SSH host -----------------------------------------

SSH_USER      ?= $(shell whoami)
SSH_HOST      ?= openlight-mini
SSH_TARGET    ?= $(SSH_USER)@$(SSH_HOST)
USER_HOME     ?= /Users/$(SSH_USER)
PROJECT_DIR   ?= $(USER_HOME)/openLight
RUNTIME_DIR   ?= $(USER_HOME)/openlight

CONFIG_LOCAL              ?= ./configs/agent.macmini.yaml
CONFIG_REMOTE             ?= $(PROJECT_DIR)/agent.yaml
BROWSER_AGENT_REMOTE_DIR  ?= $(PROJECT_DIR)/tools/browser-agent
LAUNCH_AGENT              ?= $(USER_HOME)/Library/LaunchAgents/dev.openlight.agent.plist
REPO_URL                  ?= https://github.com/evgenii-engineer/openLight.git

# ---- Smoke tests ---------------------------------------------------------

SMOKE_FLAGS        ?= -smoke
SMOKE_LLM_PROFILE  ?=

# ---- Environment exported to scripts -------------------------------------
#
# Scripts under scripts/ read these via the environment. We deliberately
# do NOT export GOOS/GOARCH/CGO_ENABLED here — those are set per-recipe
# at expansion time and exporting them would bleed into unrelated `go`
# commands (e.g. `go run` invoked by `make smoke-cli`).

export OLLAMA_ENDPOINT OLLAMA_VISION_MODEL
export BREW NPM NPX BROWSER_AGENT_DIR PLAYWRIGHT_BROWSER TESSERACT_LANG_PACK
export PI_USER PI_HOST PI_DEST_DIR PI_BIN_DIR PI_BIN_PATH
export SSH_USER SSH_HOST SSH_TARGET USER_HOME PROJECT_DIR RUNTIME_DIR
export CONFIG_LOCAL CONFIG_REMOTE BROWSER_AGENT_REMOTE_DIR LAUNCH_AGENT REPO_URL
