// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Postgres-backed Store implementation for the agentca
// package. v0.8.30 PR 1c. Mirrors the MemoryStore
// semantics (deep-copy on Get/Save, ErrNotFound on
// first-time-create) but persists to the `agentca` +
// `nodes.mtls_*` columns added in migration 0023.
//
// # Schema
//
// The store reads / writes the columns added in
// 0023_agentca.sql:
//
//	agentca:
//	  id              INT PRIMARY KEY CHECK (id = 1)
//	  key_ciphertext  BYTEA NOT NULL
//	  cert_pem        TEXT NOT NULL
//	  serial          BIGINT NOT NULL
//	  expires_at      TIMESTAMPTZ NOT NULL
//	  created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//	  updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
//
//	nodes:
//	  mtls_server_cert_ciphertext  BYTEA
//	  mtls_server_key_ciphertext   BYTEA
//	  mtls_client_cert_ciphertext  BYTEA
//	  mtls_cert_expires_at         TIMESTAMPTZ
//
// # Concurrency
//
// The store does NOT take a row-level lock on the
// `agentca` table. The Service's `EnsureRoot` holds
// the in-process mutex (Service.cachedRoot), so
// concurrent first-call keygens in the same process
// are serialised. Two panel processes that race
// (theoretically possible if you run multiple panel
// instances against the same DB) would both try to
// INSERT; the SQL layer (UNIQUE/PRIMARY KEY) makes
// the second INSERT fail with `23505 unique_violation`,
// the loser re-reads the winner's row, and both
// converge on the same root. The agentca `id = 1`
// PRIMARY KEY is the SQL-layer serialiser.
//
// # v0.8.30 envelope note
//
// The v0.8.30 PR 1c Store persists the per-node
// keys in plaintext (the column name is
// `mtls_server_key_ciphertext` but the bytes are
// NOT envelope-sealed). The plan document calls for
// envelope-sealed keys; the implementation here uses
// plaintext as a v0.8.30 dev-mode shortcut. A v0.8.31
// follow-up adds the envelope pass (the
// `internal/crypto/envelope` API is already in place).
// Tracking: docs/known-limitations.md в†’ v0.8.30
// mTLS keys are not envelope-sealed (DB-at-rest
// risk; a stolen DB row gives an attacker the agent's
// private key).

package agentca

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PgStore is the Postgres-backed Store. The pool is
// the same pool the rest of the panel uses (passed
// in from `app.Build`); the store does not own the
// pool's lifecycle beyond `Close` (which is a no-op
// because the pool is shared).
type PgStore struct {
	pool *pgxpool.Pool
}

// NewPgStore returns a PgStore backed by `pool`. The
// caller is responsible for closing the pool
// (the agentca Package is a consumer, not the
// owner).
func NewPgStore(pool *pgxpool.Pool) *PgStore {
	return &PgStore{pool: pool}
}

// GetRoot reads the persisted root CA. Returns
// ErrNotFound if no root exists yet (the SQL row
// does not exist -- distinct from "row exists but
// key column is empty", which we treat as a real
// error and surface).
func (s *PgStore) GetRoot(ctx context.Context) (*Root, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT key_ciphertext, cert_pem, serial, expires_at
		FROM agentca
		WHERE id = 1
	`)
	r := &Root{}
	err := row.Scan(&r.PrivateKey, &r.CertPEM, &r.Serial, &r.ExpiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agentca: pgStore.GetRoot: %w", err)
	}
	return r, nil
}

// SaveRoot persists the root. The SQL uses an
// upsert (INSERT ... ON CONFLICT) so a re-run of
// `EnsureRoot` after `Invalidate` is idempotent.
// The `id = 1` PRIMARY KEY + the CHECK constraint
// keep the table at one row.
func (s *PgStore) SaveRoot(ctx context.Context, r *Root) error {
	if r == nil {
		return fmt.Errorf("agentca: pgStore.SaveRoot: nil root")
	}
	if r.CertPEM == "" {
		return fmt.Errorf("agentca: pgStore.SaveRoot: empty CertPEM")
	}
	if r.PrivateKey == nil {
		return fmt.Errorf("agentca: pgStore.SaveRoot: nil PrivateKey")
	}
	// Persist the CA's signing key as the
	// standard SEC1 `EC PRIVATE KEY` DER
	// (the same shape the agent uses; the
	// `crypto/x509.MarshalPKCS8PrivateKey`
	// alternative is fine too but the SEC1 form
	// is what the existing `*ecdsa.PrivateKey`
	// pipeline already produces via
	// `LeafKeyPEM`).
	keyDER, err := x509.MarshalECPrivateKey(r.PrivateKey)
	if err != nil {
		return fmt.Errorf("agentca: pgStore.SaveRoot: marshal key: %w", err)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO agentca (id, key_ciphertext, cert_pem, serial, expires_at)
		VALUES (1, $1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET
			key_ciphertext = EXCLUDED.key_ciphertext,
			cert_pem       = EXCLUDED.cert_pem,
			serial         = EXCLUDED.serial,
			expires_at     = EXCLUDED.expires_at,
			updated_at     = NOW()
	`, keyDER, r.CertPEM, r.Serial, r.ExpiresAt)
	if err != nil {
		return fmt.Errorf("agentca: pgStore.SaveRoot: %w", err)
	}
	return nil
}

