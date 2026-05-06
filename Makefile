BIN_NAME ?= openlight-agent
BUILD_DIR ?= build/linux-arm64
OUTPUT ?= $(BUILD_DIR)/$(BIN_NAME)
PKG ?= ./cmd/agent
CLI_BIN_NAME ?= openlight-cli
CLI_OUTPUT ?= $(BUILD_DIR)/$(CLI_BIN_NAME)
CLI_PKG ?= ./cmd/cli
DOCKER_IMAGE ?= ghcr.io/evgenii-engineer/openlight
DOCKER_TAG ?= dev
DOCKERFILE ?= Dockerfile
DOCKER_PLATFORM ?= linux/$(GOARCH)
DOCKER_PLATFORMS ?= linux/amd64,linux/arm64
IMAGE_REF ?= $(DOCKER_IMAGE):$(DOCKER_TAG)
BUILD_DATE ?= $(shell date -u +"%Y-%m-%dT%H:%M:%SZ")
VCS_REF ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BREW ?= brew
NPM ?= npm
NPX ?= npx
BROWSER_AGENT_DIR ?= tools/browser-agent
PLAYWRIGHT_BROWSER ?= chromium

-include Makefile.local

PI_USER ?= pi
PI_HOST ?= raspberrypi.local
PI_DEST_DIR ?= /home/$(PI_USER)
SSH_USER ?= $(shell whoami)
SSH_HOST ?= openlight-mini
SSH_TARGET ?= $(SSH_USER)@$(SSH_HOST)
USER_HOME ?= /Users/$(SSH_USER)
PROJECT_DIR ?= $(USER_HOME)/openLight
RUNTIME_DIR ?= $(USER_HOME)/openlight
CONFIG_LOCAL ?= ./configs/agent.macmini.yaml
CONFIG_REMOTE ?= $(PROJECT_DIR)/agent.yaml
BROWSER_AGENT_REMOTE_DIR ?= $(PROJECT_DIR)/tools/browser-agent
LAUNCH_AGENT ?= $(USER_HOME)/Library/LaunchAgents/dev.openlight.agent.plist
REPO_URL ?= https://github.com/evgenii-engineer/openLight.git

GOOS ?= linux
GOARCH ?= arm64
CGO_ENABLED ?= 0

.PHONY: build build-rpi build-cli build-rpi-cli build-macmini build-macmini-cli init-rpi-config init-macmini-config check-config remote-prepare remote-sync-repo push-config push-browser-agent remote-build remote-install-launchd remote-restart remote-stop remote-status remote-health deploy-rpi-config deploy-rpi deploy-rpi-cli deploy-rpi-service deploy-rpi-all deploy-rpi-full deploy-macmini-agent deploy-macmini-config deploy-macmini deploy-macmini-cli deploy-macmini-service deploy-macmini-all deploy-macmini-full deploy-macmini-deps-host bootstrap-macmini deploy-and-smoke-rpi deploy-and-smoke-rpi-ollama deploy-and-smoke-rpi-openai deploy-and-smoke-macmini deploy-and-smoke-macmini-ollama deploy-and-smoke-macmini-openai smoke-rpi-cli smoke-rpi-cli-ollama smoke-rpi-cli-openai smoke-macmini-cli smoke-macmini-cli-ollama smoke-macmini-cli-openai logs-macmini restart-macmini status-macmini stop-macmini test test-e2e-ollama clean ollama-up ollama-pull ollama-down docker-build docker-buildx docker-push install-macmini-deps install-voice-deps install-browser-deps install-playwright

OLLAMA_COMPOSE_FILE ?= deployments/docker/ollama-compose.yaml
OLLAMA_ENDPOINT ?= http://127.0.0.1:11434
OLLAMA_MODEL ?= qwen2.5:0.5b

build:
	mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags="-s -w" -o $(OUTPUT) $(PKG)

build-rpi: build

build-macmini:
	$(MAKE) build GOOS=darwin GOARCH=arm64 BUILD_DIR=build/darwin-arm64 OUTPUT=build/darwin-arm64/$(BIN_NAME)

