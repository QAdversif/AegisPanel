# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Backend Makefile — Go service targets.
# Run `make help` for a list of available targets.

SHELL := /bin/sh
.SHELLFLAGS := -eu -c

GO         := go
BINARY     := aegis
AGENT      := aegis-agent
CMD_DIR    := ./cmd/aegis
AGENT_DIR  := ./cmd/aegis-agent
PKG        := ./...
COVERAGE   := coverage.out
LINTER_CFG := .golangci.yml
PROTO_ROOT := ../proto
PB_OUT     := internal/agentv1pb

.PHONY: help build build-agent run test test-integration test-coverage lint fmt tidy clean docker docker-build proto proto-check

help: ## Show this help.
	@printf "Backend Makefile\n\nUsage: make <target>\n\nTargets:\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

build: build-agent ## Compile both binaries (alias for now — agent is required for v0.3.0).
	@mkdir -p bin
	@$(GO) build -trimpath -ldflags "-s -w -X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/$(BINARY) $(CMD_DIR)
	@echo "✓ built ./bin/$(BINARY)"

build-agent: ## Compile the per-node agent binary to ./bin/aegis-agent.
	@mkdir -p bin
	@$(GO) build -trimpath -ldflags "-s -w -X main.version=$(shell git describe --tags --always --dirty 2>/dev/null || echo dev)" -o bin/$(AGENT) $(AGENT_DIR)
	@echo "✓ built ./bin/$(AGENT)"

run: ## Run the panel service in the foreground.
	@$(GO) run $(CMD_DIR)

test: ## Run unit tests.
	@$(GO) test -race -count=1 $(PKG)

# Integration tests require a live Postgres. The `testutil.MustNewPool`
# helper skips the suite when INTEGRATION_DATABASE_URL is unset, so
# `make test-integration` is safe to run anywhere; only an empty result
# is reported if no DB is configured.
test-integration: ## Run integration tests (requires INTEGRATION_DATABASE_URL).
	@$(GO) test -race -count=1 -tags=integration $(PKG)

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

docker: docker-build docker-build-agent ## Build both panel + agent Docker images.

docker-build: ## Build the panel Docker image.
	@docker build -t QAdversif/AegisPanel:dev -f Dockerfile ..

docker-build-agent: ## Build the per-node agent Docker image.
	@docker build -t QAdversif/aegis-agent:dev -f cmd/aegis-agent/Dockerfile .

# --- Protobuf / gRPC codegen -----------------------------------------------

# proto regenerates the gRPC stubs from ../proto/aegis/v1/agent.proto.
# Requires `protoc` (the protobuf compiler) + `protoc-gen-go` +
# `protoc-gen-go-grpc` on PATH. The two Go plugins install via:
#
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#
# On Windows, install protoc via winget:
#
#   winget install --id=Google.Protobuf -e --accept-package-agreements \
#                  --accept-source-agreements
#
# The generated files are committed to the repo (see
# docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md §Phases v0.8.29),
# so a clean checkout can build without protoc. The CI installs
# protoc + plugins and runs `make proto-check` to verify the committed
# files match a fresh codegen.
proto: ## Regenerate the gRPC stubs from ../proto (requires protoc + plugins).
	@command -v protoc >/dev/null || { echo "protoc not installed (see Makefile 'proto' target)"; exit 1; }
	@command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go not installed (go install google.golang.org/protobuf/cmd/protoc-gen-go@latest)"; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null || { echo "protoc-gen-go-grpc not installed (go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest)"; exit 1; }
	protoc \
		--proto_path=$(PROTO_ROOT) \
		--go_out=$(PB_OUT) \
		--go_opt=paths=source_relative \
		--go-grpc_out=$(PB_OUT) \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_ROOT)/aegis/v1/agent.proto
	@echo "✓ regenerated $(PB_OUT)/aegis/v1/agent.pb.go + agent_grpc.pb.go"

# proto-check runs the codegen into a scratch directory and diffs
# against the committed files. Used by CI to catch "committed stub is
# out of date with the .proto" mistakes. Locally it is a no-op when
# the stubs are up to date.
proto-check: ## Verify the committed gRPC stubs match a fresh codegen.
	@command -v protoc >/dev/null || { echo "protoc not installed (see Makefile 'proto' target)"; exit 1; }
	@command -v protoc-gen-go >/dev/null || { echo "protoc-gen-go not installed"; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null || { echo "protoc-gen-go-grpc not installed"; exit 1; }
	@mkdir -p .proto-check
	@protoc \
		--proto_path=$(PROTO_ROOT) \
		--go_out=.proto-check \
		--go_opt=paths=source_relative \
		--go-grpc_out=.proto-check \
		--go-grpc_opt=paths=source_relative \
		$(PROTO_ROOT)/aegis/v1/agent.proto
	@diff -ruN $(PB_OUT)/aegis/v1 .proto-check/aegis/v1 > .proto-check/diff.txt 2>&1; \
		if [ -s .proto-check/diff.txt ]; then \
			echo "✗ committed gRPC stubs are out of date with $(PROTO_ROOT)/aegis/v1/agent.proto"; \
			echo "  run 'make proto' and commit the result"; \
			cat .proto-check/diff.txt; \
			exit 1; \
		fi
	@rm -rf .proto-check
	@echo "✓ committed gRPC stubs are up to date"
