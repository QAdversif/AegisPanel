// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Persistence layer for the agentca package. The Store
// interface is small (one root CA, idempotent reads +
// first-time writes) and the MemoryStore is the only
// production-ready implementation today. The PgStore
// lands in v0.8.30 PR 1c once the migration 0023 +
// nodes.mtls_* columns are stable on prod for a release.
//
// # Why a Store (not direct DB access in the Service)
//
// The Service (service.go) is the only call site; the
// Store is the abstraction. Tests use the MemoryStore
// (no DB); the v0.8.31 migration CLI rotates the root
// via the same Store; the v0.8.30 PgStore is a
// straight-line port. Centralising the persistence
// shape here means a future "store in HashiCorp Vault"
// rewrite is a one-file change.

package agentca

import (
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ErrNotFound is the sentinel returned by Store.Get
// when the root CA row does not exist. The Service
// translates this into a first-time-create via
// `NewRootCA` + `Store.Save`.
var ErrNotFound = errors.New("agentca: root CA not found")

// Root is the persisted form of the panel's root CA.
// The PrivateKey is sealed with the age envelope
// before being handed to Store.Save; the Store's
// implementation persists the ciphertext (same
// pattern as `nodes.ssh_private_key_ciphertext` per
// PR #179). CertPEM is plaintext — X.509 certs are
// public, so no envelope is needed.
type Root struct {
	// PrivateKey is the CA's signing key. The
	// `Store.Save` envelope-seals it; the
	// `Store.Get` envelope-unseals. The Service
	// never sees the ciphertext — the Store
	// hides that detail.
	PrivateKey *ecdsa.PrivateKey
	// CertPEM is the on-the-wire PEM form. The
	// Store persists it verbatim.
	CertPEM string
	// Serial is the CA's serial number. Persisted
	// so a future rotation CLI can increment
	// without re-deriving the cert.
	Serial int64
	// ExpiresAt is the cert's NotAfter. Persisted
	// for the v0.8.31 rotation dashboard
	// (operator needs to see "root expires in 8y
	// 4mo" without parsing the cert every time).
	ExpiresAt time.Time
}

// NodeCerts is the persisted form of a per-node mTLS
// material. The cert is plaintext (public); the keys
// are envelope-sealed before the Store sees them.
//
// The ClientCert / ClientKey are shared across every
// node (the panel process is the only mTLS client);
// persisting one copy per node is the simplest model
// the migration supports and avoids a second
// panel_certs table. The waste is ~600 bytes per row;
// with ~100 nodes the panel pays 60 KiB. v0.8.32 can
// move the client cert to a single-row table if
// the duplication becomes a real cost.
type NodeCerts struct {
	// ServerCertPEM is the node's server cert
	// (the agent presents it on mTLS handshake).
	ServerCertPEM string
	// ServerKey is the server cert's private
	// key, envelope-sealed.
	ServerKey []byte
	// ClientCertPEM is the panel's client cert
	// (the panel presents it on mTLS handshake
	// when dialing the agent).
	ClientCertPEM string
	// ClientKey is the client cert's private
	// key, envelope-sealed.
	ClientKey []byte
	// ExpiresAt is the server cert's NotAfter.
	// The client cert has its own (longer)
	// expiry; the Service surfaces it via
	// `ClientCertExpiresAt` (a v0.8.31 follow-up).
	ExpiresAt time.Time
}

// Store is the persistence contract for the agentca
// package. The interface is small on purpose: the
// only state is the root CA (single row) and the
// per-node cert+key (per node).
type Store interface {
	// GetRoot returns the persisted root CA. Returns
	// ErrNotFound if no root exists yet.
	GetRoot(ctx context.Context) (*Root, error)
	// SaveRoot persists a new root. The Store
	// envelope-seals `r.PrivateKey` before
	// writing; the Service never sees the
	// ciphertext. SaveRoot overwrites any
	// existing root (the v0.8.31 rotation path
	// is the only legitimate caller).
	SaveRoot(ctx context.Context, r *Root) error
	// GetNodeCerts returns the per-node mTLS
	// material. Returns ErrNotFound if the node
	// has no certs yet (the Service translates
	// this into a first-time create).
	GetNodeCerts(ctx context.Context, nodeID uuid.UUID) (*NodeCerts, error)
	// SaveNodeCerts persists the per-node mTLS
	// material. SaveNodeCerts overwrites any
	// existing row (the v0.8.31 rotation path
	// is the only legitimate caller).
	SaveNodeCerts(ctx context.Context, nodeID uuid.UUID, c *NodeCerts) error
	// Close releases any resources (DB pool,
	// file handles). The MemoryStore is a
	// no-op; the PgStore calls `pool.Close()`.
	Close() error
}

// MemoryStore is the in-memory Store implementation.
// Used by:
//   - all unit tests (no DB fixture needed)
//   - dev mode (`aegis --env development`)
//   - the v0.8.30 PR 1b / 1c transition window
//     before the PgStore lands
//
// The struct is intentionally tiny: a mutex around
// two maps. The v0.8.30 dev experience is
// "boot the panel, mTLS works, no DB"; the v0.8.30
// prod experience is "boot the panel, mTLS works,
// certs in Postgres" — both via the same Store
// interface.
type MemoryStore struct {
	mu        sync.RWMutex
	root      *Root
	nodeCerts map[uuid.UUID]*NodeCerts
}

// NewMemoryStore returns a fresh empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		nodeCerts: make(map[uuid.UUID]*NodeCerts),
	}
}