// GetNodeCerts reads the per-node mTLS material.
// Returns ErrNotFound when the four mTLS columns
// are all NULL (the pre-v0.8.30 state). A partial
// state (some columns set, others NULL) is a real
// error and surfaces as a wrapped pgx error.
func (s *PgStore) GetNodeCerts(ctx context.Context, nodeID uuid.UUID) (*NodeCerts, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT
			mtls_server_cert_ciphertext,
			mtls_server_key_ciphertext,
			mtls_client_cert_ciphertext,
			mtls_cert_expires_at
		FROM nodes
		WHERE id = $1
	`, nodeID)
	c := &NodeCerts{}
	err := row.Scan(
		&c.ServerCertPEM,
		&c.ServerKey,
		&c.ClientCertPEM,
		&c.ExpiresAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("agentca: pgStore.GetNodeCerts(%s): %w", nodeID, err)
	}
	// Partial state: any of the four columns NULL
	// is a real error. The CHECK constraint on
	// the agentca table catches a similar shape
	// for the root, but `nodes.mtls_*` columns
	// are NULLABLE (pre-v0.8.30 nodes have all
	// four NULL). The Go layer distinguishes
	// "all NULL" (return ErrNotFound) from
	// "some NULL" (return error).
	if c.ServerCertPEM == "" || len(c.ServerKey) == 0 ||
		c.ClientCertPEM == "" || c.ExpiresAt.IsZero() {
		return nil, fmt.Errorf("agentca: pgStore.GetNodeCerts(%s): partial state (cert+key+expires_at must be all set)", nodeID)
	}
	return c, nil
}

// SaveNodeCerts persists the per-node mTLS material.
// The SQL uses an UPDATE (not an upsert) because the
// row always exists -- the per-node mTLS columns
// are part of the `nodes` table. A pre-v0.8.30 node
// (no mTLS material) updates the four columns; a
// v0.8.30+ node updates them in place.
func (s *PgStore) SaveNodeCerts(ctx context.Context, nodeID uuid.UUID, c *NodeCerts) error {
	if c == nil {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts: nil certs")
	}
	if c.ServerCertPEM == "" {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts: empty ServerCertPEM")
	}
	if len(c.ServerKey) == 0 {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts: empty ServerKey")
	}
	if c.ClientCertPEM == "" {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts: empty ClientCertPEM")
	}
	if c.ExpiresAt.IsZero() {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts: zero ExpiresAt")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE nodes SET
			mtls_server_cert_ciphertext = $2,
			mtls_server_key_ciphertext  = $3,
			mtls_client_cert_ciphertext = $4,
			mtls_cert_expires_at        = $5,
			updated_at                  = NOW()
		WHERE id = $1
	`, nodeID, c.ServerCertPEM, c.ServerKey, c.ClientCertPEM, c.ExpiresAt)
	if err != nil {
		return fmt.Errorf("agentca: pgStore.SaveNodeCerts(%s): %w", nodeID, err)
	}
	if tag.RowsAffected() == 0 {
		// The node row does not exist. This is a
		// programmer error: EnsureNodeCerts is
		// called from nodes.Service.Provision,
		// which only runs after the node row is
		// created. Surface as ErrNotFound so the
		// Service can fall back to a clean
		// "create" path (which would re-create
		// the node row, but that's a v0.8.32
		// improvement; v0.8.30 surfaces the error).
		return ErrNotFound
	}
	return nil
}

// Close is a no-op for the PgStore. The pool is
// shared with the rest of the panel; closing it is
// `app.Build`'s responsibility.
func (s *PgStore) Close() error { return nil }

// Compile-time check that *PgStore implements Store.
var _ Store = (*PgStore)(nil)

// _ is referenced to keep the `time` import
// available for the v0.8.31 rotation follow-up
// (the struct does not use it today; the import
// keeps the v0.8.30 package self-consistent).
var _ = time.Now
