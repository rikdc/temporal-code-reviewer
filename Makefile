.PHONY: help up down logs test test-curl build clean

# Default provider — override with: make up PROVIDER=openrouter
PROVIDER ?= bedrock

# AWS profile for Bedrock access (matches Midas convention)
AWS_PROFILE ?= tooling-ai-coding-assistant

# Map provider to config file (each has provider-specific model IDs)
ifeq ($(PROVIDER),bedrock)
  CONFIG_FILE := config.bedrock.yaml
else
  CONFIG_FILE := config.yaml
endif

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2}'

build: ## Build the lyon Docker image
	docker compose build lyon

up: build ## Start all services (Temporal + Lyon). Use PROVIDER=bedrock|openrouter
	@echo "Starting with provider: $(PROVIDER)"
ifeq ($(PROVIDER),bedrock)
	@echo "Using AWS_PROFILE=$(AWS_PROFILE)"
	AWS_PROFILE=$(AWS_PROFILE) CONFIG_FILE=$(CONFIG_FILE) docker compose up -d
else
	CONFIG_FILE=$(CONFIG_FILE) docker compose up -d
endif
	@echo ""
	@echo "Waiting for services to be healthy..."
	@docker compose exec -T lyon sh -c 'until wget -q --spider http://localhost:8082/health 2>/dev/null; do sleep 1; done' 2>/dev/null || \
		(echo "Waiting for lyon health check..." && sleep 10)
	@echo ""
	@echo "Services ready:"
	@echo "  Dashboard:    http://localhost:8081"
	@echo "  Webhook:      http://localhost:8082"
	@echo "  Temporal UI:  http://localhost:8080"
	@echo ""
	@echo "Test with:  make test-curl"

down: ## Stop all services
	docker compose down

logs: ## Tail lyon service logs
	docker compose logs -f lyon

test: ## Run Go unit tests
	go test ./...

test-curl: ## Send a test PR webhook to trigger a review workflow
	@echo "Triggering PR review workflow..."
	@curl -s -X POST http://localhost:8082/webhook/pr \
		-H "Content-Type: application/json" \
		-d '{ \
			"action": "opened", \
			"number": 1, \
			"repository": { \
				"name": "temporal-code-reviewer", \
				"owner": { "login": "rikdc" } \
			}, \
			"pull_request": { \
				"number": 1, \
				"title": "test: validate bedrock provider integration", \
				"diff_url": "https://github.com/rikdc/temporal-code-reviewer/pull/1.diff", \
				"head": { "ref": "test-branch", "sha": "abc1234" }, \
				"base": { "ref": "main", "sha": "def5678" } \
			} \
		}' | python3 -m json.tool
	@echo ""
	@echo "Check progress at: http://localhost:8081"
	@echo "Temporal UI at:    http://localhost:8080"

clean: ## Remove volumes and rebuild
	docker compose down -v
	docker compose build --no-cache lyon
