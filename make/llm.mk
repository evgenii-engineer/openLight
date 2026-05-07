# make/llm.mk
#
# Local LLM lifecycle (Ollama in Docker) plus the host-side OCR / vision
# dependencies that the LLM-driven skills rely on.
#
# Convention:
#   llm-*           — preferred, provider-agnostic names
#   ollama-*        — kept as aliases (existing scripts and docs use them)

##@ LLM

.PHONY: llm-up llm-down llm-pull llm-status \
        ollama-up ollama-down ollama-pull \
        install-vision-deps install-ocr-deps

llm-up: ## Start the local Ollama stack (docker compose up -d)
	docker compose -f $(OLLAMA_COMPOSE_FILE) up -d ollama

llm-down: ## Stop the local Ollama stack
	docker compose -f $(OLLAMA_COMPOSE_FILE) down

llm-pull: ## Pull the configured LLM model into the running Ollama
	docker compose -f $(OLLAMA_COMPOSE_FILE) run --rm ollama-pull

llm-status: ## Probe the local Ollama endpoint and list installed models
	@echo "endpoint: $(OLLAMA_ENDPOINT)"
	@curl -fsS $(OLLAMA_ENDPOINT)/api/tags || { echo "ollama not reachable"; exit 1; }

# ---- Legacy aliases ------------------------------------------------------
ollama-up:   llm-up
ollama-down: llm-down
ollama-pull: llm-pull

# ---- Host LLM-adjacent deps ---------------------------------------------

install-vision-deps: ## Install Ollama + small VLM for vision_* skills
	./scripts/install-vision-deps.sh

install-ocr-deps: ## Install Tesseract + language pack for ocr_extract
	./scripts/install-ocr-deps.sh
