PROJECT  := dataport
MAIN_PKG := ./cmd/$(PROJECT)

BUILD_DATE   := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_COMMIT := $(shell git rev-parse --short HEAD 2> /dev/null || echo unknown)
VERSION      := $(or $(IMAGE_TAG),$(shell git describe --tags --first-parent --match "v*" 2> /dev/null || echo v0.0.0))
LDFLAGS      := -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.date=$(BUILD_DATE)

# Platform for `make build`, e.g. `make build GOOS=darwin GOARCH=arm64`.
GOOS   ?= linux
GOARCH ?= amd64

.DEFAULT_GOAL := help

.PHONY: install-ui
install-ui: ## Install UI dependencies
	@echo "> Installing UI dependencies"
	@cd _ui && pnpm install --frozen-lockfile

.PHONY: build-ui
build-ui: install-ui ## Build the UI into internal/server/dist
	@echo "> Building UI assets"
	@cd _ui && pnpm run build

.PHONY: build
build: build-ui ## Build the binary with goreleaser (snapshot, linux/amd64 by default)
	@echo "> Building $(PROJECT) binary with goreleaser"
	GOOS=$(GOOS) GOARCH=$(GOARCH) goreleaser build --snapshot --clean --single-target

.PHONY: build-container
build-container: build ## Build the container image with the test tag
	docker build --platform=$(GOOS)/$(GOARCH) --build-arg TARGETPLATFORM=. \
		-t $(PROJECT):test -f ci/Dockerfile dist/$(PROJECT)_$(GOOS)_$(GOARCH)*/

.PHONY: snapshot
snapshot: build-ui ## Full snapshot release: every archive and both images, nothing published
	goreleaser release --snapshot --clean

.PHONY: run
run: ## Run the backend on :8080 with data in ./data
	go run -ldflags="$(LDFLAGS)" $(MAIN_PKG)

.PHONY: run-ui
run-ui: ## Run the Vite dev server on :3000 (proxies /api to :8080)
	@cd _ui && pnpm run dev

.PHONY: env
env: ## Start sample PostgreSQL, MySQL, SQL Server and ClickHouse databases
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml up -d

.PHONY: env-down
env-down: ## Stop the sample databases
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml down --volumes

.PHONY: test
test: ## Run Go tests
	@go test -race ./...

.PHONY: test-integration
test-integration: ## Run tests against the databases of make env, the store on PostgreSQL included
	@DATAPORT_TEST_POSTGRES_DSN='postgres://postgres:pw@127.0.0.1:55432/postgres?sslmode=disable' go test -tags integration ./...

.PHONY: lint
lint: ## Lint Go code and type-check the UI
	@golangci-lint run ./...
	@cd _ui && pnpm run check

.PHONY: help
help: ## Show this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'
