# feat(webhooks): internal/webhooks package

v0.7.0 PR #1/5.

## What

The new `internal/webhooks` package owns the panel-side
outgoing-webhook surface:

- `Endpoint` model (operator-configured URL, HMAC secret,
  event subscription, enable/disable) plus a closed-set
  `EventType` enum covering the user / plan / node / host /
  backup / inbound lifecycles
- `Delivery` + `DLQEntry` models with JSONB payload snapshots
  so manual replay sends the exact same body the receiver
  saw
- HMAC-SHA256 signing in `X-Aegis-Signature` (format
  `sha256=<hex>`), timestamp in `X-Aegis-Timestamp`,
  5-minute anti-replay window documented in the package doc
- Exponential-backoff retry schedule (1s, 5s, 25s, 2m15s,
  11m15s) with `MaxAttempts = 6` and a DLQ for the final
  failed attempt
- `Store` interface, `MemoryStore` (Phase 0 default) and
  `PgStore` (pgx-backed, selected via `AEGIS_WEBHOOKS_BACKEND=pg`)
- `Service` with full input validation (URL http/https only,
  secret 16..256 chars, event closed-enum, non-zero IDs)
- Dispatcher: synchronous, signs in-memory, records every
  attempt as a `Delivery` row, moves the final failed
  attempt to the DLQ
- Migration `0014_webhook_deliveries.sql` adds
  `webhook_deliveries` and `webhook_dlq` tables; the
  `webhook_endpoints` table was already in migration 0001

## Tests

- 7 signature tests (round-trip, wrong secret, tampered
  body, missing prefix, malformed hex, empty secret,
  determinism)
- 4 retry-schedule tests (full schedule, out of range,
  total budget, MaxAttempts consistency)
- 9 MemoryStore tests (CRUD, ordering, duplicate URL,
  rename URL, URL collision, delete cascade, defensive
  copy, DLQ round-trip)
- 16 Service tests (input validation, CRUD, dispatch happy
  path, disabled endpoints, event filtering, transport
  error, non-2xx, retry advances attempt, max-attempts
  block, DLQ replay, endpoint-deleted replay,
  send-test-event, list limit clamping, final attempt
  moves to DLQ)
- 5 pg integration tests behind `//go:build integration`
  (endpoint round-trip, delivery round-trip, DLQ round-trip,
  duplicate URL, delete cascade)

41 unit tests, all green; 0 fix-up commits on the package.

## Deferred to v0.7.x

- Background worker that picks up failed `Delivery` rows
  and schedules the next retry (v0.7.0 ships the manual
  `Service.RetryDelivery` hook the worker will call)
- sops envelope on `webhook_endpoints.secret` (plaintext
  in the DB for v0.7.0; same threat model as the rest of
  the panel)
- Wiring `Service.Dispatch` into every mutating handler
  (the call-site batch; v0.7.0 ships the package plus the
  test endpoint, not the production event flow)
- The `/webhooks` HTTP surface (PR #2/5: handler, plus
  scope, config flag, wiring, and e2e tests)

## Files

- `backend/internal/webhooks/doc.go` (rewritten from
  the v0.3.0 stub)
- `backend/internal/webhooks/endpoint.go`
- `backend/internal/webhooks/delivery.go`
- `backend/internal/webhooks/signature.go`
- `backend/internal/webhooks/retry.go`
- `backend/internal/webhooks/store.go`
- `backend/internal/webhooks/memory_store.go`
- `backend/internal/webhooks/pg_store.go`
- `backend/internal/webhooks/service.go`
- `backend/internal/webhooks/signature_test.go`
- `backend/internal/webhooks/retry_test.go`
- `backend/internal/webhooks/memory_store_test.go`
- `backend/internal/webhooks/service_test.go`
- `backend/internal/webhooks/pg_store_integration_test.go`
- `backend/migrations/0014_webhook_deliveries.sql`

No changes to existing files outside the package and the
new migration. The HTTP surface, scope, config flag, and
`cmd/aegis/main.go` wiring land in PR #2/5.
