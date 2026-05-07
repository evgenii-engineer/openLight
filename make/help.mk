# make/help.mk
#
# Self-documenting `make help` target.
#
# Targets advertise themselves with a `## description` comment on the same
# line as the target rule. Section headers come from `##@ Name` comments
# inside any included .mk file. Anything else is ignored.
#
# Pattern lifted from the standard idiom popularised by kubebuilder /
# operator-sdk; tweaked to scan the full $(MAKEFILE_LIST) so module files
# show up too.

.PHONY: help

help: ## Show this help (grouped by section)
	@awk 'BEGIN { \
		FS = ":.*##"; \
		printf "\nopenLight — developer infrastructure\n"; \
		printf "Usage: make <target> [VAR=value ...]\n"; \
	} \
	/^##@/ { \
		printf "\n\033[1m%s\033[0m\n", substr($$0, 5); next; \
	} \
	/^[a-zA-Z0-9_.-]+:.*##/ { \
		printf "  \033[36m%-28s\033[0m %s\n", $$1, $$2; \
	}' $(MAKEFILE_LIST)
	@printf "\n"
