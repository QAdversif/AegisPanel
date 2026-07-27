# Known Limitations — AegisPanel v0.4.0

This document tracks the gaps between what
`v0.4.0` ships and the full design in
`ARCHITECTURE.md` §21. Every open entry points
to the milestone that closes it. **Closed**
items are kept for context — the PR that
closed each one is named so future readers can
find the diff.

## v0.4.0 — currently open

### Operations

#### Backup / restore — v0.5.0

The DB schema is straightforward Postgres
but there is no automated backup / restore
flow yet. v0.5.0 ships `pg_dump`-based backup
with rotation + a smoke-tested restore
playbook. Originally planned for v0.2, but
`tools/scripts/backup.sh` was de-prioritised
in favour of the v0.2 handler surfaces.

#### Smoke on a fresh VM — v1.0

The Definition of Done in `ADR-0003` requires
a clean-VM smoke. v1.0 is the first milestone
that lands it. The intermediate v0.x milestones
skip the fresh-VM step because the deploy story
is not yet final.

### Cross-cutting

#### Dependabot majors for the v0.x window

Dependabot PRs 69 (frontend minor+patch) and
73 (zod 3→4) remain open, deferred to v0.5.0.
PR 69 transitively requires TypeScript 5.8+ for
`@vue/tsconfig 0.9.1`; PR 73 needs a
`@vee-validate/zod` zod-4-compatible release.
PRs 70 (vitest 3→4), 71 (vue-router 4→5),
and 72 (eslint 8→10) closed in the v0.3.0
cleanup batch (PRs 82, 84, 83). PR 68 (chi bump
with the `RealIP` deprecation fix) superseded
by PR 75.

#### Light theme polish — v1.5

The light + dark pair ships but the light
theme is unstyled beyond the CSS variable
swap. The Aegis long-term look is a slate
base, but the light variant of the same
base needs a design pass. v1.5.

#### Tailwind v4 migration — v1.5

`tailwindcss@3.4` is the v0.1.0 baseline. v4
ships oxide engine + container queries; the
move is deferred until the rest of the
ecosystem (forms / typography / animate)
publishes v4-compatible releases.

## Closed in v0.4.0

These items are kept here so a reader of
`ARCHITECTURE.md §21 / v0.4.0` can see what
was actually delivered, and so the diff
between v0.3.0 and v0.4.0 is auditable.

| Item | Closed by |
| --- | --- |
| `BatchedApplier` + real apply transport + `install_singbox` Ansible role (panel → aegis-agent → sing-box config write → reload, end-to-end) | PRs #92, #93, #94 (v0.4.0-mvp-batched) |
| `internal/users` data layer (d.1) — User + Status + MemoryStore + PgStore, 32-byte / 64-hex-char `sub_token` | PR #95 (d.1) |
| `users.User` wire-format compat with `subscription.User` (snake_case JSON, `[]uuid.UUID` for hosts) | PR #96 (d.r1) |
| Drop subscription-side user-CRUD (Store / MemoryStore / PgStore / Service-level thin wrappers) | PRs #97, #99 (d.r2, d.r3) |
| Move `admin_handler.go` to `internal/users`; drop the 4 Service thin wrappers | PR #99 (d.r3) |
| `DefaultSubTokenRotationGrace` as a public package constant; `docs/ROADMAP.md` published | PR #100 (d.r4) |
| Release workflow fixes (GHCR lowercase, `workflow_dispatch` push, UI image tag input, explicit panel semver tags) — no application code change | PRs #102, #103, #104, #111 (post-tag) |

## Closed in v0.3.0

| Item | Closed by |
| --- | --- |
| BYO-node bootstrap backend provisioner (PR #67) | v0.3.0-mvp-byo-node |
| "Add node" UI dialog (modal in `NodesView`, status badge, i18n) | v0.3.0-mvp-byo-node |
| Real `aegis-agent` Go binary + Ansible `install_agent` role (replaces the `sleep infinity` placeholder) | v0.3.0-mvp-byo-node |
| Per-node `AgentBearer` storage (`nodes.agent_bearer` column, migration 0013) | v0.3.0-mvp-byo-node |
| chi v5.2.4 → v5.3.1 + `ClientIPFrom*` IP extraction (closes `GHSA-3fxj-6jh8-hvhx`) | PR #75 |
| Trivy workflow `ignorefile:` → `trivyignores:` | PR #74 |
| Frontend `eslint --fix` (171 auto-fixable warnings → 0) | PR #76 |
| 11 reserved-package `doc.go` stubs (cabinet, caddy, cascades, decoy, events, mcp, notifications, plans, stats, subscriptions, webhooks) | PR #77 |
| vitest 3 → 4 | PR #82 |
| eslint 8 → 10 flat config | PR #83 |
| vue-router 4 → 5 + vite 6 → 7 + pinia 2 → 3 | PR #84 |
| `.gitattributes` + `npm ci` standardisation (Windows CRLF fix) | PR #87 |
| vite 7.3.0 → 7.3.6 (6 dependabot advisories, all `Development`-only) | PR #89 |
| `brace-expansion@2 → 5` + `js-yaml@3 → 4` (3 HIGH-severity OSV findings) | PR #90 |
| Custom Caddy binary (drops upstream Caddy `grpc-go` CVE by patching to `v1.82.1`) | PR #91 |

## Closed in v0.2.0

These items are kept here so a reader of
`ARCHITECTURE.md §21 / MVP-0.2` can see what
was actually delivered, and so the diff
between v0.1.0 and v0.2.0 is auditable.

| Item | Closed by |
| --- | --- |
| Per-node inbounds editor | PR #62 (PR-I) |
| Host create / edit dialogs | PR #61 (PR-H) |
| User CRUD | PR #60 (PR-G) |
| Settings UI (panelcfg HTTP) | PR #59 (PR-F) |
| OpenAPI codegen for the TS types | PR #65 (PR-L) |
| Real subscription rate-limiting | PR #64 (PR-K) |
| Argon2id for the admin password (operational gap closed by `aegis admin` CLI; production seed guard) | PR #63 (PR-J) |
| Audit log + operator profile (read surface) | PR #66 (PR-M) |
| Sub-token rotation + URL-prefix rotation | #47 |

## What's NOT a limitation

These are sometimes mistaken for gaps; they
are intentional.

- The default admin password is documented
  in `deploy/ansible/group_vars/all.yml` —
  not a backdoor, just an operator onboarding
  aid.
- The default dark theme is intentional
  (dev-tool aesthetic per `ADR-0004`).
  Light theme is a token swap away.
- Subscriptions render the sing-box format
  by default; Clash / base64 / HTML are
  available via the `?format=` query
  parameter and the `/subscription` view.
