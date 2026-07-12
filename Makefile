# SPDX-License-Identifier: AGPL-3.0-or-later
#
# Aegis monorepo — top-level Makefile.
#
# Drives the Go backend, Vue 3 frontend, VuePress docs, and the local
# dev environment. Sub-Makefiles in `backend/`, `frontend/`, `docs/`
# provide component-specific targets.
#
# Run `make help` for a list of available targets.

SHELL := /bin/sh
.SHELLFLAGS := -eu -c

# --- Variables --------------------------------------------------------------

BACKEND_DIR   := backend
FRONTEND_DIR  := frontend
DOCS_DIR      := docs
DEPLOY_DIR    := deploy

# Tooling detection. Components fall back to "echo" when a tool is missing
# so the help target always works on a fresh checkout.
GO        := $(shell command -v go 2>/dev/null)
NODE      := $(shell command -v node 2>/dev/null)
PNPM      := $(shell command -v pnpm 2>/dev/null)
NPM       := $(shell command -v npm 2>/dev/null)
DOCKER    := $(shell command -v docker 2>/dev/null)
ANSIBLE   := $(shell command -v ansible-playbook 2>/dev/null)

# --- Phony ------------------------------------------------------------------

.PHONY: help dev dev-down build test lint clean docs docs-build \
        backend frontend ansible docker docker-dev docker-prod

# --- Default ----------------------------------------------------------------

help: ## Show this help.
	@printf "Aegis monorepo — top-level Makefile\n\n"
	@printf "Usage: make <target>\n\nTargets:\n"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| sort \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# --- Local dev --------------------------------------------------------------

dev: ## Bring up the full local dev stack (docker compose + tails panel + ui logs).
	@$(MAKE) docker-dev
	@$(MAKE) -C $(BACKEND_DIR) run &
	@$(MAKE) -C $(FRONTEND_DIR) dev &
	@wait

dev-down: ## Stop the dev stack.
	@$(MAKE) docker-dev DOWN=1

# --- Build ------------------------------------------------------------------

build: ## Build all artifacts (backend binary + frontend bundle + docs).
	@$(MAKE) -C $(BACKEND_DIR) build
	@$(MAKE) -C $(FRONTEND_DIR) build

# --- Test -------------------------------------------------------------------

test: ## Run unit + integration tests across the repo.
	@$(MAKE) -C $(BACKEND_DIR) test
	@$(MAKE) -C $(FRONTEND_DIR) test

# --- Lint -------------------------------------------------------------------

lint: ## Lint everything (golangci-lint, eslint, prettier, yamllint, ansible-lint).
	@$(MAKE) -C $(BACKEND_DIR) lint
	@$(MAKE) -C $(FRONTEND_DIR) lint
	@if [ -d $(DOCS_DIR) ] && [ -f $(DOCS_DIR)/package.json ]; then \
		$(MAKE) -C $(DOCS_DIR) lint; \
	fi

# --- Clean ------------------------------------------------------------------

clean: ## Remove build artefacts and dev volumes.
	@$(MAKE) -C $(BACKEND_DIR) clean
	@$(MAKE) -C $(FRONTEND_DIR) clean
	@$(MAKE) -C $(DOCS_DIR) clean 2>/dev/null || true
	@rm -rf $(DEPLOY_DIR)/docker/.volumes 2>/dev/null || true

# --- Component shortcuts ---------------------------------------------------

backend: ## Run the backend only.
	@$(MAKE) -C $(BACKEND_DIR) run

frontend: ## Run the frontend dev server only.
	@$(MAKE) -C $(FRONTEND_DIR) dev

# --- Documentation ---------------------------------------------------------

docs: ## Start the VuePress dev server at http://localhost:8080.
	@$(MAKE) -C $(DOCS_DIR) dev

docs-build: ## Build the VuePress static site to $(DOCS_DIR)/.vuepress/dist.
	@$(MAKE) -C $(DOCS_DIR) build

# --- Ansible ---------------------------------------------------------------

ansible: ## Run the panel provisioning playbook (configure inventory first).
	@cd $(DEPLOY_DIR)/ansible && ansible-playbook -i inventories/local/hosts.ini playbooks/panel.yml

# --- Docker helpers --------------------------------------------------------

docker-dev: ## Bring up the local dev environment (postgres, redis, nats, clickhouse, caddy).
	@cd $(DEPLOY_DIR)/docker && \
		PROJECT=aegis-dev \
		$(MAKE) -f docker-compose.dev.yml up

docker-prod: ## Bring up the production stack (panel, ui, caddy, dependencies).
	@cd $(DEPLOY_DIR)/docker && \
		PROJECT=aegis-prod \
		$(MAKE) -f docker-compose.prod.yml up

docker: docker-dev ## Alias for docker-dev.
