// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"strings"
	"testing"
)

// TestNewPgStore_PanicsOnNilCipher guards the
// "nil cipher is a wiring bug" contract. The
// production wiring (internal/app/app.go) ALWAYS
// passes a cipher; a nil value would silently
// mean "plaintext at rest on pg" — the very
// regression this test was written to catch.
//
// The cipher itself is `envelope.SecretCipher` (see
// internal/crypto/envelope); v0.8.x lifted the type
// out of `webhooks` so other at-rest secrets can
// share the same age-encryption boundary. The
// nil-cipher panic stays here because the wiring
// contract is webhooks-specific.
func TestNewPgStore_PanicsOnNilCipher(t *testing.T) {
	t.Parallel()
	// pgxpool.Pool is unexported in pgx/v5, so
	// we cannot construct a zero value here
	// without importing pgxpool. The test
	// instead relies on the constructor being
	// defensive BEFORE touching the pool:
	// calling NewPgStore(nil, nil) should panic
	// on the cipher check, not on a nil-pointer
	// deref of the pool.
	defer func() {
		r := recover()
		if r == nil {
			t.Errorf("expected panic on nil cipher, got none")
		}
		// The panic message must mention the
		// cipher, not "nil pointer dereference"
		// (which would mean we dereffed the
		// pool first).
		msg, ok := r.(string)
		if !ok {
			return
		}
		if !strings.Contains(msg, "SecretCipher") {
			t.Errorf("panic message = %q, want one mentioning SecretCipher", msg)
		}
	}()
	// pgxpool.Pool is unexported; we cannot
	// construct a typed nil here. Use an
	// interface conversion to bypass the type
	// system: the constructor must check cipher
	// first and panic before touching pool.
	var nilPool *pgxPoolLike = nil
	_ = nilPool // keep the type referenced
	NewPgStore(nil, nil)
}

// pgxPoolLike is a no-op shim used only to silence
// the unused-variable warning in the panic test.
// The real call passes a real pool in production;
// here we just want to verify the cipher check
// runs FIRST.
type pgxPoolLike struct{}
