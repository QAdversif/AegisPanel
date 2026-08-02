feat(credentials): Phase 2 multi-user sing-box render data model

The v0.7.2 sing-box renderer is single-user per inbound:
the protocol-level `users` array inside the rendered
config carries exactly one user (the operator's credential,
encoded in `inbounds.params["uuid"]` for VLESS, or
`inbounds.params["password"]` for HY2 / Trojan). Multi-
user rendering is the next milestone (per
ARCHITECTURE.md §7.5 and the v0.7.2 KNOWN_LIMITATIONS
"Phase 2 multi-user sing-box render — Phase 2" entry)
but the data model has to land first.

This PR closes step 1 (data model). Steps 2-4 (renderer
signature change, builder narrow, per-user subscription)
follow in dedicated PRs.

## What lands

- **Migration `0019_user_inbound_credentials.sql`**:
  `id UUID PK, user_id FK→users ON DELETE CASCADE,
  inbound_id FK→inbounds ON DELETE CASCADE,
  credential_value TEXT NOT NULL, created_at, updated_at,
  UNIQUE (user_id, inbound_id)` plus two indexes on
  `user_id` and `inbound_id`.
- **New `backend/internal/credentials/` package**:
  - `credentials.go` — `Credential` struct plus
    `IsValid()`. JSON tags snake_case to match the
    rest of the panel's wire format.
  - `store.go` — `Store` interface plus
    `MemoryStore` (Phase 0 default, in-process map
    with secondary indexes on `byUser` and `byInb`
    for O(1) `ListByUser` and `ListByInbound`).
    `ErrNotFound` and `ErrDuplicate` exported errors.
  - `pg_store.go` — `PgStore` with `RETURNING`
    clauses on Insert / Update / Get. SQLSTATE 23505
    on Insert maps to `ErrDuplicate`; `pgx.ErrNoRows`
    on Update / Get maps to `ErrNotFound`.
  - `service.go` — `Service` with `NewService(store)`,
    `WithAudits(svc)`, `Create` / `Get` /
    `ListByUser` / `ListByInbound` / `Rotate` / `Delete`,
    `ValidationError` type. All Create / Rotate / Delete
    call `audits.RecordFromContext` with
    `credential.create` / `credential.rotate` /
    `credential.delete` actions.
  - `store_test.go` (11 tests) and `service_test.go`
    (13 tests): full MemoryStore and Service coverage,
    all green. 24/24 new unit tests pass.
- **`config.go`**: new `AEGIS_CREDENTIALS_BACKEND` env
  var (default `memory`).
- **`app.go`**: new `Credentials *credentials.Service`
  field on `App`; new wiring step (14c) using the
  generic `MustBuild[credentials.Store]`; new
  `WithAudits(a.Audits)` call mirroring the
  v0.7.x setter pattern; `cfg.CredentialsBackend`
  added to `needsPg`.

## Why this is one PR (not 4)

The Phase 2 multi-user plan has 4 PRs (data model,
renderer, builder narrow, subs + cabinet). The data-
model PR is its own PR because the table is the seam:
without it, the renderer and the builder have nothing
to read; with it but without the renderer, the table
sits empty. The empty-table state is intentional and
documented (no operator-facing behaviour change).
Splitting the data model further (migration vs Store
vs Service) would multiply CI cost with no benefit;
the file boundaries are already the right cut.

## Design notes

- **No cross-entity pre-validation.** The Service does
  NOT verify that `user_id` or `inbound_id` exist
  before insert. The pgx-backed Store relies on the
  FK constraints (migration 0019) and surfaces
  `pgx.ErrForeignKeyViolation`; the Service translates
  in a follow-up PR. The MemoryStore does an in-memory
  pre-check for fast unit-test failure (the FK is the
  canonical gate for production).
- **credential_value is opaque TEXT** at the storage
  layer. The sing-box renderer validates per-protocol
  shape (UUID for VLESS, password length for HY2 /
  Trojan, method tag for Shadowsocks 2022-blake3). The
  panel stores whatever the admin / operator provides;
  the renderer decides whether it is usable.
- **Independence from `users.sub_token`.** sub_token is
  for the cabinet / subscription surface (HTTP
  `/sub/{token}`); credential is for the sing-box
  protocol-level auth (VLESS UUID, Shadowsocks
  2022-blake3 password, etc.). A future PR may
  auto-derive credentials from the sub_token (e.g.
  `uuid = sha256(sub_token + inbound_id)`) to avoid a
  separate admin CRUD surface, but that decision is the
  inbound-templates work, not this PR.
- **No HTTP handler in this PR** — minimal data model.
  Admin HTTP layer is a follow-up PR. Pattern matches
  PR 157 (BatchedApplier) and PR 158 (e2e test):
  service-level only, no HTTP.
- **Phase 1 compat**: `inbounds.params["uuid"]` /
  `["password"]` stays. This PR has ZERO behaviour
  change. Follow-up PRs (PR 2-4 in the Phase 2 plan)
  wire multi-user in.

## Pre-fetch trade-off

`Service.Rotate` and `Service.Delete` now `GetByID` the
row before mutating, so the audit entry has a `Before`
field. One extra DB round-trip per Rotate / Delete; the
trade-off is "cleaner audit trail" vs "saves a read on
a rarely-called path". Same pattern as PR 166 (Delete
audit Before) — Delete is the slowest CRUD verb anyway,
preceded by an "are you sure" dialog 99 percent of the
time.

## Follow-up PRs

- PR 2 (renderer): `renderVLESS(spec, params,
  users []UserCredential)` — accepts list of users
  instead of single user from `params`. Analogous for
  HY2 / Trojan. Shadowsocks stays single-password.
- PR 3 (builder and BatchedApplier narrow): builder
  fetches credentials per inbound, filters by host
  allowlist and blocklist. `users.Service.enqueueUserDelta`
  narrows to nodes matching `HostsAllowlist` instead of
  fan-out to ALL nodes.
- PR 4 (subs and cabinet): subscription service renders
  per-user config URL; cabinet endpoints to view and
  manage own credentials.

## Tests

- 24 of 24 new unit tests pass.
- 25 of 25 unit packages green.
- `go vet -tags=integration ./...` clean.
- `golangci-lint v2` 0 issues.
- `gofmt` clean.

## File map

- `backend/migrations/0019_user_inbound_credentials.sql` (new)
- `backend/internal/credentials/credentials.go` (new)
- `backend/internal/credentials/store.go` (new)
- `backend/internal/credentials/pg_store.go` (new)
- `backend/internal/credentials/service.go` (new)
- `backend/internal/credentials/store_test.go` (new)
- `backend/internal/credentials/service_test.go` (new)
- `backend/internal/config/config.go` (modified, plus 23 lines)
- `backend/internal/app/app.go` (modified, plus 33 lines)
