// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the Service + MemoryStore layer. v0.8.30
// PR 1b ships the in-memory path; the PgStore lands
// in PR 1c (the migration + per-node certs are stable
// on prod for a release before the prod-only path
// cuts over).
//
// The tests cover the contract surface:
//
//  1. EnsureRoot is idempotent (first call creates,
//     second call returns the same cert).
//  2. EnsureRoot populates the in-memory cache.
//  3. Invalidate drops the cache; EnsureRoot
//     re-reads from the Store (used by the v0.8.31
//     rotation CLI).
//  4. EnsureNodeCerts is idempotent per node.
//  5. EnsureNodeCerts persists certs that validate
//     against the same root.
//  6. EnsureNodeCerts rejects uuid.Nil + empty addr.
//  7. Concurrent EnsureRoot calls do not race (the
//     inner mutex serialises the read-or-create).
//  8. The MemoryStore deep-copies on Save / Get
//     so a caller-side mutation cannot corrupt
//     the in-memory state.

package agentca

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestService_EnsureRoot_HappyPath verifies the
// first-call create + second-call idempotent path.
func TestService_EnsureRoot_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	ca1, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (first call): %v", err)
	}
	if ca1 == nil || ca1.Cert == nil {
		t.Fatal("EnsureRoot returned nil CA / nil cert")
	}
	ca2, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (second call): %v", err)
	}
	// Same cert: idempotent. We compare the cert's
	// Raw bytes (the cert doesn't change between
	// calls; the cache returns the same pointer).
	if ca1.Cert != ca2.Cert {
		t.Errorf("EnsureRoot second call returned a different cert (not idempotent)")
	}
}

// TestService_EnsureRoot_PersistsToStore verifies
// the first-call create lands in the Store, not
// just the in-memory cache.
func TestService_EnsureRoot_PersistsToStore(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	if _, err := store.GetRoot(ctx); !hasErrNotFound(err) {
		t.Fatalf("store.GetRoot before EnsureRoot: got err=%v, want ErrNotFound", err)
	}
	ca, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	persisted, err := store.GetRoot(ctx)
	if err != nil {
		t.Fatalf("store.GetRoot after EnsureRoot: %v", err)
	}
	if persisted.CertPEM != ca.RootCertPEM() {
		t.Errorf("store.CertPEM != ca.RootCertPEM: store=%q, ca=%q", persisted.CertPEM, ca.RootCertPEM())
	}
	if persisted.Serial != ca.Serial {
		t.Errorf("store.Serial=%d, ca.Serial=%d", persisted.Serial, ca.Serial)
	}
}

// TestService_Invalidate_ReadsFromStore pins the
// rotation contract: Invalidate drops the cache;
// the next EnsureRoot reads from the Store (where
// the rotation CLI would have written a new root).
func TestService_Invalidate_ReadsFromStore(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	ca1, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot: %v", err)
	}
	// Overwrite the store with a fresh root (the
	// v0.8.31 rotation CLI does this; we simulate
	// it inline because the CLI is a separate PR).
	ca2, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	if err := store.SaveRoot(ctx, &Root{
		PrivateKey: ca2.PrivateKey,
		CertPEM:    ca2.RootCertPEM(),
		Serial:     ca2.Serial,
		ExpiresAt:  ca2.Cert.NotAfter,
	}); err != nil {
		t.Fatalf("store.SaveRoot: %v", err)
	}
	// The Service's cache is still the old root.
	svc.Invalidate()
	ca3, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot after Invalidate: %v", err)
	}
	if ca3.Serial != ca2.Serial {
		t.Errorf("EnsureRoot after Invalidate: serial=%d, want %d (the new root)", ca3.Serial, ca2.Serial)
	}
	if ca1.Serial == ca3.Serial {
		t.Errorf("Invalidate did not drop the cache (got the same serial %d before and after)", ca1.Serial)
	}
}

