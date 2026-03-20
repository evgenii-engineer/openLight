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

-include Makefile.local

PI_USER ?= pi
PI_HOST ?= raspberrypi.local
PI_DEST_DIR ?= /home/$(PI_USER)

GOOS ?= linux
GOARCH ?= arm64
CGO_ENABLED ?= 0

.PHONY: build build-rpi build-cli build-rpi-cli init-rpi-config deploy-rpi-config deploy-rpi deploy-rpi-cli deploy-rpi-service deploy-rpi-all deploy-rpi-full deploy-and-smoke-rpi smoke-rpi-cli test test-e2e-ollama clean ollama-up ollama-pull ollama-down docker-build docker-buildx docker-push

OLLAMA_COMPOSE_FILE ?= deployments/docker/ollama-compose.yaml
OLLAMA_ENDPOINT ?= http://127.0.0.1:11434
OLLAMA_MODEL ?= qwen2.5:0.5b

build:
	mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags="-s -w" -o $(OUTPUT) $(PKG)

build-rpi: build

build-cli:
	$(MAKE) build BIN_NAME=$(CLI_BIN_NAME) OUTPUT=$(CLI_OUTPUT) PKG=$(CLI_PKG)

build-rpi-cli: build-cli

init-rpi-config:
	cp -n configs/agent.rpi.ollama.example.yaml configs/agent.rpi.yaml

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

SMOKE_FLAGS ?= -smoke

smoke-rpi-cli:
	ssh $(PI_USER)@$(PI_HOST) '$(PI_DEST_DIR)/$(CLI_BIN_NAME) -config /etc/openlight/agent.yaml $(SMOKE_FLAGS)'

deploy-and-smoke-rpi: deploy-rpi-all deploy-rpi-cli smoke-rpi-cli

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
