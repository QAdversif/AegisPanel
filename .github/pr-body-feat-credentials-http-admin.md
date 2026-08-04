# feat(credentials): HTTP admin surface + UI for user_inbound_credentials

Closes v0.8.2 item 2 from the roadmap. v0.8.2 item 1
(auth.me fix on pg backend) shipped in #182. With
this PR the v0.8.2 release is complete end-to-end:
both items are merged into main, ready for the
v0.8.2 tag.

## What this PR does

### Backend

- **`ScopeCredentials` constant** in `auth/scopes.go`,
  granted to every role (admin / operator / viewer)
  on the same rationale as `ScopePlans` /
  `ScopeWebhooks` (a viewer who cannot see the
  credentials table cannot answer "is user X set
  up correctly on inbound Y?").
- **Role mapping** in `auth/pg_store.go` — the
  existing `scopesForRole` switch now includes
  `ScopeCredentials` for all three roles.
- **`Store.ListAll`** on the credentials store
  interface + both `MemoryStore` and `PgStore`
  implementations. Returns every credential
  ordered by (user_id, inbound_id). The MemoryStore
  walks the rows map; the PgStore issues a single
  SELECT with the same ordering.
- **`credentials.AdminRouter`** at
  `internal/credentials/admin_handler.go` —
  chi subrouter mounted at `/api/v1/credentials/`
  with the canonical v0.6.0 / v0.7.0 shape:
  - `GET    /`                     — list every credential
  - `GET    /{id}`                 — get a single credential
  - `POST   /`                     — create a credential
  - `PATCH  /{id}`                 — rotate the value
  - `DELETE /{id}`                 — hard delete
  - `GET    /by-user/{userId}`     — per-user cross-cut
  - `GET    /by-inbound/{ibId}`    — per-inbound cross-cut
- **`router.Build`** signature: takes a
  `*credentials.Service` parameter, mounts the
  admin router with the auth middleware +
  `RequireScope(ScopeCredentials)`. The
  `app/app.go` `Build` call site now passes
  `a.Credentials`.
- **Audit log writes**: the mutating handlers go
  through `credentials.Service` which already
  records `credential.create` / `credential.rotate`
  / `credential.delete` audit entries via
  `audits.RecordFromContext` (the `WithAudits` setter
  wired in `app/app.go` from PR #166). No new
  call-site wiring needed in the handler.

### Frontend

- **`Credential` type** in `types/aegis.ts` (hand-
  mirrored, the same shape the codegen produces in
  `types/api.d.ts`).
- **`api/services/credentials.ts`** — the typed
  service functions (`listCredentials`,
  `getCredential`, `createCredential`,
  `rotateCredential`, `deleteCredential`,
  `listCredentialsByUser`,
  `listCredentialsByInbound`).
- **`schemas/credential.ts`** — zod schemas for
  the create (`credentialCreateSchema`) and rotate
  (`credentialRotateSchema`) forms. Reuses
  `uuidSchema` from `schemas/primitives.ts`.
- **`views/CredentialsView.vue`** — the admin view.
  Pattern mirrors `PlansView`:
  - DataTable with columns: user (short UUID +
    hover-to-see-full), inbound (same), value
    (Badge), updatedAt, actions
  - Create dialog with the three required fields
    (userId, inboundId, credentialValue)
  - Rotate dialog with one field
    (credentialValue)
  - Delete dialog with confirm
  - `?userId=…` query param: the page hydrates
    from `router.currentRoute.value.query.userId`
    and re-fetches via `listCredentialsByUser` when
    set. The UsersView's "View credentials" dropdown
    action pushes this query.
  - All strings wrapped in `t('credentials.*')` for
    en + ru.
- **Route** at `/credentials` (router/index.ts).
- **Nav item** in `AppLayout.vue` (KeyRound icon,
  between webhooks and settings).
- **UsersView dropdown action**: the existing
  Edit / Rotate token / Soft-delete dropdown now
  has a "View credentials" item that pushes to
  `/credentials?userId={user.id}`.

### OpenAPI

- **6 paths** added: `/credentials` (list + create),
  `/credentials/{id}` (get / patch / delete),
  `/credentials/by-user/{userId}` (list),
  `/credentials/by-inbound/{ibId}` (list).
- **4 schemas** added: `Credential`,
  `CredentialCreateRequest`, `CredentialRotateRequest`,
  `CredentialListResponse`.
- **Codegen** regenerated (`api.d.ts`); the diff
  is +441 lines of typed OpenAPI client.

## Test coverage

- **`internal/credentials/admin_handler_test.go`**
  (new, 11 tests, all unit):
  - `TestAdminRouter_RequiresAuth` — every
    endpoint rejects unauthenticated with 401
  - `TestAdminRouter_RequiresScope` — a JWT
    with the wrong scope is 403
  - `TestAdminRouter_ListEmpty` — fresh store
    returns 200 with empty list
  - `TestAdminRouter_CreateGetRotateDelete` —
    canonical end-to-end happy path
  - `TestAdminRouter_DuplicateReturns409` —
    second create with the same (user, inbound)
    pair returns 409
  - `TestAdminRouter_CreateRejectsInvalidUUID`
  - `TestAdminRouter_CreateRejectsEmptyValue`
  - `TestAdminRouter_ByUser` — per-user cross-cut
    returns 3 of 4 rows
  - `TestAdminRouter_ByInbound` — per-inbound
    cross-cut returns 2 of 2 rows
  - `TestAdminRouter_ByUserRejectsInvalidUUID`
  - `TestAdminRouter_NotFoundReturns404`
- **`pg_store_integration_test.go`** — the
  existing PgStore integration tests still pass;
  the new `ListAll` method is exercised by the
  admin handler test path through the MemoryStore
  (the handler is store-agnostic).

## What this PR does NOT ship

- No new migration. The `user_inbound_credentials`
  table from migration 0019 (v0.8.0) is the data
  layer; this PR is the HTTP wrapper.
- No new env vars. `AEGIS_CREDENTIALS_BACKEND=pg`
  is unchanged from v0.8.0; the new `ListAll` method
  is exercised by both MemoryStore and PgStore.
- No `WebSocket` / streaming / pagination. The
  admin table is small in practice; pagination is
  out of scope.
- No "credentials tab in a user detail page" UI
  change. The "View credentials" action in the
  UsersView dropdown + the `?userId=…` query
  param on the CredentialsView is the v0.8.2
  equivalent; a real user detail page is a v0.8.x
  follow-up.
- No inbound picker UI. The create form takes a
  raw UUID; the operator copies it from the
  InboundsView. A future "select inbound" picker
  is a v0.8.x follow-up.

## Verification

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l internal/...` — clean
- `go test -short -count=1 ./...` — 25/25
  packages PASS
- `golangci-lint run ./...` — 0 issues
- `npm run codegen:check` — clean
- `npm run lint` — 0 errors (72 pre-existing
  warnings; 0 warnings introduced by this PR)
- `npm run build` — clean (CredentialsView chunk
  8.55 kB / 2.45 kB gzip)
- `npm run test` — 38/38 PASS

## Pre-v0.8.2 walk (for posterity)

The data layer + Service were in place from
v0.8.0 (#167) and the v0.8.x follow-up work
(PR #168 / #169 / #170). The only thing this
PR adds is the HTTP wrapper and the admin
UI. The Service was already wired with
`WithAudits`; the mutating handlers just
delegate to it.
