-- SPDX-License-Identifier: AGPL-3.0-or-later
--
-- +migrate Up
--
-- v0.8.30 -- agent CA (mTLS cert bootstrap) + per-node mTLS material.
--
-- # Why this migration
--
-- The v0.8.30 mTLS+gRPC control plane (ARCHITECTURE.md
-- §7.5, mTLS contract) needs a self-signed root CA + a
-- per-node server cert + a panel-wide client cert. The
-- cert-generation core lands in `internal/agentca`
-- (v0.8.30 PR 1a / 1b); the persistence lands here.
--
-- The panel owns a single self-signed root CA, stored
-- in the new `agentca` table with the private key
-- sealed with the operator's age envelope (the same
-- pattern as `nodes.ssh_private_key_ciphertext` per
-- PR #179). The cert is plaintext (X.509 certs are
-- public, so no envelope is needed).
--
-- Per-node certs are stored in the `nodes` table itself
-- (the cert+key are node-scoped). The client cert is
-- panel-wide; for v0.8.30 we store one copy per node
-- (a v0.8.32 follow-up may move it to a single-row
-- panel_certs table; the duplication is ~600 bytes
-- per node, ~60 KiB at 100 nodes -- not worth a
-- separate table for the v0.8.30 release).
--
-- # Schema
--
-- `agentca`:
--   - `id` INT PRIMARY KEY CHECK (id = 1) -- the root
--     is process-wide. The CHECK enforces "exactly
--     one root"; a future rotation CLI bumps the
--     serial but does not add a row.
--   - `key_ciphertext` BYTEA NOT NULL -- the CA's
--     signing key, envelope-sealed. Decoded only in
--     memory (the agentca Service holds it for the
--     duration of the boot).
--   - `cert_pem` TEXT NOT NULL -- the on-the-wire
--     PEM. The panel pushes this to the node as
--     `/etc/aegis/agent-ca.pem`.
--   - `serial` BIGINT NOT NULL -- the CA's serial
--     (RFC 5280 §4.1.2.2). Persisted so a future
--     rotation CLI can `SELECT serial FROM agentca`
--     to compute the next CA's serial.
--   - `expires_at` TIMESTAMPTZ NOT NULL -- the
--     root's NotAfter. The operator dashboard
--     surfaces "root expires in 8y 4mo" without
--     parsing the cert every time.
--   - `created_at` / `updated_at` TIMESTAMPTZ.
--
-- `nodes` (additive ALTER TABLE):
--   - `mtls_server_cert_ciphertext` BYTEA -- the
--     per-node server cert. Envelope-sealed? No:
--     X.509 certs are public, so this is stored
--     plaintext. The column name is kept
--     `*_ciphertext` for symmetry with the SSH
--     key column; v0.8.32 may rename to
--     `mtls_server_cert_pem` if a future refactor
--     finds the inconsistency confusing.
--   - `mtls_server_key_ciphertext` BYTEA -- the
--     server cert's private key, envelope-sealed.
--   - `mtls_client_cert_ciphertext` BYTEA --
--     panel-side client cert. Plaintext. Same
--     naming-convention reservation as the server
--     cert.
--   - `mtls_cert_expires_at` TIMESTAMPTZ -- the
--     server cert's NotAfter. Client cert's expiry
--     is longer; the v0.8.31 rotation dashboard
--     surfaces it via the audit log.
--
-- # Downstream
--
-- - `internal/agentca/pg_store.go` (PR 1c) reads +
--   writes these columns.
-- - `internal/nodes/mtls.go` (PR 1c) calls
--   `agentca.Service.EnsureNodeCerts` from
--   `nodes.Service.Provision`.
-- - `internal/bootstrap/installer.go` (PR 1c) reads
--   the per-node mTLS material and pushes the
--   cert+key to the node via the existing
--   `UploadAndSwap` (ETXTBSY-safe) channel.
-- - `internal/agentgrpc/http_transport.go` and
--   `grpc_transport.go` (v0.8.30 PR 2) read the
--   material and dial the agent with mTLS.
--
-- # v0.8.30 dev path
--
-- The MemoryStore (v0.8.30 PR 1b) is the dev-mode
-- fallback. The migration runs only when the
-- operator boots the panel with `AEGIS_*_BACKEND=pg`;
-- the dev `AEGIS_*_BACKEND=memory` path skips
-- migrations entirely (the panel prints
-- "all stores in memory; skipping pg pool").

BEGIN;

-- The agentca table. The id CHECK enforces "exactly
-- one root" so a future bug that tries to insert
-- row 2 fails loudly at the SQL layer (the Go layer
-- also checks, but the SQL CHECK is the safety net).
CREATE TABLE IF NOT EXISTS agentca (
    id              INT PRIMARY KEY DEFAULT 1 CHECK (id = 1),
    key_ciphertext  BYTEA NOT NULL,
    cert_pem        TEXT NOT NULL,
    serial          BIGINT NOT NULL,
    expires_at      TIMESTAMPTZ NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Per-node mTLS material. The cert columns are
-- plaintext (X.509 is public); the key column is
-- envelope-sealed. All four columns are NULLABLE
-- because pre-v0.8.30 nodes have no certs; the
-- bootstrap installer writes them on next provision.
ALTER TABLE nodes
    ADD COLUMN IF NOT EXISTS mtls_server_cert_ciphertext  BYTEA,
    ADD COLUMN IF NOT EXISTS mtls_server_key_ciphertext   BYTEA,
    ADD COLUMN IF NOT EXISTS mtls_client_cert_ciphertext  BYTEA,
    ADD COLUMN IF NOT EXISTS mtls_cert_expires_at         TIMESTAMPTZ;

-- Index on the per-node cert expiry so the
-- v0.8.31 rotation dashboard can list "nodes
-- expiring in the next 30 days" cheaply
-- (`WHERE mtls_cert_expires_at < NOW() + interval
-- '30 days'`). Partial index excludes the 99% of
-- rows where the column is NULL (pre-v0.8.30 nodes).
CREATE INDEX IF NOT EXISTS nodes_mtls_expiring_idx
    ON nodes (mtls_cert_expires_at)
    WHERE mtls_cert_expires_at IS NOT NULL;

-- +migrate Down

DROP INDEX IF EXISTS nodes_mtls_expiring_idx;
ALTER TABLE IF EXISTS nodes
    DROP COLUMN IF EXISTS mtls_cert_expires_at,
    DROP COLUMN IF EXISTS mtls_client_cert_ciphertext,
    DROP COLUMN IF EXISTS mtls_server_key_ciphertext,
    DROP COLUMN IF EXISTS mtls_server_cert_ciphertext;
DROP TABLE IF EXISTS agentca;

COMMIT;