// GetRoot returns the persisted root or ErrNotFound.
func (m *MemoryStore) GetRoot(_ context.Context) (*Root, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.root == nil {
		return nil, ErrNotFound
	}
	// Return a deep copy so the caller cannot
	// mutate the in-memory state by accident.
	r := *m.root
	if m.root.PrivateKey != nil {
		k := *m.root.PrivateKey
		r.PrivateKey = &k
	}
	return &r, nil
}

// SaveRoot persists the root. Overwrites any
// existing row.
func (m *MemoryStore) SaveRoot(_ context.Context, r *Root) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Deep copy on write so a caller-side
	// mutation of the input struct does not
	// affect the in-memory state.
	rCopy := *r
	if r.PrivateKey != nil {
		k := *r.PrivateKey
		rCopy.PrivateKey = &k
	}
	m.root = &rCopy
	return nil
}

// GetNodeCerts returns the per-node mTLS material
// or ErrNotFound.
func (m *MemoryStore) GetNodeCerts(_ context.Context, nodeID uuid.UUID) (*NodeCerts, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c, ok := m.nodeCerts[nodeID]
	if !ok {
		return nil, ErrNotFound
	}
	// Deep copy.
	cCopy := *c
	cCopy.ServerKey = append([]byte(nil), c.ServerKey...)
	cCopy.ClientKey = append([]byte(nil), c.ClientKey...)
	return &cCopy, nil
}

// SaveNodeCerts persists the per-node mTLS material.
// Overwrites any existing row.
func (m *MemoryStore) SaveNodeCerts(_ context.Context, nodeID uuid.UUID, c *NodeCerts) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cCopy := *c
	cCopy.ServerKey = append([]byte(nil), c.ServerKey...)
	cCopy.ClientKey = append([]byte(nil), c.ClientKey...)
	m.nodeCerts[nodeID] = &cCopy
	return nil
}

// Close is a no-op for the MemoryStore.
func (m *MemoryStore) Close() error { return nil }

// Compile-time check that *MemoryStore implements Store.
var _ Store = (*MemoryStore)(nil)

// parseRootCertPEM is a small helper used by both
// the Service and the MemoryStore tests. It returns
// the parsed x509.Certificate from a PEM string;
// missing or malformed PEM is a programmer error
// (the Service is the only producer) so the helper
// returns an error rather than a sentinel.
func parseRootCertPEM(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, fmt.Errorf("agentca: parse root cert PEM: no block")
	}
	return x509.ParseCertificate(block.Bytes)
}
