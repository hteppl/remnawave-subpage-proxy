BINARY      := subpage-proxy
PKG         := ./cmd/subpage-proxy
IMAGE       ?= docker.io/hteppl/remnawave-subpage-proxy
VERSION     ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT      ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo none)
BUILD_DATE  ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
MODULE      := github.com/hteppl/remnawave-subpage-proxy

LDFLAGS := -s -w \
	-X $(MODULE)/internal/version.Version=$(VERSION) \
	-X $(MODULE)/internal/version.Commit=$(COMMIT) \
	-X $(MODULE)/internal/version.Date=$(BUILD_DATE)

# Run the Go toolchain in a container so no local Go install is required.
# Set GO=go to use a local toolchain instead.
GO_IMAGE ?= golang:1.27-alpine3.24
GO       ?= docker run --rm -v "$(CURDIR)":/src -w /src \
              -v subpage-proxy-gomod:/go/pkg/mod \
              -e GOFLAGS=-buildvcs=false $(GO_IMAGE) go

.DEFAULT_GOAL := help

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary into bin/
	$(GO) build -trimpath -ldflags="$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

.PHONY: test
test: ## Run the test suite
	$(GO) test ./...

.PHONY: race
race: ## Run the test suite with the race detector
	docker run --rm -v "$(CURDIR)":/src -w /src -v subpage-proxy-gomod:/go/pkg/mod \
		-e GOFLAGS=-buildvcs=false -e CGO_ENABLED=1 $(GO_IMAGE) \
		sh -c "apk add --no-cache gcc musl-dev >/dev/null && go test -race ./..."

.PHONY: cover
cover: ## Run tests and report coverage per package
	$(GO) test -coverprofile=coverage.out ./...
	$(GO) tool cover -func=coverage.out

.PHONY: vet
vet: ## Run go vet
	$(GO) vet ./...

.PHONY: fmt
fmt: ## Format the source tree
	$(GO) fmt ./...

.PHONY: tidy
tidy: ## Tidy go.mod and go.sum
	$(GO) mod tidy

.PHONY: check
check: fmt vet test ## Format, vet and test

.PHONY: docker
docker: ## Build the container image
	docker build \
		--build-arg VERSION=$(VERSION) \
		--build-arg COMMIT=$(COMMIT) \
		--build-arg BUILD_DATE=$(BUILD_DATE) \
		-t $(IMAGE):$(VERSION) -t $(IMAGE):latest .

COMPOSE_DEV := -f docker-compose.yml -f docker-compose.dev.yml

.PHONY: dev
dev: ## Build from source and start the stack
	docker compose $(COMPOSE_DEV) up -d --build

.PHONY: dev-down
dev-down: ## Stop the dev stack
	docker compose $(COMPOSE_DEV) down

.PHONY: up
up: ## Start the stack
	docker compose up -d

.PHONY: down
down: ## Stop the stack
	docker compose down

.PHONY: logs
logs: ## Follow the proxy logs
	docker compose logs -f remnawave-subpage-proxy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin coverage.out
