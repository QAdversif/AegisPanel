# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

### Added (v0.8.4 — admin UI button for rotate-panel-key)

- **HTTP mirror of the v0.8.3 `aegis admin node
  rotate-panel-key` CLI**: new endpoint
  `POST /api/v1/nodes/{id}/rotate-panel-key`
  that takes the operator's existing private
  key (PEM, no passphrase) and optional
  `ssh_port` / `ssh_user` overrides, SSHes into
  the node, generates a fresh ed25519 keypair,
  pushes the public half to the node's
  `~/.ssh/authorized_keys`, and seals the
  private half with the operator's age envelope
  (same path the v0.8.1 password-install
  post-install hook takes). The 200 body carries
  the new public key line and SHA256
  fingerprint so the operator can verify the
  rotation in the UI. Same handler signature
  shape as the v0.3.0 `POST /{id}/provision`
  (mounted under the same `{id}` subrouter
  with `auth.RequireScope(ScopeNodes)` already
  enforced by the parent nodes router).
- **"Rotate panel key" dropdown entry in the
  NodesView**: visible for `online` / `offline`
  / `draining` / `disabled`; hidden for `new`
  (the panel cannot SSH into a never-installed
  node because no key is in `authorized_keys`).
  Clicking opens a dialog with the operator's
  PEM textarea, the same `ssh_port` / `ssh_user`
  override fields as the provision dialog, and
  a submit button labelled "Rotate". On success
  the dialog swaps to a read-only "rotation
  result" card that shows the new public key
  line + SHA256 fingerprint so the operator can
  copy the fingerprint before closing.
- **Backend `RotatePanelKey` refactor**: the
  Service method now returns
  `(RotationResult, error)` (was just `error`)
  so the HTTP handler can surface the new
  public key line + fingerprint in the 200
  body. The v0.8.3 CLI's call-site
  (`runAdminNodeRotatePanelKey`) is updated to
  ignore the result via `_, err :=`. The shared
  body `generateAndPushKey` (used by both
  `RotatePanelKey` and the v0.8.1 post-install
  hook) gets the same signature change; the
  post-install hook discards the result via
  `_, err :=`.
- **OpenAPI spec + codegen**: new
  `NodeRotatePanelKeyRequest` /
  `NodeRotatePanelKeyResponse` schemas in
  `docs/openapi.yaml`; the generated
  `frontend/src/types/api.d.ts` carries the
  new types automatically. The
  `NodeRotatePanelKeyRequest` shape is the
  same snake_case `ssh_private_key` /
  `ssh_port` / `ssh_user` triple the
  provision request uses; the response is the
  `{ node_id, public_key_line, fingerprint }`
  triple the UI surfaces.
- **zod schema for the rotate form**:
  `nodeRotatePanelKeySchema` in
  `frontend/src/schemas/node.ts` (the
  `ssh_private_key` field is required, the
  overrides are optional with the same
  1..65535 port range as the provision
  schema).
- **i18n strings (en + ru)**:
  `nodes.rotate` / `nodes.rotateTitle` /
  `nodes.rotateDescription` /
  `nodes.rotateSshPrivateKey` /
  `nodes.rotateSshPrivateKeyHint` /
  `nodes.rotateAction` /
  `nodes.rotateResultTitle` /
  `nodes.rotateResultHelp` /
  `nodes.rotatePublicKeyLine` /
  `nodes.rotateFingerprint` / `nodes.rotated` /
  `nodes.rotateFailed`. The Russian translations
  follow the v0.8.x "звучит как оператор, а не
  как переводчик" style.

### Tests

- **7 unit tests for `HandleRotatePanelKey`**
  in
  `backend/internal/bootstrap/handler_rotate_panel_key_test.go`:
  200 happy path, 400 missing key, 400
  malformed JSON, 404 node not found, 500
  envelope not configured, 502 SSH connect
  failure, audit shape on success. The SSH
  client is mocked via a package-level
  `newSSHClientForRotate` indirection in
  `handler.go` (the default is the production
  `NewClient`; the test helper
  `withMockSSHClient(t, mock)` swaps it for a
  recording `mockClient`).
- **Existing test updates**: the v0.8.3
  `TestRotatePanelKey_NilEnvelopeFailsClosed`
  and `TestRotatePanelKey_NilClientFailsClosed`
  in
  `backend/internal/bootstrap/rotate_panel_key_test.go`
  are updated for the new
  `(RotationResult, error)` signature.

## [0.6.0] - 2026-07-31

### Added (plans CRUD surface, #131, #132, #133, #134)

The `plans` table was in migration 0001 from the start;
v0.6.0 promotes it to a real CRUD surface with a
typed Go package, an HTTP admin handler, an OpenAPI
spec, and a UI view. The plan catalog is now the
operator-facing source of truth for the tariff ladder;
every `users.plan_id` row references a row here.

