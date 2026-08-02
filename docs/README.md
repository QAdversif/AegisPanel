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
| Backend (Go 1.26+ — panel, agent, BatchedApplier, backups, CLI) | ✅ v0.8.0 |
| Frontend (Vue 3 — dashboard, nodes, users, plans, webhooks, backups) | ✅ v0.8.0 |
| Local dev environment (docker compose) | ✅ v0.5.0 |
| Core (sing-box provider, GitHub-API SHA-256 install) | ✅ v0.5.0 |
| sops+age secrets (`configure_secrets` Ansible role) | ✅ v0.5.0 |
| Backup / restore (`aegis-pg-backup`, `aegis-pg-restore`) | ✅ v0.5.0 |
| Outgoing webhooks (HMAC + retry + DLQ) | ✅ v0.7.0 |
| Operator guide + security policy + quickstart | ✅ v0.5.0 |
| Pre-PR local CI gate (`tools/scripts/pre-pr.sh`) | ✅ v0.5.0 |
| Container wiring for the sops+age secrets file | ✅ v0.5.0 |
| Cosign sign + verify for panel and agent images | ✅ v0.7.0 |
| JSON logs in production (`AEGIS_ENV=production`) | ✅ v0.7.0 |
| Webhook call-site wiring (production event flow) | ✅ v0.7.1 |
| `sops` envelope on `webhook_endpoints.secret` | ✅ v0.7.1 |
| Background worker for webhook retry | ✅ v0.7.1 |
| BatchedApplier real FlushFn + Enqueue (panel→agent pipeline) | ✅ v0.7.2 |
| Composition root (`internal/app.Build`; main.go God-object fix) | ✅ v0.7.2 |
| End-to-end integration test for the panel→agent pipeline | ✅ v0.7.2 |
| Audit log call-site wiring (every mutating service audited) | ✅ v0.8.0 |
| Phase 2 multi-user sing-box render — data model (`internal/credentials` + migration 0019) | ✅ v0.8.0 |
| Phase 2 multi-user sing-box render — multi-user renderer signature | ✅ v0.8.0 |
| Phase 2 multi-user sing-box render — builder + BatchedApplier narrow | ✅ v0.8.0 |
| Phase 2 multi-user sing-box render — per-user subscription render | ✅ v0.8.0 |
| HTTP admin surface for `user_inbound_credentials` (`/api/v1/credentials/`) | ⏳ v0.8.x |
| Inbound-templates work (per-tenant `Params` defaults) | ⏳ v0.8.x+ |
| Cabinet API (extended plans, hosts, decoys) | 🟡 v1.2+ |
| S3-compatible backup storage | 🟡 v1.2+ |
| Cascade topology | ⏳ Phase 4+ |
| MCP integration | ⏳ Phase 4+ |
| Tailwind v4 migration | ⏳ v1.5 |
| Light theme polish | ⏳ v1.5 |

> This page is generated as part of the local documentation
> tree and is **not** published until the project reaches a
> public release.
