---
title: Aegis documentation
---

# Aegis

> **Aegis** is a self-hosted, multi-protocol VPN control panel. The project
> is in pre-alpha: the design is finalised in
> [ARCHITECTURE.md](../ARCHITECTURE.md), the skeleton is being assembled,
> and documentation is being written alongside the code.

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
- [API reference](./api/) — auto-generated from the OpenAPI spec.
- [Admin user guide](./user-guide/admin/) — operator-facing manual.
- [Developer guide](./developer/) — module overview, testing,
  contributing.

## Project status

| Component | Status |
| --- | --- |
| Architecture (this doc tree) | ✅ Finalised |
| Backend (Go 1.22+ — panel, agent, BatchedApplier, backups, CLI) | ✅ v0.5.0 |
| Frontend (Vue 3 — dashboard, nodes, users, backups) | ✅ v0.5.0 |
| Local dev environment (docker compose) | ✅ v0.5.0 |
| Core (sing-box provider, GitHub-API SHA-256 install) | ✅ v0.5.0 |
| sops+age secrets (`configure_secrets` Ansible role) | ✅ v0.5.0 |
| Backup / restore (`aegis-pg-backup`, `aegis-pg-restore`) | ✅ v0.5.0 |
| Operator guide + security policy | ✅ v0.5.0 |
| Pre-PR local CI gate (`tools/scripts/pre-pr.sh`) | ✅ v0.5.0 |
| Cabinet API (extended plans, hosts, decoys) | 🟡 v0.6.0+ |
| S3-compatible backup storage | 🟡 v0.5.x+ |
| Cosign sign + verify for panel images | 🟡 v0.5.x+ |
| Cascade topology | ⏳ Phase 4 |
| MCP integration | ⏳ Phase 4 |

> This page is generated as part of the local documentation tree and
> is **not** published until the project reaches a public release.
