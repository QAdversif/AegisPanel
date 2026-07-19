// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Bearer-secret generation for the Panel<->Agent
// handshake. The panel mints a fresh random secret
// for every provisioned node; the secret is
// installed on the node as part of the systemd
// unit (the agent reads it from
// /etc/aegis/agent.env), and the panel keeps a
// hash of it in the nodes table for the future
// "agent authenticated the bootstrap handshake"
// verification path.
//
// # Why 32 bytes (256 bits)
//
// A 256-bit random secret matches the NIST SP
// 800-63B "Remembered Secret" / "Look-up Secret"
// entropy floor for offline attack resistance
// (2^80 guesses / sec ≈ 30 bits / month brute force
// in 2024 dollars). The bearer token is over
// HTTPS with TLS 1.3, so the on-path attacker has
// no offline path; the secret only needs to be
// unguessable to a network adversary who does not
// have the on-disk file.
//
// # Storage on the panel side
//
// The plain text is written to the node's
// /etc/aegis/agent.env and never leaves the
// panel again. The panel keeps only the SHA-256
// hash for verification (the agent sends a
// signed challenge in v0.4.0; for v0.3.0 the
// bootstrap is one-shot and the hash is not
// verified). v0.5.0 adds a `panel:rotate-secret`
// subcommand to mint a new secret on an existing
// node.

package bootstrap

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// SecretLen is the byte length of a freshly-minted
// bearer secret. The wire format is the hex
// string (`SecretLen*2` characters); the on-disk
// format is the same hex (the agent's env file
// loader handles either).
const SecretLen = 32

// GenerateBearerSecret mints a fresh secret. The
// two return values are the plain-text secret
// (write to the node) and the SHA-256 hex digest
// (store on the panel for future challenge
// verification). The error is non-nil only on
// crypto/rand failure, which on Linux is
// essentially "the kernel is broken" — a panic
// would be equally appropriate, but returning
// the error keeps the call-site honest.
func GenerateBearerSecret() (plain string, hash string, err error) {
	buf := make([]byte, SecretLen)
	if _, err := rand.Read(buf); err != nil {
		return "", "", fmt.Errorf("bootstrap: read random: %w", err)
	}
	plain = hex.EncodeToString(buf)
	sum := sha256.Sum256([]byte(plain))
	hash = hex.EncodeToString(sum[:])
	return plain, hash, nil
}

// HashBearerSecret computes the SHA-256 hex digest
// of a secret the panel already has on hand. The
// function exists so a future "rotate secret"
// flow can recompute the hash without re-minting
// the secret. v0.5.0 work; v0.3.0 does not need it
// (every provisioning call mints a new secret).
func HashBearerSecret(plain string) string {
	sum := sha256.Sum256([]byte(plain))
	return hex.EncodeToString(sum[:])
}
