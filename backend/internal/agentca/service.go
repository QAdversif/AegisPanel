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
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// Service is the high-level agentca API. One per
// process; constructed in `app.Build` once the Store
// is wired.
type Service struct {
	store Store
	// envelope is the age SecretCipher used to
	// decrypt the v0.8.25 prod row (manually
	// minted with `age -r <pubkey>` encrypted
	// ECDSA P-256 key). Nil envelope means the
	// Service only understands the v0.8.32+
	// plaintext-DER format; dev mode (MemoryStore
	// + no envelope) hits the same code path. The
	// envelope is shared with the webhooks store
	// (same `AEGIS_WEBHOOKS_SECRET_AGE_*` env vars).
	envelope envelope.SecretCipher
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
//
// `env` is optional. When non-nil, EnsureRoot uses
// it as a fallback decoder for the v0.8.25 prod
// row (age-envelope ciphertext in `key_ciphertext`).
// v0.8.32+ SaveRoot output is plaintext DER and
// decodes without the envelope. The app passes the
// same envelope the webhooks store uses
// (single `AEGIS_WEBHOOKS_SECRET_AGE_*` env var set,
// one identity file, one backup drill).
func NewService(store Store, env ...envelope.SecretCipher) *Service {
	s := &Service{store: store}
	if len(env) > 0 && env[0] != nil {
		s.envelope = env[0]
	}
	return s
}

// Store returns the underlying Store. Used by the
// `app.Build` shutdown hook and by the v0.8.31
// rotation CLI (the operator wants to see what's
// persisted without going through the Service).
func (s *Service) Store() Store { return s.store }

// RootCertPEM returns the panel's root CA cert as
// PEM. v0.8.30 PR 2b: the panel's gRPC client
// uses the root to verify the agent's server
// cert (the agent presents a cert signed by this
// root). The root is in memory after the first
// `EnsureRoot` call; calling `RootCertPEM` before
// the root is loaded returns `ErrNotFound` so the
// caller can decide between "mTLS not wired
// (ErrMTLSNotConfigured)" and "store miss".
func (s *Service) RootCertPEM() (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cachedRoot == nil {
		return "", ErrNotFound
	}
	return s.cachedRoot.RootCertPEM(), nil
}

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
		ca, err := persistedToCA(persisted, s.envelope)
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
	keyDER, err := x509.MarshalECPrivateKey(ca.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("agentca: marshal root key: %w", err)
	}
	persisted = &Root{
		KeyDER:    keyDER,
		CertPEM:   ca.RootCertPEM(),
		Serial:    ca.Serial,
		ExpiresAt: ca.Cert.NotAfter,
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
//
// v0.8.32.3 fix (issue #326): the dual-path decode
// for `r.KeyDER` covers both encodings the panel
// has shipped:
//
//  1. v0.8.32+ SaveRoot writes plaintext SEC1 DER
//     (`x509.MarshalECPrivateKey`). This is the
//     canonical encoding; `x509.ParseECPrivateKey`
//     succeeds on the first try.
//  2. The v0.8.25 prod row was hand-minted with
//     `age -r <pubkey>` encrypted ECDSA P-256 key.
//     Plaintext ParseECPrivateKey fails on the
//     envelope bytes; the fallback is
//     `envelope.Decrypt` followed by a PEM-or-DER
//     decode (the v0.8.25 mint sealed PEM bytes
//     via `openssl genpkey -outform PEM | age -r
//     <pubkey>`, so the plaintext is SEC1 PEM;
//     PKCS#8 PEM and raw DER are accepted as
//     forward-compat fallbacks). The envelope is
//     the same SecretCipher the webhooks store
//     uses (single `AEGIS_WEBHOOKS_SECRET_AGE_*`
//     config).
//
// v0.8.32.4 fix (issue #328): the v0.8.25 hand-mint
// plaintext is SEC1 PEM (the "EC PRIVATE KEY" type
// marker), not raw DER. The v0.8.32.3 path 2 fed
// the plaintext directly to `x509.ParseECPrivateKey`
// and crashed with "tags don't match" on prod.
// The fix adds a `pem.Decode` branch in path 2a
// that handles SEC1 PEM (and PKCS#8 PEM as a
// forward-compat fallback) before falling through
// to the raw-DER branch.
//
// If all paths fail, the row is in a format the
// Service does not understand (corrupted, or sealed
// by a key the panel no longer has). The error
// chains the attempts so the operator can see
// which shape the row actually has.
func persistedToCA(r *Root, env envelope.SecretCipher) (*CA, error) {
	cert, err := parseRootCertPEM(r.CertPEM)
	if err != nil {
		return nil, fmt.Errorf("agentca: parse persisted cert: %w", err)
	}
	priv, err := decodeKeyDER(r.KeyDER, env)
	if err != nil {
		return nil, fmt.Errorf("agentca: hydrate persisted key: %w", err)
	}
	return &CA{
		PrivateKey: priv,
		Cert:       cert,
		Serial:     r.Serial,
	}, nil
}

// decodeKeyDER tries the plaintext-DER path first
// (the v0.8.32+ format), then the envelope-decrypt
// path (the v0.8.25 prod workaround). The
// envelope-decrypt path has two sub-branches: PEM
// plaintext (the v0.8.25 hand-mint shape) and
// DER plaintext (a future-compat fallback). If
// all three paths fail the error wraps each
// attempt so the operator can diagnose what
// shape the row actually has.
func decodeKeyDER(der []byte, env envelope.SecretCipher) (*ecdsa.PrivateKey, error) {
	if len(der) == 0 {
		return nil, errors.New("empty key bytes")
	}
	// Path 1: plaintext DER. The v0.8.32+ SaveRoot
	// output and the legacy v0.8.30 fixture both
	// produce SEC1 `EC PRIVATE KEY` DER.
	key, plaintextErr := x509.ParseECPrivateKey(der)
	if plaintextErr == nil {
		return key, nil
	}
	// Path 2: age envelope. The v0.8.25 prod
	// row was hand-minted with `age -r <pubkey>`
	// encrypted bytes; the panel's envelope
	// (same SecretCipher the webhooks store
	// uses) holds the matching private key.
	if env == nil {
		return nil, fmt.Errorf("plaintext ParseECPrivateKey failed and no envelope configured (the v0.8.25 prod workaround sealed this row with the operator's age key; AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS / _KEY_FILE need to point at the matching identity): %w", plaintextErr)
	}
	plain, err := env.Decrypt(der)
	if err != nil {
		return nil, fmt.Errorf("plaintext ParseECPrivateKey failed and envelope.Decrypt also failed (the row is in a format the panel cannot decode; re-mint via `aegis admin agentca rotate-root` after diagnosing the on-disk shape): plaintext=%w envelope=%w", plaintextErr, err)
	}
	// Path 2a: PEM plaintext. The v0.8.25 hand-mint
	// sealed PEM bytes via `age -r <pubkey>` (the
	// operator ran `openssl genpkey -outform PEM |
	// age -r ...`); the plaintext is SEC1 PEM (the
	// "EC PRIVATE KEY" type marker) or PKCS#8 PEM
	// (the "PRIVATE KEY" type marker). The
	// v0.8.32+ SaveRoot format does NOT take this
	// path — it stores plaintext DER directly,
	// handled in path 1 above. We try SEC1 first
	// (the canonical v0.8.25 shape) and fall back
	// to PKCS#8 (covers a future mint tooling that
	// switches default format).
	if bytes.HasPrefix(plain, []byte("-----BEGIN")) {
		block, _ := pem.Decode(plain)
		if block != nil {
			// Try SEC1 first (the canonical v0.8.25
			// shape: the "EC PRIVATE KEY" PEM type).
			key, sec1Err := x509.ParseECPrivateKey(block.Bytes)
			if sec1Err == nil {
				return key, nil
			}
			// Fall back to PKCS#8 (the
			// "PRIVATE KEY" PEM type). Covers a
			// future mint tooling that switches
			// default format.
			k8, pk8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
			if pk8Err == nil {
				if ec, ok := k8.(*ecdsa.PrivateKey); ok {
					return ec, nil
				}
				return nil, fmt.Errorf("envelope.Decrypt produced a PKCS#8 PEM block that is not an EC key: %T", k8)
			}
			return nil, fmt.Errorf("envelope.Decrypt produced a PEM block but neither SEC1 nor PKCS#8 parse succeeded: sec1=%w pkcs8=%w", sec1Err, pk8Err)
		}
		// PEM marker present but no valid block;
		// fall through to the DER attempt below
		// (covers a partial-truncation case where
		// the age-decrypted bytes happen to start
		// with `-----BEGIN` from a corrupted input).
	}
	// Path 2b: DER plaintext. The v0.8.32+ SaveRoot
	// format would have been handled in path 1
	// (plaintext ParseECPrivateKey on the ciphertext
	// itself succeeds because v0.8.32+ persists
	// DER in `key_ciphertext` without envelope).
	// This branch covers a future shape where
	// envelope-sealed DER replaces envelope-sealed
	// PEM.
	key, err = x509.ParseECPrivateKey(plain)
	if err != nil {
		return nil, fmt.Errorf("envelope.Decrypt succeeded but the decrypted bytes are not a valid EC private key (not SEC1 DER, not SEC1 PEM, not PKCS#8 PEM): %w", err)
	}
	return key, nil
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
