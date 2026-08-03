// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package envelope is the v0.8.x shared age-encryption
// boundary for any "long-lived at-rest secret" the
// panel stores. The plaintext shape is opaque to
// this package: the only contract is `Encrypt(plain)
// -> cipher` and `Decrypt(cipher) -> plain` with the
// strong guarantee that the cipher is bound to one
// of the operator's age identities (the panel's
// `AEGIS_*_SECRET_AGE_KEY_FILE`).
//
// # Why a separate package
//
// The age envelope was first introduced in v0.7.x for
// `webhook_endpoints.secret` (see migration 0018 and
// the v0.7.x follow-up PRs). v0.8.x lifts the cipher
// out of `internal/webhooks` so other surfaces — the
// persistent node SSH key from the password-first
// install flow being the first — can share the same
// key-rotation story (recipients list + identity
// file). One envelope, one set of `AEGIS_*_SECRET_AGE_*`
// env vars, one backup / disaster-recovery drill.
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
// 49` bytes. The caller stores the bytes in whatever
// column type makes sense (BYTEA in our PG schema;
// `webhook_endpoints.secret_ciphertext` is one
// example). The DB never sees the plaintext.
//
// # Identity file
//
// `NewAgeSecretCipher` reads the operator's age
// identity from the path given by the relevant
// `AEGIS_*_SECRET_AGE_KEY_FILE` env var. The file
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
// The relevant `AEGIS_*_SECRET_AGE_RECIPIENTS`
// env var is a comma-separated list of age public
// keys. The ciphertext can be opened by ANY of the
// matching identities. Two recipients is the
// typical setup (operator + break-glass) and the
// panel does not need to know which one decrypted
// the row.
package envelope

// SecretCipher is the encryption boundary the
// Store layer relies on. The Service never sees
// ciphertext; the Store never sees plaintext. A
// no-op implementation (NewNoopSecretCipher) is
// used in dev and unit tests where the Store is
// a MemoryStore and at-rest encryption is not a
// concern.
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
