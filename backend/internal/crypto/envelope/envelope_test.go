// SPDX-License-Identifier: AGPL-3.0-or-later

package envelope

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// writeAgeIdentityFile writes a fresh age identity
// to a temp file in the standard `age-keygen` format
// and returns the path. The file is cleaned up
// automatically by `t.TempDir`. The resulting path
// is what NewAgeSecretCipher expects as the
// `identityFile` argument.
func writeAgeIdentityFile(t *testing.T, id *age.X25519Identity) string {
	t.Helper()
	recipient := id.Recipient()
	content := "# public key: " + recipient.String() + "\n" + id.String() + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "age-identity.key")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	return path
}

func TestAgeSecretCipher_RoundTrip(t *testing.T) {
	t.Parallel()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	path := writeAgeIdentityFile(t, id)
	cipher, err := NewAgeSecretCipher([]string{id.Recipient().String()}, path)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher: %v", err)
	}
	plaintext := []byte("webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa")
	ciphertext, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// The ciphertext MUST NOT contain the plaintext.
	if bytes.Contains(ciphertext, plaintext) {
		t.Errorf("ciphertext contains plaintext: %q", ciphertext)
	}
	// The ciphertext MUST be longer than the
	// plaintext (age adds a header + nonce +
	// MAC overhead). A length-equal output would
	// mean the cipher is a no-op.
	if len(ciphertext) <= len(plaintext) {
		t.Errorf("ciphertext length %d, want > plaintext length %d", len(ciphertext), len(plaintext))
	}
	got, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("Decrypt = %q, want %q", got, plaintext)
	}
}

func TestAgeSecretCipher_MultipleRecipients(t *testing.T) {
	t.Parallel()
	// Two operators, two identities, one
	// ciphertext that either can open. This
	// is the key-rotation use case: seal with
	// both the old and new key, then rotate
	// the identity at leisure.
	idA, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity A: %v", err)
	}
	idB, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity B: %v", err)
	}
	// Operator A seals the message; the
	// cipher accepts both A and B as
	// recipients.
	pathA := writeAgeIdentityFile(t, idA)
	cipher, err := NewAgeSecretCipher(
		[]string{idA.Recipient().String(), idB.Recipient().String()},
		pathA,
	)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher: %v", err)
	}
	plaintext := []byte("sealed-by-A-readable-by-A-and-B")
	ciphertext, err := cipher.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	// A opens (its own seal).
	gotA, err := cipher.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt (A): %v", err)
	}
	if !bytes.Equal(gotA, plaintext) {
		t.Errorf("Decrypt (A) = %q, want %q", gotA, plaintext)
	}
	// B also opens (a recipient, not the sealer).
	pathB := writeAgeIdentityFile(t, idB)
	cipherB, err := NewAgeSecretCipher([]string{idB.Recipient().String()}, pathB)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher (B): %v", err)
	}
	gotB, err := cipherB.Decrypt(ciphertext)
	if err != nil {
		t.Fatalf("Decrypt (B): %v", err)
	}
	if !bytes.Equal(gotB, plaintext) {
		t.Errorf("Decrypt (B) = %q, want %q", gotB, plaintext)
	}
}

func TestAgeSecretCipher_WrongIdentityFails(t *testing.T) {
	t.Parallel()
	operator, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	intruder, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity (intruder): %v", err)
	}
	operatorCipher, err := NewAgeSecretCipher(
		[]string{operator.Recipient().String()},
		writeAgeIdentityFile(t, operator),
	)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher (operator): %v", err)
	}
	intruderCipher, err := NewAgeSecretCipher(
		[]string{intruder.Recipient().String()},
		writeAgeIdentityFile(t, intruder),
	)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher (intruder): %v", err)
	}
	ciphertext, err := operatorCipher.Encrypt([]byte("operator-only-secret"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := intruderCipher.Decrypt(ciphertext); err == nil {
		t.Errorf("intruder decrypted the operator's ciphertext — encryption is broken")
	}
}

func TestNewAgeSecretCipher_RejectsEmptyRecipients(t *testing.T) {
	t.Parallel()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	path := writeAgeIdentityFile(t, id)
	_, err = NewAgeSecretCipher(nil, path)
	if err == nil {
		t.Errorf("expected error on nil recipients")
	}
	_, err = NewAgeSecretCipher([]string{}, path)
	if err == nil {
		t.Errorf("expected error on empty recipients")
	}
	_, err = NewAgeSecretCipher([]string{""}, path)
	if err == nil {
		t.Errorf("expected error on blank-only recipients")
	}
}

func TestNewAgeSecretCipher_RejectsMissingIdentityFile(t *testing.T) {
	t.Parallel()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	_, err = NewAgeSecretCipher(
		[]string{id.Recipient().String()},
		"/nonexistent/path/that/does/not/exist",
	)
	if err == nil {
		t.Errorf("expected error on missing identity file")
	}
}

func TestNewAgeSecretCipher_RejectsEmptyIdentityPath(t *testing.T) {
	t.Parallel()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	_, err = NewAgeSecretCipher([]string{id.Recipient().String()}, "")
	if err == nil {
		t.Errorf("expected error on empty identity path")
	}
}

func TestNewAgeSecretCipher_RejectsMalformedRecipient(t *testing.T) {
	t.Parallel()
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	_, err = NewAgeSecretCipher(
		[]string{"not-a-valid-age1-recipient"},
		writeAgeIdentityFile(t, id),
	)
	if err == nil {
		t.Errorf("expected error on malformed recipient")
	}
}

func TestLoadAgeIdentity_RejectsFileWithoutKey(t *testing.T) {
	t.Parallel()
	// A file with only a comment is not a valid
	// identity file. loadAgeIdentity returns
	// an error rather than panicking.
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.key")
	if err := os.WriteFile(path, []byte("# only a comment, no key line\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	id, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	_, err = NewAgeSecretCipher([]string{id.Recipient().String()}, path)
	if err == nil {
		t.Errorf("expected error on identity file with no key line")
	}
	if !strings.Contains(err.Error(), "no valid AGE-SECRET-KEY") {
		t.Errorf("error = %v, want a message about no valid key", err)
	}
}

func TestNoopSecretCipher(t *testing.T) {
	t.Parallel()
	c := NewNoopSecretCipher()
	plain := []byte("plaintext-secret")
	got, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("NoopSecretCipher.Encrypt changed the bytes")
	}
	got, err = c.Decrypt(plain)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plain) {
		t.Errorf("NoopSecretCipher.Decrypt changed the bytes")
	}
}

// Compile-time interface sanity: a NoopSecretCipher
// satisfies the SecretCipher interface. (Compile
// failure is reported as a test failure, not a
// silent wrong behaviour.)
var _ SecretCipher = NewNoopSecretCipher()
var _ SecretCipher = (*AgeSecretCipher)(nil)