build-cli:
	$(MAKE) build BIN_NAME=$(CLI_BIN_NAME) OUTPUT=$(CLI_OUTPUT) PKG=$(CLI_PKG)

build-rpi-cli: build-cli

build-macmini-cli:
	$(MAKE) build BIN_NAME=$(CLI_BIN_NAME) OUTPUT=build/darwin-arm64/$(CLI_BIN_NAME) PKG=$(CLI_PKG) GOOS=darwin GOARCH=arm64 BUILD_DIR=build/darwin-arm64

init-rpi-config:
	cp -n configs/agent.rpi.ollama.example.yaml configs/agent.rpi.yaml

init-macmini-config:
	cp -n configs/agent.macmini.example.yaml configs/agent.macmini.yaml

check-config:
	@test -f "$(CONFIG_LOCAL)" || { echo "missing local config: $(CONFIG_LOCAL)"; exit 1; }

deploy-rpi:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) PI_DEST_DIR=$(PI_DEST_DIR) BIN_NAME=$(BIN_NAME) ./scripts/deploy-rpi.sh

deploy-rpi-cli:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) PI_DEST_DIR=$(PI_DEST_DIR) BIN_NAME=$(CLI_BIN_NAME) PKG=$(CLI_PKG) ./scripts/deploy-rpi.sh

deploy-rpi-service:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) PI_DEST_DIR=$(PI_DEST_DIR) BIN_NAME=$(BIN_NAME) ./scripts/deploy-rpi-service.sh

deploy-rpi-config:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) ./scripts/deploy-rpi-config.sh

deploy-rpi-all: deploy-rpi-config deploy-rpi deploy-rpi-service

deploy-rpi-full: deploy-rpi-all deploy-rpi-cli

remote-prepare:
	@ssh $(SSH_TARGET) 'set -e; \
		mkdir -p "$(USER_HOME)" \
			"$(PROJECT_DIR)" \
			"$(PROJECT_DIR)/data" \
			"$(PROJECT_DIR)/data/browser-artifacts" \
			"$(BROWSER_AGENT_REMOTE_DIR)" \
			"$(RUNTIME_DIR)/bin" \
			"$(RUNTIME_DIR)/data" \
			"$(RUNTIME_DIR)/workspace" \
			"$(RUNTIME_DIR)/scripts" \
			"$(RUNTIME_DIR)/logs" \
			"$(USER_HOME)/Library/LaunchAgents"'

remote-sync-repo:
	@ssh $(SSH_TARGET) 'set -e; \
		if [ ! -d "$(PROJECT_DIR)/.git" ]; then \
			if [ -n "$$(find "$(PROJECT_DIR)" -mindepth 1 -maxdepth 1 -print -quit 2>/dev/null)" ]; then \
				echo "remote project dir exists but is not a git checkout: $(PROJECT_DIR)"; \
				echo "default Mac mini deploy uploads local binaries and helper files instead"; \
				exit 1; \
			fi; \
			git clone "$(REPO_URL)" "$(PROJECT_DIR)"; \
			exit 0; \
		fi; \
		cd "$(PROJECT_DIR)"; \
		if [ -n "$$(git status --porcelain)" ]; then \
			echo "remote repo has uncommitted changes: $(PROJECT_DIR)"; \
			exit 1; \
		fi; \
		git fetch --all --prune; \
		if git show-ref --verify --quiet refs/remotes/origin/main; then \
			branch=main; \
		elif git show-ref --verify --quiet refs/remotes/origin/master; then \
			branch=master; \
		else \
			echo "remote repo has neither origin/main nor origin/master"; \
			exit 1; \
		fi; \
		if git show-ref --verify --quiet "refs/heads/$${branch}"; then \
			git checkout "$${branch}"; \
		else \
			git checkout -b "$${branch}" "origin/$${branch}"; \
		fi; \
		git pull --ff-only origin "$${branch}"'

