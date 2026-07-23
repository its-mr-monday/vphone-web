# vphone-web — top-level build.
#
# Dev:  `make dev`   → Go API (hot reload via air) on :8080, Vite (HMR) on :5173.
#                      Open http://localhost:8080 — the Go server reverse-proxies
#                      non-API routes to Vite so it's a single origin.
# Prod: `make build` → vite build → embed into the Go binary → single binary.

BINARY      := bin/vphone-web
PKG         := ./cmd/vphone-web
WEB         := web
GO          := go
VITE_ORIGIN := http://localhost:5173

.DEFAULT_GOAL := help

## ---------------------------------------------------------------------------
## Development
## ---------------------------------------------------------------------------

.PHONY: dev
dev: web-deps ## Run API (hot reload) + Vite dev server together
	@echo ">> starting dev: Go :8080 (proxy → Vite :5173), Vite :5173"
	@trap 'kill 0' EXIT INT TERM; \
		$(MAKE) --no-print-directory dev-web & \
		$(MAKE) --no-print-directory dev-api & \
		wait

.PHONY: dev-api
dev-api: ## Go server only, with hot reload (requires air)
	@command -v air >/dev/null 2>&1 || { \
		echo "!! 'air' not found. Install with: make tools"; \
		echo "   Falling back to 'go run' (no hot reload)."; \
		exec $(GO) run $(PKG) -dev-proxy $(VITE_ORIGIN) -log-level debug; }
	air

.PHONY: dev-web
dev-web: web-deps ## Vite dev server only
	cd $(WEB) && npm run dev

## ---------------------------------------------------------------------------
## Production build
## ---------------------------------------------------------------------------

.PHONY: build
build: web-build ## Build frontend, embed, and compile the Go binary
	@mkdir -p bin
	$(GO) build -o $(BINARY) $(PKG)
	@echo ">> built $(BINARY)"

.PHONY: release
release: web-build ## Optimized, stripped, smaller binary
	@mkdir -p bin
	$(GO) build -trimpath -ldflags "-s -w" -o $(BINARY) $(PKG)
	@echo ">> release binary at $(BINARY)"

.PHONY: run
run: build ## Build and run the production binary
	$(BINARY)

## ---------------------------------------------------------------------------
## Frontend helpers
## ---------------------------------------------------------------------------

.PHONY: web-deps
web-deps: ## Install frontend deps if missing
	@test -d $(WEB)/node_modules || (cd $(WEB) && npm install)

.PHONY: web-build
web-build: web-deps ## Build the frontend into web/dist
	cd $(WEB) && npm run build

## ---------------------------------------------------------------------------
## Utilities
## ---------------------------------------------------------------------------

.PHONY: migrate
migrate: ## Apply DB migrations (also run automatically on server start)
	@echo ">> migrations apply automatically on server startup (internal/db)."
	@echo ">> starting server briefly to run them, then exiting..."
	@$(GO) run $(PKG) -log-level info & pid=$$!; sleep 2; kill $$pid 2>/dev/null || true

.PHONY: tools
tools: ## Install dev tooling (air for hot reload)
	$(GO) install github.com/air-verse/air@latest

## ---------------------------------------------------------------------------
## vphone-cli (git submodule)
## ---------------------------------------------------------------------------

CLI := vphone-cli

.PHONY: cli
cli: ## Build vphone-cli: toolchain + venv (setup_tools) then the signed binary
	@test -f $(CLI)/Makefile || (echo "submodule missing — run: git submodule update --init --recursive"; exit 1)
	$(MAKE) -C $(CLI) setup_tools
	$(MAKE) -C $(CLI) build

.PHONY: cli-build
cli-build: ## Rebuild the vphone-cli Swift binary only (after setup_tools)
	$(MAKE) -C $(CLI) build

.PHONY: lint
lint: ## Lint Go + frontend
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null 2>&1 && golangci-lint run || echo "(golangci-lint not installed; ran go vet only)"
	cd $(WEB) && npm run lint || true

.PHONY: test
test: ## Run Go tests
	$(GO) test ./...

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin $(WEB)/dist/assets tmp
	@echo ">> cleaned (web/dist/index.html placeholder retained for embed)"

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
