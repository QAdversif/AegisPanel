// SPDX-License-Identifier: AGPL-3.0-or-later
//
// GetStoredKey — the v0.8.5 operator-side debug
// surface for the v0.8.1 persistent panel SSH
// key feature. The CLI (`aegis admin node
// rotate-panel-key`, PR #184) and the HTTP
// mirror (`POST /api/v1/nodes/{id}/rotate-panel-key`,
// PR #185) both WRITE a fresh ed25519
// keypair to the node. This is the READ side:
// the operator asks "what does the panel
// currently have stored for this node?".
//
// # Why it exists
//
// The v0.8.1 post-install hook and the
// v0.8.3/v0.8.4 rotation flows both encrypt
// the generated ed25519 private key with the
// operator's age envelope and persist the
// ciphertext in `nodes.ssh_private_key_ciphertext`
// (migration 0020). The next re-provision
// decrypts and reuses it. This is a
// round-tripping feature: the operator
// never sees the key in cleartext through
// normal operation. The "Show stored key"
// surface gives them visibility into the
// state without changing the secret
// material — the panel decrypts the stored
// ciphertext in memory, derives the public
// key (which is in the node's authorized_keys
// already — same key, operator-visible
// surface), and returns the public-key line
// + SHA-256 fingerprint.
//
// # Security shape
//
// The endpoint exposes the public key and
// the fingerprint, not the private key. The
// public key is already in the node's
// `~/.ssh/authorized_keys` (the panel
// pushed it there in v0.8.1 / v0.8.3 / v0.8.4),
// so revealing it through the panel adds no
// new attack surface: any operator with
// shell on the node can `cat authorized_keys`
// and see the same line. The fingerprint is
// the SHA-256 of the public-key bytes —
// a one-way hash, not a secret.
//
// The private key stays in the panel
// process only for the duration of the
// decrypt; the response carries no
// private-key material. The audit log
// records every decrypt (the
// `node.stored-key.read` action) so the
// operator can see who looked at the stored
// key in the audit UI.

package nodes

import (
	"context"
	"crypto/ed25519"
	"encoding/pem"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"
)

// StoredKey is the public surface of the
// node's stored panel SSH key. The struct
// carries the derived public-key line and
// SHA-256 fingerprint (the same strings
// `ssh-keygen -lf` reports) plus a few
// metadata fields that the UI surfaces for
// at-a-glance verification:
//
//   - Algorithm: the SSH key type
//     (`ssh-ed25519` today; the provisioner
//     is hard-coded to ed25519 in v0.8.1,
//     but the field is in the wire shape so
//     a future RSA / ECDSA path does not
//     require a UI change).
//   - KeyUpdatedAt: the row's `updated_at`
//     timestamp. The ciphertext column has
//     no independent timestamp; the
//     `updated_at` field reflects the last
//     write of any field on the row, which
//     in practice is the last rotate (or
//     the first install, if no rotate has
//     happened). The field is the operator's
//     "is this the key I think it is" sanity
//     check.
//
// The OpenSSH key comment
// (`aegis-panel@node-<nodeName>`) is NOT a
// separate field — it is the third
// whitespace-separated token of
// `PublicKeyLine` (the OpenSSH authorized_keys
// format), so the UI can parse it back via
// `line.split(' ', 3)`. Carrying it twice
// (once as a dedicated field, once embedded
// in the line) is a footgun; the line is
// the source of truth.
//
// HasStoredKey is the "row has a stored
// ciphertext" flag. It is false for `new`
// nodes that have never been installed via
// the v0.8.1+ path; the UI surfaces a
// "no stored key yet" hint in that case.
type StoredKey struct {
	HasStoredKey  bool      `json:"has_stored_key"`
	PublicKeyLine string    `json:"public_key_line,omitempty"`
	Fingerprint   string    `json:"fingerprint,omitempty"`
	Algorithm     string    `json:"algorithm,omitempty"`
	KeyUpdatedAt  time.Time `json:"key_updated_at,omitempty"`
}

