// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net"
	"os"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"
)

// testAddr is a deterministic non-nil net.Addr
// for the hostKeyCallback tests. The knownhosts
// library dereferences the address for logging,
// so nil panics.
var testAddr = &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 22}

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

// TestNewClient_AuthMethodXOR covers the
// first-time-install UX: exactly one of
// PrivateKey or Password must be set, never both,
// never neither. Both set is ambiguous (the
// client can't pick); neither set means no auth.
// The test runs the cross product and asserts the
// right error in each cell.
func TestNewClient_AuthMethodXOR(t *testing.T) {
	cases := []struct {
		name    string
		key     []byte
		pass    string
		wantErr bool
	}{
		{"both empty", nil, "", true},
		{"only key set", []byte("k"), "", false},
		{"only password set", nil, "secret", false},
		{"both set (ambiguous)", []byte("k"), "secret", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := NewClient(ClientConfig{
				Address:    "h:22",
				User:       "root",
				PrivateKey: c.key,
				Password:   c.pass,
				KnownHosts: "/tmp/kh",
			})
			if c.wantErr && err == nil {
				t.Errorf("NewClient(%+v) = nil, want error", c)
			}
			if !c.wantErr && err != nil {
				t.Errorf("NewClient(%+v) = %v, want nil", c, err)
			}
		})
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

// TestHostKeyCallback_EmptyKnownHosts_TOFU_Accepts
// is the regression test for the v0.8.19
// live-smoke bug. On a fresh install the panel's
// known_hosts file exists as a 0-byte mount point;
// before PR #230 the hostKeyCallback early-returned
// the strict knownhosts.New callback and the TOFU
// policy was never consulted, so the very first
// provision hit "knownhosts: key is unknown" with
// no fallback. The fix: the TOFU policy IS the
// callback; the known_hosts file is consulted
// inside the TofuAcceptAndAppend branch.
func TestHostKeyCallback_EmptyKnownHosts_TOFU_Accepts(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	// Empty file (0 bytes) — the bug condition.
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("seed empty known_hosts: %v", err)
	}
	signer := newTestSigner(t)
	c := &sshClient{
		cfg: ClientConfig{
			Address:             "h:22",
			User:                "root",
			Password:            "secret",
			KnownHosts:          path,
			Tofu:                TofuAcceptAndAppend,
			ExpectedFingerprint: ssh.FingerprintSHA256(signer.PublicKey()),
		},
	}
	cb, err := c.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb("h:22", testAddr, signer.PublicKey()); err != nil {
		t.Fatalf("callback with empty known_hosts + matching fp: %v (pre-PR-#230 bug: 'key is unknown')", err)
	}
	// And the key must be stashed for the post-handshake append.
	if c.tofuKey == nil {
		t.Fatal("tofuKey not stashed — Connect will not append on success")
	}
}

// TestHostKeyCallback_KnownKey_Accepted is the
// happy-path: an existing known_hosts entry must
// be accepted silently (no fingerprint compare, no
// append). PR #230 preserves this behavior — the
// known_hosts lookup runs inside the TOFU
// callback and short-circuits on match.
func TestHostKeyCallback_KnownKey_Accepted(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	signer := newTestSigner(t)
	// Pre-populate known_hosts with the test key.
	if err := appendKnownHosts(path, "h:22", signer.PublicKey()); err != nil {
		t.Fatalf("seed: %v", err)
	}
	c := &sshClient{
		cfg: ClientConfig{
			Address:             "h:22",
			User:                "root",
			Password:            "secret",
			KnownHosts:          path,
			Tofu:                TofuAcceptAndAppend,
			ExpectedFingerprint: "SHA256:wrong-on-purpose", // should be ignored
		},
	}
	cb, err := c.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	if err := cb("h:22", testAddr, signer.PublicKey()); err != nil {
		t.Fatalf("callback with known key: %v", err)
	}
	// The tofuKey must NOT be stashed — the key is
	// already in known_hosts; no append needed.
	if c.tofuKey != nil {
		t.Error("tofuKey stashed for an already-known host — would re-append on Connect")
	}
}

// TestHostKeyCallback_EmptyKnownHosts_RejectsOnMismatch
// is the safety net: even with an empty file,
// TofuAcceptAndAppend must reject a key whose
// fingerprint does not match ExpectedFingerprint.
func TestHostKeyCallback_EmptyKnownHosts_RejectsOnMismatch(t *testing.T) {
	path := t.TempDir() + "/known_hosts"
	if err := os.WriteFile(path, nil, 0600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	signer := newTestSigner(t)
	c := &sshClient{
		cfg: ClientConfig{
			Address:             "h:22",
			User:                "root",
			Password:            "secret",
			KnownHosts:          path,
			Tofu:                TofuAcceptAndAppend,
			ExpectedFingerprint: "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=",
		},
	}
	cb, err := c.hostKeyCallback()
	if err != nil {
		t.Fatalf("hostKeyCallback: %v", err)
	}
	err = cb("h:22", testAddr, signer.PublicKey())
	if err == nil {
		t.Fatal("callback accepted mismatched fingerprint")
	}
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Errorf("want ErrHostKeyMismatch, got %v", err)
	}
}
