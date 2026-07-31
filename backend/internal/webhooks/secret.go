// SPDX-License-Identifier: AGPL-3.0-or-later
//
// SecretCipher is the v0.7.x encryption boundary
// for `webhook_endpoints.secret`. The plaintext
// HMAC key never touches the database — the
// Service hands plaintext to the Store, the Store
// hands ciphertext to Postgres, and the Store
// hands plaintext back to the Service on read.
//
// # Why age and not the sops Go library
//
// The panel's out-of-band secrets infra (PR #119)
// uses the `sops` CLI to encrypt the .env file on
// the operator's machine; the panel itself only
// reads the already-decrypted env at boot. The
// `AEGIS_SECRETS_BACKEND=sops` config flag is a
// placeholder for future in-process encryption
// use cases; the v0.7.x webhook envelope is the
// first such case.
//
// The sops Go library (`github.com/getsops/sops/v3`)
// is heavy and designed for structured data
// (JSON, YAML). It uses age as one of its
// backends — exactly the primitive we need here —
// but the JSON envelope (data key + recipient
// list + version) is metadata, not security. For
// a single 32-byte HMAC key, age directly is the
// right tool: the same X25519 + ChaCha20-Poly1305
// cryptographic primitive, no metadata overhead,
// and a much smaller dependency surface.
//
// If a future use case needs sops's full envelope
// (e.g. encrypting a structured config blob with
// multiple recipients and rotation metadata),
// the SecretCipher interface is the seam where
// the sops-backed implementation would slot in.
//
// # Format on disk
//
// The ciphertext is the raw age binary output:
// STREAM-KEY (1 byte) + 16-byte nonce + ChaCha20-
// Poly1305 ciphertext. Roughly `len(plaintext) +
// 49` bytes. Stored as BYTEA in
// `webhook_endpoints.secret_ciphertext` (see
// migration 0018). The DB never sees the plaintext.
//
// # Identity file
//
// `NewAgeSecretCipher` reads the operator's age
// identity from the path given by
// `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE`. The file
// is the standard `age-keygen` output:
//
//	# public key: age1xxxxxxxxxxxxxxxxxxxxxxxxxx
//	AGE-SECRET-KEY-1QQQQQQQQQQQQQQQQQQQQQQQQQQQQQ
//
// The same file is used by the `sops` CLI (PR
// #119's sops+age infra) and by the `age-keygen`
// tool. The panel and the operator's sops workflow
// share one identity.
//
// # Recipients
//
// `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` is a
// comma-separated list of age public keys. The
// ciphertext can be opened by ANY of the matching
// identities. Two recipients is the typical setup
// (operator + break-glass) and the panel does
// not need to know which one decrypted the row.

package webhooks

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
)

// SecretCipher is the encryption boundary the
// Store layer relies on. The Service never sees
// ciphertext; the Store never sees plaintext. A
// no-op implementation (NewNoopSecretCipher) is
// used in dev and unit tests where the Store
// is a MemoryStore and at-rest encryption is
// not a concern.
type SecretCipher interface {
	// Encrypt seals plaintext. Returns the
	// envelope (raw age binary output for the
	// age-backed cipher).
	Encrypt(plaintext []byte) ([]byte, error)
	// Decrypt opens the envelope. Returns the
	// original plaintext or an error if the
	// envelope was not produced by one of the
	// cipher's identities.
	Decrypt(ciphertext []byte) ([]byte, error)
}

// NoopSecretCipher is the dev-mode cipher. The
// Store calls Encrypt and Decrypt as usual but
// the bytes pass through unchanged. Production
// always uses AgeSecretCipher.
type NoopSecretCipher struct{}

// NewNoopSecretCipher returns a cipher that
// passes plaintext through unchanged. The
// MemoryStore uses this in tests; the production
// PgStore uses AgeSecretCipher.
func NewNoopSecretCipher() *NoopSecretCipher { return &NoopSecretCipher{} }

// Encrypt is a no-op.
func (NoopSecretCipher) Encrypt(plaintext []byte) ([]byte, error) { return plaintext, nil }

// Decrypt is a no-op.
func (NoopSecretCipher) Decrypt(ciphertext []byte) ([]byte, error) { return ciphertext, nil }

