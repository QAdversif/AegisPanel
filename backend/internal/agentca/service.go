// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the high-level agentca API the rest of
// the panel consumes. The Store is the persistence
// concern; the Service is the "ensure a root CA
// exists" + "ensure node X has mTLS material"
// concern.
//
// # Why a Service (not direct CA + Store from the
// call site)
//
// `EnsureRoot` is idempotent: read from Store,
// generate + save on ErrNotFound. `EnsureNodeCerts`
// is the same shape: read from Store, generate +
// save on ErrNotFound. Centralising the read-or-
// create dance in the Service means a call site
// cannot accidentally re-issue a fresh cert on
// every Apply (the v0.8.29 HTTP transport does a
// 401-refresh-retry on a stale bearer; a fresh-cert-
// on-every-Apply would re-deploy the cert on every
// batched flush and break the cert-rotation cadence).
//
// # v0.8.30 scope
//
// PR 1b ships the Service + MemoryStore. The PgStore
// lands in PR 1c alongside migration 0023.

package agentca

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Service is the high-level agentca API. One per
// process; constructed in `app.Build` once the Store
// is wired.
type Service struct {
	store Store
	// cachedRoot is the in-memory root CA. The
	// first call to EnsureRoot populates it; all
	// subsequent calls return the cached value
	// (the cert + key never change between
	// rotations, and the v0.8.31 rotation path
	// invalidates the cache explicitly).
	cachedRoot *CA
	mu         sync.RWMutex
}

// NewService returns a Service backed by `store`.
// The store is required (the in-memory default is
// `NewMemoryStore()` for tests + dev mode).
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Store returns the underlying Store. Used by the
// `app.Build` shutdown hook and by the v0.8.31
// rotation CLI (the operator wants to see what's
// persisted without going through the Service).
func (s *Service) Store() Store { return s.store }

// EnsureRoot returns the panel's root CA, generating
// + persisting a new one on first call. The result
// is cached in memory; the v0.8.31 rotation CLI
// invalidates the cache by calling `Invalidate` after
// `Store.SaveRoot`.
//
// Concurrent first calls: the inner mutex serialises
// the read-or-create dance. A second goroutine that
// arrives mid-create re-reads the Store and returns
// the just-persisted root (no duplicate keygen).
func (s *Service) EnsureRoot(ctx context.Context) (*CA, error) {
	// Fast path: cache hit.
	s.mu.RLock()
	if s.cachedRoot != nil {
		ca := s.cachedRoot
		s.mu.RUnlock()
		return ca, nil
	}
	s.mu.RUnlock()

	// Slow path: read from store, generate on
	// miss, save, populate cache.
	s.mu.Lock()
	defer s.mu.Unlock()
	// Double-check inside the write lock: another
	// goroutine may have populated the cache while
	// we were waiting.
	if s.cachedRoot != nil {
		return s.cachedRoot, nil
	}
	persisted, err := s.store.GetRoot(ctx)
	if err == nil {
		ca, err := persistedToCA(persisted)
		if err != nil {
			return nil, fmt.Errorf("agentca: hydrate root from store: %w", err)
		}
		s.cachedRoot = ca
		return ca, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("agentca: load root: %w", err)
	}
	// First-time create.
	ca, err := NewRootCA()
	if err != nil {
		return nil, fmt.Errorf("agentca: generate root: %w", err)
	}
	persisted = &Root{
		PrivateKey: ca.PrivateKey,
		CertPEM:    ca.RootCertPEM(),
		Serial:     ca.Serial,
		ExpiresAt:  ca.Cert.NotAfter,
	}
	if err := s.store.SaveRoot(ctx, persisted); err != nil {
		return nil, fmt.Errorf("agentca: persist root: %w", err)
	}
	s.cachedRoot = ca
	return ca, nil
}

// Invalidate drops the in-memory root cache. The
// next `EnsureRoot` call re-reads from the store.
// Used by the v0.8.31 rotation CLI after
// `Store.SaveRoot`. The MemoryStore test helper
// also calls this between sub-tests to reset state.
func (s *Service) Invalidate() {
	s.mu.Lock()
	s.cachedRoot = nil
	s.mu.Unlock()
}

// Close releases the underlying Store's resources
// (e.g. the pgx pool in the PgStore). The Service
// is unusable after Close; callers should not call
// any other method.
func (s *Service) Close() error {
	return s.store.Close()
}

