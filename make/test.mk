# make/test.mk
#
# Test layers (see docs/REGRESSION.md):
#   P0 — `make test` and `make smoke-cli`. Fast, deterministic, no host deps.
#   P1 — `make regression`. P0 + extended deterministic checks.
#   P2 — `make smoke-rpi` / `make smoke-macmini`. Real host, opt-in only.

##@ Test

.PHONY: test smoke-cli smoke-darwin regression \
        smoke-rpi smoke-macmini \
        smoke-rpi-cli smoke-rpi-cli-ollama smoke-rpi-cli-openai \
        smoke-macmini-cli smoke-macmini-cli-ollama smoke-macmini-cli-openai

test: ## P0: run the Go unit and integration suite
	GOCACHE=/tmp/go-build GOSUMDB=off go test ./...

smoke-cli: ## P0: deterministic CLI checks against configs/agent.test.yaml
	@mkdir -p ./data
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "skills"
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "watch list"
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "notes"

# smoke-darwin: P0 sanity check that the macOS-native system provider
# (sysctl, vm_stat, top) actually returns sensible values when run on
# a Mac. Does not require Telegram, LLM, or any deploy.
smoke-darwin: ## P0 (macOS): verify /cpu, /memory, /uptime work locally
	@if [ "$$(uname -s)" != "Darwin" ]; then echo "smoke-darwin must run on macOS (got $$(uname -s))"; exit 1; fi
	@mkdir -p ./data
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "/cpu"
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "/memory"
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "/uptime"
	GOCACHE=/tmp/go-build GOSUMDB=off go run ./cmd/openlight cli -config ./configs/agent.test.yaml -exec "/status"

regression: test smoke-cli ## P1: full unit/integration suite plus deterministic CLI smoke

# ---- P2: real-host smoke (opt-in, requires SSH access) -------------------

smoke-rpi: smoke-rpi-cli ## P2: smoke the deployed CLI on the Raspberry Pi
smoke-macmini: smoke-macmini-cli ## P2: smoke the deployed CLI on the Mac mini

smoke-rpi-cli:
	ssh $(PI_USER)@$(PI_HOST) '$(if $(strip $(SMOKE_LLM_PROFILE)),LLM_PROFILE=$(SMOKE_LLM_PROFILE) ,)$(PI_DEST_DIR)/$(BIN_NAME) cli -config /etc/openlight/agent.yaml $(SMOKE_FLAGS)'

smoke-rpi-cli-ollama:
	$(MAKE) smoke-rpi-cli SMOKE_LLM_PROFILE=ollama

smoke-rpi-cli-openai:
	$(MAKE) smoke-rpi-cli SMOKE_LLM_PROFILE=openai

smoke-macmini-cli:
	@ssh $(SSH_TARGET) '$(if $(strip $(SMOKE_LLM_PROFILE)),LLM_PROFILE=$(SMOKE_LLM_PROFILE) ,)"$(RUNTIME_DIR)/bin/$(BIN_NAME)" cli -config "$(CONFIG_REMOTE)" $(SMOKE_FLAGS)'

smoke-macmini-cli-ollama:
	$(MAKE) smoke-macmini-cli SMOKE_LLM_PROFILE=ollama

smoke-macmini-cli-openai:
	$(MAKE) smoke-macmini-cli SMOKE_LLM_PROFILE=openai
