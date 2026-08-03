// SPDX-License-Identifier: AGPL-3.0-or-later

package envelope

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"strings"

	"filippo.io/age"
)

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
// shape for `AEGIS_*_SECRET_AGE_RECIPIENTS` is a
// comma-separated list.
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
		return nil, errors.New("envelope: at least one recipient is required for sealing")
	}
	recipients := make([]age.Recipient, 0, len(recipientKeys))
	for _, key := range recipientKeys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		r, err := age.ParseX25519Recipient(key)
		if err != nil {
			return nil, fmt.Errorf("envelope: parse recipient %q: %w", key, err)
		}
		recipients = append(recipients, r)
	}
	if len(recipients) == 0 {
		return nil, errors.New("envelope: no non-empty recipients after trimming")
	}
	if identityFile == "" {
		return nil, errors.New("envelope: identity file path is required for opening")
	}
	identity, err := loadAgeIdentity(identityFile)
	if err != nil {
		return nil, fmt.Errorf("envelope: %w", err)
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
		return nil, fmt.Errorf("envelope: age encrypt: %w", err)
	}
	if _, err := wc.Write(plaintext); err != nil {
		return nil, fmt.Errorf("envelope: age encrypt write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return nil, fmt.Errorf("envelope: age encrypt close: %w", err)
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
		return nil, fmt.Errorf("envelope: age decrypt: %w", err)
	}
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		return nil, fmt.Errorf("envelope: age decrypt read: %w", err)
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
