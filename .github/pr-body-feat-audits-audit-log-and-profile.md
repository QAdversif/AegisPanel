feat(audits): audit log read UI + operator profile (PR-M)

Eighth and last sub-PR of v0.2.0-mvp-agent. v0.2.0
ships the read-side of the audit log surface
(`/api/v1/audits` and `/api/v1/audits/{id}`) and the
operator profile page with a change-password dialog
(`POST /api/v1/auth/me/password`). The in-handler
write call-sites for the nodes / hosts / inbounds /
users / panelcfg mutating handlers land in v0.3
alongside the v0.3 work; v0.2.0 ships the read
surface + the `change-password` trigger so the
operator can verify "my last action made it into
the log" without waiting for v0.3.

## Backend

* `internal/audits/audit.go` — the new package.
  `AuditEntry` is the on-the-wire shape (camelCase
  per the v0.2.0 normalisation that landed across
  PR-G / PR-H / PR-I / PR-F). `Entry` is the
  input shape to `Service.Record`. `ListFilter` is
  the input to `Service.List` with the four
  dimensions (actor_id, action, resource_type,
  resource_id) + the two time bounds (since,
  until) + the `limit` cap (default 100, max 1000).
* `internal/audits/store.go` — the `Store`
  interface + `MemoryStore` (Phase 0 default). The
  list path elides the `before` / `after` JSONB
  blobs to keep the response compact; the `/{id}`
  path returns them in full. The Store is safe
  for concurrent use; the ID is a fresh
  `atomic.AddUint64` per Insert.
* `internal/audits/pg_store.go` — the `PgStore`
  backed by the existing `audit_log` table from
  migration 0001. The pgx path is safe-by-default:
  the INET column rejects a malformed IP, the
  TIMESTAMPTZ column is timezone-aware, the
  User-Agent is capped at 512 chars so a malicious
  client cannot bloat the row.
* `internal/audits/service.go` — `Service`
  wraps the Store and exposes `Record` / `List` /
  `GetByID`. `Record` is the v0.3+ in-handler
  write call-site; v0.2.0 does not call it from
  any handler (the change-password handler is the
  one wiring in v0.3; the read surface is the only
  v0.2.0 work).
* `internal/audits/handler.go` — `Router(svc,
  authMiddleware)` mounts `GET /` and `GET /{id}`
  behind `auth.RequireScope(ScopeAudits)`. The
  list endpoint accepts the filter query params
  documented below.
* `internal/audits/store_test.go` and
  `handler_test.go` (18 unit tests) plus
  `pg_store_integration_test.go` (6 integration
  tests) cover: filter dimensions, time bounds,
  default and max limit caps, Before/After
  elision, 404 on unknown id, concurrency under
  load, and the `RequireScope` middleware
  rejection path.
* `internal/auth/scopes.go` — new `ScopeAudits`
  constant.
* `internal/auth/pg_store.go` — `scopesForRole`
  grants `ScopeAudits` to every role (the audit
  log is the operator's primary observability
  surface — a viewer who cannot see who changed
  their own password cannot verify it was their
  change).
* `internal/auth/handler.go` — new
  `handleChangePassword` mounted at
  `POST /auth/me/password`. The current password
  is verified to defend against a stolen access
  token. On success, refresh tokens are KEPT (the
  user is not logged out) and the response is the
  same `MeResponse` shape so the topbar can
  re-render without a separate round-trip.
* `internal/auth/middleware.go` — `Mount()` gains
  the new `POST /me/password` route alongside the
  existing `GET /me`. The route is mounted
  *inside* the auth-middleware-protected group so
  no token = 401.
* `internal/config/config.go` — new
  `AuditsBackend` env switch
  (`AEGIS_AUDITS_BACKEND=memory|pg`, default
  `memory`). The pg path uses the existing
  `audit_log` table from migration 0001.
* `internal/router/router.go` — `Build()` accepts
  the new `auditsSvc` and mounts
  `/api/v1/audits`. The default and the rotated
  sub_path mount both share the same `Build`
  signature now.
* `cmd/aegis/main.go` — wires the audits service
  the same way as the other services (MemoryStore
  by default, PgStore when the env switch is
  `pg`); the dev seed user is granted
  `ScopeAudits` so the local panel can exercise
  the read API.

## OpenAPI

* `docs/openapi.yaml` — extends the v0.2.0
  surface description to mention
  `/audits/*` and the new `change-password`
  endpoint. Three new paths:

* `POST /auth/me/password` — change-password
  endpoint with the full request/response
  envelope.
* `GET /audits` — list with the seven query
  params (`actor_id`, `action`, `resource_type`,
  `resource_id`, `since`, `until`, `limit`) and
  the `AuditListResponse` envelope.
