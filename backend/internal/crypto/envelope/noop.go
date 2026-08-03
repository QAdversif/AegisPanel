// SPDX-License-Identifier: AGPL-3.0-or-later

package envelope

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
