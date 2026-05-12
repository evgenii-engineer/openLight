# make/deploy.mk
#
# Deployment to real hosts: Raspberry Pi (systemd) and Mac mini (launchd).
#
# All non-trivial logic lives in scripts/ — this file only stitches
# steps together with environment variables exported from common.mk.

##@ Deploy: Config

.PHONY: init-rpi-config init-macmini-config check-config

init-rpi-config: ## Seed configs/agent.rpi.yaml from the Ollama example
	cp -n configs/agent.rpi.ollama.example.yaml configs/agent.rpi.yaml

init-macmini-config: ## Seed configs/agent.macmini.yaml from the example
	cp -n configs/agent.macmini.example.yaml configs/agent.macmini.yaml

check-config: ## Fail if $(CONFIG_LOCAL) is missing
	@test -f "$(CONFIG_LOCAL)" || { echo "missing local config: $(CONFIG_LOCAL)"; exit 1; }

##@ Deploy: Raspberry Pi

.PHONY: deploy-rpi deploy-rpi-service deploy-rpi-config \
        deploy-rpi-all deploy-rpi-full deploy-pi

# openlight is a single binary: `openlight agent`, `openlight cli`,
# `openlight doctor`. There is no separate CLI binary to deploy, so the
# old deploy-rpi-cli / deploy-macmini-cli targets are gone.

deploy-rpi: ## Build + ship the openlight binary to the Pi
	BIN_NAME=$(BIN_NAME) PKG=$(PKG) ./scripts/deploy-rpi.sh

deploy-rpi-service: ## Install + restart the systemd unit on the Pi
	BIN_NAME=$(BIN_NAME) ./scripts/deploy-rpi-service.sh

deploy-rpi-config: ## Push the local Pi config to the Pi
	./scripts/deploy-rpi-config.sh

deploy-rpi-all: deploy-rpi-config deploy-rpi deploy-rpi-service ## config + binary + service

deploy-rpi-full: deploy-rpi-all ## Alias for deploy-rpi-all (the CLI ships in the same binary)

# Preferred conventional alias.
deploy-pi: deploy-rpi-full ## Alias for deploy-rpi-full

##@ Deploy: Mac mini

.PHONY: deploy-macmini deploy-macmini-agent deploy-macmini-config \
        deploy-macmini-service \
        deploy-macmini-all deploy-macmini-full \
        deploy-macmini-deps-host bootstrap-macmini

deploy-macmini: ## Full Mac mini deploy (config + binary + service + health)
	$(MAKE) check-config
	$(MAKE) remote-prepare
	$(MAKE) push-config
	$(MAKE) push-browser-agent
	$(MAKE) deploy-macmini-agent
	$(MAKE) remote-install-launchd
	$(MAKE) remote-restart
	$(MAKE) remote-status
	$(MAKE) remote-health

deploy-macmini-agent: remote-prepare ## Build + ship the openlight binary to the Mac mini
	BIN_NAME=$(BIN_NAME) PKG=$(PKG) ./scripts/deploy-macmini.sh

deploy-macmini-config: push-config ## Push the local Mac mini config to the host

deploy-macmini-service: remote-install-launchd ## Install the LaunchAgent plist on the Mac mini

deploy-macmini-all: deploy-macmini ## Alias for deploy-macmini
deploy-macmini-full: deploy-macmini ## Alias for deploy-macmini

deploy-macmini-deps-host: push-browser-agent ## Install host deps (brew, npm, ollama) on the Mac mini
	./scripts/macmini-deps-host.sh

bootstrap-macmini: ## First-time setup: deps + full deploy
	$(MAKE) deploy-macmini-deps-host
	$(MAKE) deploy-macmini

##@ Deploy: Generic agent

.PHONY: deploy-agent

# `deploy-agent` is the conventional name. Without an explicit target host,
# default to the Mac mini path because that is what the project normally
# auto-deploys against. Override with `make deploy-agent TARGET=rpi` to
# pick the Pi flow instead.
TARGET ?= macmini

