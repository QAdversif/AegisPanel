-- +migrate Up
--
-- v0.7.0 — add UNIQUE constraint on
-- webhook_endpoints.url.
--
-- The v0.3.0 stub of the table in migration 0001
-- did not declare UNIQUE on url. The v0.7.0
-- Service + MemoryStore both enforce URL
-- uniqueness (ErrDuplicate), but the pgx
-- implementation relied on the constraint to
-- surface SQLSTATE 23505. Without the
-- constraint, two endpoints with the same URL
-- silently coexist in pgx; the MemoryStore
-- behaviour diverged.
--
-- v0.7.0 makes the constraint explicit. Existing
-- duplicate rows (if any) block the ALTER; the
-- operator is expected to clean up via the
-- admin UI before re-running. For fresh
-- installs the constraint is a no-op.

ALTER TABLE webhook_endpoints ADD CONSTRAINT webhook_endpoints_url_key UNIQUE (url);

-- +migrate Down

ALTER TABLE webhook_endpoints DROP CONSTRAINT webhook_endpoints_url_key;
