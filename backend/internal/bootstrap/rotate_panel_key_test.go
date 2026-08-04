// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

// envelopeNoop is a test-local SecretCipher
// stand-in. The real `envelope` package is
// the production implementation; the
// rotate-panel-key code accepts any
// envelope.SecretCipher via the
// generateAndPushKey signature. We use a
// local no-op here to keep the test hermetic
// and avoid pulling in the real age cipher
// (which requires a generated key file).
type envelopeNoop struct{}

func (envelopeNoop) Encrypt(plaintext []byte) ([]byte, error) {
	out := make([]byte, len(plaintext))
	copy(out, plaintext)
	return out, nil
}
func (envelopeNoop) Decrypt(ciphertext []byte) ([]byte, error) {
	out := make([]byte, len(ciphertext))
	copy(out, ciphertext)
	return out, nil
}

// TestRotatePanelKey_NilEnvelopeFailsClosed
// pins the "no envelope" failure mode. The
// v0.8.3 CLI is a "never persist plaintext
// PEM" tool; a nil envelope (the panel's
// webhooks Store was not configured) is the
// canonical "this deploy is broken" path.
// The function returns without touching the
// database.
func TestRotatePanelKey_NilEnvelopeFailsClosed(t *testing.T) {
	store := newMockNodeProvider()
	s := NewService(ServiceConfig{
		Nodes:    store,
		Envelope: nil,
	})
	// Pass any client — the function must
	// reject before using it.
	if err := s.RotatePanelKey(context.Background(), uuid.New(), "test-node", nil); err == nil {
		t.Fatal("RotatePanelKey with nil envelope should fail, got nil error")
	}
	// The DB row's ciphertext column must
	// be unchanged (empty).
	for _, r := range store.rows {
		if len(r.SSHPrivateKeyCiphertext) != 0 {
			t.Fatalf("RotatePanelKey wrote ciphertext despite nil envelope")
		}
	}
}

// TestRotatePanelKey_NilClientFailsClosed
// pins the "no client" failure mode. The
// function must not panic on a nil SSH
// client; it returns an error without
// touching the DB.
func TestRotatePanelKey_NilClientFailsClosed(t *testing.T) {
	store := newMockNodeProvider()
	s := NewService(ServiceConfig{
		Nodes:    store,
		Envelope: envelopeNoop{},
	})
	if err := s.RotatePanelKey(context.Background(), uuid.New(), "test-node", nil); err == nil {
		t.Fatal("RotatePanelKey with nil client should fail, got nil error")
	}
	for _, r := range store.rows {
		if len(r.SSHPrivateKeyCiphertext) != 0 {
			t.Fatalf("RotatePanelKey wrote ciphertext despite nil client")
		}
	}
}

// _ = errors.Is keeps the errors import in
// use even if every per-call helper later
// moves to a different file. (Currently
// unused but kept for future test additions.)
var _ = func() error { return nil }