// TestService_EnsureNodeCerts_HappyPath verifies
// the per-node read-or-create + validation against
// the root.
func TestService_EnsureNodeCerts_HappyPath(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	nodeID := uuid.New()
	addr := "10.0.0.5:7001"

	issued, err := svc.EnsureNodeCerts(ctx, nodeID, addr)
	if err != nil {
		t.Fatalf("EnsureNodeCerts: %v", err)
	}
	if issued.ServerCertPEM == "" {
		t.Error("ServerCertPEM is empty")
	}
	if len(issued.ServerKeyPEM) == 0 {
		t.Error("ServerKeyPEM is empty")
	}
	if issued.ClientCertPEM == "" {
		t.Error("ClientCertPEM is empty")
	}
	if issued.ServerExpiresAt.Before(time.Now()) {
		t.Errorf("ServerExpiresAt in the past: %s", issued.ServerExpiresAt)
	}
	// The server cert must validate against the
	// root. We do this by re-fetching the root +
	// leaf and calling Verify.
	ca, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (for verification): %v", err)
	}
	if err := verifyCertChain(ca.Cert, issued.ServerCertPEM, nodeID.String(), x509.ExtKeyUsageServerAuth); err != nil {
		t.Errorf("server cert chain verification: %v", err)
	}
	if err := verifyCertChain(ca.Cert, issued.ClientCertPEM, "", x509.ExtKeyUsageClientAuth); err != nil {
		t.Errorf("client cert chain verification: %v", err)
	}
}

// TestService_EnsureNodeCerts_Idempotent verifies
// the per-node read-or-create dance. The second
// call must return the same ServerExpiresAt
// (no re-issuance).
func TestService_EnsureNodeCerts_Idempotent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	nodeID := uuid.New()
	addr := "10.0.0.5:7001"

	first, err := svc.EnsureNodeCerts(ctx, nodeID, addr)
	if err != nil {
		t.Fatalf("EnsureNodeCerts (first): %v", err)
	}
	second, err := svc.EnsureNodeCerts(ctx, nodeID, addr)
	if err != nil {
		t.Fatalf("EnsureNodeCerts (second): %v", err)
	}
	if !first.ServerExpiresAt.Equal(second.ServerExpiresAt) {
		t.Errorf("ServerExpiresAt differs: first=%s, second=%s (not idempotent)",
			first.ServerExpiresAt, second.ServerExpiresAt)
	}
	if first.ServerCertPEM != second.ServerCertPEM {
		t.Error("ServerCertPEM differs (not idempotent)")
	}
}

// TestService_EnsureNodeCerts_RejectsBadInput pins
// the input-validation gate. The BatchedApplier
// passes the node UUID + addr; a nil/empty
// argument would surface as a confusing panic in
// x509 generation.
func TestService_EnsureNodeCerts_RejectsBadInput(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	if _, err := svc.EnsureNodeCerts(ctx, uuid.Nil, "10.0.0.5:7001"); err == nil {
		t.Error("EnsureNodeCerts with uuid.Nil should fail; got nil")
	}
	if _, err := svc.EnsureNodeCerts(ctx, uuid.New(), ""); err == nil {
		t.Error("EnsureNodeCerts with empty addr should fail; got nil")
	}
}

// TestService_EnsureRoot_Concurrent verifies the
// inner mutex serialises concurrent first calls.
// A naive implementation would keygen twice (and
// either crash on the second Save or persist the
// second key as the "winner" — both wrong).
func TestService_EnsureRoot_Concurrent(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()
	const goroutines = 32
	var (
		wg      sync.WaitGroup
		cntErr  atomic.Int32
		serials sync.Map
	)
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			ca, err := svc.EnsureRoot(ctx)
			if err != nil {
				cntErr.Add(1)
				return
			}
			serials.Store(ca.Serial, struct{}{})
		}()
	}
	wg.Wait()
	if cntErr.Load() != 0 {
		t.Errorf("%d goroutines errored", cntErr.Load())
	}
	// All goroutines must have observed the same
	// root (the inner mutex serialises the
	// read-or-create; the winner's keygen is the
	// only one that runs).
	count := 0
	serials.Range(func(_, _ any) bool { count++; return true })
	if count != 1 {
		t.Errorf("expected 1 distinct serial, got %d (concurrent goroutines raced)", count)
	}
}

