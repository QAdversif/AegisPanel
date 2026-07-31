-- +migrate Up
--
-- v0.7.0 — add `updated_at` to webhook_endpoints.
--
-- The v0.3.0 stub of the table in migration 0001
-- did not include an `updated_at` column. The
-- v0.7.0 `internal/webhooks` package tracks it
-- (mirroring the plans / users / nodes pattern)
-- so the admin UI can show "last edited". A
-- DEFAULT NOW() is fine for the backfill: any
-- existing rows are operator-created, so the
-- "edit time" is approximately the "create
-- time".

ALTER TABLE webhook_endpoints ADD COLUMN updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW();

-- +migrate Down

ALTER TABLE webhook_endpoints DROP COLUMN updated_at;
