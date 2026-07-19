// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// TestNewClient_RequiredFields exercises the
// ClientConfig validation. The function is the
// only synchronous guard on the install path
// before the network is touched; missing a check
// here means a misconfigured operator gets a
// confusing "dial timeout" instead of a clear
// "private key missing".
func TestNewClient_RequiredFields(t *testing.T) {
	cases := []struct {
		name string
		cfg  ClientConfig
	}{
		{"missing address", ClientConfig{User: "root", PrivateKey: []byte("k"), KnownHosts: "/tmp/kh"}},
		{"missing user", ClientConfig{Address: "h:22", PrivateKey: []byte("k"), KnownHosts: "/tmp/kh"}},
		{"missing private key", ClientConfig{Address: "h:22", User: "root", KnownHosts: "/tmp/kh"}},
		{"missing known_hosts", ClientConfig{Address: "h:22", User: "root", PrivateKey: []byte("k")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := NewClient(c.cfg); err == nil {
				t.Errorf("NewClient(%+v) = nil, want error", c.cfg)
			}
		})
	}
	if _, err := NewClient(ClientConfig{
		Address: "h:22", User: "root", PrivateKey: []byte("k"), KnownHosts: "/tmp/kh",
	}); err != nil {
		t.Errorf("NewClient with all fields: %v", err)
	}
}

// TestFingerprintEqual is a small helper for
// the TOFU code. The on-the-wire form is
// "SHA256:base64"; the operator's paste may
// have any case, so the comparison is case-
// insensitive.
func TestFingerprintEqual(t *testing.T) {
	a := "SHA256:abc123"
	b := "sha256:abc123"
	c := "SHA256:def456"
	if !fingerprintEqual(a, b) {
		t.Error("fingerprintEqual should be case-insensitive")
	}
	if fingerprintEqual(a, c) {
		t.Error("fingerprintEqual should reject different fingerprints")
	}
}

// TestExecError_MessageIsBounded verifies the
// 200-char stderr cap. A 10-MB log from a
// failed systemctl run should not blow up the
// HTTP error envelope.
func TestExecError_MessageIsBounded(t *testing.T) {
	big := strings.Repeat("a", 10_000)
	err := &ExecError{Cmd: "systemctl status", ExitStatus: 1, Stderr: big}
	msg := err.Error()
	if len(msg) > 300 {
		t.Errorf("ExecError message len = %d, want < 300 (truncated)", len(msg))
	}
	if !strings.Contains(msg, "truncated") {
		t.Error("ExecError should mention truncation for long stderr")
	}
	if !strings.Contains(msg, "exit 1") && !strings.Contains(msg, "exited 1") {
		t.Errorf("ExecError message should include exit status, got: %q", msg)
	}
}

// TestAppendKnownHosts_CreatesAndAppends exercises
// the TOFU append. The test writes one key, then
// writes a second, and asserts the file has
// both lines. The `knownhosts.Line` helper
// strips the port and renders `host1` (not
// `host1:22`) — the round-trip through the
// OpenSSH `knownhosts.New` parser accepts
// either, so this is purely cosmetic.
func TestAppendKnownHosts_CreatesAndAppends(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	signer := newTestSigner(t)

	if err := appendKnownHosts(path, "host1:22", signer.PublicKey()); err != nil {
		t.Fatalf("append 1: %v", err)
	}
	if err := appendKnownHosts(path, "host2:22", signer.PublicKey()); err != nil {
		t.Fatalf("append 2: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "host1 ") {
		t.Errorf("file missing host1 entry, got: %q", string(body))
	}
	if !strings.Contains(string(body), "host2 ") {
		t.Errorf("file missing host2 entry, got: %q", string(body))
	}
}

// TestAppendKnownHosts_PreservesExistingContent
// verifies the append is non-destructive: a
// pre-existing entry is kept.
func TestAppendKnownHosts_PreservesExistingContent(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	const existing = "preserve.example.com ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIExamplePreExistingKeyForTestingDoNotUse\n"
	if err := os.WriteFile(path, []byte(existing), 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	signer := newTestSigner(t)
	if err := appendKnownHosts(path, "new.example.com:22", signer.PublicKey()); err != nil {
		t.Fatalf("append: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(body), "preserve.example.com") {
		t.Error("existing entry lost after append")
	}
	if !strings.Contains(string(body), "new.example.com") {
		t.Error("new entry missing")
	}
}

// TestAppendKnownHosts_RejectsEmptyPath is a
// defensive guard. The function is best-effort
// but a missing path is a hard error.
func TestAppendKnownHosts_RejectsEmptyPath(t *testing.T) {
	if err := appendKnownHosts("", "h:22", nil); err == nil {
		t.Error("empty path should error")
	}
}

// newTestSigner returns a fresh ed25519 signer
// for the duration of a test. The keys are
// ephemeral; do not use them outside the test
// process.
func newTestSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatalf("NewSignerFromKey: %v", err)
	}
	return signer
}