// TestMemoryStore_DeepCopy pins the immutability
// contract: a caller-side mutation of a fetched
// Root or NodeCerts must not affect the in-memory
// state. A regression here would let a stale
// cert leak into a subsequent EnsureNodeCerts
// call (the per-node state is what the agent
// presents on mTLS; a stale cert after rotation
// is a 502 in the BatchedApplier).
func TestMemoryStore_DeepCopy(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()
	nodeID := uuid.New()
	addr := "10.0.0.5:7001"
	svc := NewService(store)
	if _, err := svc.EnsureNodeCerts(ctx, nodeID, addr); err != nil {
		t.Fatalf("EnsureNodeCerts: %v", err)
	}
	// 1. Root: get, mutate, ensure next get is
	// unchanged.
	root1, err := store.GetRoot(ctx)
	if err != nil {
		t.Fatalf("store.GetRoot: %v", err)
	}
	root1.CertPEM = "tampered"
	root2, err := store.GetRoot(ctx)
	if err != nil {
		t.Fatalf("store.GetRoot (after tamper): %v", err)
	}
	if root2.CertPEM == "tampered" {
		t.Error("MemoryStore Root.Get returned a shared reference; mutation leaked")
	}
	// 2. NodeCerts: get, mutate ServerKey, ensure
	// next get is unchanged.
	nc1, err := store.GetNodeCerts(ctx, nodeID)
	if err != nil {
		t.Fatalf("store.GetNodeCerts: %v", err)
	}
	originalKeyLen := len(nc1.ServerKey)
	for i := range nc1.ServerKey {
		nc1.ServerKey[i] = 0xFF
	}
	nc2, err := store.GetNodeCerts(ctx, nodeID)
	if err != nil {
		t.Fatalf("store.GetNodeCerts (after tamper): %v", err)
	}
	if len(nc2.ServerKey) != originalKeyLen {
		t.Errorf("ServerKey length changed: %d -> %d", originalKeyLen, len(nc2.ServerKey))
	}
	for _, b := range nc2.ServerKey {
		if b == 0xFF {
			t.Error("MemoryStore NodeCerts.Get returned a shared ServerKey slice; mutation leaked")
			break
		}
	}
}

// TestMemoryStore_CloseIsNoop pins the dev-mode
// behaviour: the MemoryStore's Close is a no-op so
// `app.Build` shutdown does not need to special-case
// the dev path.
func TestMemoryStore_CloseIsNoop(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Close(); err != nil {
		t.Errorf("MemoryStore.Close: %v", err)
	}
}

// hasErrNotFound reports whether err is (or wraps)
// ErrNotFound. Wrapped errors are common in the
// MemoryStore path (the store hides the envelope
// details, but tests can hit the bare sentinel).
func hasErrNotFound(err error) bool { return errors.Is(err, ErrNotFound) }

// verifyCertChain verifies that the leaf PEM
// (certPEM) is signed by `root` and is valid for
// `dnsName` under `usage` (ServerAuth / ClientAuth).
// The helper is shared by the per-node test (DNS =
// nodeUUID for server) and the panel-client test
// (DNS = "" because the client cert's SAN is
// optional).
func verifyCertChain(root *x509.Certificate, certPEM, dnsName string, usage x509.ExtKeyUsage) error {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return fmt.Errorf("verifyCertChain: no PEM block")
	}
	leaf, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("verifyCertChain: parse leaf: %w", err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(root)
	opts := x509.VerifyOptions{
		Roots:       pool,
		KeyUsages:   []x509.ExtKeyUsage{usage},
		CurrentTime: time.Now(),
	}
	if dnsName != "" {
		opts.DNSName = dnsName
	}
	if _, err := leaf.Verify(opts); err != nil {
		return fmt.Errorf("verifyCertChain: leaf.Verify: %w", err)
	}
	return nil
}
