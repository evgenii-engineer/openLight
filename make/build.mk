# make/build.mk
#
# Compile targets for the openlight binary.
# Default GOOS/GOARCH is Raspberry Pi (linux/arm64); override on the
# command line for other platforms or use the named build-* targets.
#
# openlight is a single binary with subcommands:
#   openlight agent    - run the Telegram bot
#   openlight cli      - run a one-shot or interactive CLI session
#   openlight doctor   - validate config and probe dependencies

##@ Build

.PHONY: build build-rpi build-macmini fmt lint vet tidy clean

build: ## Compile openlight for $(GOOS)/$(GOARCH)
	@mkdir -p $(BUILD_DIR)
	GOOS=$(GOOS) GOARCH=$(GOARCH) CGO_ENABLED=$(CGO_ENABLED) \
		go build $(GO_BUILD_FLAGS) -o $(OUTPUT) $(PKG)

build-rpi: build ## Compile openlight for Raspberry Pi (linux/arm64)

build-macmini: ## Compile openlight for Mac mini (darwin/arm64)
	$(MAKE) build GOOS=darwin GOARCH=arm64 \
		BUILD_DIR=build/darwin-arm64 OUTPUT=build/darwin-arm64/$(BIN_NAME)

fmt: ## Run gofmt on the tree
	gofmt -s -w .

vet: ## Run go vet
	go vet ./...

tidy: ## Run go mod tidy
	go mod tidy

lint: vet ## Run static checks (currently: go vet)

clean: ## Remove build artifacts
	rm -rf build
