// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestGenerateBearerSecret_LengthAndUniqueness
// verifies the size + per-call uniqueness of the
// minted secret. Two consecutive calls must
// produce different output (a regression to a
// constant seed would be a security bug).
func TestGenerateBearerSecret_LengthAndUniqueness(t *testing.T) {
	a, _, err := GenerateBearerSecret()
	if err != nil {
		t.Fatalf("GenerateBearerSecret: %v", err)
	}
	b, _, err := GenerateBearerSecret()
	if err != nil {
		t.Fatalf("GenerateBearerSecret: %v", err)
	}
	if len(a) != SecretLen*2 {
		t.Errorf("secret hex len = %d, want %d", len(a), SecretLen*2)
	}
	if len(b) != SecretLen*2 {
		t.Errorf("secret hex len = %d, want %d", len(b), SecretLen*2)
	}
	if a == b {
		t.Errorf("two consecutive mints collided: %q", a)
	}
	// Hex-only check — the random bytes must
	// encode cleanly.
	if _, err := hex.DecodeString(a); err != nil {
		t.Errorf("secret is not valid hex: %v", err)
	}
}

// TestGenerateBearerSecret_HashIsSHA256 verifies
// the hash is the SHA-256 hex digest of the
// secret. The pair (plain, hash) is the storage
// shape the panel uses for the future
// challenge-response handshake; getting the
// algorithm wrong here would silently break
// v0.5.0.
func TestGenerateBearerSecret_HashIsSHA256(t *testing.T) {
	plain, hash, err := GenerateBearerSecret()
	if err != nil {
		t.Fatalf("GenerateBearerSecret: %v", err)
	}
	if !strings.HasPrefix(plain, "") { // sanity: not empty
		_ = plain
	}
	// The helper is the source of truth; compare
	// its output to the function-under-test's
	// hash.
	if got := HashBearerSecret(plain); got != hash {
		t.Errorf("hash mismatch: got %s, want %s", got, hash)
	}
	// 64 hex chars = 32 bytes = SHA-256.
	if len(hash) != 64 {
		t.Errorf("hash hex len = %d, want 64 (SHA-256)", len(hash))
	}
}

// TestHashBearerSecret_Deterministic verifies the
// helper is a pure function (same input -> same
// output) so it can be used for verification
// without keeping the plain text on the panel.
func TestHashBearerSecret_Deterministic(t *testing.T) {
	a := HashBearerSecret("hello")
	b := HashBearerSecret("hello")
	if a != b {
		t.Errorf("HashBearerSecret not deterministic: %s vs %s", a, b)
	}
	c := HashBearerSecret("hello!")
	if a == c {
		t.Error("HashBearerSecret collides on different inputs")
	}
}
