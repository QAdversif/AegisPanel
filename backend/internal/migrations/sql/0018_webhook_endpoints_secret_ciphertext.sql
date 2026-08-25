-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- +migrate Up
--
-- v0.7.x — move `webhook_endpoints.secret` under
-- the age envelope. v0.7.0 stored the HMAC key
-- in plaintext; v0.7.x encrypts the value with
-- the operator's age public key before insert
-- and decrypts on read with the panel's age
-- identity (see `backend/internal/webhooks/secret.go`).
--
-- The migration is a hard rename + type change
-- because:
--
--   1. There is no production data on the live
--      deploy (which is v0.4.0, well before
--      v0.7.0). The first row in
--      `webhook_endpoints` will be inserted by
--      a v0.7.x-era operator, who will use the
--      new Store layer that encrypts.
--
--   2. Mixing an old plaintext `secret` column
--      with a new `secret_ciphertext` BYTEA
--      column would force the Service to keep
--      the plaintext path alive forever, which
--      is the security regression we are
--      closing. A clean break is safer.
--
-- The `webhook_pending_retries` table (added in
-- 0017) holds delivery_id only — it does not
-- touch the secret and needs no change.
--
-- Both ALTER statements are single-line per the
-- sqlfluff LT02 rule (no indented multi-line
-- ALTERs).

ALTER TABLE webhook_endpoints RENAME COLUMN secret TO secret_ciphertext;
ALTER TABLE webhook_endpoints ALTER COLUMN secret_ciphertext TYPE BYTEA USING secret_ciphertext::BYTEA;

-- +migrate Down

-- The Down migration is symmetric. A fresh
-- v0.7.0 deploy (live is v0.4.0, so no rollback
-- is on the critical path) restores the
-- plaintext TEXT column. The data is
-- ciphertext at this point (the Store encrypted
-- it before INSERT), so the cast produces
-- garbage bytes — but that is the operator's
-- signal that the rollback lost the data. The
-- operator must re-enter the secret.
ALTER TABLE webhook_endpoints ALTER COLUMN secret_ciphertext TYPE TEXT USING convert_from(secret_ciphertext, 'UTF8');
ALTER TABLE webhook_endpoints RENAME COLUMN secret_ciphertext TO secret;
