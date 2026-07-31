# feat(webhooks): background retry worker

Closes the v0.7.0 known limitation: in v0.7.0 the
dispatcher records a failed attempt in
`webhook_deliveries` and returns `DeliveryStatusRetry`,
but the next attempt had to be triggered out-of-band
(manually via `POST /api/v1/webhooks/deliveries/{id}/retry`).
This PR adds a background goroutine that fires
retries on the dispatcher-computed schedule
(1s, 5s, 25s, 2m15s, 11m15s).

## What this PR does

* Adds the v0.7.x background worker goroutine
  (`backend/internal/webhooks/worker.go`) that ticks
  on a configurable interval and fires every pending
  retry whose `next_attempt_at` is in the past.

* Migration `0017_webhook_pending_retries.sql`
  introduces a separate work-queue table
  (`webhook_pending_retries`), distinct from the
  immutable `webhook_deliveries` audit log. The
  table has an FK to `webhook_deliveries(id)` with
  `ON DELETE CASCADE` so the row disappears when the
  underlying delivery is removed.

* Store layer gains three new methods
  (`EnqueueRetry` / `DequeueRetry` / `ListDueRetries`)
  on both `MemoryStore` and `PgStore`.
  `EnqueueRetry` is idempotent via
  `ON CONFLICT (delivery_id) DO UPDATE`.

* `Service.deliverSync` and `Service.recordFailure`
  enqueue a retry on every non-final failure.
  `Service.RetryDelivery` dequeues the OLD row after
  firing the new attempt; the new attempt's
  `deliverSync` re-enqueues itself on its own
  failure.

* New `Service.ProcessDueRetries` pulls a batch of
  due ids and fires each via `RetryDelivery`; the
  new `Worker.Run` goroutine calls it on every tick.

* Two new config flags in
  `backend/internal/config/config.go`, both with
  safe defaults:
  `AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED` (default
  true) and `AEGIS_WEBHOOKS_RETRY_WORKER_INTERVAL`
  (default 5s).

* `backend/cmd/aegis/main.go` wires the worker in
  the same in-process goroutine pattern as the
  existing `backupsSvc.Run` scheduler. The worker
  is cancellable via the boot context so SIGINT
  and SIGTERM clean-shutdown unblocks `Run`.

## Why a separate work-queue table

v0.7.0 has one row per attempt in
`webhook_deliveries`. A "pending retry" state on the
existing row would create the "old rows" problem
(a successful attempt 2 leaves the attempt 1 row
with stale `next_attempt_at`).

The work queue is intentionally separate from the
audit log:

* `webhook_deliveries` is the immutable history.
  Every POST attempt is its own row, and the
  operator sees the full attempt chain.

* `webhook_pending_retries` is the live work queue.
  Rows are inserted on failure, deleted on the
  next attempt (success or fail), and never
  inspected by the operator (the deliveries table
  is the operator-facing view; this table is
  purely internal).

The cascade FK means a single DELETE on
`webhook_endpoints` cleans up both tables in
the same transaction.

## Files changed

* `backend/internal/webhooks/worker.go` (new, 145 lines)
* `backend/internal/webhooks/worker_test.go` (new, 152 lines)
* `backend/migrations/0017_webhook_pending_retries.sql` (new, 53 lines)
* `backend/internal/webhooks/store.go` (interface additions)
* `backend/internal/webhooks/memory_store.go` (3 method impls)
* `backend/internal/webhooks/pg_store.go` (3 method impls)
* `backend/internal/webhooks/service.go` (enqueue in deliverSync, dequeue in RetryDelivery, new ProcessDueRetries)
* `backend/internal/webhooks/memory_store_test.go` (4 new tests)
* `backend/internal/webhooks/service_test.go` (10 new tests)
* `backend/internal/webhooks/pg_store_integration_test.go` (3 new tests, 1 TRUNCATE update)
* `backend/internal/config/config.go` (2 new flags)
* `backend/cmd/aegis/main.go` (worker wiring)

Total: 12 files, 978 insertions, 7 deletions.

## Test plan

* `go test ./internal/webhooks/...` — 72 unit tests
  pass (was 53 before this PR).
* `go test -tags=integration ./internal/webhooks/...`
  — 3 new pg integration tests cover the
  `ON CONFLICT` upsert path, the cascade-delete
  path, and the basic round-trip. Tests skip with
  a helpful message when no Postgres is configured
  (CI is the only environment that must set
  `INTEGRATION_DATABASE_URL`).
* `go test ./...` — all 21 backend packages still
  pass.
* `golangci-lint run ./...` — 0 issues.
* `go vet ./...` and `go vet -tags=integration ./...`
  — clean.

## Out of scope (deferred to v0.7.x follow-ups)

* Multi-replica leader election (etcd or pg
  advisory lock). v0.7.x targets a single-replica
  panel; the worker design assumes one writer.
  A future v0.8 plus HA mode would pin the worker
  to a leader. The store methods are atomic
  enough to survive a stray double-fire
  (`DequeueRetry` is idempotent) but the operator
  would see two HTTP requests per retry.

* sops envelope on `webhook_endpoints.secret`
  (the next PR in the v0.7.x roadmap).

* Wiring `Service.Dispatch` to every mutating
  handler (the third PR in the v0.7.x roadmap).

Refs ROADMAP v0.7.x "Background worker for retry".
