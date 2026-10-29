PROJECT  := dataport
MAIN_PKG := ./cmd/$(PROJECT)

BUILD_DATE   := $(shell date -u '+%Y-%m-%d_%H:%M:%S')
BUILD_COMMIT := $(shell git rev-parse --short HEAD 2> /dev/null || echo unknown)
VERSION      := $(or $(IMAGE_TAG),$(shell git describe --tags --first-parent --match "v*" 2> /dev/null || echo v0.0.0))
LDFLAGS      := -X main.version=$(VERSION) -X main.commit=$(BUILD_COMMIT) -X main.date=$(BUILD_DATE)

# Platform for `make build`, e.g. `make build GOOS=darwin GOARCH=arm64`.
# Sample databases started by `make env`; see env/compose.yaml.
PROFILE ?= all

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
	GOOS=$(GOOS) GOARCH=$(GOARCH) goreleaser build --snapshot --clean --single-target --id $(PROJECT)

.PHONY: build-cgo
build-cgo: build-ui ## Build bin/dataport-cgo with ODBC and DuckDB (needs cgo and unixODBC headers)
	CGO_ENABLED=1 go build -trimpath -tags odbc,duckdb -ldflags="-s -w $(LDFLAGS)" -o bin/$(PROJECT)-cgo $(MAIN_PKG)

.PHONY: build-container
build-container: build-cgo ## Build the container image (cgo build) with the test tag
	docker build --platform=linux/amd64 --build-arg TARGETPLATFORM=. \
		-t $(PROJECT):test -f ci/Dockerfile bin/

.PHONY: build-cluster
build-cluster: build-ui ## Build bin/dataport-linux for the replicas of make env PROFILE=cluster
	CGO_ENABLED=0 GOOS=linux GOARCH=$(shell go env GOARCH) go build -trimpath -ldflags="-s -w $(LDFLAGS)" -o bin/$(PROJECT)-linux $(MAIN_PKG)

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
env: ## Start sample databases; PROFILE picks them, e.g. make env PROFILE="postgres mysql" (default all)
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml $(foreach p,$(PROFILE),--profile $(p)) up -d

.PHONY: env-list
env-list: ## List the profiles make env accepts
	@docker compose --file=env/compose.yaml --profile all config --services | sed 's/^/  /'
	@echo "  all (default), store (postgres for the app database), cluster (3 replicas on :8081-8083, after make build-cluster)"

.PHONY: env-down
env-down: ## Stop every sample database
	docker compose --project-name=$(PROJECT) --file=env/compose.yaml --profile all down --volumes

.PHONY: test
test: ## Run Go tests
	@go test -race ./...

.PHONY: test-cgo
test-cgo: ## Run Go tests with the ODBC and DuckDB drivers
	@CGO_ENABLED=1 go test -race -tags odbc,duckdb ./...

.PHONY: test-integration
test-integration: ## Run tests against the databases of make env, the store on PostgreSQL included
	@DATAPORT_TEST_POSTGRES_DSN='postgres://postgres:pw@127.0.0.1:55432/postgres?sslmode=disable' go test -tags integration ./...

.PHONY: lint
lint: ## Lint Go code and type-check the UI
	@golangci-lint run ./...
	@golangci-lint run --build-tags odbc,duckdb ./...
	@cd _ui && pnpm run check

.PHONY: help
help: ## Show this help
	@grep -h -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-16s\033[0m %s\n", $$1, $$2}'
