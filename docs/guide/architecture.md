---
title: Architecture
---

# Architecture

The full design lives in [`ARCHITECTURE.md`](https://github.com/QAdversif/AegisPanel/blob/main/ARCHITECTURE.md)
in the repository root. The in-repo version is the source of truth and
should be edited in lockstep with the code.

## Current version

**v9.3 (2026-07-31) — post-v0.7.0 sync.** Five
milestone tags landed since v9.2: `v0.4.0` (the
d-r-series plus Path C consolidation), `v0.4.0-post`
(release workflow fixes for PRs 102, 103, 104, 111),
`v0.5.0` (sops+age secrets, backups, pre-PR gate,
container wiring, operator guide, SECURITY, quickstart
in PRs 119 through 126), `v0.6.0` (`internal/plans`
promoted from the migration 0001 stub to a full CRUD
in PRs 131 through 134), and `v0.7.0`
(`internal/webhooks` with HMAC signing, retry, and DLQ
in PRs 136 through 140). v9.3 is a status sync: the
§21 unified roadmap status table is brought in line with
what the repo actually ships; the `v0.4.0` (at
`39d4d9e`), `v0.5.0` (at `d7182a0`), and the
`v0.6.0` / `v0.7.0` tags (pending) are documented. See
the `v9.3` entry in `ARCHITECTURE.md` §25 for the
per-PR detail (the v0.5.0 batch, the v0.6.0 plans
batch, the v0.7.0 webhooks batch, and the post-v0.7.0
4-PR Go+frontend dependency batch in PRs 141 through
144). The doc body itself is unchanged from v9.2; the
§21 / §25 status tables and this "Current version"
note are the only diffs.

**v9.2 (2026-07-23) — roadmap sync + post-v0.3.0 cleanup.**
v9 introduced the "variant A — sing-box only MVP" model
(ADR-0003 supersedes ADR-0001; Xray moves to v2.0+).
v9.1 locked the frontend stack on shadcn-vue,
Reka UI, and TailwindCSS (ADR-0004).
v9.2 is a status sync: the §21 unified roadmap is
rewritten under the v0.x / v1.0 / v2.0+ milestone
ladder, §21 markers are brought in line with what
the repo actually ships, and the
`v0.1.0-mvp-render` (at `5840c13`) and
`v0.2.0-mvp-agent` (at `c2e773c`) tags are
created retroactively. See the `v9.2` entry in
`ARCHITECTURE.md` §25 for the per-PR detail
(PR #74 trivy fix, PR #75 chi v5.2.4 to v5.3.1
plus the `ClientIPFrom*` rewrite, PR #76
eslint --fix, PR #77 11 reserved-package
`doc.go` stubs).

**v9 (2026-07-17, variant A — sing-box only MVP).**
The "Xray as production core" assumption
(ADR-0001) is **cancelled**. ADR-0003
(`sing-box as MVP-only core, Xray at v2.0+`)
takes its place. The §21 unified roadmap is
rewritten under the solo-team timeline
(5–7 weeks to MVP-1.0, vs the v8 25–35 weeks).

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

**Post-v0.4.0 release workflow fixes (2026-07-27).**
Four PRs land on `main` after the `v0.4.0` tag
(commit `39d4d9e`) and stabilise the `release.yml`
contract:

- #102 — lowercase the GHCR image names (the OCI
  spec requires the path portion of the image
  ref to be lowercase; buildx rejected
  `ghcr.io/QAdversif/AegisPanel-ui:v0.4.0`).
- #103 — allow `workflow_dispatch` to actually
  push (the previous gating silently no-op'd
  the registry write on re-runs).
- #104 — use the `tag` input for the UI image
  on `workflow_dispatch` (the previous
  `github.ref_name` resolved to the branch
  name `main`, not the supplied tag).
- #111 — explicit semver tags for the panel
  image (the `metadata-action`'s semver
  extraction only worked on tag-push events;
  on `workflow_dispatch` the panel
  `0.4.0` / `0.4` tags stayed on the
  original digest).

Documented under `[Unreleased]` in
`CHANGELOG.md`; no application code changed,
only `.github/workflows/release.yml`. See
`docs/ROADMAP.md` §"v0.4.0 release workflow
contract" for the consolidated contract.

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