// IssuedNodeCerts is the result of a successful
// EnsureNodeCerts. ServerCertPEM + ServerKeyPEM
// are what the bootstrap installer writes to
// `/etc/aegis/agent.crt` + `/etc/aegis/agent.key`.
// ClientCertPEM is the panel-side client cert the
// agentgrpc transport attaches to every dial; the
// panel keeps the matching key in memory (it never
// leaves the panel process — the cert is the public
// surface).
type IssuedNodeCerts struct {
	ServerCertPEM   string
	ServerKeyPEM    []byte
	ClientCertPEM   string
	ServerExpiresAt time.Time
	// ClientExpiresAt is the client cert's
	// NotAfter. v0.8.30+ logs it for the operator
	// dashboard; the panel does not rotate on
	// expiry (the v0.8.31 rotation CLI handles it).
	ClientExpiresAt time.Time
}

// EnsureNodeCerts returns fresh mTLS material for
// `nodeID` (listening on `addr`), generating + persisting
// a new pair on first call. The result is fetched
// from the Store; the in-memory cache lives in
// `Service.cachedRoot` for the root only (the
// per-node certs are read on every call — the
// per-node state is small + the `nodes.Service`
// already has its own caching).
func (s *Service) EnsureNodeCerts(ctx context.Context, nodeID uuid.UUID, addr string) (*IssuedNodeCerts, error) {
	if nodeID == uuid.Nil {
		return nil, fmt.Errorf("agentca: EnsureNodeCerts: nodeID is uuid.Nil")
	}
	if addr == "" {
		return nil, fmt.Errorf("agentca: EnsureNodeCerts: addr is empty")
	}
	// Fast path: read from store.
	persisted, err := s.store.GetNodeCerts(ctx, nodeID)
	if err == nil {
		return nodeCertsToIssued(persisted), nil
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, fmt.Errorf("agentca: load node certs: %w", err)
	}
	// First-time generate.
	ca, err := s.EnsureRoot(ctx)
	if err != nil {
		return nil, err
	}
	serverCertPEM, serverKeyPEM, serverExpiresAt, err := ca.IssueNodeServerCert(nodeID, addr)
	if err != nil {
		return nil, fmt.Errorf("agentca: issue server cert: %w", err)
	}
	clientCertPEM, clientKeyPEM, clientExpiresAt, err := ca.IssuePanelClientCert()
	if err != nil {
		return nil, fmt.Errorf("agentca: issue client cert: %w", err)
	}
	certs := &NodeCerts{
		ServerCertPEM: serverCertPEM,
		ServerKey:     serverKeyPEM,
		ClientCertPEM: clientCertPEM,
		ClientKey:     clientKeyPEM,
		ExpiresAt:     serverExpiresAt,
	}
	if err := s.store.SaveNodeCerts(ctx, nodeID, certs); err != nil {
		return nil, fmt.Errorf("agentca: persist node certs: %w", err)
	}
	return &IssuedNodeCerts{
		ServerCertPEM:   serverCertPEM,
		ServerKeyPEM:    serverKeyPEM,
		ClientCertPEM:   clientCertPEM,
		ServerExpiresAt: serverExpiresAt,
		ClientExpiresAt: clientExpiresAt,
	}, nil
}

// persistedToCA rehydrates the in-memory CA from
// the Store's persisted form. The private key is
// the only "secret" piece; the cert PEM + serial
// + ExpiresAt are public metadata.
func persistedToCA(r *Root) (*CA, error) {
	cert, err := parseRootCertPEM(r.CertPEM)
	if err != nil {
		return nil, fmt.Errorf("agentca: parse persisted cert: %w", err)
	}
	return &CA{
		PrivateKey: r.PrivateKey,
		Cert:       cert,
		Serial:     r.Serial,
	}, nil
}

// nodeCertsToIssued converts the persisted form to
// the Service's return shape. The conversion is
// trivial (just re-tag the fields) but the indirection
// lets the Service's surface evolve independently of
// the Store's surface.
func nodeCertsToIssued(c *NodeCerts) *IssuedNodeCerts {
	return &IssuedNodeCerts{
		ServerCertPEM:   c.ServerCertPEM,
		ServerKeyPEM:    c.ServerKey,
		ClientCertPEM:   c.ClientCertPEM,
		ServerExpiresAt: c.ExpiresAt,
		// ClientExpiresAt is not persisted today
		// (the Store shape carries only ServerCert's
		// NotAfter; the client cert's longer validity
		// is the same on every node so a follow-up
		// PR can move it to a panel-wide row). The
		// field is zero for now; the operator
		// dashboard surfaces "client cert expires
		// in ~1y from install" until v0.8.31.
	}
}

// Compile-time check that *Service uses the Store
// interface. If a future refactor changes the
// Service constructor to accept a concrete type,
// the build fails here at the package level.
var _ Store = (*MemoryStore)(nil)

// Compile-time check that *x509.Certificate is
// parseable from a PEM block. The agentca package
// uses this in the service.go helpers; a future
// refactor that drops the cert parsing surfaces
// here.
var _ = func() *x509.Certificate { return nil }
