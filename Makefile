BIN_NAME ?= openlight-agent
BUILD_DIR ?= build/linux-arm64
OUTPUT ?= $(BUILD_DIR)/$(BIN_NAME)
PKG ?= ./cmd/agent

-include Makefile.local

PI_USER ?= pi
PI_HOST ?= raspberrypi.local
PI_DEST_DIR ?= /home/$(PI_USER)

GOOS ?= linux
GOARCH ?= arm64
CGO_ENABLED ?= 0

.PHONY: build build-rpi init-rpi-config deploy-rpi-config deploy-rpi deploy-rpi-service deploy-rpi-all test test-e2e-ollama clean ollama-up ollama-pull ollama-down

OLLAMA_COMPOSE_FILE ?= deployments/docker/ollama-compose.yaml
OLLAMA_ENDPOINT ?= http://127.0.0.1:11434
OLLAMA_MODEL ?= qwen2.5:0.5b

build:
	mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) go build -trimpath -ldflags="-s -w" -o $(OUTPUT) $(PKG)

build-rpi: build

init-rpi-config:
	cp -n configs/agent.rpi.ollama.example.yaml configs/agent.rpi.yaml

deploy-rpi:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) PI_DEST_DIR=$(PI_DEST_DIR) BIN_NAME=$(BIN_NAME) ./scripts/deploy-rpi.sh

deploy-rpi-service:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) PI_DEST_DIR=$(PI_DEST_DIR) BIN_NAME=$(BIN_NAME) ./scripts/deploy-rpi-service.sh

deploy-rpi-config:
	PI_USER=$(PI_USER) PI_HOST=$(PI_HOST) ./scripts/deploy-rpi-config.sh

deploy-rpi-all: deploy-rpi-config deploy-rpi deploy-rpi-service

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

clean:
	rm -rf build