push-config: check-config remote-prepare
	@ssh $(SSH_TARGET) 'set -e; \
		if [ ! -d "$(PROJECT_DIR)" ]; then \
			echo "remote project dir missing: $(PROJECT_DIR). run make remote-prepare first"; \
			exit 1; \
		fi; \
		mkdir -p "$$(dirname "$(CONFIG_REMOTE)")"; \
		if [ -f "$(CONFIG_REMOTE)" ]; then \
			backup="$(CONFIG_REMOTE).bak.$$(date +%Y%m%d-%H%M%S)"; \
			cp "$(CONFIG_REMOTE)" "$$backup"; \
			chmod 600 "$$backup"; \
		fi'
	@rsync -az "$(CONFIG_LOCAL)" "$(SSH_TARGET):$(CONFIG_REMOTE)"
	@ssh $(SSH_TARGET) 'chmod 600 "$(CONFIG_REMOTE)"'

push-browser-agent: remote-prepare
	@rsync -az \
		tools/browser-agent/index.mjs \
		tools/browser-agent/package.json \
		"$(SSH_TARGET):$(BROWSER_AGENT_REMOTE_DIR)/"

remote-build:
	@ssh $(SSH_TARGET) 'set -e; \
		cd "$(PROJECT_DIR)"; \
		test -f go.mod || { \
			echo "remote source checkout not found: $(PROJECT_DIR)"; \
			echo "use make deploy-macmini to upload local darwin/arm64 binaries instead"; \
			exit 1; \
		}; \
		go mod download; \
		CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$(RUNTIME_DIR)/bin/openlight-agent" ./cmd/agent; \
		CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build -o "$(RUNTIME_DIR)/bin/openlight-cli" ./cmd/cli; \
		test -x "$(RUNTIME_DIR)/bin/openlight-agent"; \
		test -x "$(RUNTIME_DIR)/bin/openlight-cli"'

remote-install-launchd:
	@{ \
		printf '%s\n' '<?xml version="1.0" encoding="UTF-8"?>'; \
		printf '%s\n' '<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">'; \
		printf '%s\n' '<plist version="1.0">'; \
		printf '%s\n' '<dict>'; \
		printf '%s\n' '  <key>Label</key>'; \
		printf '%s\n' '  <string>dev.openlight.agent</string>'; \
		printf '%s\n' '  <key>ProgramArguments</key>'; \
		printf '%s\n' '  <array>'; \
		printf '%s\n' '    <string>$(RUNTIME_DIR)/bin/openlight-agent</string>'; \
		printf '%s\n' '    <string>-config</string>'; \
		printf '%s\n' '    <string>$(CONFIG_REMOTE)</string>'; \
		printf '%s\n' '  </array>'; \
		printf '%s\n' '  <key>WorkingDirectory</key>'; \
		printf '%s\n' '  <string>$(PROJECT_DIR)</string>'; \
		printf '%s\n' '  <key>RunAtLoad</key>'; \
		printf '%s\n' '  <true/>'; \
		printf '%s\n' '  <key>KeepAlive</key>'; \
		printf '%s\n' '  <true/>'; \
		printf '%s\n' '  <key>StandardOutPath</key>'; \
		printf '%s\n' '  <string>$(RUNTIME_DIR)/logs/agent.out.log</string>'; \
		printf '%s\n' '  <key>StandardErrorPath</key>'; \
		printf '%s\n' '  <string>$(RUNTIME_DIR)/logs/agent.err.log</string>'; \
		printf '%s\n' '  <key>EnvironmentVariables</key>'; \
		printf '%s\n' '  <dict>'; \
		printf '%s\n' '    <key>PATH</key>'; \
		printf '%s\n' '    <string>/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>'; \
		printf '%s\n' '  </dict>'; \
		printf '%s\n' '</dict>'; \
		printf '%s\n' '</plist>'; \
	} | ssh $(SSH_TARGET) 'cat > "$(LAUNCH_AGENT)" && plutil -lint "$(LAUNCH_AGENT)"'

remote-restart:
	@ssh $(SSH_TARGET) 'set -e; \
		launchctl unload "$(LAUNCH_AGENT)" 2>/dev/null || true; \
		launchctl load "$(LAUNCH_AGENT)"'

remote-stop:
	@ssh $(SSH_TARGET) 'launchctl unload "$(LAUNCH_AGENT)" 2>/dev/null || true'

