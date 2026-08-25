-- +migrate Up
--
-- v0.7.0 — webhook delivery history and DLQ.
-- The `webhook_endpoints` table was added in
-- migration 0001 (column shape is final: id, url,
-- secret, events, enabled, last_delivery_at,
-- last_status_code, created_at). v0.7.0 adds two
-- new tables: `webhook_deliveries` (every POST
-- attempt the dispatcher made) and `webhook_dlq`
-- (failed deliveries the operator can replay).

CREATE TABLE webhook_deliveries (
    id              UUID PRIMARY KEY,
    endpoint_id     UUID NOT NULL REFERENCES webhook_endpoints(id) ON DELETE CASCADE,
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    request_url     TEXT NOT NULL,
    request_body    BYTEA NOT NULL,
    signature       TEXT NOT NULL,
    timestamp       TIMESTAMPTZ NOT NULL,
    status_code     INTEGER,
    response_body   TEXT,
    error           TEXT,
    attempt         INTEGER NOT NULL DEFAULT 1,
    duration_ms     INTEGER,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_deliveries_endpoint_id ON webhook_deliveries(endpoint_id);
CREATE INDEX idx_webhook_deliveries_created_at ON webhook_deliveries(created_at DESC);
CREATE INDEX idx_webhook_deliveries_event_type ON webhook_deliveries(event_type);

CREATE TABLE webhook_dlq (
    id              UUID PRIMARY KEY,
    endpoint_id     UUID NOT NULL,                -- logical reference; no FK because the endpoint may be deleted
    endpoint_url    TEXT NOT NULL,                -- snapshot of the URL at enqueue time
    event_type      TEXT NOT NULL,
    payload         JSONB NOT NULL,
    last_error      TEXT NOT NULL,
    attempts        INTEGER NOT NULL,
    last_attempt_at TIMESTAMPTZ NOT NULL,
    enqueued_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_webhook_dlq_endpoint_id ON webhook_dlq(endpoint_id);
CREATE INDEX idx_webhook_dlq_enqueued_at ON webhook_dlq(enqueued_at DESC);

-- +migrate Down

DROP TABLE IF EXISTS webhook_dlq;
DROP TABLE IF EXISTS webhook_deliveries;