// AgeSecretCipher seals and opens with the
// filippo.io/age X25519 + ChaCha20-Poly1305
// construction. The recipients are read once
// at construction; the identity is parsed once.
// Both are kept in memory; the identity file is
// not re-read on every call.
type AgeSecretCipher struct {
	recipients []age.Recipient
	identity   age.Identity
}

// NewAgeSecretCipher builds a cipher from the
// operator's age public keys (for sealing) and
// the panel's age identity file (for opening).
//
// `recipientKeys` is a slice of public-key strings
// in the `age1...` format. The standard input
// shape for `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS`
// is a comma-separated list.
//
// `identityFile` is a path to a `age-keygen`-style
// file (one `AGE-SECRET-KEY-1...` line, optional
// `# public key: ...` comment). The same file is
// shared with the operator's sops CLI (PR #119).
//
// Returns an error if any recipient is malformed
// or the identity file cannot be parsed. Callers
// fail fast on these errors at boot.
func NewAgeSecretCipher(recipientKeys []string, identityFile string) (*AgeSecretCipher, error) {
	if len(recipientKeys) == 0 {
		return nil, errors.New("age secret cipher: at least one recipient is required for sealing new endpoints")
	}
	recipients := make([]age.Recipient, 0, len(recipientKeys))
	for _, key := range recipientKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		r, err := age.ParseX25519Recipient(key)
		if err != nil {
			return nil, fmt.Errorf("age secret cipher: parse recipient %q: %w", key, err)
		}
		recipients = append(recipients, r)
	}
	if len(recipients) == 0 {
		return nil, errors.New("age secret cipher: no non-empty recipients after trimming")
	}
	if identityFile == "" {
		return nil, errors.New("age secret cipher: identity file path is required for opening endpoints")
	}
	identity, err := loadAgeIdentity(identityFile)
	if err != nil {
		return nil, fmt.Errorf("age secret cipher: %w", err)
	}
	return &AgeSecretCipher{
		recipients: recipients,
		identity:   identity,
	}, nil
}

// Encrypt seals plaintext with the cipher's
// recipients. Any one of them can open the
// resulting envelope.
func (c *AgeSecretCipher) Encrypt(plaintext []byte) ([]byte, error) {
	var buf bytes.Buffer
	// age.Encrypt is the stdlib-like helper:
	// returns an io.WriteCloser that buffers
	// the STREAM header + nonce. Copy the
	// plaintext in, then Close to flush the
	// ChaCha20-Poly1305 trailer.
	wc, err := age.Encrypt(&buf, c.recipients...)
	if err != nil {
		return nil, fmt.Errorf("age encrypt: %w", err)
	}
	if _, err := wc.Write(plaintext); err != nil {
		return nil, fmt.Errorf("age encrypt write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return nil, fmt.Errorf("age encrypt close: %w", err)
	}
	return buf.Bytes(), nil
}

// Decrypt opens the envelope with the cipher's
// identity. Returns an error if the envelope was
// not produced for this identity (e.g. the row
// was sealed by a different operator's key).
func (c *AgeSecretCipher) Decrypt(ciphertext []byte) ([]byte, error) {
	r, err := age.Decrypt(bytes.NewReader(ciphertext), c.identity)
	if err != nil {
		return nil, fmt.Errorf("age decrypt: %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("age decrypt read: %w", err)
	}
	return buf.Bytes(), nil
}

// loadAgeIdentity reads an age-keygen file and
// returns the first parsed identity. A real
// identity file has exactly one
// `AGE-SECRET-KEY-1...` line; we accept any
// non-comment, non-empty line as a candidate so
// the helper is robust to leading comment
// headers like `# public key: age1...` (which
// `age-keygen` writes as a hint).
func loadAgeIdentity(path string) (age.Identity, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read identity file %q: %w", path, err)
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		// age.ParseX25519Identity accepts the
		// `AGE-SECRET-KEY-1...` form.
		id, err := age.ParseX25519Identity(line)
		if err != nil {
			continue
		}
		return id, nil
	}
	return nil, fmt.Errorf("identity file %q: no valid AGE-SECRET-KEY line found", path)
}