remote-status:
	@ssh $(SSH_TARGET) 'set +e; \
		echo "launchctl:"; \
		launchctl list | grep openlight || true; \
		echo; \
		echo "processes:"; \
		pgrep -fl openlight-agent || true; \
		echo; \
		echo "stderr (last 40):"; \
		tail -n 40 "$(RUNTIME_DIR)/logs/agent.err.log" 2>/dev/null || true; \
		echo; \
		echo "stdout (last 40):"; \
		tail -n 40 "$(RUNTIME_DIR)/logs/agent.out.log" 2>/dev/null || true; \
		echo; \
		echo "cli status:"; \
		"$(RUNTIME_DIR)/bin/openlight-cli" -config "$(CONFIG_REMOTE)" -exec "status" || true'

remote-health:
	@ssh $(SSH_TARGET) 'set -e; \
		curl -fsS http://127.0.0.1:11434/api/tags >/dev/null; \
		launchctl list | grep -q openlight; \
		pgrep -f openlight-agent >/dev/null; \
		"$(RUNTIME_DIR)/bin/openlight-cli" -config "$(CONFIG_REMOTE)" -exec "status" >/dev/null'

deploy-macmini:
	$(MAKE) check-config
	$(MAKE) remote-prepare
	$(MAKE) push-config
	$(MAKE) push-browser-agent
	$(MAKE) deploy-macmini-agent
	$(MAKE) deploy-macmini-cli
	$(MAKE) remote-install-launchd
	$(MAKE) remote-restart
	$(MAKE) remote-status
	$(MAKE) remote-health

deploy-macmini-agent: remote-prepare
	SSH_USER=$(SSH_USER) SSH_HOST=$(SSH_HOST) SSH_TARGET=$(SSH_TARGET) \
	RUNTIME_DIR=$(RUNTIME_DIR) BIN_NAME=$(BIN_NAME) PKG=$(PKG) \
	./scripts/deploy-macmini.sh

deploy-macmini-config: push-config

deploy-macmini-cli: remote-prepare
	SSH_USER=$(SSH_USER) SSH_HOST=$(SSH_HOST) SSH_TARGET=$(SSH_TARGET) \
	RUNTIME_DIR=$(RUNTIME_DIR) BIN_NAME=$(CLI_BIN_NAME) PKG=$(CLI_PKG) \
	./scripts/deploy-macmini.sh

deploy-macmini-service: remote-install-launchd

deploy-macmini-all: deploy-macmini

deploy-macmini-full: deploy-macmini

deploy-macmini-deps-host: push-browser-agent
	@ssh $(SSH_TARGET) 'set -e; \
		export PATH="/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:$${PATH}"; \
		brew_bin="$$(command -v $(BREW) || true)"; \
		if [ -z "$$brew_bin" ]; then \
			echo "brew not found on remote host"; \
			exit 1; \
		fi; \
		if [ ! -d "$(PROJECT_DIR)/$(BROWSER_AGENT_DIR)" ]; then \
			echo "browser agent directory not found: $(PROJECT_DIR)/$(BROWSER_AGENT_DIR)"; \
			exit 1; \
		fi; \
		"$$brew_bin" install ffmpeg whisper-cpp node; \
		npm_bin="$$(command -v $(NPM) || true)"; \
		npx_bin="$$(command -v $(NPX) || true)"; \
		if [ -z "$$npm_bin" ] || [ -z "$$npx_bin" ]; then \
			echo "npm or npx not found after installing node"; \
			exit 1; \
		fi; \
		"$$npm_bin" --prefix "$(PROJECT_DIR)/$(BROWSER_AGENT_DIR)" install; \
		"$$npx_bin" --prefix "$(PROJECT_DIR)/$(BROWSER_AGENT_DIR)" playwright install $(PLAYWRIGHT_BROWSER)'

bootstrap-macmini:
	$(MAKE) deploy-macmini-deps-host
	$(MAKE) deploy-macmini

SMOKE_FLAGS ?= -smoke
SMOKE_LLM_PROFILE ?=

