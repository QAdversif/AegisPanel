// SPDX-License-Identifier: AGPL-3.0-or-later
//
// v0.8.32.3 regression tests for issue #326 (the
// `agentca: pgStore.GetRoot: cannot scan bytea into
// **ecdsa.PrivateKey` panic that blocked the v0.8.32.2
// deploy).
//
// The pre-fix code in `pg_store.go:GetRoot` did
//
//	row.Scan(&r.PrivateKey, &r.CertPEM, ...)
//
// where `r.PrivateKey` is `*ecdsa.PrivateKey`. pgx
// does NOT auto-decode a `bytea` column into an
// `*ecdsa.PrivateKey` (it tries to call `.scan(into
// &ecdsa.PrivateKey)` which is not implemented), so
// every boot of the v0.8.32.2 image crashed.
//
// The fix changed `Root.PrivateKey *ecdsa.PrivateKey`
// to `Root.KeyDER []byte` (the raw bytes; the Service
// is responsible for the encoding shape). The tests
// below cover the four shapes the row can be in:
//
//  1. v0.8.32+ SaveRoot output (plaintext SEC1 DER).
//     This is the canonical encoding. EnsureRoot
//     decodes it on the plaintext-DER path.
//  2. The v0.8.25 hand-minted prod row (age-envelope
//     ciphertext). The Service falls back to
//     `envelope.Decrypt` when plaintext fails.
//  3. The v0.8.25 row WITHOUT the envelope configured
//     (dev mode / wrong key). EnsureRoot returns a
//     clear error chaining both attempts so the
//     operator can diagnose the on-disk shape.
//  4. Garbage bytes (DB corruption, wrong key with
//     non-envelope input, etc). EnsureRoot returns
//     a clear error.
//
// The age fixture uses a test-only X25519 identity
// generated per test (not the operator's real age
// key). The plaintext path is hermetic -- no envelope.

package agentca

import (
	"context"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"

	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// newTestAgeCipher generates a fresh X25519 identity,
// writes it to a temp file in the standard age-keygen
// format, and returns the cipher. The fixture is
// hermetic: no test depends on the operator's real
// age key, and the temp file is deleted at test end
// (t.TempDir cleanup is automatic).
func newTestAgeCipher(t *testing.T) *envelope.AgeSecretCipher {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("age.GenerateX25519Identity: %v", err)
	}
	identityLine := identity.String()
	recipient := identity.Recipient().String()

	dir := t.TempDir()
	keyFile := filepath.Join(dir, "age.key")
	if err := os.WriteFile(keyFile, []byte(identityLine+"\n"), 0o600); err != nil {
		t.Fatalf("write age key file: %v", err)
	}

	cipher, err := envelope.NewAgeSecretCipher([]string{recipient}, keyFile)
	if err != nil {
		t.Fatalf("envelope.NewAgeSecretCipher: %v", err)
	}
	return cipher
}

// keyDERFor builds a fresh ECDSA P-256 root via
// NewRootCA, then returns the plaintext SEC1 DER
// representation of the same key. The DER is the
// canonical plaintext shape the v0.8.32+ SaveRoot
// writes to the bytea column. The returned CA's
// PrivateKey, Cert, and Serial are consistent with
// that DER (the cert is self-signed by the same key).
func keyDERFor(t *testing.T) (*CA, []byte) {
	t.Helper()
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	der, err := x509.MarshalECPrivateKey(ca.PrivateKey)
	if err != nil {
		t.Fatalf("x509.MarshalECPrivateKey: %v", err)
	}
	return ca, der
}

// TestService_EnsureRoot_PlaintextDER_RoundTrip is
// the happy-path test for the v0.8.32+ SaveRoot
// shape: the first EnsureRoot generates + saves a
// plaintext-DER row; a second call after Invalidate
// must decode it via the plaintext path and return
// the same cert.
func TestService_EnsureRoot_PlaintextDER_RoundTrip(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store) // no envelope: plaintext path
	ctx := context.Background()

	// 1. Seed: first EnsureRoot generates + saves.
	ca1, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (seed): %v", err)
	}
	// 2. Simulate a panel restart: drop the cache,
	// re-read the persisted row.
	svc.Invalidate()
	ca2, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (re-read plaintext DER): %v", err)
	}
	if !ca1.PrivateKey.Equal(ca2.PrivateKey) {
		t.Error("plaintext DER round-trip: PrivateKey differs after Invalidate+re-read")
	}
	if !ca1.Cert.NotAfter.Equal(ca2.Cert.NotAfter) {
		t.Errorf("plaintext DER round-trip: Cert.NotAfter differs (orig=%s, reread=%s)", ca1.Cert.NotAfter, ca2.Cert.NotAfter)
	}
}

// TestService_EnsureRoot_PlaintextDER_HandSeededRow
// exercises the explicit "row was written by
// SaveRoot, then someone bumped the schema or
// migrated" path. The fixture is hand-seeded into
// the MemoryStore (bypassing SaveRoot) so the test
// pins the Service's decode behaviour in isolation
// from the SaveRoot path.
func TestService_EnsureRoot_PlaintextDER_HandSeededRow(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store) // no envelope: plaintext path
	ctx := context.Background()

	caOrig, keyDER := keyDERFor(t)
	if err := store.SaveRoot(ctx, &Root{
		KeyDER:    keyDER,
		CertPEM:   caOrig.RootCertPEM(),
		Serial:    caOrig.Serial,
		ExpiresAt: caOrig.Cert.NotAfter,
	}); err != nil {
		t.Fatalf("store.SaveRoot (seed plaintext DER): %v", err)
	}

	ca, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (hand-seeded plaintext DER): %v", err)
	}
	if !ca.PrivateKey.Equal(caOrig.PrivateKey) {
		t.Error("decoded PrivateKey differs from seeded plaintext DER (plaintext-DER decode broken)")
	}
	if !ca.Cert.NotAfter.Equal(caOrig.Cert.NotAfter) {
		t.Errorf("decoded Cert.NotAfter differs from seeded: got=%s, want=%s", ca.Cert.NotAfter, caOrig.Cert.NotAfter)
	}
}

