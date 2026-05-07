# make/build.mk
#
# Compile targets for the agent and CLI binaries.
# Default GOOS/GOARCH is Raspberry Pi (linux/arm64); override on the
# command line for other platforms or use the named build-* targets.

##@ Build

.PHONY: build build-rpi build-cli build-rpi-cli build-macmini build-macmini-cli \
        fmt lint vet tidy clean

build: ## Compile the agent for $(GOOS)/$(GOARCH)
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
		go build $(GO_BUILD_FLAGS) -o $(OUTPUT) $(PKG)

build-rpi: build ## Compile the agent for Raspberry Pi (linux/arm64)

build-macmini: ## Compile the agent for Mac mini (darwin/arm64)
	$(MAKE) build GOOS=darwin GOARCH=arm64 \
		BUILD_DIR=build/darwin-arm64 OUTPUT=build/darwin-arm64/$(BIN_NAME)

build-cli: ## Compile the CLI for $(GOOS)/$(GOARCH)
	$(MAKE) build BIN_NAME=$(CLI_BIN_NAME) OUTPUT=$(CLI_OUTPUT) PKG=$(CLI_PKG)

build-rpi-cli: build-cli ## Compile the CLI for Raspberry Pi

build-macmini-cli: ## Compile the CLI for Mac mini (darwin/arm64)
	$(MAKE) build BIN_NAME=$(CLI_BIN_NAME) PKG=$(CLI_PKG) \
		GOOS=darwin GOARCH=arm64 \
		BUILD_DIR=build/darwin-arm64 \
		OUTPUT=build/darwin-arm64/$(CLI_BIN_NAME)

fmt: ## Run gofmt on the tree
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Run go mod tidy
	go mod tidy

lint: vet ## Run static checks (currently: go vet)

clean: ## Remove build artifacts
	rm -rf build