- **`backend/internal/plans`** (new, #131) — the
  Go-side owner of the `plans` table. Layout
  follows the d-refactor pattern that
  `internal/users` established: `Plan` model +
  `ResetPeriod` closed enum (daily / weekly /
  monthly / never), `Store` interface with
  sentinels (`ErrNotFound` / `ErrDuplicate` /
  `ErrInvalid` / `ValidationError`),
  `MemoryStore` (in-process, used by unit tests
  and dev), `PgStore` (pgx-backed, used when
  `AEGIS_PLANS_BACKEND=pg`), `Service` with
  full input validation (Name 1..64 chars trimmed,
  Duration [1 minute, 10 years], non-negative
  numbers, ResetPeriod enum), and a 30-day
  per-month policy on the `pgtype.Interval` round
  trip. 23 unit tests + 4 pg integration tests
  (gated on `INTEGRATION_DATABASE_URL` +
  `//go:build integration`). The
  `pgtype.Interval{Valid: true}` footgun (default
  zero value encodes as SQL NULL) is documented
  in the package doc comment so a future refactor
  does not silently break the encode path.
- **`backend/internal/plans/admin_handler.go`**
  (new, #132) — `AdminRouter(svc, authMW)` mounts
  `GET /`, `GET /{id}`, `POST /`, `PATCH /{id}`,
  `DELETE /{id}` behind `RequireScope(ScopePlans)`.
  Maps the package's sentinels to 400 / 404 / 409
  via a tiny `writePlanError` helper (same shape
  as `users.writeUserError`; the duplication is
  cheaper than a shared httpkit). 11 end-to-end
  tests cover auth required, scope required, every
  CRUD happy / error path.
- **`backend/internal/auth/scopes.go` + `pg_store.go`**
  (#132) — new `ScopePlans` constant, granted to
  every role (admin / operator / viewer). The
  same fail-closed argument as `ScopeAudits`:
  every operator-facing surface that lists users
  reads the plan catalog to resolve a `plan_id`
  to a name, so a viewer who cannot see the
  catalog cannot render the UsersView correctly.
- **`backend/internal/config/config.go`** (#132)
  — new `PlansBackend` field + `AEGIS_PLANS_BACKEND`
  env var, default `memory`. Same pattern as
  every other service's backend flag.
- **`backend/internal/router/router.go`** (#132)
  — `Build(...)` signature gains `plansSvc
  *plans.Service`. `r.Mount("/plans", plans.
  AdminRouter(...))` sits next to `/users`.
- **`backend/cmd/aegis/main.go`** (#132) —
  `cfg.PlansBackend` plugged into `needsPg`;
  new `plansSvc` construction block between
  `usersSvc` (8) and the subscription service
  (renumbered to 10); passed to `router.Build`.
- **`docs/openapi.yaml`** (#133) — adds the
  `/plans` paths (GET / POST on the collection,
  GET / PATCH / DELETE on the item), the `Plan`
  schema, `PlanCreateRequest`, `PlanUpdateRequest`,
  `PlanListResponse`, and the `PlanResetPeriod`
  enum. `info.version` bumped 0.2.0 → 0.6.0.
  The /plans section sits between /hosts and
  /users in the data-graph order (hosts →
  plan_pool → plans → users). The wire format
  is `camelCase` to match the rest of the spec
  (the existing `camelizeKeys` response
  interceptor in `client.ts` bridges the camelCase
  spec to the snake_case Go JSON tags; a
  full wire-format normalization is a separate
  work item).
- **`frontend/src/api/services/plans.ts`**
  (new, #133) — hand-mirrored service. 5
  functions (`listPlans`, `getPlan`, `createPlan`,
  `updatePlan`, `deletePlan`) + the
  `CreatePlanRequest` / `UpdatePlanRequest`
  request types. Follows the exact shape of
  `services/users.ts`. The `aegis.ts` `Plan`
  interface was renamed `durationDays` →
  `durationNs` to match the wire format; the UI
  converts to a human-readable "30 days" string
  at the rendering layer.
- **`frontend/src/types/aegis.ts` +
  `frontend/src/types/api.d.ts`** (#133) —
  updated Plan interface; `api.d.ts` was
  regenerated by `npm run codegen` from the new
  spec.
- **`frontend/src/views/PlansView.vue`**
  (new, #134) — the admin view. CRUD surface
  (list + create + edit + delete) with the same
  pattern as `UsersView` (vue-i18n `t('plans.*')`
  for every user-facing string, zod schema for
  the form, `useZodForm` `onSubmit` handler).
  Duration is edited as a human-readable
  `<N><unit>` string ("30d" / "1h" / "5m") and
  converted to int64 nanoseconds at submit
  time; the table formats ns back to a string
  via a `formatDurationNs` helper.
- **`frontend/src/layouts/AppLayout.vue`** (#134)
  — `Package` icon from lucide-vue-next, `plans`
  entry in `navItems` between `hosts` and
  `subscription` (the data-graph order).
- **`frontend/src/router/index.ts`** (#134) —
  `/plans` route with `titleKey: 'nav.plans'`
  and the lazy `() => import('@/views/PlansView.vue')`
  chunk.
- **`frontend/src/i18n/locales/{en,ru}.json`**
  (#134) — `nav.plans` and the `plans.*`
  namespace (form labels, reset-period enum,
  toast strings, search / empty-state, duration
  format strings).
- **`docs/ROADMAP.md`** (#135) — `v0.6.0` row
  updated to `✅ shipped (#131, #132, #133, #134)`.

### What is NOT in v0.6.0

- **`plan_pool` writes** — the `plan_pool` join
  table is intentionally NOT touched by
  `internal/plans` in v0.6.0. The subscription
  package continues to own its read path
  (`ListPoolsForUser`). v0.6.x will fold the
  `plan_pool` writes into this package and have
  subscription delegate to it.
- **Audit log writes** — the call-site wiring is
  a separate batch across all admin handlers
  (nodes, hosts, inbounds, users, plans,
  panelcfg). v0.2.0 shipped the
  `audits.RecordFromRequest` helper; the
  call-sites are a v0.3+ TODO that v0.6.0
  follows. The batch lands when the audit
  package is wired into the handlers in a
  single follow-up PR.
- **`plan_pool` UI** — no `HostPool` picker in
  the plan create / edit dialog yet. v0.6.x
  adds the binding management UI.

## [0.7.0] - 2026-07-31

### Added (outgoing-webhook surface, #136, #137, #138, #139)

The `webhook_endpoints` table was in migration 0001
from the start (a v0.3.0 stub); v0.7.0 promotes
it to a real outgoing-webhook surface with a
typed Go package, an HTTP admin handler, an
OpenAPI spec, and a UI view. The package ships
HMAC-SHA256 signing, exponential-backoff retry,
and a dead-letter queue. v0.7.0 does NOT wire
`Service.Dispatch` from every mutating handler
(production event flow is a v0.7.x follow-up
batch); the operator uses the new `POST
/api/v1/webhooks/{id}/test` endpoint to verify
their setup end-to-end.

- **`backend/internal/webhooks`** (new, #136) — the
  Go-side owner of the `webhook_endpoints`,
  `webhook_deliveries`, and `webhook_dlq` tables.
  Layout follows the plans / users pattern:
  `Endpoint` model with a closed-set `EventType`
  enum (18 event types covering user / plan /
  node / host / backup / inbound lifecycles),
  `Delivery` + `DLQEntry` models with JSONB payload
  snapshots so manual replay sends the exact same
  body the receiver saw, `Store` interface with
  three concerns (endpoints, deliveries, DLQ),
  `MemoryStore` (in-process) + `PgStore` (pgx-
  backed, selected via `AEGIS_WEBHOOKS_BACKEND=pg`).
  `Service` owns input validation (URL http/https
  only, secret 16..256 chars, events in closed
  enum), the synchronous dispatcher (signs in-
  memory, records every attempt as a `Delivery`
  row, moves the final failed attempt to the DLQ),
  and the manual-retry / replay hooks
  (`Service.RetryDelivery`, `Service.ReplayDLQEntry`,
  `Service.SendTestEvent`). HMAC signature helpers
  in `signature.go` (canonical `sha256=<hex>` form,
  constant-time compare via `crypto/hmac.Equal`).
  Exponential-backoff retry in `retry.go` (1s, 5s,
  25s, 2m15s, 11m15s — `MaxAttempts = 6`).
  41 unit tests + 5 pg integration tests
  (gated on `INTEGRATION_DATABASE_URL`).
- **`backend/internal/webhooks/admin_handler.go`**
  (new, #137) — `AdminRouter(svc, authMW)` mounts
  the admin surface behind `RequireScope(ScopeWebhooks)`:
  `GET /`, `GET /{id}`, `POST /`, `PATCH /{id}`,
  `DELETE /{id}`, `GET /{id}/deliveries`,
  `POST /{id}/test`, `GET /dlq`,
  `GET /dlq/{did}`, `POST /dlq/{did}/replay`,
  `DELETE /dlq/{did}`. The `secret` field is shown
  VERBATIM in the immediate Create response (so
  the operator can copy it to their receiver's
  HMAC config) and redacted to `***` on every
  subsequent read. 13 end-to-end tests cover
  every CRUD + test + replay path.
- **`docs/openapi.yaml`** (updated, #138) — version
  bump 0.6.0 → 0.7.0. 11 new paths under
  `/api/v1/webhooks/*`, 12 new schemas
  (`WebhookEventType`, `WebhookDeliveryStatus`,
  `WebhookEndpoint` with create / update / list
  variants, `WebhookDelivery` with list response,
  `WebhookDLQEntry` with list response,
  `WebhookDispatchResult`).
- **`frontend/src/api/services/webhooks.ts`** (new,
  #138) — hand-mirrored from the OpenAPI spec. 12
  functions (listWebhooks, getWebhook, createWebhook,
  updateWebhook, deleteWebhook, listDeliveries,
  sendTestEvent, listDLQ, getDLQ, deleteDLQ,
  replayDLQ) + 2 request DTOs
  (CreateWebhookRequest, UpdateWebhookRequest) +
  5 type re-exports. Registered in
  `services/index.ts`.
- **`frontend/src/views/WebhooksView.vue`** (new,
  #139) — list, create, edit, delete, send a
  synthetic test event, inspect the per-endpoint
  delivery history, and replay / drop entries in
  the cross-endpoint DLQ. The one-time HMAC-secret
  display widget is rendered as a prominent amber
  card above the table right after Create so the
  operator copies the secret to their receiver
  before dismissing. Sidebar nav entry with the
  `Webhook` lucide icon, between `Backups` and
  `Profile`. Full `webhooks.*` i18n namespace
  (en + ru).
- **Auth scope** — new `auth.ScopeWebhooks`
  constant, granted to every role (admin /
  operator / viewer) so the endpoint-health
  widget is visible from every role, matching the
  `ScopePlans` precedent.
- **Config flag** — `AEGIS_WEBHOOKS_BACKEND`
  (default `memory`) selects the persistence
  layer; `cmd/aegis/main.go` wires the store and
  the service, and the `needsPg` OR-chain picks up
  the new flag.

### Fixed (webhook_endpoints schema gaps, #136)

The v0.3.0 stub of `webhook_endpoints` in
migration 0001 was missing two things v0.7.0
needed. Both gaps only surface at pgx integration
time; the `MemoryStore` enforces both invariants
in code, the `PgStore` relies on the SQL
constraints.

- Migration 0015 adds `updated_at TIMESTAMPTZ NOT
  NULL DEFAULT NOW()` to `webhook_endpoints` so
  the `Endpoint.UpdatedAt` field has a backing
  column.
- Migration 0016 adds a `UNIQUE (url)` constraint
  on `webhook_endpoints` so duplicate-URL
  detection in `PgStore.CreateEndpoint` /
  `UpdateEndpoint` surfaces `SQLSTATE 23505` →
  `ErrDuplicate`, matching the `MemoryStore`
  behaviour. v0.7.x move `webhook_endpoints.secret`
  under the sops envelope (plaintext in the DB
  today).

### Fixed (pgtype.Interval encode footgun, #136)

The default zero value of `pgtype.Interval` has
`Valid: false`, which encodes as SQL `NULL` and
silently breaks the `NOT NULL` constraint on the
column. The plans package already documented this
(v0.6.0). The webhooks package now uses the same
canonical pattern: every `pgtype.Interval` value
the encode path produces sets `Valid: true` and
every `pgtype.Text` (the `response_body` /
`error` columns are nullable) is wrapped in
`pgtype.Text` on the scan side so `NULL` reads
back as an empty string. CI integration tests
gated the regression; the project-wide rule
"every `pgtype.*` type with a `Valid` field must
set `Valid: true` on the encode path" is now part
of the v0.7.x code-review checklist.

### Fixed (Postgres JSONB byte-equality footgun, #136)

Postgres JSONB normalises whitespace on read-back
(`{"x":1}` → `{"x": 1}`), so a test that does
`if string(got.Payload) != jsonLiteral` will
fail on the round-trip. The integration tests
now use a `jsonEqual(t, raw, want any)` helper
that parses both sides into a generic structure
and compares with `reflect.DeepEqual`. The
production dispatcher stores canonical bytes in
a `request_body` column (BYTEA in the DB) for
replay, so the JSONB normalisation on the
`payload` column is purely a queryability
concern — but the test path needs the helper.

### Changed (sqlfluff LT02 lint, #136)

CI's sqlfluff lint flags `ALTER TABLE ... ADD
CONSTRAINT` if the second line is indented. The
canonical style across the existing 14 migrations
is to keep `ALTER TABLE` + the verb
(`ADD COLUMN` / `DROP COLUMN` / `ADD CONSTRAINT`
/ `DROP CONSTRAINT`) on a single line. Migrations
0014 / 0015 / 0016 follow this rule.

### Changed (gitleaks generic-api-key false positive, #136)

Gitleaks's `generic-api-key` rule flags the
high-entropy test HMAC secrets as possible real
API keys. The fix is a low-entropy fixture
pattern (`webhook-fixture-secret-aaaa...` with
repeated characters) so the entropy check stays
well below the threshold while still satisfying
the Service's `MinSecretLen=16` validation. The
pattern must be applied BEFORE the first commit
on a new branch — fixup commits don't work
because gitleaks scans the full PR diff (which
includes the OLD strings in the "before" context).

### Security (HMAC-SHA256 signing + 5-minute anti-replay)

Every dispatch the panel makes carries the
canonical HMAC-SHA256 signature in
`X-Aegis-Signature` (format `sha256=<hex>`) and
the request timestamp in `X-Aegis-Timestamp`
(RFC 3339 nano). The receiver MUST verify the
signature with constant-time compare
(`crypto/hmac.Equal`) and reject any event
whose timestamp is more than 5 minutes from the
receiver's wall clock (the anti-replay window
documented in `internal/webhooks/signature.go`).
The v0.7.0 surface ships the verify contract;
receiver-side implementations are out of scope.

### Deferred to v0.7.x (call-site wiring)

- **Background worker** that picks up failed
  `Delivery` rows and schedules the next retry.
  v0.7.0 ships the manual
  `Service.RetryDelivery` hook the worker will
  call.
- **sops envelope** on `webhook_endpoints.secret`
  (plaintext in the DB today).
- **Wiring `Service.Dispatch`** into every
  mutating handler (user / plan / node / host /
  inbound CRUD). v0.7.0 ships the package + the
  HTTP surface + the test endpoint; the
  production event flow lands in the v0.7.x
  follow-up batch, alongside the v0.6.x audit-log
  call-site wiring.
- **Shared zod schema** at
  `frontend/src/schemas/webhook.ts` (v0.7.0 view
  uses inline zod via `useZodForm`).

## [0.7.1] - 2026-08-01

Five-PR v0.7.x follow-up batch. Every item was
"deferred to v0.7.x" in the v0.7.0 section above;
v0.7.1 closes all four deferred items, plus adds
the events multi-select UI that the v0.7.0
deferred list did not call out (the wire surface
already supported it; only the UI was holding the
feature back). The package-level `internal/webhooks`
API is unchanged from v0.7.0; the additions are
the production event flow + the secret-at-rest
hardening + the retry loop.

### Added (webhook call-site wiring, #148)

The v0.7.0 view shipped `Service.Dispatch` as a
tested, wire-ready event hook but no caller invoked
it on the production event flow. v0.7.1 wires
`Dispatch` into every mutating handler in
`internal/{users,plans,nodes,hosts,inbounds,backups}`,
so `user.created`, `plan.deleted`, `node.updated`,
etc. fan out to every endpoint that subscribed to
that event type. Concretely:

- **`webhooks.MustDispatch`** (new helper in
  `internal/webhooks/dispatcher.go`) — non-blocking,
  nil-safe, 5s-bounded-context wrapper. The Service
  calls it AFTER the row is persisted (not before),
  so a receiver that acts on `user.created` sees a
  committed row.
- **`WithWebhooks(svc)` setter** on every
  mutating Service (users / plans / nodes / hosts /
  inbounds / backups) — chosen over a constructor
  argument so the 167+ existing test fixtures stay
  untouched (the dispatch field stays `nil` in
  unit tests, the constructor still takes only the
  Store).
- **Wire payloads** are minimal — `Delete` is
  `map[string]string{"id": "..."}` (no tombstones);
  `backups.Service` fires `backup.created` on
  insert and `backup.completed` / `backup.failed`
  on the terminal state; `users.Service` fires
  `user.updated` on `RotateSubToken` (the closed
  enum has no `user.token_rotated`; an `AddEventType`
  for it is a v0.8 follow-up).
- **`cmd/aegis/main.go`** wires a single
  `webhooksSvc` into all six services. 6 service
  files + `cmd/aegis/main.go` touched, +1120 / -24.
- **`internal/webhooks/spy.go`** (new test helper) —
  cross-package test double wired with a no-op
  HTTP dialer. Records dispatch via the `Delivery`
  rows the Service writes BEFORE the HTTP exchange,
  so the test can assert without any actual HTTP.
  6 `dispatcher_test.go` files (one per service)
  cover the happy path + the nil-safety contract.

### Added (webhook background retry worker, #146)

The v0.7.0 retry schedule (1s, 5s, 25s, 2m15s,
11m15s, hard ceiling 24h, `MaxAttempts = 6`) lived
inside `Service.RetryDelivery`; nobody called it
on a timer. v0.7.1 lands the worker.

- **`webhook_pending_retries` table** (migration
  0017) — `delivery_id UUID PRIMARY KEY REFERENCES
  webhook_deliveries(id) ON DELETE CASCADE,
  attempt INT, next_attempt_at TIMESTAMPTZ, last_error
  TEXT, updated_at TIMESTAMPTZ`. The FK cascade
  means a manual `DELETE` of an endpoint (or a
  `DELETE FROM webhook_deliveries`) drops the
  pending retry row alongside the delivery row.
- **`Store.EnqueueRetry` / `DequeueRetry` /
  `ListDueRetries`** — the three CRUD methods on
  both `MemoryStore` (for unit tests) and `PgStore`.
  `EnqueueRetry` uses `ON CONFLICT (delivery_id)
  DO UPDATE` so re-enqueueing a row that already
  has a pending retry overwrites the schedule
  instead of failing the unique-key check.
- **`internal/webhooks/worker.go`** (new) — the
  goroutine: `for { tick := select next due row;
  ctx, cancel := context.WithTimeout(parent, tick);
  service.RetryDelivery(ctx, row); cancel(); sleep
  min(interval, next_due - now) }`. The per-tick
  context is bounded to the interval so a hung
  HTTP exchange cannot block the next tick.
- **`Service.ProcessDueRetries`** (new) — public
  API the worker calls. Dequeues the OLD row
  (deletes the pending retry on success) and
  re-invokes `deliverSync` which re-enqueues a
  fresh retry row if the new attempt also fails.
- **Config**:
  `AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED` (default
  `true`), `AEGIS_WEBHOOKS_RETRY_WORKER_INTERVAL`
  (default `5s`). The flag is here so a CI test
  that needs to control timing can disable the
  worker without re-architecting the boot path.
- **13 files** changed, +1484 / -7. **19 new
  tests** (72 total in the package, was 53).
  One fixup commit on the PR — the new pg
  integration tests for `EnqueueRetry` initially
  inserted random UUIDs without pre-creating a
  matching `webhook_deliveries` row, so the FK
  constraint rejected them with SQLSTATE 23503.
  Fix: a `seedEndpointAndDelivery(t, s, urlSuffix)`
  helper at the top of `pg_store_integration_test.go`
  that pre-creates the endpoint + delivery so the
  FK is satisfied (see "FK constraint catches test
  bugs early" memory entry).

### Added (webhook age envelope on endpoint secret, #147)

`webhook_endpoints.secret` was stored in plaintext
in v0.7.0. v0.7.1 moves the column to sops+age at
rest while keeping the wire-level redaction
contract (verbatim on Create, `***` on every
subsequent read).

- **Migration 0018** (destructive; live is still
  v0.4.0 so no production data to migrate):
  `ALTER TABLE webhook_endpoints RENAME COLUMN
  secret TO secret_ciphertext; ALTER COLUMN
  secret_ciphertext TYPE BYTEA USING
  secret_ciphertext::BYTEA`.
- **`SecretCipher` interface** (new in
  `internal/webhooks/secret.go`) — the seam. The
  `Store` takes a `cipher SecretCipher` at
  construction; `NewPgStore(pool, nil)` PANICS so a
  misconfigured boot is loud. `NewMemoryStore(clock,
  nil)` does NOT panic (the MemoryStore is the
  test-only path; tests use `NoopSecretCipher`).
- **`AgeSecretCipher`** — the production
  implementation. `filippo.io/age v1.3.1`,
  X25519 + ChaCha20-Poly1305. Multi-recipient
  envelope so a key rotation is a config-only
  change (`AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS`
  is a CSV of `age1...` public keys).
- **`NoopSecretCipher`** — the dev / test
  implementation. Pass-through (encrypt returns
  the plaintext as bytes, decrypt returns the
  bytes as a string). The unit tests do not
  exercise real crypto; the pg integration tests
  use the Noop cipher so the test data stays
  readable in `psql` output.
- **Config**:
  `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` (csv of
  `age1...` recipients) + `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE`
  (path to the X25519 identity file).
- **9 files** changed, +1019 / -20. **10 new
  unit tests** in `secret_test.go` (82 unit tests
  in the package, was 72). One fixup commit on
  the PR — the integration tests initially
  called `testutil.MustNewPool(t)` twice in the
  same test body. The second call deadlocked on
  the `pg_advisory_lock(42)` in `testutil/db.go`.
  Fix: reuse the first pool (`s.pool`) instead of
  acquiring a second one (see "testutil.MustNewPool
  deadlock on 2nd call" memory entry).

### Added (webhook events multi-select in UI, #150)

v0.7.0's `WebhooksView` listed the `events`
column read-only and hard-coded `events: []`
(the all-events wildcard) on Create. The wire
shapes already accepted `events: WebhookEventType[]`
on both POST and PATCH; v0.7.1 surfaces the
field on both dialogs.

- **`frontend/src/components/WebhookEventsPicker.vue`**
  (new) — grid of native checkboxes for the
  18 closed event types, grouped by entity
  (user / plan / node / host / backup / inbound).
  2 cols on mobile, 3 on desktop. The header
  badge shows "N of 18 selected" or the
  "all" wildcard when the value is `[]`.
  Contract: `value` / `change` event so it pairs
  with `useZodForm` via `setFieldValue` without
  depending on a slot inside `FormField` (which
  is built around a single-value input control).
- **`WebhooksView.vue`** — both create and
  edit dialogs render the picker below the
  url / secret / enabled fields with a
  "label and help text" header that matches
  the other `FormField` rhythm. The edit
  dialog's `editTarget` watcher hydrates
  the field with
  `events: values.events` (the server treats
  `[]` as "all" so the wire level matches
  the existing wildcard semantics).
- **i18n** (en + ru) — `webhooks.eventsPicker.heading`,
  `selectedCount`, and `groups.{user,plan,node,
  host,backup,inbound}`.
- **5 files** changed, +236 / -6 (4 src files +
  1 new component).

### Refactored (shared zod schema, #149)

The v0.7.0 `WebhooksView` inlined the create and
edit form schemas inside the view's script block.
v0.7.1 moves them to
`frontend/src/schemas/webhook.ts` so the dialogs
share a single source of truth and so future
features (e.g. event-type multi-select) extend
the schema without re-touching the view.

- **`frontend/src/schemas/webhook.ts`** (new) —
  `webhookEventTypeSchema` (z.enum of the 18
  closed types), `webhookUrlSchema` (http/https,
  10..2048), `webhookSecretSchema` (16..256),
  `webhookCreateSchema` (url, secret, enabled
  and events), `webhookUpdateSchema`
  (`.partial().strict()`; secret is
  `z.union([z.literal(''), webhookSecretSchema])
  .optional()` so an empty string still means
  "leave unchanged" in the edit dialog).
- **`frontend/src/schemas/index.ts`** re-exports
  the new module alongside `user` / `host` /
  `inbound` / `node` / `panelcfg`.
- **`WebhooksView.vue`** drops the `zod` import
  and the inline `createSchema` / `editSchema`
  constants.
- **Side benefit** — the edit form's secret
  field is now validated with
  `webhookSecretSchema`'s 16-character minimum.
  The previous inline schema was
  `z.string().optional().or(z.literal(''))` with
  no length check, which was a latent bug —
  a 1-character secret would have passed
  frontend validation and round-tripped to the
  Go backend. The view's submit handler still
  skips empty strings, so the "leave unchanged"
  path is preserved.
- **3 files** changed, +123 / -29 (refactor,
  no wire-level change).

### Changed (Go+frontend dependency batch + docs sync, #141, #142, #143, #144, #145)

Five sequential PRs (post-v0.7.0) brought every
dependency that was on its previous major /
minor track forward.

- **`chore(deps): bump Go minors` (#141)** —
  `prometheus/client_golang 1.20.5 → 1.24.1`,
  `caarlos0/env/v11 11.2.2 → 11.4.1`,
  `zerolog 1.33.0 → 1.35.1`. 3 explicit + 7
  indirect minor bumps. 0 source code changes.
- **`chore(deps): bump pinia to 4.0.2 and add
  @vue/devtools-api` (#142)** — `pinia
  3.0.4 → 4.0.2`, added `@vue/devtools-api
  ^8.2.1` (pinia 4 peer dep; was transitive
  before). Hit the pnpm-store artifact conflict
  footgun: `node_modules/.pnpm/` from a previous
  pnpm run made `npm install` skip lockfile
  regeneration.
- **`chore(deps): bump vue-tsc to 3.3.8 + fix
  WebhooksView DataTable prop names` (#143)** —
  `vue-tsc 2.x → 3.3.8`. The TS strictness
  upgrade caught a pre-existing prop-name
  mismatch in `WebhooksView.vue`'s `DataTable`
  usage (was passing `empty-message` /
  `loading-message` as data props; the actual
  prop names are `loading` / `empty-key`).
  Fixed in the same PR.
- **`chore(deps): bump vue-i18n to 11.4.8` (#144)**.
- **`docs: sync to v0.7.0 and the post-v0.7.0
  4-PR dependency batch` (#145)** — refreshed
  README / ROADMAP / KNOWN_LIMITATIONS /
  docs/api/index.md / docs/guide/architecture.md
  / CONTRIBUTING.md to reflect v0.7.0 and the
  pre-v0.7.1 dep batch.

## [0.7.2] - 2026-08-02

Three-PR audit-batch closeout. v0.7.2 is purely
internal: no API surface change, no migration
change, no operator-facing configuration change.
The release closes the remaining two findings
from the 2026-08-01 colleague review (audit #1
and audit #2). The package-level `internal/app`
and `internal/cores/builder` are new, the
`cmd/aegis/main.go` shed 556 lines, and the
end-to-end panel→agent pipeline is now exercised
by a real-Postgres integration test.

### Added (real BatchedApplier FlushFn + Enqueue, #157)

The v0.4.0-mvp-batched `cores.BatchedApplier`
shipped with a no-op FlushFn AND Enqueue was
never called outside tests. v0.7.2 wires the
v0.5.0 real path end-to-end:

- New `internal/cores/builder/builder.go`:
  `BuildCoreConfigForNode(ctx, src, nodeID)`
  turns the panel's inbounds table into a
  `cores.CoreConfig` (disabled inbounds
  skipped; nil `Params` maps to an empty map).
  `NewFlushFn(src, renderer, nodeID, name)`
  returns the per-node closure the
  BatchedApplier calls: `Build → Render → Apply`
  with structured error logging.
- `users.Service.WithBatchApplier(map)` +
  `enqueueUserDelta(Delta)` fan out to every
  registered applier. `Create` → `DeltaAddUser`,
  `Update` → `DeltaAddUser` OR
  `DeltaSetLimit{Bytes: TrafficLimitBytes}`
  (JSON `{"bytes": <int64>}` payload) when
  `in.TrafficLimitBytes` is the only changed
  field, `Delete` → `DeltaRemoveUser`,
  `RotateSubToken` → `DeltaAddUser`.
- `inbounds.Service.WithBatchApplier(map)` +
  `enqueueForNode(nodeID, kind)` narrows the
  fan-out to the single applier for the
  inbound's node (the inbound already carries
  the node reference). `Create`/`Update` →
  `DeltaAddUser{UserID: uuid.Nil}`,
  `Delete` (with pre-fetch of `prev.NodeID`)
  → `DeltaRemoveUser{UserID: uuid.Nil}`. The
  `UserID: uuid.Nil` on inbound deltas is the
  BatchedApplier's coalescing contract:
  "inbound change" is not user-scoped, and
  the appliers' last-write-wins under
  `uuid.Nil` collapses multiple inbound CRUD
  events in the same window to one flush.
- `App.BatchedAppliers map[uuid.UUID]*cores.BatchedApplier`
  and `App.AddNodeBatchedApplier(ctx, nodeID, name, flushFn)`
  (registers the per-node applier, spawns the
  `Run` goroutine, owns the cancel funcs).
  `App.Close()` cancels every BatchedApplier
  goroutine alongside the existing webhook
  worker cancel and pg pool close.
- `cmd/aegis/main.go` `singboxWiring` now takes
  `*app.App` and gates on
  `cfg.BatchedApplierEnabled`. The two
  `WithBatchApplier` calls run BEFORE the
  per-node loop so a Create handler that fires
  during boot enqueues into a fully-built map.
  The flag-off path returns nil after
  `Configure()`: no appliers, no goroutines,
  no fan-out (operator escape hatch for
  Ansible/Terraform-managed installs).
- New env var `AEGIS_BATCHED_APPLIER_ENABLED`
  (default `true`).

Phase 1 caveat (documented in PR body): the
sing-box renderer is single-user per inbound
(operator's credential in `inbound.Params`).
The FlushFn re-renders the same config on
every flush until the inbound-templates work
lands. The infrastructure
(BatchedApplier + Enqueue + FlushFn) is the
deliverable; a future Phase 2 PR fills in
the per-user mapping. The agent's diff is
what determines whether the file on disk
actually changes.

### Added (end-to-end integration test, #158)

`backend/internal/cores/builder/flushfn_integration_test.go`
behind `//go:build integration`. Self-skips
when `INTEGRATION_DATABASE_URL` is unset
(local `go test ./...` skips; CI's backend
job runs it). The headline test
`TestIntegration_EndToEnd_RealPgCreateUserTriggersApply`
drives a real `users.Service.Create` against
a real pg (via `testutil.MustNewPool`); the
post-commit enqueue reaches the per-node
BatchedApplier; the 200ms window fires; the
FlushFn re-renders the sing-box config
(reading through the inbounds PgStore); the
fake agent receives a POST /v1/apply whose
JSON envelope contains exactly the vless
inbound we seeded with the UUID we put in
`inb.Params`. The test pins the panel→agent
wire contract end-to-end. The earlier
`flushfn_smoke_test.go` covers the
MemoryStore path (no pg); the new test is
the only place a "the panel wrote to pg and
the FlushFn picked it up via SELECT"
regression surfaces.

### Changed (composition root extracted from main.go, #156)

`cmd/aegis/main.go` went from 728 lines to
199 (the audit #1 "God-object main.go" fix).
The composition root moved to a new
`internal/app` package exposing a single
`Build(ctx, cfg) (*App, error)` plus an
`App.Close()`. The pattern matches the wire
sweet-spot (too small for google wire's
codegen payoff, just right for a generic
`MustBuild[T]` helper with a
`StoreBuilder[T]` struct + centralized
production-vs-memory check). 11 services are
wired through `MustBuild` (auth, nodes,
inbounds, hosts, users, plans, subscription,
panelcfg, audits); two are one-offs (webhooks
for the age cipher dependency, backups for
the OSBackend). The `internal/app/app_test.go`
smoke verifies every service handle is wired
with all-memory backends and that
`App.Close()` is idempotent.

`cmd/aegis/main.go` keeps only the cmd-level
concerns: logger setup, subcommand dispatch
(`aegis migrate`, `aegis admin`), the singbox
wiring path, signal handling, and graceful
shutdown. `router.Build` now takes a
`ctx context.Context` as the first parameter
(used for the `panelcfgSvc.GetActive` read at
the rotated sub_path mount, was hardcoded
`context.Background()`); the boot context
applies, so a SIGINT during boot aborts the
read.

### Closed (audit batch, 2026-08-01 colleague review)

The 2026-08-01 colleague review raised six
findings. v0.7.1 closed audit #3 (UI tests via
PR #155), audit #4 (promptPassword echo via
PR #154), and audit #6 (state enum regression
guard via PR #153). v0.7.2 closes the remaining
two:

- **#1 God-object main.go** — closed by #156.
  `main.go` is now 199 lines (was 728). The
  composition root, the per-service store
  selector, and the cross-cutting wiring
  (webhooks worker, batched appliers) live on
  `*app.App` with a clean lifecycle in
  `App.Close()`.
- **#2 BatchedApplier no-op stub** — closed by
  #157 + #158. The FlushFn now re-renders
  the node config and POSTs it to the agent.
  Enqueue is called from every user/inbound
  mutation. A real-Postgres integration test
  pins the end-to-end pipeline.
- **#5** — was a numbering artifact (the
  colleagues' review went #1, #2, #3, #4, #6
  with no #5). No action required.

### Changed (Go+frontend dependency batch, post-v0.7.1)

The PRs in this batch landed on `main`
*after* the v0.7.1 git tag and are picked
up by v0.7.2. None are application-code
changes; all are infrastructure or
regression-guard fixes:

- `fix(nodes): pin State enum to migration
  0006 with a regression guard` (#153,
  closed audit #6) — already in v0.7.1
  CHANGELOG; cross-referenced here for
  completeness.
- `fix(cli): suppress echo on aegis admin
  password prompts` (#154, closed audit #4)
  — `golang.org/x/term v0.45.0`; the
  `promptPassword` helper opens `/dev/tty`
  directly and calls `term.ReadPassword` so
  the kernel toggles `ECHOCTL`/`ICANON`. The
  non-tty path keeps the legacy
  `bufio.Reader` for the `aegis admin add`
  automation in `deploy/ansible/`.
- `test(ui): add vitest suite for zod schemas`
  (#155, closed audit #3) — 38 vitest tests
  across `primitives.ts` + `user.ts` +
  `webhook.ts`; `npm run test` uncommented
  in `.github/workflows/ci.yml`.
- `refactor(backend): extract internal/app.Build
  from main.go` (#156) — the audit #1 fix
  described above.
- `feat(cores): real BatchedApplier FlushFn +
  Enqueue` (#157) — the audit #2 fix
  described above.
- `test(cores): end-to-end integration test
  for BatchedApplier + FlushFn` (#158) —
  the test that closes audit #2 end-to-end.

### Fixed (gofmt nit, #158)

`backend/internal/cores/builder/flushfn_integration_test.go:267`
was not aligned to gofmt's preferred column.
Amended + `gofmt -w` + force-push. The CI's
golangci-lint + gofmt job is the canonical
formatter; local `gofmt -w` after every
test file edit is the right pattern.

### Not changed (v0.7.2 vs v0.7.1)

- **No API surface change.** `docs/openapi.yaml`
  is still at `0.7.0`. The `/webhooks/*`,
  `/plans/*`, `/users/*`, `/hosts/*`, and
  `/nodes/*` shapes are byte-for-byte
  identical between v0.7.1 and v0.7.2. The
  frontend `npm run codegen:check` job
  passes without a regeneration.
- **No migration change.** `migrations/0001..0018`
  is byte-for-byte identical between v0.7.1
  and v0.7.2. The schema-version string in
  the audit_log row is unchanged.
- **No operator-facing configuration change.**
  The only new env var is
  `AEGIS_BATCHED_APPLIER_ENABLED` (default
  `true`), and it is opt-out for operators
  who run an external config manager
  (Ansible, Terraform) and want to prevent
  the panel from clobbering the
  externally-managed config.

## [0.8.0] - 2026-08-02

v0.8.0 is the **Phase 2 multi-user sing-box render
milestone** plus a frontend dependency batch
and the audit-log call-site wiring. The
production API surface is unchanged (the
OpenAPI spec is still at `0.7.0`); every
change is either internal infrastructure or
new migration tables that the admin surface
will query in a follow-up HTTP PR. v0.8.0 is
**end-to-end multi-user**: an operator can
issue per-(user, inbound) credentials via the
admin surface (the HTTP layer is the next
slice), and the running config + the per-user
sub URL both pick up the per-user credential
automatically. The sing-box renderer emits
multi-user `users: [...]` arrays; the
BatchedApplier fan-out is narrowed by the
user's `HostsAllowlist` / `Blocklist`; the
phase 1 (single-operator-credential) path is
preserved as the fallback when a user has no
per-inbound credential yet.

The 9 PRs:

- **#159** `chore(frontend-deps): bump ts/types toolchain` — `@types/node` 22.12.0 → 26.1.2; `@vue/tsconfig` 0.7.0 → 0.8.1; `typescript` 5.6.3 → 5.8.3 (forced by the 0.8.x peer dep that enables `libReplacement: false`); `prettier` 3.4.1 → 3.9.6; `globals` 17.7.0 → 17.8.0. Also fixed two latent type errors in `PlansView.vue` (the `noUncheckedIndexedAccess` strictness tightened by the bump) and the README.md MD004 dash→plus bullet-style fix.
- **#161** `chore(frontend-deps): bump css/sass toolchain` — `autoprefixer` 10.4.27 → 10.5.4; `postcss` 8.5.19 → 8.5.24; `sass` 1.101.0 → 1.102.0.
- **#163** `chore(frontend-deps): bump axios 1.18.1 → 1.19.0` (CVE-2026 GHSA-hmw2-7cc7-3qxx; closes the CRLF-injection path via the `form-data@^4.0.6` floor). Bundle +2.46 kB raw / +0.82 kB gzipped for the new `AxiosHeaders.parseParameters` and 520 status code support.
- **#165** `chore(frontend-deps): bump @vue/tsconfig 0.8.1 → 0.9.1 + postcss 8.5.24 → 8.5.25` — closes the `verbatimModuleSyntax` strictness without source changes (the codebase already used `import type` correctly).
- **#166** `feat(audits): wire audit_log call-sites into every mutating service` — the v0.6.0/v0.7.0 audit surface finally gets every `Create` / `Update` / `Delete` audited. `audits.RecordFromContext(ctx, svc, e)` Service-layer mirror of the existing `RecordFromRequest`; pulls actor from `auth.ClaimsFromContext`; IP/UA blank. Six services: `users`, `plans`, `nodes`, `hosts`, `inbounds`, `backups`. Pre-fetch for audit `Before` on `users.Service.Delete` + `plans.Service.Delete` (extra round-trip; same trade-off as PR #157 / PR #167). **Closes the v0.7.x KNOWN_LIMITATIONS entry "Audit log call-site wiring — v0.7.x+".**
- **#167** `feat(credentials): Phase 2 multi-user sing-box render data model` — new `user_inbound_credentials` table (migration 0019): `id UUID PK, user_id FK→users ON DELETE CASCADE, inbound_id FK→inbounds ON DELETE CASCADE, credential_value TEXT NOT NULL, created_at, updated_at, UNIQUE (user_id, inbound_id)` + 2 indexes. New `internal/credentials/` package: `Credential` struct, `Store` interface (`Insert/Update/Delete/GetByID/ListByUser/ListByInbound`), `MemoryStore` (Phase 0), `PgStore` (SQLSTATE 23505 → `ErrDuplicate`, `pgx.ErrNoRows` → `ErrNotFound`), `Service` with `Create/Get/ListByUser/ListByInbound/Rotate/Delete` + `WithAudits(svc)` setter, all mutating methods call `audits.RecordFromContext` with `credential.create` / `credential.rotate` / `credential.delete` actions. Wired into `internal/app` (`a.Credentials` field, `AEGIS_CREDENTIALS_BACKEND` env, `needsPg` registration). 24 unit tests. **Phase 2 multi-user — step 1 of 4 done.**
- **#168** `feat(cores): multi-user sing-box renderer (Phase 2 step 2)` — the sing-box renderer's per-protocol signatures take a per-(user, inbound) credential list (`renderVLESS(spec, params, users)`, `renderHY2(spec, params, users)`, `renderTrojan(spec, params, users)`). When `users` is non-empty, the renderer emits a multi-user `users: [{name, uuid|password}, ...]` array of length N. When empty (Phase 1 path), the renderer falls back to `params["uuid"]` / `["password"]` and emits a length-1 array. `renderShadowsocks` is unchanged (single-password protocol by design). New `ExperimentalInboundCredentialsKey` constant + `extractCredentialsByTag` helper (defensive: missing key, wrong-typed value, wrong-typed per-tag entry all fall through to the Phase 1 path). 5 new tests + 28 existing tests unchanged (Phase 1 path is byte-identical to v0.7.2). **Phase 2 multi-user — step 2 of 4 done.**
- **#169** `feat(credentials+cores): wire credentials through builder and narrow BatchedApplier fan-out (Phase 2 step 3)` — the panel-side wiring of Phase 2. New `ListCredentialsByInbound` source interface on `internal/cores/builder`; `BuildCoreConfigForNode` and `NewFlushFn` take `credSrc`; for every enabled inbound, the builder queries credentials, dereferences `*Credential` → value slice, populates `cfg.Experimental[ExperimentalInboundCredentialsKey]`. Per-inbound query failures are fail-soft (log + Phase 1 fallback). `users.Service.enqueueUserDelta(d, user)` filters the BatchedApplier map by `user.HostsAllowlist` and `user.HostsBlocklist`. Blocklist wins over allowlist. Empty allowlist + empty blocklist = default allow (v0.5.0 behaviour). 4 call sites updated: Create/Update/RotateSubToken pass `out`, Delete passes `cur`. New `BatchedApplier.QueueLen()` method (enqueue-pressure metric, also used by the new tests). 4 new builder tests + 5 new fan-out tests. **Phase 2 multi-user — step 3 of 4 done.**
- **#170** `feat(subscription): per-user credential render (Phase 2 step 4)` — the per-user sub URL is the per-user cabinet. `subscription.Service` gains `creds *credentials.Service` + per-render `userCreds map[inboundID]credentials.Credential` cache. `WithCreds(svc)` setter (nil-safe, mirrors `WithAudits` / `WithWebhooks` pattern). `precomputeUserCreds(ctx, u)` does ONE `ListByUser` call per render (not one per inbound). `RenderSingbox` and `RenderClash` thread the per-endpoint `userCred` into the per-protocol builders (`buildSingboxVLESS`, `buildSingboxHysteria2`, `buildSingboxTrojan`, `buildClashVLESS`, `buildClashHysteria2`, `buildClashTrojan`). Each builder uses `userCred` when non-empty, falls back to `params["uuid"]` / `["password"]` when empty. Shadowsocks unchanged. 4 new tests including the auth-boundary `TestRenderSingbox_Phase2_OtherUserCredNotLeaked`. **Phase 2 multi-user — step 4 of 4 done. End-to-end.**

What this means for operators:

- The panel can now issue per-(user, inbound) credentials via the admin surface. The HTTP layer that exposes the credential CRUD to the admin UI is a follow-up PR; v0.8.x. The `user_inbound_credentials` table is the data layer; the Service + Store are ready.
- The BatchedApplier already pulls per-user credentials into the rendered sing-box config (when the Builder is populated by a future PR that queries the credentials table; the infrastructure is in place). For now, the Builder's `credSrc` is `a.Credentials` (set in #169); the data flow is end-to-end.
- The per-user sub URL is per-user. The same `sub_token` now resolves to a config that uses the user's own UUID/password, not the operator's. The `?target=singbox` and `?target=clash` formats both honour the new per-user auth material; the `?target=base64` and `?target=html` formats are URL-list + wrapper (no auth material in the body) and are unchanged.
- The `WithCreds(nil)` setter is the v0.7.2 migration path: a panel that has not yet installed the credentials source keeps its v0.7.2 output byte-for-byte. Operators onboard users gradually, populating the credentials as they go.

What this PR does NOT ship (deferred to v0.8.x follow-ups):

- **HTTP admin surface for `user_inbound_credentials`** — the Service + Store are ready; the AdminRouter (`/api/v1/credentials/` mount) and the OpenAPI spec are a separate PR. The admin UI (Credentials tab in the user detail page) lands with the HTTP layer.
- **Host → node mapping in the Builder-side filter** — the Builder does not filter credentials by `user.HostsAllowlist` today. The user-level filter is in `users.Service.enqueueUserDelta` (which decides which nodes get a FlushFn re-render). A host-to-inbound mapping is a future PR that will let the Builder filter at render time as well.
- **Cosign sign + verify for our Docker images** — still v0.5.x follow-up. v0.7.0 closed the `latest` tag + cosign sign/verify pair for the panel and agent images, but the post-v0.7.0 workflow contract (PRs 102/103/104/111) does not yet include cosign re-signing on every release. Tracked separately.
- **JSON logs in production** — `AEGIS_ENV=production` switch is still the v0.5.x follow-up. The `internal/obs` package has the right code; the wiring in `cmd/aegis/main.go` is the one-liner that was deferred from v0.5.0. v0.8.x.
- **Smoke test on fresh VM in CI** — v0.9.0 candidate. `tools/scripts/smoke-local.sh` (PR #152) covers the local docker-compose path; a terraform + ansible + boot-log CI job is a separate work unit.
- **`internal/cabinet` (doc.go-only) end-user surface** — the per-user sub URL is the per-user cabinet for v0.8.0. A separate end-user-facing cabinet (login UI, sub URL fetch, traffic stats, plan change) is v1.2+.

## [0.8.1] - 2026-08-04

v0.8.1 is the **auto-deploy bootstrap batch**: a
shared age-encryption envelope package, a
frontend dependency CVE fix, password-based first
auth for the BYO Node flow with persistent panel
key reuse, and the matching three-way radio in the
admin UI. Net effect: an operator clicks "+ Add node",
fills in name / region / SSH address / domain,
pastes the VPS root password, clicks submit — the
panel SSHes in once, installs the agent, and
generates an ed25519 keypair that the panel
re-uses on every subsequent re-provision. The
operator never has to paste a key.

The four PRs:

- **#177** `refactor(crypto): extract internal/crypto/envelope from webhooks/secret` — the v0.7.x age cipher (`SecretCipher` interface, `AgeSecretCipher`, `NoopSecretCipher`) was webhook-specific. v0.8.1 lifts it into a shared `internal/crypto/envelope` package so any future at-rest secret can share the same age boundary and the same `AEGIS_*_SECRET_AGE_*` env vars. The webhooks `PgStore` now imports `envelope.SecretCipher` instead of declaring its own interface. The bootstrap service will use the same cipher in #179. Pure refactor: byte-for-byte identical output for every input, no schema change, no env change. 9 files changed, +578 / -536.
- **#178** `chore(frontend-deps): bump brace-expansion 5.0.8 → 5.0.9` (CVE GHSA-rgw5-rvv9-x895, ReDoS in `expand`). `package.json` `overrides` block: `^5.0.8` → `^5.0.9`. Lockfile + integrity hash updated. 2 files, +4/-4. One-line security fix; dev-only (the `expand` function is reached through `glob` → `vitest` / `eslint` tooling, not the production browser bundle).
- **#179** `feat(bootstrap): password-based first auth + persistent node SSH key` — the BYO Node flow. v0.3.0 required the operator to paste a PEM private key on the provision form. v0.8.1 adds two new modes. **First-install via password**: the operator pastes the VPS root password; the panel SSHes in once, installs the agent, and the agent switches to bearer-token auth (password is one-shot, never stored). **Persistent panel key**: on a successful password install, the panel generates an ed25519 keypair, encrypts the private half with the operator's age envelope, stores the ciphertext in a new `nodes.ssh_private_key_ciphertext` column (migration 0020), and pushes the public half to `$HOME/.ssh/authorized_keys` on the node. The next re-provision decrypts the stored key and uses it for the install — the operator never pastes anything. The wire format is the two-field XOR `ssh_private_key` / `ssh_password` (mutually exclusive, both optional; the Go provisioner picks the auth method by precedence: stored key > request key > request password). 13 files changed, +1117 / -57. Migration 0020 (BYTEA column, default empty bytes — the "no key yet" sentinel).
- **#180** `feat(ui): password / stored-key radio for the node provision form` — the matching UI. The provision dialog gets a three-way radio (key / password / stored) at the top. Conditional rendering: the key or password field appears below the radio based on the selected method. XOR + conditional-required validation in the zod schema's `superRefine`. The "Stored panel key" option is disabled for first-time installs (state `new`); it is enabled for re-provisions (state `offline`). The form's default auth method is "Stored panel key" for state `offline` (a re-provision is literally one click — no input). New i18n strings in en + ru. 4 files changed, +324 / -12.

What this means for operators:

- The BYO Node flow is now "auto-deploy": the operator does not need to paste a private key on every re-provision. The panel generates a keypair on first install, encrypts the private half with the age envelope, and re-uses the key for every subsequent install.
- The first-time install path is "paste the VPS root password" instead of "generate a key, copy it to the node, paste it in the form". The operator's mental model is "the panel does the rest" — the password is one-shot and never stored.
- A v0.3.0..v0.7.x node that was provisioned before this PR has an empty `nodes.ssh_private_key_ciphertext` and no panel key on the agent. Re-provisioning such a node on v0.8.1 takes the "operator-supplied key" path (the operator pastes their existing PEM) until a follow-up CLI command (`aegis admin node rotate-panel-key <id>`) lands; deferred.

What this PR does NOT ship (deferred to v0.8.2/v0.8.3):

- **BatchedApplier decrypt-and-use path for the stored panel key** — the applier reads `nodes.ssh_private_key_ciphertext` and decrypts via the envelope to authenticate POST /v1/apply. v0.8.x.
- **Re-provision path for v0.3.0..v0.7.x nodes** — a CLI command + UI button that generates a fresh panel key for an existing node (uses the operator's current key as the bootstrap credential, then rotates). v0.8.x.
- **Host → node mapping in the Builder-side filter** — the `user.HostsAllowlist` filter is on the BatchedApplier fan-out (post-#169). The Builder-side filter on the rendered config (so a user's render does not include credentials for inbounds on hosts the user cannot see) needs the host-to-inbound mapping to be modelled; v0.8.x.
- **"Show me the stored public key" debug surface** — the operator can paste a private key, but there is no "what is the panel's key on this node right now" debug view. The public-key fingerprint would be safe to display; deferred.
- **Merged "Add node + Provision" dialog** — the 2-step shape (Create, then Provision) is preserved. A merged dialog with the auth method radio pre-selected per state is a UX follow-up; v0.8.x.
- **shadcn-vue RadioGroup primitive** — the radio group in #180 is hand-rolled. The codebase does not yet have `RadioGroup` in `components/ui/`; adding the primitive is a separate task.
- **Cosign sign + verify for our Docker images on every release** — the initial sign + verify pair shipped in v0.7.0; the post-v0.7.0 workflow contract does not yet include re-signing. v0.8.x.
- **JSON logs in production** — `AEGIS_ENV=production` switch is still the v0.5.x follow-up. The `internal/obs` package has the right code; the wiring in `cmd/aegis/main.go` is the one-liner that was deferred from v0.5.0. v0.8.x.
- **Smoke test on fresh VM in CI** — v0.9.0 candidate. `tools/scripts/smoke-local.sh` covers the local docker-compose path; a terraform + ansible + boot-log CI job is a separate work unit.
- **`internal/cabinet` (doc.go-only) end-user surface** — the per-user sub URL is the per-user cabinet for v0.8.0. A separate end-user-facing cabinet (login UI, sub URL fetch, traffic stats, plan change) is v1.2+.

## [Unreleased]

The next-up work is captured in [`docs/ROADMAP.md`](docs/ROADMAP.md). The
detailed per-PR notes that used to live in this section have been
migrated to the matching tagged release sections:

- v0.5.0 content (`#119`-`#126`) — moved to `[0.5.0] - 2026-07-30` below
- v0.4.0-post content (`#102`, `#103`, `#104`, `#111`) — folded into `[0.4.0]`
- v0.7.x Go+frontend dep batch (`#141`-`#144`) — duplicate of the
  `[0.7.1]` section; removed

## [0.5.0] - 2026-07-30

Eight-PR operations-grade polish batch. Closes the
"v0.5.0 is the smallest surface that the soft
launch needs" target from the v0.4.0 release
notes. The release is purely additive: no API
surface change, no operator-facing configuration
change, no migration. The `secrets via sops+age`
indirection, the `backups` package + UI + CLI, the
`pre-pr.sh` local gate, and the
`install_singbox` runtime SHA-256 lookup are the
four pillars; the operator guide + security
policy + quickstart docs land the soft-launch
documentation contract; the container-wiring PR
binds #119 into the panel + agent systemd units.

### Added (operator guide + security policy + quickstart docs, #126)

- **`docs/operator-guide.md`** (new) — the
  canonical "from a fresh VPS to a panel that serves
  real users" reference. Audience, TL;DR, prerequisites,
  architecture-in-one-screen, install path, secrets
  management, first node, daily operations (backups,
  restores, rotations), disaster recovery, upgrades,
  observability, common pitfalls. Cross-links to
  `deploy/secrets/README.md` for the sops+age
  field-by-field workflow.
- **`docs/SECURITY.md`** (new) — the threat model and
  disclosure flow. Sections: reporting a vulnerability
  (GitHub Security Advisories), supported versions,
  threat model (what Aegis defends against and what it
  does not), cryptography (JWT, age, sing-box, backup
  integrity), container isolation (distroless, nonroot,
  read-only secrets.env, loopback port), privilege
  boundaries (aegis-deploy / aegis-agent / \_sing-box),
  supply chain (panel images, sing-box, sops+age), what
  to do if a compromise is suspected.
- **`docs/guide/quickstart.md`** (new) — the 5-minute
  "fresh VPS to panel running" flow. Promoted out of
  the in-line "Operator quickstart (v0.5.0+)" section
  in `getting-started.md` and expanded with the backup
  cron entry.
- **`docs/guide/getting-started.md`** — refreshed to
  the dev-stack entry (Postgres + Redis + NATS on a
  laptop). The old operator-quickstart section is
  gone; the new `quickstart.md` is the operator
  entry. Adds a `make pre-pr-install` line so
  developers hit the local CI gate on first checkout.
- **`docs/guide/index.md`** — adds links to
  `quickstart`, `operator-guide`, and `security`.
- **`docs/README.md`** — `Where to start` block
  reorders the operator path to the top (quickstart
  → operator guide → security), with the dev path
  below. The `Project status` table is updated to
  v0.5.0 reality (the v0.5.0 row is now `✅ shipped`
  with the per-component status).
- **`docs/developer/index.md`** — branch pattern
  updated (`feat/<scope>/<name>`, `chore/<scope>/<name>`,
  `fix/<scope>/<name>`, `refactor/<scope>/<name>`;
  the `develop` branch is gone). Adds a
  `Pre-PR local gate` section with the
  `tools/scripts/pre-pr.sh` scope flags. Adds a
  `Module overview` table with the new CLI binaries
  and the v0.5.0 packages.
- **`docs/ROADMAP.md`** — v0.5.0 row updated to
  `✅ shipped (#119, #120, #121, #122, #123, #124,
  #125, #126)`. The v0.5.0 scope section
  reorganised to "All eight items landed" with a
  per-item PR cross-reference; the items that did
  not land (JSON logs, cosign, VM smoke test,
  GPG-verify) are listed in a "Deferred" sub-section
  with the v0.5.x follow-up path.
- The VuePress site (`docs/.vuepress/config.ts`)
  sidebar is **not** updated — the site is local-only
  and not published until v1.0.0. The new pages are
  reachable via direct paths in the rendered HTML.

### Added (aegis-pg-backup + aegis-pg-restore CLI, #125)

- **`feat(cli): operator-side backup CLI`** —
  two new binaries under `cmd/` that call the
  `backups.Service` directly, bypassing the
  panel's HTTP surface. The canonical
  cron-friendly entry point for the
  operator's own scheduler (`crontab`,
  systemd-timer, etc.).
  - `cmd/aegis-pg-backup/main.go` (~250 LOC) —
    five subcommands: `list`, `get <id>`,
    `create [--trigger manual|scheduled]`,
    `delete <id>`, `download <id> <path>`.
    Every subcommand writes a single JSON
    value to stdout and exits 0; errors
    go to stderr in `{"error":"..."}` shape.
    Reads `AEGIS_BACKUPS_DIR` (default
    `./var/backups`) and `AEGIS_POSTGRES_DSN`
    (required for `create`). The `download`
    subcommand refuses to write into the
    backups dir itself (a typo by the
    operator would otherwise overwrite a
    managed dump with itself).
  - `cmd/aegis-pg-restore/main.go` (~200 LOC) —
    a SEPARATE binary from `aegis-pg-backup`
    so the safety boundary is enforced at
    the process level. The CLI surface is
    one positional arg (`<id>`) plus
    `--yes` / `--dry-run` flags. Two-step
    confirmation: the operator must type
    the backup id again before the
    destructive op runs. Reads
    `AEGIS_BACKUPS_ALLOW_UI_RESTORE` as a
    sanity check (the DSN is the actual
    security boundary; the flag catches
    a typo in the operator's
    `EnvironmentFile`). `--dry-run` runs
    `pg_restore --list` for an eyeball
    check.
  - `.gitignore` — added the
    `.git-commit-*.md` pattern (alongside
    the existing `.git-commit-*.txt`) so
    the commit-message draft files don't
    sneak into the working tree.

- **Why two binaries, not one with a
  `restore` subcommand:** restore is
  destructive (drops and recreates the
  target database). Keeping the binaries
  separate enforces the safety boundary at
  the process level: an operator who
  types `aegis-pg-backup restore <id>`
  gets an `unknown subcommand` error, not
  a silent data wipe. The `aegis-pg-backup`
  binary is the safe default; the
  `aegis-pg-restore` binary is the
  intentional one-off path.

- **What this PR does NOT ship** (deferred to
  follow-ups):
  - The HTTP-level restore endpoint is still
    `ScopeBackups` + `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
    gated (see #120). The CLI is the
    operator-only path; the UI path is
    intentionally NOT exposed in v0.5.0.
  - A `restore --to <timestamp>` flag
    ("point-in-time recovery from the
    archive") — would need a separate
    basebackup + WAL-replay workflow. v0.5.x
    follow-up.
  - shell completion (`complete -C
    "aegis-pg-backup list" ...`). Cosmetic
    but useful for a daily-driver CLI.

- Verification:
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `golangci-lint run ./cmd/aegis-pg-backup/
    ./cmd/aegis-pg-restore/` — 0 issues
    (errcheck, errorlint, gosec, gofmt all
    caught and fixed in the same PR cycle)
  - `go test -count=1 ./...` — all existing
    tests pass (no new test files; the
    binaries are CLI-thin and the
    underlying `backups.Service` is
    already covered by the #120 tests)

### Added (container wiring for #119 secrets, #124)

- **`chore(ops): install_panel role + prod
  compose + secrets.env mount`** — wires
  the v0.5.0 sops+age indirection from #119
  into the actual production deploy. The
  `configure_secrets` role (PR #119) writes
  `/etc/aegis/secrets.env` (mode 0600,
  owner aegis-deploy) on the panel host.
  This PR adds the three pieces that consume
  that file end-to-end:
  - `deploy/docker/docker-compose.prod.yml`
    (new, ~80 lines) — the production panel
    stack. Pulls `ghcr.io/qadversif/aegispanel:${AEGIS_PANEL_IMAGE_TAG}`
    (default `latest`), bind-mounts
    `/etc/aegis/secrets.env:ro` into the
    container via `env_file:`, bind-mounts
    `/var/lib/aegis` for the future backups
    volume, and publishes the panel's port
    8080 on the loopback only
    (`127.0.0.1:8080:8080`) — the reverse
    proxy (Caddy or any other) is the
    public ingress. The data services
    (Postgres, Redis, NATS) are NOT
    managed by this compose; the operator
    provisions them out-of-band (managed
    RDS, a sibling compose, etc.) and
    wires the panel's `aegis.postgres_dsn`
    in the sops+age secrets file.
  - `deploy/ansible/roles/install_panel/`
    (new, three files: defaults + tasks +
    handlers) — refuses to run without
    `/etc/aegis/secrets.env`, drops the
    compose file in `/etc/aegis/`, pulls
    the image, starts the stack, and
    prints `docker compose ps` as a
    summary. Idempotent: re-runs are no-ops
    (compose pull + up skip when the
    stack is already at the desired state).
  - `deploy/ansible/playbooks/panel.yml`
    (new) — the three-role canonical
    deploy for a panel host:
    `bootstrap_node` → `configure_secrets`
    → `install_panel`. Operators pin
    `aegis_panel_image_tag` in
    `group_vars/all.yml` to a stable
    release (e.g. `0.5.0` — note: no `v`
    prefix, per the release workflow
    rewrite in #111).
  - `deploy/ansible/roles/install_agent/files/aegis-agent.service`
    — added a secondary
    `EnvironmentFile=-/etc/aegis/secrets.env`
    line (the leading `-` tells systemd to
    silently skip a missing file). On
    panel hosts with `configure_secrets`
    this means the agent picks up any
    future AEGIS_* secret from the
    canonical source; on dev hosts that
    do not run `configure_secrets` the
    service is unaffected. Per-node
    values in `agent.env` still take
    precedence over panel-level values
    in `secrets.env` on a key collision
    (later env vars in the same file
    override earlier ones).

- **Why this PR does NOT also provision the data
  services:** the panel's data layer (Postgres,
  Redis, NATS) is operator-managed. The panel
  already speaks the canonical pgx DSN /
  `redis://` / `nats://` URL shape; the sops+age
  secrets file is the canonical place to set
  them. A future PR can ship a sibling
  `docker-compose.data.yml` for operators that
  want a single-host dev/prod path; v0.5.0 ships
  panel-only.

- **What this PR does NOT ship** (deferred to
  follow-ups):
  - The reverse proxy (Caddy) is still installed
    per-node by `install_caddy`. A future PR adds
    a panel-side Caddy that fronts the panel
    container on `127.0.0.1:8080`.
  - A healthcheck for the panel container (the
    distroless image has no shell; a v0.5.x
    follow-up ships a tiny healthcheck binary
    inside the image, or a sibling `wget` shim
    via buildx).
  - The `/var/lib/aegis` bind mount is reserved
    for the v0.5.x backups volume; the current
    PR mounts the directory but the panel does
    not yet write to it.

### Changed (singbox install role — runtime SHA-256 lookup, #123)

- **`chore(ops): install_singbox looks up the
  SHA-256 via the GitHub Releases API`** —
  the v0.4.0-c hardcoded `aegis_singbox_sha256`
  default is gone. Bumping `aegis_singbox_version`
  in `group_vars/all.yml` is now a one-line
  change; the role queries the GitHub Releases
  API at install time, picks the `assets[]`
  entry whose `name` matches the per-arch
  tarball, and uses the `digest` field
  (format `sha256:<hex>`) as the `get_url
  checksum:` argument.
  - `deploy/ansible/roles/install_singbox/defaults/main.yml` —
    removed `aegis_singbox_sha256`; added
    `aegis_singbox_release_api_url` (default
    `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v{{ version }}`)
    and `aegis_singbox_release_api_token`
    (optional, for rate-limit headroom on
    busy CI matrices).
  - `deploy/ansible/roles/install_singbox/tasks/main.yml` —
    replaced the `Refuse to run without a
    SHA-256 pin` assert with two tasks:
    `Look up the sing-box SHA-256 via the
    GitHub Releases API` (3 retries, 5s
    delay, optional Bearer auth) and
    `Extract the SHA-256 of the target tarball
    from the API response` (filter by name,
    strip the `sha256:` prefix, fail with
    "no asset" if the arch is missing for
    the version). The rest of the pipeline
    is unchanged — the `get_url checksum:`
    field still pins the download.
  - `docs/guide/getting-started.md` — added
    an `Operator quickstart (v0.5.0+)`
    section that walks the `playbooks/panel.yml`,
    plus `playbooks/node.yml` two-step install
    flow and points the operator at the
    sops+age indirection from #119.

- **Why no GPG / SHA256SUMS verification** —
  the original scope included a detached
  signature check. Research during this
  PR showed that SagerNet does NOT publish
  `SHA256SUMS` or detached GPG/minisign
  signatures for sing-box GitHub releases
  (the only integrity metadata is the
  per-asset `digest` field in the API
  JSON). The trust model is therefore the
  GitHub API response itself, which is
  authenticated by the standard
  `X-GitHub-...` headers and TLS. Cosign
  signing of our own Docker images
  (panel + agent) is the v0.5.x
  equivalent for the panel/agent supply
  chain and is a separate, future PR.

- Operator-visible changes:
  - No more `aegis_singbox_sha256` in
    `group_vars/all.yml`. The role no longer
    reads or writes this variable.
  - Bumping `aegis_singbox_version` is now
    a one-line change. The role fails with
    a clear error if the requested version
    does not ship the requested arch.
  - Operators running the role in a
    hermetic / air-gapped environment (no
    outbound `api.github.com`) need to
    either set `aegis_singbox_release_base_url`
    to a local mirror that also serves the
    same JSON shape, or stay on the
    v0.4.0-c hardcoded hash flow. The
    v0.5.0 release notes call this out.

### Added (pre-PR local CI gate, #124)

- **`chore(ops): tools/scripts/pre-pr.sh + pre-push
  hook + Makefile target`** — run the
  CI-equivalent checks locally before pushing a
  PR. The script catches the lint / test /
  markdown formatting failures that otherwise cost
  a 5+ minute round-trip through GitHub Actions;
  the v0.5.0 PR batch (#120, #121) shipped with
  a `fix(ci)` follow-up commit on each push
  because the local gate did not exist.
  - `tools/scripts/pre-pr.sh` — the canonical
    script. Runs:
    1. `gofmt -l backend/`
    2. `go build -trimpath ./...` (skip with
       `--quick`)
    3. `go test -short -count=1 ./...` (skip
       with `--quick`)
    4. `golangci-lint run --config .golangci.yml`
       with `GOFLAGS=-tags=integration`
    5. `npm ci` (skipped if `node_modules` is
       already present; the CI uses `npm ci` for
       a clean install)
    6. `npm run codegen:check` (openapi-typescript
       up to date)
    7. `npm run type-check` (vue-tsc)
    8. `npm run lint` (eslint + check-raw-text)
    9. `npm run build` (skip with `--quick`)
    10. `markdownlint-cli2` on `**/*.md` (fetched
        via `npx -y`; the CI pins the same version
        via the `DavidAnson/markdownlint-cli2-action@v19`
        action)
    Each step prints pass/fail with elapsed
    seconds; the failing step's stdout+stderr is
    dumped verbatim so the operator can fix and
    re-run. The final summary is green-on-red and
    the script exits non-zero on the first failure.
  - `tools/scripts/install-pre-push.sh` — installs
    `.git/hooks/pre-push` to delegate to
    `pre-pr.sh`. Idempotent (re-running rewrites
    the stub). One-line uninstall: `rm
    .git/hooks/pre-push`.
  - `Makefile` — new `pre-pr` and `pre-pr-install`
    targets (so `make pre-pr` and `make pre-pr-install`
    work alongside the existing `test` / `lint` /
    `build` targets).
  - Scope flags: `--backend`, `--frontend`,
    `--docs`, `--quick`. The default is `all`
    (everything, full set). The CI doesn't
    parallelise per-scope yet, but the flags are
    there for the day we add a pre-PR
    parallel-orchestrator.

- Out of scope (deferred to follow-ups):
  - Parallel orchestrator (e.g. `dx pre-pr
    --parallel`) — the per-scope flags are in
    place but the script is sequential today.
    The CI matrix already parallelises per
    job, so a local parallel mode is a
    convenience, not a correctness gate.
  - A pre-commit hook that runs the same gate
    on `git commit` (rather than `git push`).
    The pre-push gate is enough for the v0.5.0
    polish; a pre-commit gate would be
    annoying during a work-in-progress commit
    chain.

### Added (backups UI, #121)

- **`feat(backups): BackupsView.vue + API client`**
  — the SPA surface for the v0.5.0 backup
  package. The view is reachable from the
  sidebar under a Database icon between
  `Audit log` and `Profile`, and ships a
  toolbar with `Refresh` + `Create backup`
  actions, a six-column DataTable (id,
  createdAt, size, trigger, status badge,
  node/user/host counts), and per-row
  download + delete buttons.
  - `frontend/src/api/services/backups.ts` —
    the v0.5.0 client for the
    `/api/v1/backups/*` surface shipped in
    #120. Exports: `listBackups`, `getBackup`,
    `createBackup`, `deleteBackup`,
    `restoreBackup` (not yet wired into the
    UI; the v0.5.0 surface intentionally
    hides UI-driven restore), and
    `downloadBackup` (the blob + ObjectURL
    plus anchor.click() dance for browser-side
    file save with a Bearer-authenticated
    GET).
  - `frontend/src/views/BackupsView.vue` —
    the page component. Polls the list
    endpoint every 2 seconds while at
    least one row is in `running` status,
    so the transition to `ok` (or
    `failed`) shows up without a manual
    refresh. Failed rows expose the
    pg_dump error string as a tooltip on
    the destructive-status badge.
  - `frontend/src/router/index.ts` — new
    `/backups` route (auth-required, app
    layout) wired to the BackupsView.
  - `frontend/src/layouts/AppLayout.vue`
    — new `Backups` nav entry with a
    `Database` lucide icon, positioned
    between `Audit log` and `Profile`.
  - `frontend/src/types/aegis.ts` — new
    `Backup`, `BackupTrigger`, `BackupStatus`
    TS types mirroring the Go struct's
    wire format. The `api/client.ts`
    response interceptor already
    snake_case -> camelCases incoming
    JSON, so the UI types stay in
    camelCase while the wire stays in
    snake_case.
  - `frontend/src/i18n/locales/en.json`
    plus `ru.json` — full `backups` key
    set (title, subtitle, actions,
    statuses, triggers, error
    messages) plus a `backups` entry
    under `nav` and `profile.scopes`.

- Out of scope (deferred to follow-ups):
  - The `Restore` action is intentionally
    not in the v0.5.0 UI: a UI-driven
    restore is dangerous (it drops the
    panel DB) and the operator's safer
    path is the future `cmd/aegis-pg-restore`
    CLI binary. The endpoint is already
    wired in `api/services/backups.ts` so
    a follow-up PR can surface it behind a
    confirmation dialog without touching the
    wire format.
  - The `cmd/aegis-pg-backup` /
    `aegis-pg-restore` CLI binaries —
    the Service API is stable enough to add
    them without touching the handler or
    the wire format; this PR is the
    bookkeeping (UI + types + i18n) only.

### Added (backups package, #120)

- **`feat(backups): internal/backups package +
  admin router`** — the v0.5.0 backup surface. The
  panel can now dump its own Postgres on demand,
  keep a retention window of the most recent N
  dumps, and stream the dump back to an operator
  over the admin API. Restore is gated behind an
  explicit opt-in env var and is not exposed in
  the v0.5.0 UI (the v0.5.x follow-up #121 wires
  the button into the SPA).
  - `internal/backups/backup.go` — the canonical
    `Backup` row struct, plus `Trigger`
    (`manual`/`scheduled`) and `Status`
    (`running`/`ok`/`failed`) enums. JSON tags are
    snake_case to match the rest of the panel's
    wire format.
  - `internal/backups/store.go` — the
    `Store` interface and the v0.5.0
    `LocalStore` implementation. The metadata is
    a single `<backupsDir>/_index.json` file
    re-sorted by `CreatedAt` ascending on every
    write. The dump bytes are written and read
    via the `Backend` interface, with the
    `osBackend` rooted at `BackupsDir` rejecting
    `..`, absolute paths, and backslashes to keep
    the safety guarantees identical to a future
    S3 backend.
  - `internal/backups/service.go` — the
    orchestrator. `Create` is single-flight via
    an `inflight sync.Mutex`; a second concurrent
    caller gets `ErrBackupInProgress` (HTTP 409).
    The full lifecycle is: allocate ID → insert
    `running` row → stream `pg_dump -Fc` through
    gzip to `<id>.dump.gz` → SHA-256 the file →
    write `<id>.sha256` sidecar → update the row
    to `ok` with size, hash, and per-table counts
    → run a retention `Cleanup` pass. A failed
    Create persists the `failed` row (so the
    operator sees the failure in the UI) and
    removes the partial file.
  - `internal/backups/schedule.go` — a tiny
    custom 5-field cron parser (wildcards +
    specific values only; no `*/N` step and no
    `1-5` range in v0.5.0) and a `Service.Run`
    method that ticks every minute and fires
    `Create(TriggerScheduled)` on match. The
    scheduler is started from `main()` only when
    `AEGIS_BACKUPS_CRON` is set; an empty
    expression disables it (manual-only mode,
    the v0.5.0 default for dev).
  - `internal/backups/handler.go` — the HTTP
    surface, mounted at `/api/v1/backups` by
    `router.go`. Endpoints: `POST /` (create,
    202), `GET /` (list), `GET /{id}` (get),
    `GET /{id}/download` (stream gzip with
    `Content-Disposition: attachment`), `DELETE
    /{id}` (204), `POST /{id}/restore` (202,
    gated by `AEGIS_BACKUPS_ALLOW_UI_RESTORE`).
  - `internal/auth/scopes.go` — new
    `ScopeBackups = "backups"`. Granted only to
    the `admin` role; viewers and operators
    cannot see or touch backups.
  - `internal/router/router.go` — mounts the
    backup handler behind `authSvc.Middleware()`,
    plus `auth.RequireScope(auth.ScopeBackups)`.
  - `cmd/aegis/main.go` — constructs the
    `backups.Service` from `cfg.BackupsDir`,
    passes it to `router.Build`, and (when
    `cfg.BackupsCron != ""`) spawns the
    scheduler goroutine on a child of the
    shutdown context.
  - `internal/config/config.go` — five new
    env vars: `AEGIS_BACKUPS_DIR` (default
    `./var/backups`), `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
    (`false`), `AEGIS_BACKUPS_RETENTION_DAYS`
    (30), `AEGIS_BACKUPS_MAX_COUNT` (0 = off),
    `AEGIS_BACKUPS_CRON` (empty = scheduler
    disabled).
  - `internal/router/router_test.go` —
    updated the `Build()` test helper to thread
    `nil` for the new `backupsSvc` parameter
    (the test scope is route wiring, not the
    backup surface).
  - Tests: 11 new tests across `store_test.go`,
    `schedule_test.go`, `service_test.go`
    covering LocalStore CRUD, the path-traversal
    rejection, cron parser accept/reject paths,
    service happy path, single-flight
    `ErrBackupInProgress`, dump failure, delete
    idempotency, gzip magic bytes on
    `Open()`, age-based retention, count-based
    retention, and the `ErrBackupDisabled`
    gate.

- The store is **deliberately orthogonal to
  Postgres**: a restore is exactly the case where
  the panel DB is unavailable. The JSON index
  sits next to the dumps and is self-describing
  — no separate DB query is required to know
  what files exist. Restoring from a partial
  filesystem (some dump files missing) is a
  trivial "list + filter" walk.

- The `pg_dump` subprocess is invoked with
  `-Fc --no-password --dbname=<db>` and `PGPASSWORD`
  inherited from the panel's own DSN. A custom
  5-field cron parser avoids pulling in
  `github.com/robfig/cron/v3` for one line of
  code (the only schedule the v0.5.0 operator
  will write is `0 2 * * *`).

- Restore from the UI is **off by default**. The
  v0.5.x follow-up CLI binary
  (`cmd/aegis-pg-restore`, not in this PR) is
  the only thing trusted to drop the panel DB;
  the HTTP path is the convenience surface for
  dev environments that set
  `AEGIS_BACKUPS_ALLOW_UI_RESTORE=true`.

- Out of scope (deferred to follow-ups):
  - The BackupsView.vue UI (#121) — the surface
    is implemented and curl-able, but the
    buttons and download links live in a
    follow-up PR.
  - Wiring the panel container's `--env-file`
    for `AEGIS_BACKUPS_*` (#119 follow-up
    chore) — the envs are read by the panel
    directly, the operator sets them in
    `secrets.env` after #119 lands.
  - `docs/operator-guide.md` and
    `docs/guide/quickstart.md` updates with the
    backup workflow (a follow-up alongside the
    secrets wiring chore).

### Added (secrets via sops+age, #119)

- **`chore(ops): secrets via sops+age`** —
  replaces the Phase 1 fixture-credentials-in-env
  model with a proper sops+age encrypted file
  committed to the repo. The phase 1 deploy
  shipped JWT, DB password, and admin password as
  hard-coded env vars on the VPS (`aegis-fixture-*`
  in `~/.aegis/deploy.local.md`); v0.5.0 moves
  every one of them to `deploy/secrets/secrets.yml`,
  encrypted with an operator-generated age keypair.
  - `.sops.yaml` at the repo root defines
    `creation_rules` matching `.*secrets.*\.yml$` to
    the operator's age public key. The committed
    example public key is a throwaway for the PR
    demo — operators replace it with their own via
    `sops updatekeys`.
  - `deploy/secrets/secrets.example.yml` is a
    sops-encrypted schema reference. Decrypting it
    shows the field layout (jwt_secret,
    admin_password, postgres_password, agent_bearer,
    panel_path.admin, panel_path.sub,
    dev.singbox.{version,sha256}). Operators copy
    this to `secrets.yml` (gitignored), fill in real
    values, and run `sops --encrypt --in-place`.
  - `deploy/secrets/README.md` documents the
    one-time age keygen, the field-by-field
    generation commands, the rotation procedure,
    and the security stance.
  - `deploy/secrets/.gitignore` blocks plaintext
    `secrets.yml` / `secrets.local.yml` while
    allowing the example and any future `*.enc`
    through.
  - `deploy/ansible/roles/configure_secrets/` is
    the deploy-side role that installs sops+age
    (apt or direct download) and decrypts
    `secrets.yml` to `/etc/aegis/secrets.env` (mode
    0600, owner `aegis-deploy`). The role is
    idempotent and runs a round-trip decrypt smoke
    test before declaring success. The panel
    container mounts the file at
    `/run/aegis/secrets.env` and reads it via
    `--env-file` (the env-var passthrough in
    `deploy/docker/docker-compose.dev.yml` becomes
    the only place that mentions the
    `AEGIS_*_SECRET` env names; the values move from
    being hard-coded in the compose file to
    being sourced from the env file).
  - Root `.gitignore` had a top-level `secrets/`
    rule that was a catch-all for ad-hoc local
    files. Removed in favour of the explicit
    `deploy/secrets/.gitignore` so the canonical
    `deploy/secrets/` tree can be committed.

- The `secrets.example.yml` ships ENCRYPTED, not
  plaintext. Reviewers without the matching age
  private key see only the sops metadata
  (`sops:` + `ENC[AES256_GCM,...]` blobs). The
  plaintext is documented in the file's own
  block-comment at the top, which is also encrypted;
  decrypting once with `sops --decrypt` reveals
  the schema.

- The example public key in `.sops.yaml` is a
  throwaway generated for the PR demo
  (`age1ekwhyq7xftg3vqjka4rssrg77acrsa7hjjzs2vvlugc23j3gwfpqep7ggk`).
  The matching private key is at
  `~/.aegis/test-keys/age-example.key` on the original
  author's machine only — **not** committed, **not**
  in the repo. Operators replace both with their own
  (`age-keygen -o ~/.aegis/age.key` + `sops updatekeys`).
  This is the same trust model as SSH keypairs.

- Out of scope (deferred to a follow-up):
  - Wiring the `/etc/aegis/secrets.env` mount into
    the panel container's `docker run` (the role
    writes the file; the panel's `install_panel`
    role needs to add the `--env-file` flag).
  - Wiring the same into the `aegis-agent`
    binary's systemd unit (the agent reads its
    bearer from `/etc/aegis/agent-bearer`; the
    `install_agent` role needs to copy the
    `aegis.agent_bearer` value out of
    `secrets.env`).
  - Documentation update of `docs/operator-guide.md`
    (the new doc) and `docs/guide/quickstart.md`
    with the sops+age flow.

## [0.4.0] - 2026-07-26

**Tag:** `v0.4.0` on this commit. v0.4.0 ships two parallel
work streams:

1. **v0.4.0-mvp-batched** (PRs #92 / #93 / #94) — the
   `BatchedApplier` + real apply transport + the
   `install_singbox` Ansible role. The end-to-end
   panel → aegis-agent → sing-box config write → reload
   flow ships green. Closes the v0.4.0-a / b / c
   sub-PRs.
2. **v0.4.0-d Path C** (PRs #95 / #96 / #97 / #99 / #100) —
   the user-CRUD surface moves from `internal/subscription`
   into a dedicated `internal/users` package. The
   subscription package is now a pure render
   orchestrator: zero user-CRUD surface. The d-r-series
   cuts roughly 800 lines out of subscription and
   consolidates the wire format.

### Added (v0.4.0-mvp-batched, #92 / #93 / #94)

- **`internal/cores` `BatchedApplier`** — per-node delta
  queue with `CancelReplace` semantics (an `add_user`
  followed by a `remove_user` for the same `UserID`
  within the window is a no-op). 20s window, 1000 max
  queue. `FlushFn` callback. The `cmd/aegis-agent` /
  apply transport is wired through the new
  `Provider.Configure(nodes, httpClient)` pattern. Closes #92.
- **`cmd/aegis-agent` real `/v1/apply` handler** —
  `writeAtomic` (write to temp + fsync + `os.Rename`),
  `runReload` (subprocess via `exec.CommandContext`,
  no shell — `strings.Fields(reloadCmd)`), and
  `applyEnvelope` / `applyResponse` with `reloaded: bool`,
  plus `reload_took_ms: int64`. Closes #93.
- **`deploy/ansible/roles/install_singbox/`** — pins
  sing-box 1.14.0-beta.2, hard-coded SHA-256
  `f68715815741e59f25e32904cabcd5924a0461a910d8e9c9612512b957709ef4`.
  Playbook order: `bootstrap` → `install_caddy` →
  `install_fail2ban` → `install_singbox` →
  `install_agent` → `setup_decoy` (install_singbox
  comes before install_agent because the agent's env
  file references `/etc/sing-box/config.json`). Closes #94.

### Added (v0.4.0-d, #95 / #96 / #97 / #99 / #100)

- **`internal/users` data layer** — the new home for the
  end-user CRUD surface (User + Status enum + Create /
  Update / Delete / RotateSubToken + MemoryStore +
  PgStore). 32-byte / 64-hex-char `sub_token` (d.1
  bumped from 16/32 for higher entropy). Closes #95.
- **`users.User` wire-format compat** with
  `subscription.User` — both Go types have identical
  JSON shape (snake_case fields, `[]uuid.UUID` for
  hosts allow/block lists). Makes the d.r2 → d.r3
  move possible without render-code churn. Closes #96.
- **Drop subscription-side user-CRUD** — `Store`,
  `MemoryStore`, `PgStore` no longer carry the 7
  user-CRUD methods. The 4 Service-level thin
  wrappers (`GetUserBySubToken` / `RotateSubToken` /
  `CreateUser` / `ListUsers`) carry the work
  temporarily. Closes #97.
- **Move `admin_handler.go` to `users`** — the
  user-CRUD admin surface (mounted at `/api/v1/users`)
  lives in `internal/users/admin_handler.go` now. The
  Service-level thin wrappers are gone; the render
  handler consults `*users.Service` directly for the
  sub_token lookup. Closes #99.
- **Cleanup pass + roadmap** — `DefaultSubTokenRotationGrace`
  is now a public package constant on `users` (was a
  magic-number literal). `docs/ROADMAP.md` documents
  the v0.4.0-d Path C status, v0.5.0 polish, v0.6.0 plans,
  v0.7.0 webhooks, v1.0.0-mvp-soft-launch GA, and the 9
  open-gap packages. `.markdownlint.json` disables
  `MD060` (the default "aligned" table style is fragile
  under PR review). Closes #100.

### Behaviour changes (v0.4.0-d)

- **`sub_token` is now 64 hex chars (32 bytes)**, not
  32 hex chars (16 bytes). The d.1 design bumped from
  16 bytes to 32 bytes of entropy. Existing fixtures
  in `internal/users/*_test.go` and the integration
  tests updated.
- **`RotateSubToken` grace semantics changed** —
  `grace <= 0` no longer invalidates the prev token
  immediately. The d.1 `users.Service.RotateSubToken`
  maps `grace <= 0` to the canonical 24h default
  (matching the 3X-UI convention). The pre-existing
  test that asserted the d.0 behaviour was rewritten
  as a documentation test.

### Changed (repo hygiene, post-Phase 1)

- **`chore(repo): gitignore the operator deploy
  scripts`** — the Phase 1 deploy scripts (the
  `aegis-*.{py,sh}` set under `tools/scripts/`) live in
  the repo path but are operator-only artefacts: they
  hardcode the VPS IP, the deploy-user SSH pubkey, the
  DB password, the container names, the panel sub-path,
  and the panel/UI image tags. They were untracked by
  accident (nothing matched them in `.gitignore`), so a
  future `git add tools/scripts/` could have pushed
  them to a public GH history. Two new patterns under
  `tools/scripts/`: `aegis-*.py` and `aegis-*.sh`. The
  rest of the scripts in that directory (the shared
  developer tooling: `branch-start.sh`, `release.sh`,
  `smoke-frontend.sh`, `backup.sh`, `restore.sh`) stay
  trackable. The canonical private notes for the deploy
  live in `~/.aegis/deploy.local.md` (outside the repo).
- **`chore(repo): drop the tracked stale pr-body from
  #117** — the file
  `.github/pr-body-fix-ui-runtime-api-quirks.md`
  was committed in #117. The
  `gh pr create --body-file` draft got
  `git add`-ed along with the actual code.
  The gitignore pattern
  `.github/pr-body-*.md` was planned for
  #117 as a future-proofing measure but the
  file that triggered the rule was already
  in the same squash commit. The PR
  description now lives on GitHub at #117;
  the local file is redundant. The deletion
  is folded into the same chore PR as the
  gitignore change above so #117 stops
  being a one-off exception.

### Fixed (release workflow post-v0.4.0 follow-ups, #102 / #103 / #104 / #111)

These four PRs land on `main` after the `v0.4.0` git
tag (which points to `39d4d9e`). They touch only
`.github/workflows/release.yml`; no application code
changed. Their purpose is to make the `v0.4.0`
GHCR images land in the expected state on
`workflow_dispatch` re-runs (and to leave a stable
release contract for future maintainers). Documented
under `[Unreleased]` because they are not part of
the `v0.4.0` tag itself; they will ship in `v0.4.1`
or the next release.

- **`fix(ci): lowercase the GHCR image names in
  release.yml` (#102)** — `release.yml` hardcoded
  the image paths as `ghcr.io/QAdversif/AegisPanel`
  and `ghcr.io/QAdversif/AegisPanel-ui`. The OCI
  image-spec requires the path portion (after the
  registry) to be lowercase, and buildx rejected
  the v0.4.0 release build with
  `repository name must be lowercase`. The
  `ci.yml` workflow already used the lowercase
  form (fixed in the v0.3.0 cleanup batch);
  `release.yml` was the hold-out. The two
  `QAdversif` / `AegisPanel` tokens became
  `qadversif` / `aegispanel`. Closes #102.
- **`fix(ci): allow workflow_dispatch to actually
  push in release.yml` (#103)** — the
  `release.yml` workflow gated the GHCR push
  (and the `Login to GHCR` step) on
  `github.event_name == 'push'`. On
  `workflow_dispatch` re-runs, the build steps
  `push: ${{ github.event_name == 'push' }}`
  evaluated to `false`, so the build "succeeded"
  but the registry write was a no-op. The
  `Create GitHub release` step is intentionally
  left gated on `'push'` only (a re-run is for
  re-pushing images, not re-creating the
  release). The three push/login conditions are
  extended to `push || workflow_dispatch`.
  Closes #103.
- **`fix(ci): use tag input for UI image in
  release.yml workflow_dispatch` (#104)** — the
  UI image build step hardcoded `github.ref_name`
  as the image tag. On `workflow_dispatch`,
  `github.ref_name` is the branch name (`main`),
  not the operator-supplied `tag` input, so the
  UI image ended up tagged
  `ghcr.io/qadversif/aegispanel-ui:main` instead
  of `:v0.4.0` on the v0.4.0 re-run. A new
  job-level `env.release_tag` resolves to
  `github.ref_name` on `push` and to `inputs.tag`
  on `workflow_dispatch`; the UI image tag uses
  it. The `Show tag` step echoes
  `release_tag = ${{ env.release_tag }}` for log
  visibility. Closes #104.
- **`fix(ci): explicit semver tags for panel image
  in release.yml` (#111)** — the panel
  `metadata-action` used
  `type=semver,pattern={{version}}` which only
  derives a version from the ref on `push` events.
  On `workflow_dispatch` the ref is the branch
  (`main`), the action emits no semver tags, and
  the `0.4.0` / `0.4` tags stayed on the original
  tag-push digest (acceptable for `v0.4.0` since
  the same code is on both digests; brittle for
  any future re-publish that includes an
  application-code change). A new
  `Compute release version` step derives `version`
  and `short` from `env.release_tag` (bash
  parameter expansion + `sed`) and feeds them to
  the metadata-action as `type=raw` values. The
  `latest=auto` flavor and the
  `enable={{is_default_branch}}` raw `latest` tag
  are kept. Both event paths now produce the
  same `[version, short, latest]` tag list.
  Closes #111.

## [0.3.0-mvp-byo-node] - 2026-07-23

**Tag:** `v0.3.0-mvp-byo-node` on `ba78b35` (post-cleanup-batch
HEAD). v0.3.0 ships the BYO-node bootstrap path: the
operator can provision a fresh Linux node, install the
Caddy reverse proxy + the `aegis-agent` Go binary, and
have it register with the panel — all from the panel
admin UI. Closes #67 (v0.3.0-a backend), and the
subsequent cleanup batch (PRs #74 / #75 / #76 / #77
/ #82 / #83 / #84 / #87 / #91).

### Added (v0.3.0)

- **`internal/bootstrap/`** package — SSH client (`x/crypto/ssh` +
  `pkg/sftp`), TOFU host-key policy, 32-byte bearer secret
  generation, 5-step install workflow, state machine, provisioner.
  Closes v0.3.0-a (backend). Closes #67.
- **11 reserved-package `doc.go` stubs** for the Phase 2-4 slots
  (`cabinet`, `caddy`, `cascades`, `decoy`, `events`, `mcp`,
  `notifications`, `plans`, `stats`, `subscriptions`,
  `webhooks`). Closes #77.
- **`cmd/aegis-agent` real Go binary** — replaces the
  v0.2.0 `sleep infinity` placeholder. Ansible role
  `install_agent` uploads the binary, writes
  `/etc/aegis/agent.env`, registers the systemd unit.
- **Per-node `AgentBearer` storage** — `nodes.agent_bearer`
  column (migration 0013). v0.3.0 nodes get empty bearer
  until re-provisioned; production should use Postgres
  TDE or disk encryption on the agent_bearer column.

### Fixed (cleanup batch, post-v0.3.0-a)

- **chi v5.2.4 → v5.3.1.** Replaced the deprecated
  `middleware.RealIP` (vulnerable to XFF spoofing, GHSA-3fxj-6jh8-hvhx
  family) with the chi v5.3 `ClientIPFrom*` + `GetClientIP` family.
  Closes #75.
- **`internal/audits/clientIP` re-pointed to `middleware.GetClientIP`.**
  No more local XFF parsing in the audit handler — single source
  of truth in the chi middleware. Same fixup as #75.
- **Trivy workflow: `ignorefile:` → `trivyignores:`** (the
  trivy-action input key, not the silent reject that was
  hiding the `.trivyignore` entries). Closes #74.
- **Frontend `eslint --fix`** across the six view files —
  171 auto-fixable warnings → 0. Closes #76.
- **Dependabot #68 (Go minor+patch)** superseded by #75; #69
  (frontend minor+patch) deferred to v0.4.0 cleanup window
  (transitively requires a TypeScript 5.8+ major).
- **vitest 3 → 4** (#82) — `vi.useFakeTimers` + global setup
  pattern. The vitest test suite went from 24 flaky
  tests on CI to 0 in 4.1.
- **eslint 8 → 10 flat config** (#83) — the new flat
  config file pattern. Catches plugin ordering bugs the
  legacy config silently allowed.
- **vue-router 4 → 5 + vite 6 → 7 + pinia 2 → 3** (#84) —
  the vue-router 5 breaking change is the data-loader
  pattern; pinia 3 adds the `defineStore` setup-style
  syntax that the data loaders need.
- **`.gitattributes` + `npm ci` standardisation** (#87) —
  the footgun fix that makes Windows contributors'
  CRLF/LF noise disappear; CI is now `npm ci` (not
  `npm install`).
- **vite 7.3.0 → 7.3.6** (#89) — 6 dependabot advisories
  (all `Development`-only impact).
- **brace-expansion@2 → 5 + js-yaml@3 → 4** (#90) — 3
  HIGH-severity OSV findings resolved.
- **Custom Caddy binary** (#91) — drops the upstream
  Caddy `grpc-go` CVE by patching to `v1.82.1` in a
  BuildKit-built binary. Closes the `trivy-frontend`
  HIGH findings.

### Documentation (v9.2 roadmap sync, #78)

- **ARCHITECTURE.md §21** markers synced with the code: v0.1.0
  and v0.2.0 marked `[done]`, v0.3.0 marked `[wip]`. §21
  timing table updated. New §25 entry v9.2 documenting the
  sync + the cleanup batch. See PR #78.
- **Tags created retroactively** (and pushed):
  - `v0.1.0-mvp-render` on `5840c13` (PR #50, last v0.1.0 commit).
  - `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0 commit).
- **KNOWN_LIMITATIONS.md** restructured: previously-v0.1.0
  entries that closed in v0.2.0 moved to a "Closed" section;
  v0.3.0+ open items live under the v0.3.0 heading.
- **README.md** status table, Go version, repo layout, and
  frontend view list all updated to v0.3.0-era.

## [0.2.0-mvp-agent] - 2026-07-19

**Tag:** `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0
commit). v0.2.0 delivers the `cmd/aegis-agent` placeholder
binary, all backend handler surfaces for the v0.1.0 UI, and
the OpenAPI codegen pipeline.

### Added (v0.2.0)

- **Backend handler surfaces** for the v0.1.0 admin UI:
  - `/api/v1/panelcfg` (PR-F, #59) — sub-path rotation.
  - `/api/v1/users` (PR-G, #60) — admin user CRUD.
  - `/api/v1/hosts` (PR-H, #61) — host create/edit dialogs.
  - `/api/v1/nodes/{id}/inbounds` (PR-I, #62) — per-node
    inbounds CRUD with JSONB `params` editor.
  - `/api/v1/audits` + `/api/v1/auth/me/password` (PR-M, #66)
    — audit log read surface + operator change-password.
- **Argon2id operator CLI** (PR-J, #63) — `aegis admin add
  <user>`, `aegis admin passwd <user>`, `aegis admin list`.
  Production seed guard: `AEGIS_ENV=production` refuses to
  start with the dev seed user.
- **Per-sub_token rate limiting** (PR-K, #64) — in-memory
  token bucket with `Retry-After` header.
- **OpenAPI 3.0 codegen** (PR-L, #65) — `pnpm run codegen`
  regenerates `frontend/src/types/api.d.ts`;
  `pnpm run codegen:check` enforces byte-equality in CI.
- **Sub-token rotation + URL prefix rotation** (#47) —
  Panel-side helpers that let the operator rotate a user's
  sub-token or the panel-wide sub-path without code changes.
- **Placeholder `cmd/aegis-agent`** — `sleep infinity`
  systemd unit so the Apply path can be smoke-tested
  end-to-end without a real agent binary. Real Go binary
  ships in v0.3.0-c.

### Fixed

- **i18n coverage gap** between RU/EN locales (PR-E, #58).
- **KNOWN_LIMITATIONS.md** v0.1.0 gap list (PR-E, #58) —
  the per-scope list of what was open at v0.1.0 cut.
- **postcss 8.4 → 8.5** for GHSA-qx2v-qp2m-jg93 (#57).
- **`.gitattributes` LF policy** for Windows contributors
  (#56) — eliminates CRLF noise in CI.
- **go-chi 5.0 → 5.2.4** (#13) — security baseline.

## [0.1.0-mvp-render] - 2026-07-17

**Tag:** `v0.1.0-mvp-render` on `5840c13` (PR #50, last
v0.1.0 commit). v0.1.0 ships the renderable MVP: every
surface except the actual `Apply` call works through the
API + UI. The Apply call is a stub returning
`ErrApplyNotImplemented` — that is **OK for v0.1.0** per
the DoD in `ARCHITECTURE.md §21 / MVP-0.1`.

### Added (v0.1.0)

- **Subscription `PgStore`** (#50) — `internal/subscription/store_pg.go`
  and migration. Subscription URL endpoint works end-to-end
  against Postgres (MemoryStore still available for dev).
- **Panelcfg `PgStore`** (#50) — same package split; sub-path
  config persists in `panel_path_config` table.
- **Frontend stack** (ADR-0004, PR-B, #51) — TailwindCSS,
  shadcn-vue, Reka UI, `@tanstack/vue-table` (DataTable),
  `vee-validate`, `zod` (forms), `lucide-vue-next`.
- **DataTable + form primitives** (PR-C, #54) —
  `frontend/src/components/{Form,FormField,FormFieldError,DataTable}.vue`
  and `frontend/src/composables/useZodForm.ts` typed wrapper.
- **CRUD pages + auth flow** (PR-D, #55) — Dashboard, Nodes,
  Inbounds, Hosts, Subscription, Users, Settings, Login views
  with full create/edit/delete flows.
- **Smoke test** (`tools/scripts/smoke-frontend.sh`,
  PR-E, #58) — runs `vite preview` and validates the
  served HTML + asset graph.

### Architecture (v9 + v9.1, prereq to v0.1.0)

- **ADR-0003** (`docs/adr/0003-mvp-singbox-vertical-slice.md`)
  — sing-box is the only MVP core. Xray deferred to v2.0+.
  Batched Apply is the primary user-enforcement strategy.
- **ADR-0004** (`docs/adr/0004-frontend-ui-kit-shadcn-vue.md`)
  — shadcn-vue + Reka UI stack fix. Alternatives (NaiveUI,
  PrimeVue, Element Plus, Vuetify) considered and rejected
  with rationale.
- **ADR-0001** (`docs/adr/0001-xray-as-production-core.md`)
  marked **Superseded by ADR-0003**. Kept in-tree for history.
- **ARCHITECTURE.md v9** (`#49`) — full rewrite after the
  ADR-0001 cancellation. §21 unified roadmap is the single
  source of truth for phases. v8 (Phase 4 split roadmap +
  addendum) folded in.
- **ARCHITECTURE.md v9.1** (`#48` followup) — UI stack fix
  in §1 + §21 Phase 1 / MVP-0.1.

### Known gaps (closed in v0.2.0)

These are documented in detail in `KNOWN_LIMITATIONS.md` under
the "Closed in v0.2.0" section. Top items:

- Per-node inbounds editor (closed by PR-I, #62).
- Host create / edit dialogs (closed by PR-H, #61).
- User CRUD (closed by PR-G, #60).
- Settings UI / panelcfg HTTP (closed by PR-F, #59).
- OpenAPI codegen (closed by PR-L, #65).
- Per-sub_token rate limiting (closed by PR-K, #64).
- Argon2id operator CLI (closed by PR-J, #63).

## [0.0.1] - 2026-07-13

Pre-alpha skeleton. Architecture v7 is finalised; the code tree is in
place. Nothing is wired up to run end-to-end yet; that is Phase 0 →
Phase 1.

### Added (skeleton)
- Repository skeleton (monorepo: `backend/`, `frontend/`, `docs/`, `deploy/`).
- Backend: Go 1.22+ service skeleton (`chi`, env config, structured
  logging, healthcheck, metrics stub, initial SQL migration).
- Frontend: Vue 3 + TS + Vite admin UI skeleton (Pinia, vue-i18n
  ru/en, dashboard view).
- Docs: VuePress 2 site (local-only, not published yet).
- Dev environment: Docker Compose stack (PostgreSQL 16, Redis 7,
  NATS 2.10, ClickHouse 24, MinIO, Caddy 2).
- Deploy: Ansible roles, Caddyfile templates for panel and node
  (with decoy + masquerade ports), fail2ban jails, systemd units.
- GitHub: workflows (ci, release), dependabot, issue / PR templates,
  community health files (CONTRIBUTING, CODE_OF_CONDUCT, SECURITY).
- Tooling: `tools/scripts/{release,restore,backup,branch-start}.sh`.
- Conventional Commits template (`.gitmessage.txt`).
- Architecture document (`ARCHITECTURE.md`, 28 sections).
