---
title: Architecture
---

# Architecture

The full design lives in [`ARCHITECTURE.md`](https://github.com/QAdversif/AegisPanel/blob/main/ARCHITECTURE.md)
in the repository root. The in-repo version is the source of truth and
should be edited in lockstep with the code.

## Current version

**v8 (2026-07-17) — review-driven fixes.** Major changes:

- **Unified Roadmap (§21)** — single source of truth for the Phase plan.
  Previously the plan was scattered across §10.3.7, §21, and the (now
  archived) `ARCHITECTURE_ADDENDUM_1.md`.
- **§7 Core abstraction** — Xray is now the **production core** (gRPC
  `HandlerService.AddUser/RemoveUser` for dynamic users). sing-box stays as
  **specialty core** for HY2/TUIC-inbounds + dev. New §7.5 **Batched Apply**
  strategy for cores without dynamic users API.
- **§10.1.2 Wildcard `*`** — explicit ban on `wildcard_sni + reality`
  combination. REALITY requires real SNI in dest's `serverNames`.
- **§14.1 Prometheus** — per-user metrics forbidden (cardinality bomb).
  Per-user data in Postgres (Phase 0–2) or ClickHouse (Phase 3+). Prometheus
  keeps only aggregates.
- **§15.1 Cloudflare + mTLS agents** — separate `panel-direct` hostname in
  grey-cloud mode for the agent channel. Cloudflare (free) cannot pass client
  certificates to origin.
- **§17 MCP** — read-only default, dry-run for write, threat model for
  prompt-injection, streamable-HTTP opt-in only.
- **§19.4.4 Node Profile separation** — `reality-direct` vs `caddy-fronted`,
  validator forbids `caddy-fronted + reality` and `wildcard_sni + reality`.
- **§21 (Unified Roadmap)** — what we ship, in what order, with realistic
  solo-team estimates. Cascade, MCP, Decoy, Subscription Profiles, SRH, OCI
  agent+core image all in **Phase 4+**.
- **§26 (Decoy)** — moved to **Future / Phase 4+**. Document kept as design
  reference; secret paths via `panel_path_config` give baseline masking on
  MVP.
- **`ARCHITECTURE_ADDENDUM_1.md` archived** to
  `docs/archive/ARCHITECTURE_ADDENDUM_1.merged-into-v3.md` (its content is
  merged into v3/v8 of the main doc).

For detailed history see §25 of `ARCHITECTURE.md`.

## Sections at a glance

0. Terms
1. Vision and MVP scope
2. Functional requirements
3. Non-functional requirements
4. Architectural principles
5. High-level architecture
6. Panel components
7. Core abstraction (multi-core) + §7.5 Batched Apply
8. Nodes and agents
9. Auto-deployment (BYO Node)
10. Host manager
11. Protocol configuration
12. Users, plans, traffic
13. Cabinet API
14. Monitoring and observability
15. Security + §15.1 Cloudflare mTLS agents
16. Data model
17. MCP integration (read-only default)
18. Technology stack
19. Deployment
20. Scaling
21. **Unified Roadmap** (single source of truth)
22. Added value
23. Open questions
24. Summary
25. History of changes
26. Decoy sites & URL masking (Future / Phase 4+)
27. License and tenancy
28. Repository structure

## Cross-references

- For new architectural decisions, write an ADR in `docs/adr/NNNN-title.md`.
- `ARCHITECTURE.md` is the overview; ADRs are the per-decision records.

> **Note:** the in-repo doc is the canonical source. This page is a
> pointer + a changelog of recent revisions.
