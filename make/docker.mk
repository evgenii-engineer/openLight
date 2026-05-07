# make/docker.mk
#
# Image build/push and the openlight-compose stack lifecycle.
# LLM (Ollama) compose lives in make/llm.mk.

##@ Docker

.PHONY: docker-build docker-buildx docker-push docker-up docker-down docker-logs

docker-build: ## Build the openlight image for the host platform
	docker build \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		-f $(DOCKERFILE) .

docker-buildx: ## buildx build for $(DOCKER_PLATFORM), loaded into local docker
	docker buildx build \
		--platform $(DOCKER_PLATFORM) \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		--load \
		-f $(DOCKERFILE) .

docker-push: ## Multi-arch buildx build and push to $(DOCKER_IMAGE)
	docker buildx build \
		--platform $(DOCKER_PLATFORMS) \
		--build-arg VERSION=$(DOCKER_TAG) \
		--build-arg REVISION=$(VCS_REF) \
		--build-arg CREATED=$(BUILD_DATE) \
		-t $(IMAGE_REF) \
		--push \
		-f $(DOCKERFILE) .

docker-up: ## Start the bundled openlight docker stack (compose up -d)
	docker compose -f $(OPENLIGHT_COMPOSE_FILE) up -d

docker-down: ## Stop the bundled openlight docker stack
	docker compose -f $(OPENLIGHT_COMPOSE_FILE) down

docker-logs: ## Tail logs from the openlight docker stack
	docker compose -f $(OPENLIGHT_COMPOSE_FILE) logs -f