// GetStoredKey returns the public surface of
// the node's stored panel SSH key. The
// function decrypts the stored ciphertext
// (via the service's age envelope) and
// derives the public-key line + SHA-256
// fingerprint. The private key bytes stay
// in the function's stack frame for the
// duration of the parse; the returned
// `StoredKey` carries no private-key material.
//
// The function is fail-closed: a nil envelope
// returns an error (the panel was booted
// without AEGIS_WEBHOOKS_SECRET_AGE_*; the
// operator must fix the env and retry).
// A non-existent node returns
// `ErrNotFound`; the HTTP handler maps
// that to 404.
//
// The caller is expected to call this from
// an HTTP handler (which records the audit
// entry) or from a CLI subcommand (which
// records via the CLI-side audit helper).
func (s *Service) GetStoredKey(ctx context.Context, nodeID uuid.UUID) (StoredKey, error) {
	if s.envelope == nil {
		return StoredKey{}, fmt.Errorf("nodes: GetStoredKey: envelope is not configured (set AEGIS_WEBHOOKS_SECRET_AGE_* env vars)")
	}
	row, err := s.store.GetByID(ctx, nodeID)
	if err != nil {
		return StoredKey{}, err
	}
	if len(row.SSHPrivateKeyCiphertext) == 0 {
		// No stored key yet (v0.3.0..v0.7.x
		// node that has not been back-filled
		// with the v0.8.3 CLI; or a never-
		// installed `new` row). The HTTP
		// handler maps this to 200 with
		// HasStoredKey: false (NOT 404 —
		// the row exists, the question is
		// "does it have a key").
		return StoredKey{
			HasStoredKey: false,
			KeyUpdatedAt: row.UpdatedAt,
		}, nil
	}
	// Decrypt. The plaintext is an OpenSSH
	// ed25519 PEM block (the v0.8.1 provisioner
	// path uses ssh.MarshalPrivateKey with
	// the comment "aegis-panel@node-<name>").
	privPEM, err := s.envelope.Decrypt(row.SSHPrivateKeyCiphertext)
	if err != nil {
		return StoredKey{}, fmt.Errorf("nodes: decrypt stored SSH key: %w", err)
	}
	pub, err := parseOpenSSHPrivateKey(privPEM)
	if err != nil {
		return StoredKey{}, fmt.Errorf("nodes: parse stored SSH key: %w", err)
	}
	// Build the authorized_keys line and
	// the SHA-256 fingerprint. Same pattern
	// as the v0.8.1 post-install hook and
	// the v0.8.4 rotate-panel-key handler.
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		return StoredKey{}, fmt.Errorf("nodes: derive public key: %w", err)
	}
	pubLine := ssh.MarshalAuthorizedKey(sshPub)
	fp := ssh.FingerprintSHA256(sshPub)
	return StoredKey{
		HasStoredKey:  true,
		PublicKeyLine: string(pubLine),
		Fingerprint:   fp,
		Algorithm:     sshPub.Type(),
		KeyUpdatedAt:  row.UpdatedAt,
	}, nil
}

// parseOpenSSHPrivateKey decodes an OpenSSH
// `-----BEGIN OPENSSH PRIVATE KEY-----` PEM
// block and returns the embedded ed25519
// public key bytes. The function is the
// read-side mirror of the write-side
// `ssh.MarshalPrivateKey` call in
// `bootstrap.generateAndPushKey`; the two
// round-trip via the same wire format so the
// public-key derivation is byte-for-byte the
// key the panel pushed to the node's
// authorized_keys.
//
// Errors:
//
//   - malformed PEM (the bytes are not a
//     PEM block, or the block type is not
//     `OPENSSH PRIVATE KEY`)
//   - the PEM payload is not a valid OpenSSH
//     private-key wire frame
//   - the key is not an ed25519 key (the
//     panel only generates ed25519 today, but
//     a future RSA / ECDSA path will need
//     separate handling here)
//
// The OpenSSH key comment (the
// `aegis-panel@node-<name>` string the
// v0.8.1+ write path embeds) is NOT
// returned here. The `golang.org/x/crypto/ssh`
// v1.5 parser's public API does not surface
// the comment on the returned
// `crypto.PrivateKey`; pulling it would
// require either a custom OpenSSH-wire
// parser or shelling out to `ssh-keygen -l`.
// Neither is worth the complexity: the
// comment is the third whitespace-
// separated token of `PublicKeyLine` (the
// `ssh.MarshalAuthorizedKey` output), so
// the UI gets it back via
// `line.split(' ', 3)`.
func parseOpenSSHPrivateKey(privPEM []byte) (ed25519.PublicKey, error) {
	block, _ := pem.Decode(privPEM)
	if block == nil {
		return nil, errors.New("stored key: not a PEM block")
	}
	if block.Type != "OPENSSH PRIVATE KEY" {
		return nil, fmt.Errorf("stored key: unexpected PEM block type %q (want OPENSSH PRIVATE KEY)", block.Type)
	}
	parsed, err := ssh.ParseRawPrivateKey(privPEM)
	if err != nil {
		return nil, fmt.Errorf("stored key: parse OpenSSH private key: %w", err)
	}
	// `ssh.ParseRawPrivateKey` returns a
	// `crypto.PrivateKey` whose concrete type
	// is `*ed25519.PrivateKey` for ed25519
	// keys (the pointer is the convention; the
	// package's older API used the value
	// type). The type assertion checks the
	// pointer; the v0.8.x write path uses
	// the same `crypto.PrivateKey` round-trip
	// (see `bootstrap.generateAndPushKey`).
	edKey, ok := parsed.(*ed25519.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("stored key: not an ed25519 key (got %T)", parsed)
	}
	// Derive the public key from the private
	// key bytes. ed25519.PrivateKey is a
	// fixed-size byte array; the public half
	// is the second half.
	privBytes := []byte(*edKey)
	if len(privBytes) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("stored key: invalid ed25519 key size %d", len(privBytes))
	}
	pub := ed25519.PrivateKey(privBytes).Public().(ed25519.PublicKey)
	return pub, nil
}
