---
title: Aegis documentation
---

# Aegis

> **Aegis** is a self-hosted, multi-protocol VPN control panel.
> The project is in pre-alpha: the design is finalised in
> [ARCHITECTURE.md](../ARCHITECTURE.md) (v9.5), the
> skeleton is being assembled, and documentation is being
> written alongside the code.

## Where to start

- [What is Aegis?](./guide/) — overview, motivation, scope.
- [Quickstart](./guide/quickstart) — 5 minutes from a fresh VPS
  to a panel running.
- [Operator guide](./operator-guide) — the full install +
  daily-ops reference (backups, restores, rotations, upgrades).
- [Security policy](./security) — threat model, supply chain,
  disclosure flow.
- [Architecture](./guide/architecture) — the full design document
  (also available at the repo root as `ARCHITECTURE.md`).
- [Getting started](./guide/getting-started) — running the local
  dev stack on a laptop.
- [API reference](./api/) — auto-generated from the OpenAPI spec
  (still at v0.7.0; v0.7.1, v0.7.2, v0.8.0 did not change the
  API surface).
- [Admin user guide](./user-guide/admin/) — operator-facing manual.
- [Developer guide](./developer/) — module overview, testing,
  contributing.

## Project status

| Component | Status |
| --- | --- |
| Architecture (this doc tree) | ✅ Finalised (v9.5) |
| Backend (Go 1.26+ — panel, agent, BatchedApplier, backups, CLI) | ✅ v0.8.9 |
| Frontend (Vue 3 — dashboard, nodes, users, plans, webhooks, backups, credentials) | ✅ v0.8.9 |
| Local dev environment (docker compose) | ✅ v0.5.0 |
| Core (sing-box provider, GitHub-API SHA-256 install) | ✅ v0.5.0 |
| sops+age secrets (`configure_secrets` Ansible role + decrypt-on-operator pattern) | ✅ v0.5.0 (canonical manual path refined in v0.8.x) |
| Backup / restore (`aegis-pg-backup`, `aegis-pg-restore`) | ✅ v0.5.0 |
| Outgoing webhooks (HMAC + retry + DLQ) | ✅ v0.7.0 |
| Operator guide + security policy + quickstart | ✅ v0.5.0 (v0.8.x refresh) |
| Pre-PR local CI gate (`tools/scripts/pre-pr.sh`) | ✅ v0.5.0 (vet-integration + memory-size checks added 2026-08-06, PR #190) |
| Container wiring for the sops+age secrets file | ✅ v0.5.0 |
| Cosign sign + verify for panel and agent images | ✅ v0.7.0 |
| Cosign re-sign + verify on every release (30s settle + re-sign + verify) | ✅ v0.8.9 |
| JSON logs in production (`AEGIS_ENV=production`) | ✅ v0.7.0 |
| JSON-logs config guard (refuses `development` + any `AEGIS_*_BACKEND=pg`) | ✅ v0.8.6 |
| Webhook call-site wiring (production event flow) | ✅ v0.7.1 |
| `sops` envelope on `webhook_endpoints.secret` | ✅ v0.7.1 |
| Background worker for webhook retry | ✅ v0.7.1 |
| BatchedApplier real FlushFn + Enqueue (panel→agent pipeline) | ✅ v0.7.2 |
| Composition root (`internal/app.Build`; main.go God-object fix) | ✅ v0.7.2 |
| End-to-end integration test for the panel→agent pipeline | ✅ v0.7.2 |
| Audit log call-site wiring (every mutating service audited) | ✅ v0.8.0 |
| Phase 2 multi-user sing-box render end-to-end (data + renderer + builder + subscription) | ✅ v0.8.0 |
| Persistent panel SSH key (ed25519 + sops+age envelope) | ✅ v0.8.1 |
| Three-way auth radio (key / password / stored) on the provision UI | ✅ v0.8.1 |
| Server-side `auth.me` fix on pg backend (closes the v0.8.0 500 on pg) | ✅ v0.8.2 |
| HTTP admin surface for `user_inbound_credentials` (`/api/v1/credentials/`) | ✅ v0.8.2 |
| `aegis admin node rotate-panel-key` CLI (v0.3.0..v0.7.x re-provision) | ✅ v0.8.3 |
| HTTP mirror of the rotate-panel-key CLI | ✅ v0.8.4 |
| "Show stored key" debug surface in NodesView | ✅ v0.8.5 |
| `nodes.Service.RefreshAgentBearer` (operator-side bearer recovery) | ✅ v0.8.7 |
| BatchedApplier 401→auto-refresh integration | ✅ v0.8.8 |
| Host → node mapping in Builder filter | ✅ v0.8.x (PR #192) |
| Subscription URL display in UsersView (admin copy-link UX) | ✅ v0.8.x (PR #193) |
| Inbound-templates work (per-tenant `Params` defaults) | ⏳ v0.8.x+ |
| Merged "Add node + Provision" dialog | ✅ shipped (v0.8.12+) |
| shadcn-vue `RadioGroup` primitive | ✅ shipped (PR #202) |
| Pre-existing eslint warnings cleanup (chore PR) | ⏳ v0.8.x+ |
| Per-user credential filter in Builder (closes the v0.7.x Phase 2 multi-user TODO) | ✅ shipped (v0.8.10+) |
| Cabinet API (extended plans, hosts, decoys) | 🟡 v1.2+ |
| S3-compatible backup storage | 🟡 v1.2+ |
| Cascade topology | ⏳ Phase 4+ |
| MCP integration | ⏳ Phase 4+ |
| Smoke test on fresh VM in CI (terraform + ansible + boot log) | ⏳ v0.9.0 |
| Tailwind v4 migration | ⏳ v1.5 |
| Light theme polish | ⏳ v1.5 |

> This page is generated as part of the local documentation
> tree and is **not** published until the project reaches a
> public release.
