-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0002 — admin refresh tokens.
-- Persists opaque (single-use) refresh tokens for the panel's
-- admin /api/v1/auth surface. The token itself is never stored —
-- only its SHA-256 hash. On rotation the old row is marked
-- `used_at`; a subsequent ConsumeRefresh on the same hash is
-- rejected. A real Phase 1.1 follow-up will introduce
-- refresh-chain revocation (marking the entire chain `used`
-- on suspected theft).

BEGIN;

-- +migrate Up

CREATE TABLE admin_refresh_tokens (
    id              UUID PRIMARY KEY,
    user_id         UUID NOT NULL REFERENCES admins(id) ON DELETE CASCADE,
    token_hash      BYTEA NOT NULL UNIQUE,     -- SHA-256 of the opaque token
    expires_at      TIMESTAMPTZ NOT NULL,
    used_at         TIMESTAMPTZ,              -- NULL = still valid, non-NULL = consumed
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX admin_refresh_tokens_user_id_idx  ON admin_refresh_tokens (user_id);
CREATE INDEX admin_refresh_tokens_expires_at_idx ON admin_refresh_tokens (expires_at);

-- +migrate Down

DROP TABLE IF EXISTS admin_refresh_tokens;

COMMIT;
