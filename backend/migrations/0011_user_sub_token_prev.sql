-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- Aegis migration 0011 — sub-token rotation grace period.
--
-- Why:
--
--   The subscription package's `users.sub_token` can be
--   rotated by the operator (or by the user from the
--   cabinet UI in Phase 2). A naive rotation
--   invalidates the old token immediately, which
--   surprises the end user — the VPN client is still
--   trying to fetch from the old URL and now gets 404.
--
--   The convention in 3X-UI / X-UI is a 24h grace
--   window: the old token keeps working for 24h
--   after rotation, then stops. The end user has
--   time to re-import the new URL on every device.
--
--   We add two columns:
--     sub_token_prev            — the previous token
--     sub_token_prev_expires_at — when it stops working
--   The Service.GetUserBySubToken lookup chain tries
--   the current token first, then the prev token
--   (when present and not yet expired).
--
-- Why a partial index on the previous token:
--
--   Most users never rotate. The previous-token column
--   is empty for ~all rows. A partial index on
--   `sub_token_prev` (WHERE sub_token_prev IS NOT NULL)
--   keeps the lookup O(log n) for the rotated users
--   and zero-cost for everyone else.
--
-- The sub_token lookup chain does NOT need a new
-- index on the primary `sub_token` column — that
-- already has a UNIQUE constraint (migration 0001).

BEGIN;

-- +migrate Up

ALTER TABLE users ADD COLUMN sub_token_prev TEXT NULL;
ALTER TABLE users ADD COLUMN sub_token_prev_expires_at TIMESTAMPTZ NULL;

-- Partial index: only the rotated users. The NULL
-- majority is excluded from the index.
CREATE UNIQUE INDEX users_sub_token_prev_key ON users (sub_token_prev) WHERE sub_token_prev IS NOT NULL;

-- +migrate Down

DROP INDEX IF EXISTS users_sub_token_prev_key;
ALTER TABLE users DROP COLUMN sub_token_prev_expires_at;
ALTER TABLE users DROP COLUMN sub_token_prev;

COMMIT;
