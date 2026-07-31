// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HMAC-SHA256 signing for the outgoing-webhook
// surface. The package exposes two helpers:
//
//   - Sign(body, secret) -> "sha256=<hex>" header
//   - Verify(body, secret, header) -> bool (constant-time)
//
// The signature is computed over the exact bytes
// the panel sent (post-canonicalisation). The
// receiver MUST verify the same way: re-hash the
// raw request body with the shared secret and
// compare with crypto/hmac.Equal (never ==).
//
// # Anti-replay window
//
// The signature alone is not enough — a captured
// delivery can be replayed by an attacker who
// exfiltrates the secret. The receiver MUST
// additionally reject any delivery whose
// `X-Aegis-Timestamp` is more than 5 minutes from
// the receiver's wall clock. v0.7.0 sends the
// timestamp alongside the signature; the receiver
// enforces the window on its side.

package webhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
)

// SignatureHeader is the value format the panel
// writes into `X-Aegis-Signature`. The "sha256="
// prefix is a version marker so a future v2 scheme
// (e.g. ed25519) can co-exist on the receiver side
// without a flag day.
const SignatureHeader = "sha256="

// Sign computes the HMAC-SHA256 of body using
// secret, and returns the canonical header value
// "sha256=<hex>". The returned string is safe to
// drop directly into the X-Aegis-Signature header.
//
// # Errors
//
// Returns an error only if hex.EncodeToString
// fails (which is impossible for a [32]byte input
// but kept on the signature for symmetry with the
// Verify path). Production callers can treat the
// error as "this cannot happen".
func Sign(body []byte, secret string) (string, error) {
	if secret == "" {
		return "", errors.New("sign: empty secret")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	// hmac.Hash.Hash.Write never returns an error.
	_, _ = mac.Write(body)
	sum := mac.Sum(nil)
	return SignatureHeader + hex.EncodeToString(sum), nil
}

// Verify checks the signature header against the
// HMAC-SHA256 of body using secret. The comparison
// is constant-time (crypto/hmac.Equal).
//
// # Header format
//
// The header value MUST be in the canonical form
// "sha256=<hex>". Receivers that send the bare
// hex (no prefix) are rejected. The strict format
// is part of the contract; see package doc.
//
// # Errors
//
// Returns ErrBadSignature on any mismatch (wrong
// header prefix, malformed hex, mismatched MAC).
// Callers should treat every non-nil error as
// "reject this delivery".
func Verify(body []byte, secret, header string) error {
	if secret == "" {
		return ErrBadSignature
	}
	if len(header) <= len(SignatureHeader) {
		return ErrBadSignature
	}
	if header[:len(SignatureHeader)] != SignatureHeader {
		return ErrBadSignature
	}
	got, err := hex.DecodeString(header[len(SignatureHeader):])
	if err != nil {
		return ErrBadSignature
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write(body)
	want := mac.Sum(nil)
	if !hmac.Equal(got, want) {
		return ErrBadSignature
	}
	return nil
}

// ErrBadSignature is returned by Verify on any
// mismatch. Sentinel so the handler can branch on
// it without string-matching.
var ErrBadSignature = errors.New("webhooks: bad signature")
