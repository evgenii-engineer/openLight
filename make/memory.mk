# make/memory.mk
#
# Long-term memory: the Qdrant container on the Pi and the embedding
# model on the brain node.
#
# The split mirrors the architecture. The Pi owns durable state — the
# RAW archive, SQLite, and the vector index — and the brain owns the
# compute. Neither target reaches into the other's half.
#
# Most of these are escape hatches. openLight provisions itself on first
# start: directories, schema, the embedding model on the brain node, and
# the Qdrant collection. What it cannot do for itself is create a root
# directory under /mnt, install a system package, or start a container —
# that is what install-memory-deps covers.

##@ Memory

.PHONY: install-memory-deps memory-up memory-down memory-restart memory-logs \
        memory-status memory-dirs memory-pull-embeddings memory-reindex \
        memory-test

install-memory-deps: ## Provision memory locally: SSD dirs, pdftotext, Qdrant
	./scripts/install-memory-deps.sh

memory-up: memory-dirs ## Start Qdrant for long-term memory (docker compose up -d)
	docker compose -f $(QDRANT_COMPOSE_FILE) up -d
	@echo "qdrant: rest $(QDRANT_REST_ENDPOINT) / grpc $(QDRANT_GRPC_ENDPOINT)"
	@echo "storage: $(QDRANT_STORAGE)"

memory-down: ## Stop Qdrant (the index on disk is untouched)
	docker compose -f $(QDRANT_COMPOSE_FILE) down

memory-restart: ## Recreate the Qdrant container; verifies the index survives
	docker compose -f $(QDRANT_COMPOSE_FILE) down
	docker compose -f $(QDRANT_COMPOSE_FILE) up -d
	@echo "recreated; collection count should be unchanged:"
	@$(MAKE) --no-print-directory memory-status

memory-logs: ## Tail the Qdrant container log
	docker compose -f $(QDRANT_COMPOSE_FILE) logs -f --tail=100

memory-status: ## Probe Qdrant and list its collections
	@echo "endpoint: $(QDRANT_REST_ENDPOINT)"
	@curl -fsS $(QDRANT_REST_ENDPOINT)/collections || { echo "qdrant not reachable"; exit 1; }
	@echo

memory-dirs: ## Create the SSD directories the memory subsystem needs
	@mkdir -p $(MEMORY_ROOT)/raw $(MEMORY_ROOT)/index $(QDRANT_STORAGE)
	@echo "ready: $(MEMORY_ROOT)"

memory-pull-embeddings: ## Pull the embedding model by hand (the agent does this itself on first start)
	@echo "pulling $(MEMORY_EMBED_MODEL) into $(MEMORY_EMBED_ENDPOINT)"
	@curl -fsS -X POST $(MEMORY_EMBED_ENDPOINT)/api/pull \
		-H 'Content-Type: application/json' \
		-d '{"model":"$(MEMORY_EMBED_MODEL)"}' \
		| tail -1
	@echo

memory-reindex: ## Rebuild the vector index from RAW storage (queues work; the agent drains it)
	$(OPENLIGHT_BIN) memory reindex --all

# The memory unit tests run against an in-memory fake and need nothing.
# This target adds the integration tests, which exercise the real gRPC
# client: point-id encoding, payload round-tripping, filters, and the
# reindex path. They are skipped unless OPENLIGHT_QDRANT_URL is set, so
# `make test` stays container-free.
memory-test: ## Run the memory tests against a live Qdrant (starts one if needed)
	$(MAKE) --no-print-directory memory-up
	@until curl -fsS $(QDRANT_REST_ENDPOINT)/collections >/dev/null 2>&1; do sleep 1; done
	OPENLIGHT_QDRANT_URL=$(QDRANT_GRPC_ENDPOINT) go test -count=1 ./internal/memory/...