deploy-agent: ## Deploy the agent to TARGET (macmini|rpi). Default: macmini
ifeq ($(TARGET),macmini)
	$(MAKE) deploy-macmini
else ifeq ($(TARGET),rpi)
	$(MAKE) deploy-rpi-full
else ifeq ($(TARGET),pi)
	$(MAKE) deploy-rpi-full
else
	$(error unknown TARGET=$(TARGET); expected macmini|rpi|pi)
endif

##@ Deploy: Remote helpers

.PHONY: remote-prepare remote-sync-repo remote-build \
        push-config push-browser-agent \
        remote-install-launchd remote-restart remote-stop \
        remote-status remote-health

remote-prepare: ## Create the remote directory tree on $(SSH_HOST)
	@./scripts/remote-prepare.sh

remote-sync-repo: ## git clone or fast-forward the remote checkout
	@./scripts/remote-sync-repo.sh

remote-build: ## Build agent + CLI from the remote checkout
	@./scripts/remote-build.sh

push-config: check-config remote-prepare ## Upload $(CONFIG_LOCAL) to $(CONFIG_REMOTE)
	@./scripts/push-config.sh

push-browser-agent: remote-prepare ## Sync tools/browser-agent to the Mac mini
	@./scripts/push-browser-agent.sh

remote-install-launchd: ## Generate + install the per-user LaunchAgent plist
	@./scripts/remote-install-launchd.sh

remote-restart: ## launchctl unload + load on the Mac mini
	@./scripts/remote-launchctl.sh restart

remote-stop: ## launchctl unload on the Mac mini
	@./scripts/remote-launchctl.sh stop

remote-status: ## Print launchd / process / log status on the Mac mini
	@./scripts/remote-status.sh

remote-health: ## Hard health check on the Mac mini (Ollama + agent)
	@./scripts/remote-health.sh

##@ Deploy: Mac mini convenience

.PHONY: logs-macmini logs-macmini-follow restart-macmini status-macmini stop-macmini

# One-shot snapshot of the last $(LINES) lines.
# Override with `make logs-macmini LINES=500`.
# Defaults to agent.out.log only; pass `WITH_ERR=1` to also tail agent.err.log.
LINES    ?= 200
WITH_ERR ?= 0

logs-macmini: ## Print last $(LINES) lines of agent.out.log (one-shot). Pass WITH_ERR=1 to also include agent.err.log
	@LINES=$(LINES) FOLLOW=0 WITH_ERR=$(WITH_ERR) ./scripts/remote-logs.sh

logs-macmini-follow: ## Stream agent.out.log with tail -f. Pass WITH_ERR=1 to also include agent.err.log
	@LINES=$(LINES) FOLLOW=1 WITH_ERR=$(WITH_ERR) ./scripts/remote-logs.sh

restart-macmini: remote-restart ## Restart the Mac mini agent

status-macmini: remote-status ## Show Mac mini agent status

stop-macmini: remote-stop ## Stop the Mac mini agent

##@ Deploy: smoke

.PHONY: deploy-and-smoke-rpi deploy-and-smoke-rpi-ollama deploy-and-smoke-rpi-openai \
        deploy-and-smoke-macmini deploy-and-smoke-macmini-ollama deploy-and-smoke-macmini-openai

deploy-and-smoke-rpi: deploy-rpi-all smoke-rpi-cli ## Pi: deploy + smoke

deploy-and-smoke-rpi-ollama:
	$(MAKE) deploy-and-smoke-rpi SMOKE_LLM_PROFILE=ollama

deploy-and-smoke-rpi-openai:
	$(MAKE) deploy-and-smoke-rpi SMOKE_LLM_PROFILE=openai

deploy-and-smoke-macmini: ## Mac mini: deploy + smoke
	$(MAKE) deploy-macmini
	$(MAKE) smoke-macmini-cli

deploy-and-smoke-macmini-ollama:
	$(MAKE) deploy-and-smoke-macmini SMOKE_LLM_PROFILE=ollama

deploy-and-smoke-macmini-openai:
	$(MAKE) deploy-and-smoke-macmini SMOKE_LLM_PROFILE=openai
