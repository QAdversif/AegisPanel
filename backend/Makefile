# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Backend Makefile — Go service targets.
# Run `make help` for a list of available targets.

SHELL := /bin/sh
.SHELLFLAGS := -eu -c

GO         := go
BINARY     := aegis
CMD_DIR    := ./cmd/aegis
PKG        := ./...
COVERAGE   := coverage.out
LINTER_CFG := .golangci.yml

.PHONY: help build run test test-coverage lint fmt tidy clean docker docker-build

help: ## Show this help.
	@printf "Backend Makefile\n\nUsage: make <target>\n\nTargets:\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: ## Compile the binary to ./bin/aegis.
	@mkdir -p bin
	@$(GO) build -trimpath -ldflags "-s -w" -o bin/$(BINARY) $(CMD_DIR)
	@echo "✓ built ./bin/$(BINARY)"

run: ## Run the service in the foreground.
	@$(GO) run $(CMD_DIR)

test: ## Run unit tests.
	@$(GO) test -race -count=1 $(PKG)

test-coverage: ## Run unit tests with coverage report.
	@$(GO) test -race -count=1 -coverprofile=$(COVERAGE) $(PKG)
	@$(GO) tool cover -func=$(COVERAGE) | tail -n 1
	@$(GO) tool cover -html=$(COVERAGE) -o coverage.html
	@echo "✓ coverage report → coverage.html"

lint: ## Run golangci-lint.
	@command -v golangci-lint >/dev/null || { echo "golangci-lint not installed (go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)"; exit 1; }
	@golangci-lint run --config $(LINTER_CFG) $(PKG)

fmt: ## Format Go sources.
	@$(GO) fmt $(PKG)
	@command -v goimports >/dev/null && goimports -w $(PKG) || true

tidy: ## Tidy go.mod / go.sum.
	@$(GO) mod tidy

clean: ## Remove build artefacts.
	@rm -rf bin/ $(COVERAGE) coverage.html

docker: docker-build ## Alias for docker-build.

docker-build: ## Build the panel Docker image.
	@docker build -t aegispanel/aegis:dev -f Dockerfile ..