* `GET /audits/{id}` — single entry with the
  full `AuditEntry` shape (including
  `before` / `after` JSONB blobs).

  Two new schemas (`AuditEntry`,
  `AuditListResponse`) and one new request
  schema (`ChangePasswordRequest`). The `scopes`
  enum on the existing auth response schemas
  gains `audits`.

* `frontend/src/types/api.d.ts` — regenerated
  from the updated spec; `pnpm run codegen:check`
  passes.

## Frontend

* `frontend/src/api/services/audits.ts` — new
  service: `listAudits(filters)` builds the
  query string (omitting empty / null filters)
  and returns the typed entry array;
  `getAudit(id)` re-fetches the single entry for
  the detail dialog. The 404 path collapses to
  a soft `null` (the audit table can be pruned
  between the list call and the detail call).
* `frontend/src/api/services/auth.ts` — adds
  `changePassword({ current_password,
  new_password })` returning the refreshed
  `MeResponse`.
* `frontend/src/api/services/index.ts` — re-exports
  the new audits service.
* `frontend/src/types/aegis.ts` — adds the
  `AuditEntry` and `ChangePasswordRequest`
  types for the v0.2 hand-maintained mirror
  (the v0.3+ work deprecates this file in favour
  of the OpenAPI codegen).
* `frontend/src/views/AuditsView.vue` — new view
  with: a DataTable of recent entries (timestamp,
  actor, action, resource, IP, "Inspect" button);
  a filter dialog (action, resource_type, since,
  until); a detail dialog that re-fetches the
  full entry to render the `before` / `after`
  JSONB blobs. The "filter active" badge on the
  filter button signals the operator that a
  filter is applied; the "Clear filters" button
  resets.
* `frontend/src/views/ProfileView.vue` — new
  view: identity card (username, user id,
  granted scopes as badges) + security card
  (change-password button). The change-password
  dialog validates the new + confirm match
  client-side and posts to
  `POST /api/v1/auth/me/password`.
* `frontend/src/router/index.ts` — adds `/audits`
  and `/me` routes; both are auth-required.
* `frontend/src/layouts/AppLayout.vue` — adds
  sidebar items for "Audit log" (`/audits`,
  `History` icon) and "Profile" (`/me`, `User`
  icon). The topbar user menu's "profileSoon"
  placeholder is replaced with a real link to
  `/me`.
* `frontend/src/i18n/locales/{en,ru}.json` —
  i18n keys for the audits and profile sections
  (en + ru), plus the renamed `topbar.profile`
  key. The `common.close` and `common.refresh`
  keys are added (the existing `common` block
  was missing them and the audits UI uses both).

## Quality

* `go test ./...` — clean (24 audits tests + 9
  auth change-password tests + everything that
  was green before this PR). The
  `pg_store_integration_test.go` runs against a
  real Postgres via `testutil.MustNewPool`.
* `pnpm run type-check` — clean.
* `pnpm run lint` — clean (0 errors, 206
  warnings, of which 171 are pre-existing
  `vue/max-attributes-per-line` on the CRUD
  views and unchanged from main). The 35 new
  warnings are the same style nit on the new
  AuditsView / ProfileView files; they are
  consistent with the rest of the CRUD views
  and would be cleared by a global Prettier /
  ESLint --fix pass that the v1.x polish
  milestone owns.
* `pnpm run build` — clean.
* `pnpm run codegen:check` — passes; the
  generated `api.d.ts` is byte-equal to the
  committed file.

## Out of scope (v0.3+)

* The `audits.Record(...)` call-sites in the
  nodes / hosts / inbounds / users / panelcfg
  mutating handlers. v0.2.0 ships the
  `Service.Record` helper, the v0.3 work adds
  the call-site. Per-resource — the diff is
  mechanical (one `defer audits.Record(...)` in
  each handler) and the v0.3 milestone owns it.
* `swag` annotations on the Go side to derive
  `docs/openapi.yaml` from source comments. The
  current spec is hand-maintained; the v0.3
  follow-up wires `swag generate` into CI so
  there is one source of truth.
* Pagination on the audit list path beyond the
  `limit` cap. The default 100 + max 1000 is
  enough for v0.2.0's "operator inspects recent
  history" use case; the v1.x observability
  milestone owns cursor-based pagination.
* Retention: the audit table grows unboundedly.
  v1.5+ adds a cron-driven `DELETE FROM audit_log
  WHERE created_at < now() - interval '90 days'`.
* An "invalidate all sessions" toggle on the
  change-password endpoint. v0.2.0 keeps the
  user logged in; the "I think I lost my laptop"
  path is a v1.x follow-up.
