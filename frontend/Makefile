# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Frontend Makefile — Vue 3 + Vite targets.

SHELL := /bin/sh
.SHELLFLAGS := -eu -c

PKG_MGR := $(shell command -v pnpm 2>/dev/null || command -v npm 2>/dev/null)

.PHONY: help dev build test test-watch lint lint-fix format type-check clean docker docker-build

help: ## Show this help.
	@printf "Frontend Makefile\n\nUsage: make <target>\n\nTargets:\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m-18s\033[0m %s\n", $$1, $$2}'

dev: ## Run the Vite dev server at http://localhost:5173.
	@$(PKG_MGR) run dev

build: ## Build production bundle to dist/.
	@$(PKG_MGR) run build

test: ## Run Vitest unit tests once.
	@$(PKG_MGR) run test

test-watch: ## Run Vitest in watch mode.
	@$(PKG_MGR) run test:watch

lint: ## Run eslint.
	@$(PKG_MGR) run lint

lint-fix: ## Run eslint --fix.
	@$(PKG_MGR) run lint:fix

format: ## Format with Prettier.
	@$(PKG_MGR) run format

type-check: ## vue-tsc --noEmit.
	@$(PKG_MGR) run type-check

clean: ## Remove dist, coverage, and caches.
	@rm -rf dist coverage .vite .vitest-cache

docker: docker-build ## Alias for docker-build.

docker-build: ## Build the UI production image (served via Caddy).
	@docker build -t QAdversif/AegisPanel-ui:dev -f Dockerfile ..
