# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added
- Architecture document (`ARCHITECTURE.md`, 28 sections).
- Monorepo skeleton: `backend/` (Go 1.22+), `frontend/` (Vue 3 + TS),
  `docs/` (VuePress 2), `deploy/` (Ansible, Caddy, fail2ban, docker,
  systemd).
- Backend: minimal HTTP server with `chi` router, env-driven config
  (`internal/config`), structured logging, healthcheck, metrics stub
  (`internal/obs`).
- Backend: initial SQL migration covering admins, nodes, cores,
  inbound_sets, hosts, host_pools, users, plans, audit_log,
  webhook_endpoints, panel_path_config.
- Frontend: Vue 3 + Pinia + vue-i18n + Vite skeleton, dark theme,
  ru/en locales, dashboard view that probes the panel health endpoint.
- Docker Compose dev stack: PostgreSQL 16, Redis 7, NATS 2.10,
  ClickHouse 24, MinIO, Caddy 2.
- Ansible roles: `bootstrap_node`, `install_agent`, `install_caddy`,
  `install_fail2ban`, `setup_decoy` (Phase 0 stubs).
- systemd units for `aegis-panel` and `aegis-agent` with hardening
  defaults.
- Caddyfile templates for the panel (decoy + secret admin / sub paths)
  and for nodes (decoy + secret proxy path on the standard +
  masquerade ports).
- fail2ban jail + filter for SSH and panel-login brute-force.
- VuePress documentation site (`docs/`) with guide, API reference
  skeleton, admin / developer / internal placeholders.
- AGPL-3.0 license file.
- Top-level `Makefile` orchestrating `make dev`, `build`, `test`,
  `lint`, `docs`, `ansible`, `docker`.
