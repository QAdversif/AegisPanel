# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.0.1] - 2026-07-13

Pre-alpha skeleton. Architecture v7 is finalised; the code tree is in
place. Nothing is wired up to run end-to-end yet; that is Phase 0 →
Phase 1.

### Added (skeleton)
- Repository skeleton (monorepo: `backend/`, `frontend/`, `docs/`, `deploy/`).
- Backend: Go 1.22+ service skeleton (`chi`, env config, structured
  logging, healthcheck, metrics stub, initial SQL migration).
- Frontend: Vue 3 + TS + Vite admin UI skeleton (Pinia, vue-i18n
  ru/en, dashboard view).
- Docs: VuePress 2 site (local-only, not published yet).
- Dev environment: Docker Compose stack (PostgreSQL 16, Redis 7,
  NATS 2.10, ClickHouse 24, MinIO, Caddy 2).
- Deploy: Ansible roles, Caddyfile templates for panel and node
  (with decoy + masquerade ports), fail2ban jails, systemd units.
- GitHub: workflows (ci, release), dependabot, issue / PR templates,
  community health files (CONTRIBUTING, CODE_OF_CONDUCT, SECURITY).
- Tooling: `tools/scripts/{release,restore,backup,branch-start}.sh`.
- Conventional Commits template (`.gitmessage.txt`).

## [Unreleased]

### Added
- Architecture document (`ARCHITECTURE.md`, 28 sections).
- Backend skeleton (`backend/`).
- Frontend skeleton (`frontend/`).
- Docs skeleton (`docs/`).
- Deploy assets (`deploy/`).
- Top-level `Makefile`, `README.md`, `LICENSE` (AGPL-3.0),
  `CHANGELOG.md`, `.gitignore`, `.editorconfig`.
