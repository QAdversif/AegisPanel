-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- +migrate Up
--
-- v0.7.x — webhook pending-retries work queue.
--
-- The v0.7.0 dispatcher records every POST attempt
-- in `webhook_deliveries` and, on a non-final
-- failure, returns `DeliveryStatusRetry`. The next
-- attempt has to be triggered out-of-band (the
-- v0.7.0 admin surface exposes it as a manual
-- `POST /api/v1/webhooks/deliveries/{id}/retry`).
--
-- v0.7.x adds a background worker that fires
-- retries on a fixed schedule (1s, 5s, 25s, 2m15s,
-- 11m15s — see `webhooks.NextAttemptDelay`). The
-- work queue is this table: one row per delivery
-- that is waiting for its next attempt.
--
-- The queue is intentionally separate from
-- `webhook_deliveries`. The deliveries table is
-- the immutable audit log (one row per attempt,
-- never updated after creation). The pending-retries
-- table is the live work queue — rows are inserted
-- on failure, deleted on the next attempt (success
-- or fail), and never inspected by the operator
-- (the deliveries table is the operator-facing
-- view; this table is purely internal).
--
-- `delivery_id` is a PK so the worker can claim
-- a row atomically (EnqueueRetry uses
-- ON CONFLICT DO UPDATE; DequeueRetry is a plain
-- DELETE; the worker tick reads with
-- `WHERE next_attempt_at <= now() ORDER BY
-- next_attempt_at ASC LIMIT N`).
--
-- The FK to `webhook_deliveries` has
-- ON DELETE CASCADE so the row goes away when the
-- underlying delivery row is removed (e.g. the
-- operator deletes the endpoint and the deliveries
-- cascade-delete with it).

CREATE TABLE webhook_pending_retries (
    delivery_id     UUID PRIMARY KEY REFERENCES webhook_deliveries(id) ON DELETE CASCADE,
    next_attempt_at TIMESTAMPTZ NOT NULL,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_pending_retries_due ON webhook_pending_retries(next_attempt_at);

-- +migrate Down

DROP TABLE IF EXISTS webhook_pending_retries;
