# AegisPanel — Roadmap

> **Living document.** Updated each milestone as the d-refactor / v0.x / v1.0
> ladder progresses. The next unreleased tag is at the top; completed tags
> are listed in `CHANGELOG.md` (per-release log) and `docs/adr/`
> (architecturally significant decisions).

## Status (2026-07-26)

| Tag                  | Scope                                                                                                  | Status               |
| ---                  | ---                                                                                                    | ---                  |
| `v0.1.0-mvp-render`  | Public-facing subscription endpoint, render orchestrator, base64 + singbox + Clash + HTML            | ✅ shipped (#50–58)  |
| `v0.2.0-mvp-agent`   | `aegis-agent` (Go binary) on every node, real `apply` writes, reload                                       | ✅ shipped (#59–66)  |
| `v0.3.0-mvp-byo-node` | BYO-node bootstrap (SSH + TOFU + provisioning)                                                          | ✅ shipped (#67, #74–77, #82–84, #87) |
| `v0.4.0-mvp-batched` | `BatchedApplier` + real apply transport + `install_singbox` Ansible role                                  | ✅ shipped (#92, #93, #94) |
| `v0.4.0-d.1`         | `internal/users` data layer (post-d.0 split; precedes the Path C consolidation)                          | ✅ shipped (#95)      |
| `v0.4.0-d.r1`        | `users.User` wire-format compat with `subscription.User`                                                  | ✅ shipped (#96)      |
| `v0.4.0-d.r2`        | Drop subscription-side Store / MemoryStore / PgStore user-CRUD (Path C step 2)                          | ✅ shipped (#97)      |
| `v0.4.0-d.r3`        | Move `admin_handler.go` to `users`; drop Service-level thin wrappers (Path C step 3)                   | ✅ shipped (#99)      |
| `v0.4.0-d.r4`        | Cleanup pass + roadmap (this doc) — Path C step 4                                                       | 🔄 this PR           |
| `v0.4.0`             | Tag for the d-r-series; aggregate of #95–#99 + this PR                                                  | ⏳ tag after merge   |
| `v0.5.0`             | Polish: smoke on fresh VM, backup/restore, JSON logs, quickstart doc, GPG-verify sing-box, GitHub API SHA-256 | ⏳ next |
| `v0.6.0`             | `internal/plans` (table already exists in migration 0001)                                                | ⏳                   |
| `v0.7.0`             | `internal/webhooks` (table already exists in migration 0001)                                             | ⏳                   |
| `v1.0.0-mvp-soft-launch` | GA tag — minimum surface for the public release                                                       | ⏳                   |

## Path C: v0.4.0-d consolidation

The d.1 PR (#95) shipped a `users` package that duplicated
`subscription`'s user-CRUD. Path C is the consolidation:

1. **d.1 (#95)**: `internal/users` data layer — landed.
2. **d.r1 (#96)**: `users.User` wire-format compat with
   `subscription.User` (snake_case JSON, `[]uuid.UUID` for
   hosts allow/block lists). Made the move possible without
   render-code churn. — landed.
3. **d.r2 (#97)**: Drop subscription-side Store / MemoryStore /
   PgStore user-CRUD. Type-alias `User = users.User` makes
   the render code (`render.go` / `render_singbox.go` /
   `render_clash.go` / `render_vars.go`) a no-op compile
   change. Service keeps 4 thin wrappers for the admin +
   render paths. — landed.
4. **d.r3 (#99)**: Move `admin_handler.go` to
   `internal/users/admin_handler.go`. Drop the 4 thin
   wrappers from `subscription.Service`. Render handler
   consults `*users.Service` directly for the
   `sub_token`-→-user lookup. — landed.
5. **d.r4 (this PR)**: Cleanup pass + this roadmap.
   `DefaultSubTokenRotationGrace` is now a public constant
   on `users.Service` (was a test-internal re-export).
   Subscription package doc trimmed of the d.r2
   "AEGIS_USERS_BACKEND" reference. — this PR.

**Net Path C diff:** `internal/subscription` shed ~600 lines
(Store / MemoryStore / PgStore user-CRUD + Service thin
wrappers + admin handler); `internal/users` gained ~900
lines (d.1 + d.r1 + d.r2 + d.r3 + d.r4 in this PR). The
subscription package is now a pure render orchestrator.

## v0.5.0 — polish before v0.6.0+

Scope is the "operations-grade" feature set the panel
needs to be deployable for the soft launch:

- **Smoke test on fresh VM** — bootstrap path
  end-to-end (panel install, at least one node provision,
  at least one user subscription). Run against a
  throwaway VM in CI; capture the boot log.
- **Backup / restore** — `pg_dump` + `aegis-pg-restore`
  binary; tested with a cron schedule (`aegis-cron`).
- **JSON logs** — production flag (`AEGIS_ENV=production`)
  switches zerolog from `ConsoleWriter` to the JSON output.
  Per the operator-side `journalctl` expectations.
- **Quickstart doc** — `docs/guide/quickstart.md` covers
  the operator path: panel install → first admin user →
  first node → first user → first subscription fetch.
- **GPG-verify sing-box tarballs** — sing-box does not
  publish SHA-256 sidecars; the install_singbox role
  hard-codes the digest. Add a `gpg --verify` step
  (key from sing-box's published release key) so a
  compromised mirror cannot ship a backdoored binary.
- **GitHub API SHA-256 fetch** — replace the hard-coded
  digest with a runtime fetch from
  `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v1.14.0-beta.2`,
  plus digest verification. The current hard-coded digest
  is a v0.4.0-c + v0.5.0 transitional state.

## v0.6.0 — `internal/plans`

The `plans` table is in migration 0001; the package
exists as a `doc.go` stub (#77). The v0.6.0 work:

- `plans.Store` interface (MemoryStore + PgStore).
- `plans.Service` with input validation, list / get /
  create / update / delete.
- Admin handler at `plans.AdminRouter(plansSvc, auth)`.
- Route mount: `r.Mount("/plans", plans.AdminRouter(...))`
  with `auth.RequireScope(auth.ScopePlans)`.
- Wire format: `plans.Plan` JSON DTO with the same
  field shape as the table.
- Integration tests (pgx path) + the existing
  MemoryStore pattern.
- Optional UI: the admin frontend (Phase 2) will
  surface the plans list. Out of scope for the API work.

## v0.7.0 — `internal/webhooks`

Same shape as plans: the `webhooks` table is in
migration 0001, the `doc.go` stub is in #77. The
v0.7.0 work:

- `webhooks.Store` (MemoryStore + PgStore).
- `webhooks.Service` with delivery (HTTP POST to the
  configured URL) + retry (exponential backoff,
  max 5 retries).
- Admin handler at `webhooks.AdminRouter(webhooksSvc, auth)`.
- Event-emission hook: when an event package
  (post-v0.6.0 / v0.8.0 `events` package) fires, the
  webhooks service delivers to all enabled subscriptions
  matching the event type.
- HMAC signature on the payload (operator-configured
  secret on the webhook row; SHA-256 in the
  `X-Aegis-Signature` header) so the receiver can
  verify the source.

## v1.0.0-mvp-soft-launch

The minimum surface for the public release:

- The v0.4.0-mvp-batched end-to-end flow (panel →
  aegis-agent → sing-box config write → reload) on at
  least one node with at least one user.
- The v0.4.0-d Path C consolidation (this PR plus
  #99, #97, #96, and #95).
- A `docs/operator-guide.md` for the soft launch
  operators.
- A `SECURITY.md` with the disclosure policy and the
  GPG-verify path for sing-box.
- The four empty packages that are NOT in the v1.0
  cut: `cabinet`, `caddy`, `cascades`, `decoy`,
  `events`, `mcp`, `notifications`, `plans` (v0.6.0),
  `stats`, `subscriptions-plural`, `webhooks` (v0.7.0).
  v1.0 ships without them; they land post-v1.0 in
  named v1.x releases.

## Open gaps (post-v0.4.0 audit)

11 packages are `doc.go`-only and un-wired (per the
v0.4.0-d audit). Of these, `plans` and `webhooks` are
on the v0.6.0 / v0.7.0 path; the remaining 9 are
post-v1.0:

- `cabinet` — end-user self-service UI (sub-token
  rotation, traffic stats, plan view). v1.2+ target.
- `caddy` — Caddy reverse-proxy admin API integration.
  Not a v1.0 dependency (Caddy runs out-of-band).
- `cascades` — the existing `BatchedApplier` already
  has cancel/replace semantics; cascades is the
  multi-user delta. v1.1+ target.
- `decoy` — decoy site content storage. v1.0 ships with
  the decoy-site static content as a single tarball
  (no admin UI); the v1.1+ work is a per-decoy CRUD
  surface.
- `events` — internal event bus. v0.7.0 (webhooks)
  has the minimum event-emission hook; the full
  events package lands v1.1+ with the audit log
  shape.
- `mcp` — Model Context Protocol server. v1.2+ target.
  Out of scope for the soft launch.
- `notifications` — outbound notification channels
  (Telegram, email, Discord). v1.2+ target.
- `stats` — per-user traffic stats (the
  `traffic_used_bytes` column is updated by the
  agent on every Apply). v1.1+ target.
- `subscriptions-plural` — the external Squads UI
  (the multi-tenant surface). v1.3+ target.

## Tagging policy

- v0.x.y tags land in `git tag -a v0.x.y -m "..."`
  on `main` after the milestone PR merges.
- The tag message includes the per-PR list (e.g.
  for v0.4.0-d: #95, #96, #97, #99, this PR).
- `CHANGELOG.md` is updated with the per-PR summary
  in the same merge as the tag.
- v1.0.0-mvp-soft-launch is the only tag in the v1.0
  range; v1.0.x patch releases are bugfixes only,
  no new features.
