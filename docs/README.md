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
- [Architecture](./guide/architecture) — the full design document
  (also available at the repo root as `ARCHITECTURE.md`).
- [Getting started](./guide/getting-started) — running the local dev stack.
- [API reference](./api/) — auto-generated from the OpenAPI spec.
- [Admin user guide](./user-guide/admin/) — operator-facing manual.
- [Developer guide](./developer/) — module overview, testing, contributing.

## Project status

| Component | Status |
| --- | --- |
| Architecture (this doc tree) | ✅ Finalised |
| Backend skeleton (Go 1.22+) | 🟡 Phase 0 in progress |
| Frontend skeleton (Vue 3) | 🟡 Phase 0 in progress |
| Local dev environment (docker compose) | 🟡 Phase 0 in progress |
| Core (sing-box provider) | ⏳ Phase 1 |
| Cabinet API (auth, users, plans, hosts) | ⏳ Phase 1 |
| Subscription render (sing-box / Clash / base64) | ⏳ Phase 2 |
| Cascade topology | ⏳ Phase 4 |
| MCP integration | ⏳ Phase 4 |

> This page is generated as part of the local documentation tree and
> is **not** published until the project reaches a public release.
