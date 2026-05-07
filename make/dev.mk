# make/dev.mk
#
# Developer shortcuts: run locally, install host-side dependencies for the
# voice / browser skills, and other day-to-day conveniences.

##@ Runtime

.PHONY: run-local run-agent

run-local: ## Run the agent locally via scripts/run-local.sh
	./scripts/run-local.sh

run-agent: ## Run the agent in the foreground (go run ./cmd/agent)
	go run $(PKG)

##@ Dev tooling

.PHONY: install-voice-deps install-browser-deps install-playwright install-macmini-deps

install-voice-deps: ## Install ffmpeg + whisper-cpp via brew
	$(BREW) install ffmpeg whisper-cpp

install-browser-deps: ## Install node + browser-agent npm deps
	$(BREW) install node
	$(NPM) --prefix $(BROWSER_AGENT_DIR) install

install-playwright: ## Install Playwright $(PLAYWRIGHT_BROWSER) for the browser agent
	$(NPX) --prefix $(BROWSER_AGENT_DIR) playwright install $(PLAYWRIGHT_BROWSER)

install-macmini-deps: install-voice-deps install-browser-deps install-playwright install-ocr-deps install-vision-deps ## Bundle of all Mac mini host deps