// TestService_EnsureRoot_AgeEncrypted_PreExistingRow
// is THE regression test for issue #326. The
// fixture mirrors the v0.8.25 prod workaround: a
// hand-minted `agentca.key_ciphertext` row whose
// bytes are age-envelope ciphertext (the plaintext
// underneath is the SEC1 DER of an ECDSA P-256
// key). EnsureRoot must NOT crash; it must
// transparently decrypt + parse.
func TestService_EnsureRoot_AgeEncrypted_PreExistingRow(t *testing.T) {
	cipher := newTestAgeCipher(t)

	store := NewMemoryStore()
	svc := NewService(store, cipher) // envelope: enabled
	ctx := context.Background()

	// 1. Hand-seed a row with age-envelope
	// ciphertext. We encrypt the canonical plaintext
	// DER of a fresh root.
	caOrig, keyDERPlaintext := keyDERFor(t)
	keyCiphertext, err := cipher.Encrypt(keyDERPlaintext)
	if err != nil {
		t.Fatalf("cipher.Encrypt: %v", err)
	}
	if err := store.SaveRoot(ctx, &Root{
		KeyDER:    keyCiphertext,
		CertPEM:   caOrig.RootCertPEM(),
		Serial:    caOrig.Serial,
		ExpiresAt: caOrig.Cert.NotAfter,
	}); err != nil {
		t.Fatalf("store.SaveRoot (seed age-ciphertext): %v", err)
	}

	// 2. EnsureRoot must decrypt + parse without
	// crashing. This is the exact shape that
	// panicked the v0.8.32.2 panel on prod.
	ca, err := svc.EnsureRoot(ctx)
	if err != nil {
		t.Fatalf("EnsureRoot (hand-seeded age-ciphertext): %v", err)
	}
	if !ca.PrivateKey.Equal(caOrig.PrivateKey) {
		t.Error("decoded PrivateKey differs from the plaintext underneath the envelope (envelope-decrypt path broken)")
	}
}

// TestService_EnsureRoot_AgeEncrypted_NoEnvelope
// pins the error path: when the row is
// age-envelope ciphertext (the v0.8.25 prod shape)
// but the panel has no envelope configured (wrong
// identity file, or memory-mode dev environment
// pointed at a prod-shaped row by mistake),
// EnsureRoot returns a clear error chaining both
// decode attempts.
func TestService_EnsureRoot_AgeEncrypted_NoEnvelope(t *testing.T) {
	cipher := newTestAgeCipher(t)

	store := NewMemoryStore()
	svc := NewService(store) // NO envelope
	ctx := context.Background()

	// Hand-seed a row with age-envelope ciphertext
	// (the v0.8.25 prod shape).
	caOrig, keyDERPlaintext := keyDERFor(t)
	keyCiphertext, err := cipher.Encrypt(keyDERPlaintext)
	if err != nil {
		t.Fatalf("cipher.Encrypt: %v", err)
	}
	if err := store.SaveRoot(ctx, &Root{
		KeyDER:    keyCiphertext,
		CertPEM:   caOrig.RootCertPEM(),
		Serial:    caOrig.Serial,
		ExpiresAt: caOrig.Cert.NotAfter,
	}); err != nil {
		t.Fatalf("store.SaveRoot (seed): %v", err)
	}

	_, err = svc.EnsureRoot(ctx)
	if err == nil {
		t.Fatal("EnsureRoot on age-ciphertext row without envelope must error; got nil")
	}
	// The error chain must mention BOTH decode
	// attempts so the operator can diagnose what
	// shape the row actually has.
	msg := err.Error()
	if !strings.Contains(msg, "ParseECPrivateKey") {
		t.Errorf("error must mention the plaintext ParseECPrivateKey attempt: %v", err)
	}
	if !strings.Contains(msg, "envelope") && !strings.Contains(msg, "AGE") {
		t.Errorf("error must mention the envelope / age path: %v", err)
	}
}

// TestService_EnsureRoot_CorruptedKey pins the
// "garbage bytes in key_ciphertext" path. EnsureRoot
// must return a clear error, not panic, not loop.
func TestService_EnsureRoot_CorruptedKey(t *testing.T) {
	store := NewMemoryStore()
	svc := NewService(store)
	ctx := context.Background()

	caOrig, _ := keyDERFor(t)
	if err := store.SaveRoot(ctx, &Root{
		KeyDER:    []byte{0x00, 0x01, 0x02, 0x03, 0xFF, 0xFE, 0xFD},
		CertPEM:   caOrig.RootCertPEM(),
		Serial:    caOrig.Serial,
		ExpiresAt: caOrig.Cert.NotAfter,
	}); err != nil {
		t.Fatalf("store.SaveRoot (garbage key): %v", err)
	}

	_, err := svc.EnsureRoot(ctx)
	if err == nil {
		t.Fatal("EnsureRoot on garbage key must error; got nil")
	}
	if !strings.Contains(err.Error(), "ParseECPrivateKey") {
		t.Errorf("error must mention the ParseECPrivateKey failure: %v", err)
	}
}