smoke-rpi-cli:
	ssh $(PI_USER)@$(PI_HOST) '$(if $(strip $(SMOKE_LLM_PROFILE)),LLM_PROFILE=$(SMOKE_LLM_PROFILE) ,)$(PI_DEST_DIR)/$(CLI_BIN_NAME) -config /etc/openlight/agent.yaml $(SMOKE_FLAGS)'

smoke-rpi-cli-ollama:
	$(MAKE) smoke-rpi-cli SMOKE_LLM_PROFILE=ollama

smoke-rpi-cli-openai:
	$(MAKE) smoke-rpi-cli SMOKE_LLM_PROFILE=openai

smoke-macmini-cli:
	@ssh $(SSH_TARGET) '$(if $(strip $(SMOKE_LLM_PROFILE)),LLM_PROFILE=$(SMOKE_LLM_PROFILE) ,)"$(RUNTIME_DIR)/bin/$(CLI_BIN_NAME)" -config "$(CONFIG_REMOTE)" $(SMOKE_FLAGS)'

smoke-macmini-cli-ollama:
	$(MAKE) smoke-macmini-cli SMOKE_LLM_PROFILE=ollama

smoke-macmini-cli-openai:
	$(MAKE) smoke-macmini-cli SMOKE_LLM_PROFILE=openai

deploy-and-smoke-rpi: deploy-rpi-all deploy-rpi-cli smoke-rpi-cli

deploy-and-smoke-rpi-ollama:
	$(MAKE) deploy-and-smoke-rpi SMOKE_LLM_PROFILE=ollama

deploy-and-smoke-rpi-openai:
	$(MAKE) deploy-and-smoke-rpi SMOKE_LLM_PROFILE=openai

deploy-and-smoke-macmini:
	$(MAKE) deploy-macmini
	$(MAKE) smoke-macmini-cli

deploy-and-smoke-macmini-ollama:
	$(MAKE) deploy-and-smoke-macmini SMOKE_LLM_PROFILE=ollama

deploy-and-smoke-macmini-openai:
	$(MAKE) deploy-and-smoke-macmini SMOKE_LLM_PROFILE=openai

test:
	GOCACHE=/tmp/go-build GOSUMDB=off go test ./...

test-e2e-ollama:
	OPENLIGHT_E2E_OLLAMA=1 OPENLIGHT_E2E_OLLAMA_ENDPOINT=$(OLLAMA_ENDPOINT) OPENLIGHT_E2E_OLLAMA_MODEL=$(OLLAMA_MODEL) GOCACHE=/tmp/go-build GOSUMDB=off go test ./internal/core -run 'TestAgentRunPollingEndToEndWithRealOllama' -count=1 -v

ollama-up:
	docker compose -f $(OLLAMA_COMPOSE_FILE) up -d ollama

ollama-pull:
	docker compose -f $(OLLAMA_COMPOSE_FILE) run --rm ollama-pull

ollama-down:
	docker compose -f $(OLLAMA_COMPOSE_FILE) down

install-voice-deps:
	$(BREW) install ffmpeg whisper-cpp

install-browser-deps:
	$(BREW) install node
	$(NPM) --prefix $(BROWSER_AGENT_DIR) install

install-playwright:
	$(NPX) --prefix $(BROWSER_AGENT_DIR) playwright install $(PLAYWRIGHT_BROWSER)

install-macmini-deps: install-voice-deps install-browser-deps install-playwright

logs-macmini:
	@ssh $(SSH_TARGET) 'touch "$(RUNTIME_DIR)/logs/agent.out.log" "$(RUNTIME_DIR)/logs/agent.err.log"; tail -n 40 -f "$(RUNTIME_DIR)/logs/agent.out.log" "$(RUNTIME_DIR)/logs/agent.err.log"'

restart-macmini: remote-restart

status-macmini: remote-status

stop-macmini: remote-stop

docker-build:
	docker build \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		-f $(DOCKERFILE) .

docker-buildx:
	docker buildx build \
		--platform $(DOCKER_PLATFORM) \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		--load \
		-f $(DOCKERFILE) .

docker-push:
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		--push \
		-f $(DOCKERFILE) .

clean:
	rm -rf build
