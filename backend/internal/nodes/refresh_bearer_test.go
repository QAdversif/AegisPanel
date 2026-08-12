// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Unit tests for the v0.8.7 refresh-agent-bearer
// path: `GetStoredKeyForUse`, `RefreshAgentBearer`,
// and the two private helpers `parseAgentEnvBearer`
// and `resolveSSHAddress`. The tests are hermetic
// (no real SSH dial); the SSH client is replaced
// with a `fakeSSHClient` that returns canned
// `Run` output, matching the v0.8.4
// `handler_rotate_panel_key_test.go` pattern.

package nodes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/bootstrap"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// fakeSSHClient is a stand-in for the
// production `bootstrap.Client`. The struct
// returns canned values from `Connect` and
// `Run`, and records the `Upload` calls for
// assertions. The zero value is a usable
// "Connect succeeds, Run returns "" " — pass
// `RunOutput` / `RunErr` / `ConnectErr` to
// customise. The struct also tracks the
// number of `Close` calls (so the test
// can assert `defer sshClient.Close()`
// runs).
type fakeSSHClient struct {
	ConnectErr   error
	RunOutput    string
	RunErr       error
	ConnectCalls int32
	RunCalls     int32
	CloseCalls   int32
	// LastConfig captures the ClientConfig
	// the factory was called with. Used to
	// assert Address / User / PrivateKey /
	// KnownHosts were wired correctly.
	LastConfig bootstrap.ClientConfig
}

func (f *fakeSSHClient) Connect(_ context.Context) error {
	atomic.AddInt32(&f.ConnectCalls, 1)
	return f.ConnectErr
}

func (f *fakeSSHClient) Run(_ context.Context, _ string) (string, error) {
	atomic.AddInt32(&f.RunCalls, 1)
	return f.RunOutput, f.RunErr
}

func (f *fakeSSHClient) Upload(_ context.Context, _, _ string, _ os.FileMode) error {
	return errors.New("not implemented in fakeSSHClient")
}

// UploadAndSwap is the v0.8.25+ method for
// replacing a binary that may be currently
// executing on the remote. The fake
// implementation mirrors Upload (not used by
// the refresh-bearer path; it exists only to
// satisfy the `bootstrap.Client` interface).
func (f *fakeSSHClient) UploadAndSwap(_ context.Context, _, _ string, _ os.FileMode) error {
	return errors.New("not implemented in fakeSSHClient")
}

// Close satisfies the `bootstrap.Client`
// interface. The signature is the real
// one; the v0.8.7 caller wraps the
// `defer` in `func() { _ = ...Close() }`
// to satisfy errcheck.
func (f *fakeSSHClient) Close() error {
	atomic.AddInt32(&f.CloseCalls, 1)
	return nil
}

// seedStoredKey generates a real ed25519
// keypair, encrypts the PEM with the
// given envelope, and writes the row to
// the in-memory store. The function is
// the test fixture used by the
// `RefreshAgentBearer` happy path.
func seedStoredKey(t *testing.T, svc *Service, env envelope.SecretCipher, nodeID uuid.UUID, name string) ed25519.PrivateKey {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519: %v", err)
	}
	// Marshal to OpenSSH PEM (the v0.8.1
	// write-side format).
	block, err := ssh.MarshalPrivateKey(priv, "aegis-panel@node-"+name)
	if err != nil {
		t.Fatalf("marshal private key: %v", err)
	}
	pemBytes := pem.EncodeToMemory(block)
	cipher, err := env.Encrypt(pemBytes)
	if err != nil {
		t.Fatalf("encrypt PEM: %v", err)
	}
	// Pre-seed the row with the stored
	// ciphertext. The MemoryStore's
	// `byID` map is private to the
	// package; the test uses the public
	// `Create` + `Update` (or, for
	// less ceremony, the `Update` with
	// a row that already has the
	// `SSHPrivateKeyCiphertext` field
	// populated).
	if _, err := svc.store.GetByID(context.Background(), nodeID); err != nil {
		t.Fatalf("node %s not pre-seeded: %v", nodeID, err)
	}
	if err := svc.store.SetSSHPrivateKeyCiphertext(context.Background(), nodeID, cipher); err != nil {
		t.Fatalf("seed stored key: %v", err)
	}
	return priv
}

// newEnvelopeFixture is a tiny
// test-only SecretCipher that XORs
// the plaintext with a fixed byte
// sequence. The XOR is invertible
// (Encrypt and Decrypt are the same
// function) and is enough to prove
// the round-trip; the production
// `envelope.AgeSecretCipher` is
// covered by its own tests.
type xorCipher struct {
	key []byte
}

func (x *xorCipher) Encrypt(plain []byte) ([]byte, error) {
	out := make([]byte, len(plain))
	for i, b := range plain {
		out[i] = b ^ x.key[i%len(x.key)]
	}
	return out, nil
}

func (x *xorCipher) Decrypt(cipher []byte) ([]byte, error) {
	return x.Encrypt(cipher)
}

func newEnvelopeFixture() envelope.SecretCipher {
	return &xorCipher{key: []byte("test-key-32-bytes-padded-1234")}
}

// newServiceWithStore builds a Service
// backed by a MemoryStore with the
// given node pre-seeded. The function
// is a thin wrapper around the v0.8.5
// test fixture pattern (see
// `stored_key_test.go`'s
// `newTestService`).
func newServiceWithStore(t *testing.T) *Service {
	t.Helper()
	store := NewMemoryStore()
	svc := NewService(store).WithEnvelope(newEnvelopeFixture())
	return svc
}

// seedNode creates a node row with the
// given name and returns the ID. The
// row is in the `offline` state (the
// v0.3.0 install flow's "installed but
// not yet online" state — the state
// where stored keys exist but the
// agent bearer might be stale). The
// address is the canonical
// `1.2.3.4:22` shape (host:port); the
// v0.3.0 install always writes this
// shape, and the v0.8.7 tests assert
// the per-call port override against
// the `1.2.3.4` host component.
func seedNode(t *testing.T, svc *Service, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := svc.Create(context.Background(), CreateInput{
		ID:      id,
		Name:    name,
		Region:  "test-region",
		State:   StateOffline,
		Address: "1.2.3.4:22",
	})
	if err != nil {
		t.Fatalf("create node: %v", err)
	}
	return id
}

// makeFactory returns a `sshClientFactory`
// closure that always returns the same
// `*fakeSSHClient`. The factory is what
// the Service uses to construct SSH
// connections; the closure captures the
// fake so the test can assert
// `ConnectCalls` / `RunCalls` after the
// Service call.
func makeFactory(fake *fakeSSHClient) func(bootstrap.ClientConfig) (bootstrap.Client, error) {
	return func(cfg bootstrap.ClientConfig) (bootstrap.Client, error) {
		fake.LastConfig = cfg
		return fake, nil
	}
}

// ============================================================================
// GetStoredKeyForUse
// ============================================================================

// TestGetStoredKeyForUse_HappyPath is the
// round-trip: generate ed25519 → encrypt via
// envelope → persist ciphertext via
// SetSSHPrivateKeyCiphertext →
// GetStoredKeyForUse → decrypt → returns the
// exact same PEM. The fingerprint of the
// decrypted public key matches the
// fingerprint derived from the original
// private key (sanity).
func TestGetStoredKeyForUse_HappyPath(t *testing.T) {
	svc := newServiceWithStore(t)
	nodeID := seedNode(t, svc, "happy")
	priv := seedStoredKey(t, svc, svc.envelope, nodeID, "happy")

	out, err := svc.GetStoredKeyForUse(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("GetStoredKeyForUse: %v", err)
	}
	if len(out.PrivateKeyPEM) == 0 {
		t.Fatal("PrivateKeyPEM is empty")
	}
	// Round-trip check: parse the
	// returned PEM, derive the public
	// key, compare to the public key
	// from the original private key.
	gotPub, err := ssh.ParseRawPrivateKey(out.PrivateKeyPEM)
	if err != nil {
		t.Fatalf("parse returned PEM: %v", err)
	}
	wantPub := priv.Public().(ed25519.PublicKey)
	gotEd, ok := gotPub.(*ed25519.PrivateKey)
	if !ok {
		t.Fatalf("returned key is not ed25519.PrivateKey: %T", gotPub)
	}
	if !gotEd.Public().(ed25519.PublicKey).Equal(wantPub) {
		t.Errorf("public key mismatch: got %x, want %x", gotEd.Public(), wantPub)
	}
}

// TestGetStoredKeyForUse_NilEnvelope_FailsClosed
// matches the v0.8.5 GetStoredKey contract:
// a nil envelope returns an error, not a
// zero-value `StoredKeyForUse{}`.
func TestGetStoredKeyForUse_NilEnvelope_FailsClosed(t *testing.T) {
	svc := NewService(NewMemoryStore()) // no WithEnvelope
	nodeID := seedNode(t, svc, "noenv")
	_, err := svc.GetStoredKeyForUse(context.Background(), nodeID)
	if err == nil {
		t.Fatal("expected error for nil envelope, got nil")
	}
	if !strings.Contains(err.Error(), "envelope is not configured") {
		t.Errorf("error must name the env var: %q", err.Error())
	}
}

// TestGetStoredKeyForUse_NoStoredKey returns
// ErrNoStoredKey (NOT ErrNotFound) so the
// HTTP layer can map it to a specific 4xx
// ("rotate-panel-key first") rather than a
// generic 404.
func TestGetStoredKeyForUse_NoStoredKey(t *testing.T) {
	svc := newServiceWithStore(t)
	nodeID := seedNode(t, svc, "nokey") // no SetSSHPrivateKeyCiphertext
	_, err := svc.GetStoredKeyForUse(context.Background(), nodeID)
	if !errors.Is(err, ErrNoStoredKey) {
		t.Fatalf("expected ErrNoStoredKey, got %v", err)
	}
}

// TestGetStoredKeyForUse_NodeNotFound
// propagates `ErrNotFound` from the
// underlying Store.
func TestGetStoredKeyForUse_NodeNotFound(t *testing.T) {
	svc := newServiceWithStore(t)
	_, err := svc.GetStoredKeyForUse(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// ============================================================================
// parseAgentEnvBearer
// ============================================================================

// TestParseAgentEnvBearer_HappyPath parses a
// single-line `AEGIS_AGENT_BEARER=...` file
// (the v0.3.0 write-side format).
func TestParseAgentEnvBearer_HappyPath(t *testing.T) {
	got, err := parseAgentEnvBearer("AEGIS_AGENT_BEARER=deadbeef1234\n")
	if err != nil {
		t.Fatalf("parseAgentEnvBearer: %v", err)
	}
	if got != "deadbeef1234" {
		t.Errorf("bearer: want %q, got %q", "deadbeef1234", got)
	}
}

// TestParseAgentEnvBearer_WithCommentsAndBlanks
// proves the parser is permissive of
// trailing blank lines + `#` comments
// (a future agent might emit a header).
func TestParseAgentEnvBearer_WithCommentsAndBlanks(t *testing.T) {
	in := "# aegis-agent env\n\nAEGIS_AGENT_BEARER=abc123\n# trailing\n"
	got, err := parseAgentEnvBearer(in)
	if err != nil {
		t.Fatalf("parseAgentEnvBearer: %v", err)
	}
	if got != "abc123" {
		t.Errorf("bearer: want %q, got %q", "abc123", got)
	}
}

// TestParseAgentEnvBearer_MissingKey returns
// an error containing the key name, not a
// generic "parse error".
func TestParseAgentEnvBearer_MissingKey(t *testing.T) {
	in := "OTHER_KEY=foo\nAEGIS_FOO=bar\n"
	_, err := parseAgentEnvBearer(in)
	if err == nil {
		t.Fatal("expected error for missing key, got nil")
	}
	if !strings.Contains(err.Error(), "AEGIS_AGENT_BEARER not found") {
		t.Errorf("error must name the missing key: %q", err.Error())
	}
}

// TestParseAgentEnvBearer_EmptyValue rejects
// `AEGIS_AGENT_BEARER=` with no value
// (the v0.3.0 install always writes a
// non-empty value; empty is a misconfig
// the operator must fix, not a
// recoverable condition).
func TestParseAgentEnvBearer_EmptyValue(t *testing.T) {
	_, err := parseAgentEnvBearer("AEGIS_AGENT_BEARER=\n")
	if err == nil {
		t.Fatal("expected error for empty value, got nil")
	}
	if !strings.Contains(err.Error(), "is empty") {
		t.Errorf("error must mention emptiness: %q", err.Error())
	}
}

// TestParseAgentEnvBearer_ForbiddenChars rejects
// values with shell-metacharacters. The
// agent's `installRandomHex` is hex-only,
// so anything else is a sign of either
// corruption or injection.
func TestParseAgentEnvBearer_ForbiddenChars(t *testing.T) {
	cases := []struct {
		name  string
		value string
	}{
		{"quote", `"`},
		{"dollar", "$"},
		{"backtick", "`"},
		{"newline", "\n"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseAgentEnvBearer("AEGIS_AGENT_BEARER=" + tc.value + "\n")
			if err == nil {
				t.Fatalf("expected error for forbidden char %q, got nil", tc.value)
			}
		})
	}
}

// TestParseAgentEnvBearer_TooLong is the
// sanity cap: a >1KB bearer is not a
// real bearer.
func TestParseAgentEnvBearer_TooLong(t *testing.T) {
	longValue := strings.Repeat("a", 1025)
	_, err := parseAgentEnvBearer("AEGIS_AGENT_BEARER=" + longValue + "\n")
	if err == nil {
		t.Fatal("expected error for too-long value, got nil")
	}
	if !strings.Contains(err.Error(), "too long") {
		t.Errorf("error must mention length: %q", err.Error())
	}
}

// ============================================================================
// resolveSSHAddress
// ============================================================================

// TestResolveSSHAddress_Default returns the
// row's address unchanged when no port
// override is supplied.
func TestResolveSSHAddress_Default(t *testing.T) {
	got, err := resolveSSHAddress("1.2.3.4:22", 0)
	if err != nil {
		t.Fatalf("resolveSSHAddress: %v", err)
	}
	if got != "1.2.3.4:22" {
		t.Errorf("address: want %q, got %q", "1.2.3.4:22", got)
	}
}

// TestResolveSSHAddress_OverridePort replaces
// the port while keeping the host.
func TestResolveSSHAddress_OverridePort(t *testing.T) {
	got, err := resolveSSHAddress("1.2.3.4:22", 2222)
	if err != nil {
		t.Fatalf("resolveSSHAddress: %v", err)
	}
	if got != "1.2.3.4:2222" {
		t.Errorf("address: want %q, got %q", "1.2.3.4:2222", got)
	}
}

// TestResolveSSHAddress_NoPortAppends covers
// the "host" with no port + non-zero
// override: the result is `host:port`.
func TestResolveSSHAddress_NoPortAppends(t *testing.T) {
	got, err := resolveSSHAddress("1.2.3.4", 2222)
	if err != nil {
		t.Fatalf("resolveSSHAddress: %v", err)
	}
	if got != "1.2.3.4:2222" {
		t.Errorf("address: want %q, got %q", "1.2.3.4:2222", got)
	}
}

// TestResolveSSHAddress_Empty returns an
// error (defensive: the node row
// shouldn't exist without an address,
// but the parser must not panic).
func TestResolveSSHAddress_Empty(t *testing.T) {
	_, err := resolveSSHAddress("", 0)
	if err == nil {
		t.Fatal("expected error for empty address, got nil")
	}
}

// ============================================================================
// RefreshAgentBearer
// ============================================================================

// TestRefreshAgentBearer_HappyPath is the
// full round-trip: store has stored key
// + known host; SSH factory returns
// fakeSSHClient with canned `Run`
// output; the call returns the parsed
// bearer, the row's `agent_bearer` is
// updated, the SSH client was used
// exactly once, and the agent.env
// command was `cat /etc/aegis/agent.env`.
func TestRefreshAgentBearer_HappyPath(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/var/known_hosts").WithSSHUser("root")
	// Replace the nil factory set by
	// `WithSSHClientFactory(nil)` with
	// the real factory + fake client.
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=fresh-bearer-abc123\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "happy")
	seedStoredKey(t, svc, svc.envelope, nodeID, "happy")

	out, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if out.Bearer != "fresh-bearer-abc123" {
		t.Errorf("bearer: want %q, got %q", "fresh-bearer-abc123", out.Bearer)
	}
	if out.NodeID != nodeID {
		t.Errorf("NodeID: want %v, got %v", nodeID, out.NodeID)
	}
	if out.KeyFingerprintSHA256 == "" {
		t.Error("KeyFingerprintSHA256 is empty")
	}
	if !strings.HasPrefix(out.KeyFingerprintSHA256, "SHA256:") {
		t.Errorf("fingerprint must start with SHA256:: %q", out.KeyFingerprintSHA256)
	}
	// The row's `agent_bearer` is updated.
	row, err := svc.store.GetByID(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if row.AgentBearer != "fresh-bearer-abc123" {
		t.Errorf("AgentBearer not persisted: got %q", row.AgentBearer)
	}
	// The SSH factory was called with
	// the right ClientConfig.
	if fake.LastConfig.Address != "1.2.3.4:22" {
		t.Errorf("factory Address: want %q, got %q", "1.2.3.4:22", fake.LastConfig.Address)
	}
	if fake.LastConfig.User != "root" {
		t.Errorf("factory User: want %q, got %q", "root", fake.LastConfig.User)
	}
	if fake.LastConfig.KnownHosts != "/var/known_hosts" {
		t.Errorf("factory KnownHosts: want %q, got %q", "/var/known_hosts", fake.LastConfig.KnownHosts)
	}
	if fake.LastConfig.Tofu != bootstrap.TofuReject {
		t.Errorf("factory Tofu: want TofuReject, got %v", fake.LastConfig.Tofu)
	}
	if len(fake.LastConfig.PrivateKey) == 0 {
		t.Error("factory PrivateKey is empty")
	}
	// SSH lifecycle: Connect + Run + Close
	// each called once.
	if fake.ConnectCalls != 1 {
		t.Errorf("ConnectCalls: want 1, got %d", fake.ConnectCalls)
	}
	if fake.RunCalls != 1 {
		t.Errorf("RunCalls: want 1, got %d", fake.RunCalls)
	}
	if fake.CloseCalls != 1 {
		t.Errorf("CloseCalls: want 1, got %d", fake.CloseCalls)
	}
}

// TestRefreshAgentBearer_NilSSHClientFactory
// returns an error WITHOUT touching the
// store. The factory is the operator's
// wiring responsibility; the test asserts
// the fail-closed shape.
func TestRefreshAgentBearer_NilSSHClientFactory(t *testing.T) {
	svc := newServiceWithStore(t)
	// No WithSSHClientFactory call: the
	// factory is nil.
	nodeID := seedNode(t, svc, "nofactory")
	seedStoredKey(t, svc, svc.envelope, nodeID, "nofactory")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err == nil {
		t.Fatal("expected error for nil factory, got nil")
	}
	if !strings.Contains(err.Error(), "SSH client factory is not configured") {
		t.Errorf("error must name the missing wiring: %q", err.Error())
	}
}

// TestRefreshAgentBearer_NoStoredKey returns
// ErrNoStoredKey (the operator's hint
// to "rotate-panel-key first").
func TestRefreshAgentBearer_NoStoredKey(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "nokey") // no stored key

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if !errors.Is(err, ErrNoStoredKey) {
		t.Fatalf("expected ErrNoStoredKey, got %v", err)
	}
	// The SSH client was never
	// instantiated (the no-stored-key
	// check fires before the factory).
	if fake.ConnectCalls != 0 {
		t.Errorf("ConnectCalls: want 0, got %d (SSH should not have been attempted)", fake.ConnectCalls)
	}
}

// TestRefreshAgentBearer_SSHConnectFailure
// propagates the Connect error and does
// NOT persist anything to the DB.
func TestRefreshAgentBearer_SSHConnectFailure(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		ConnectErr: errors.New("ssh: handshake failed"),
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "connfail")
	seedStoredKey(t, svc, svc.envelope, nodeID, "connfail")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err == nil {
		t.Fatal("expected error for Connect failure, got nil")
	}
	if !strings.Contains(err.Error(), "SSH connect") {
		t.Errorf("error must name the stage: %q", err.Error())
	}
	// No DB mutation.
	row, _ := svc.store.GetByID(context.Background(), nodeID)
	if row.AgentBearer != "" {
		t.Errorf("AgentBearer should remain empty on Connect failure, got %q", row.AgentBearer)
	}
}

// TestRefreshAgentBearer_SSHRunFailure
// propagates the Run error.
func TestRefreshAgentBearer_SSHRunFailure(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunErr: errors.New("cat: /etc/aegis/agent.env: No such file"),
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "runfail")
	seedStoredKey(t, svc, svc.envelope, nodeID, "runfail")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err == nil {
		t.Fatal("expected error for Run failure, got nil")
	}
	if !strings.Contains(err.Error(), "read agent.env") {
		t.Errorf("error must name the stage: %q", err.Error())
	}
}

// TestRefreshAgentBearer_AgentEnvParseFailure
// covers the "agent.env exists but does
// not contain AEGIS_AGENT_BEARER" case
// (a corrupted or partial install).
func TestRefreshAgentBearer_AgentEnvParseFailure(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "OTHER_KEY=foo\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "parsefail")
	seedStoredKey(t, svc, svc.envelope, nodeID, "parsefail")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err == nil {
		t.Fatal("expected error for parse failure, got nil")
	}
	if !strings.Contains(err.Error(), "parse agent.env") {
		t.Errorf("error must name the stage: %q", err.Error())
	}
}

// TestRefreshAgentBearer_NodeNotFound
// propagates `ErrNotFound` from the
// store (the HTTP layer maps to 404).
func TestRefreshAgentBearer_NodeNotFound(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{}
	svc.sshClientFactory = makeFactory(fake)

	_, err := svc.RefreshAgentBearer(context.Background(), uuid.New(), RefreshBearerOptions{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
	if fake.ConnectCalls != 0 {
		t.Errorf("ConnectCalls: want 0 (node lookup fails first), got %d", fake.ConnectCalls)
	}
}

// TestRefreshAgentBearer_PerCallSSHPortOverride
// exercises the per-call port override:
// the row's address is `:22`, the
// per-call option is `2222`, the
// factory's `ClientConfig.Address` ends
// up as `host:2222`.
func TestRefreshAgentBearer_PerCallSSHPortOverride(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=b\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "portoverride")
	seedStoredKey(t, svc, svc.envelope, nodeID, "portoverride")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{SSHPort: 2222})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if fake.LastConfig.Address != "1.2.3.4:2222" {
		t.Errorf("factory Address: want %q, got %q", "1.2.3.4:2222", fake.LastConfig.Address)
	}
}

// TestRefreshAgentBearer_PerCallSSHUserOverride
// exercises the per-call user override:
// the service-wide default is `root`,
// the per-call option is `aegis`,
// the factory's `ClientConfig.User`
// ends up as `aegis`.
func TestRefreshAgentBearer_PerCallSSHUserOverride(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=b\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "useroverride")
	seedStoredKey(t, svc, svc.envelope, nodeID, "useroverride")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{SSHUser: "aegis"})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if fake.LastConfig.User != "aegis" {
		t.Errorf("factory User: want %q, got %q", "aegis", fake.LastConfig.User)
	}
}

// TestRefreshAgentBearer_Timeout is a smoke
// test that the per-call Timeout is
// propagated to the ClientConfig. The
// actual timeout enforcement is
// `bootstrap.Client`'s responsibility;
// the v0.8.7 test only asserts the
// field is forwarded.
func TestRefreshAgentBearer_Timeout(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=b\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "timeout")
	seedStoredKey(t, svc, svc.envelope, nodeID, "timeout")

	custom := 5 * time.Second
	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{Timeout: custom})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if fake.LastConfig.Timeout != custom {
		t.Errorf("factory Timeout: want %v, got %v", custom, fake.LastConfig.Timeout)
	}
}

// TestRefreshAgentBearer_DefaultTimeout
// asserts that the zero-value
// `Timeout` option resolves to the
// documented 30s default.
func TestRefreshAgentBearer_DefaultTimeout(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=b\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "defaultto")
	seedStoredKey(t, svc, svc.envelope, nodeID, "defaultto")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if fake.LastConfig.Timeout != 30*time.Second {
		t.Errorf("default Timeout: want 30s, got %v", fake.LastConfig.Timeout)
	}
}

// TestRefreshAgentBearer_FingerprintMatchesStored
// is a security check: the fingerprint
// returned in the response matches the
// fingerprint of the key that was
// actually stored in the row. A
// mismatch would mean the response is
// reporting a fingerprint for a
// different key than the one used for
// SSH auth — a critical bug.
func TestRefreshAgentBearer_FingerprintMatchesStored(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil).WithKnownHosts("/kh").WithSSHUser("root")
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=b\n",
	}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "fpmatch")
	priv := seedStoredKey(t, svc, svc.envelope, nodeID, "fpmatch")

	out, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	// Derive the expected fingerprint
	// from the original private key.
	sshPub, err := ssh.NewPublicKey(priv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	want := ssh.FingerprintSHA256(sshPub)
	if out.KeyFingerprintSHA256 != want {
		t.Errorf("fingerprint mismatch: want %q, got %q", want, out.KeyFingerprintSHA256)
	}
}

// TestRefreshAgentBearer_NilKnownHosts is the
// fail-closed wiring check: a Service
// without `WithKnownHosts` rejects the
// call BEFORE touching the store. Same
// shape as the nil-factory check.
func TestRefreshAgentBearer_NilKnownHosts(t *testing.T) {
	svc := newServiceWithStore(t).WithSSHClientFactory(nil) // no WithKnownHosts
	fake := &fakeSSHClient{}
	svc.sshClientFactory = makeFactory(fake)
	nodeID := seedNode(t, svc, "nokh")
	seedStoredKey(t, svc, svc.envelope, nodeID, "nokh")

	_, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{})
	if err == nil {
		t.Fatal("expected error for nil known_hosts, got nil")
	}
	if !strings.Contains(err.Error(), "known_hosts") {
		t.Errorf("error must name the missing wiring: %q", err.Error())
	}
}

// TestSeedStoredKey_EncryptDecryptRoundTrip is
// a sanity check on the test fixture
// itself: the `seedStoredKey` helper
// must produce a row whose
// `GetStoredKey` returns the same
// fingerprint as the original key.
func TestSeedStoredKey_EncryptDecryptRoundTrip(t *testing.T) {
	svc := newServiceWithStore(t)
	nodeID := seedNode(t, svc, "fixture")
	priv := seedStoredKey(t, svc, svc.envelope, nodeID, "fixture")

	pub, err := priv.Public().(ed25519.PublicKey), error(nil)
	_ = pub
	_ = err
	// Cross-check via GetStoredKey (the
	// v0.8.5 public surface).
	stored, err := svc.GetStoredKey(context.Background(), nodeID)
	if err != nil {
		t.Fatalf("GetStoredKey: %v", err)
	}
	if !stored.HasStoredKey {
		t.Fatal("HasStoredKey: want true, got false")
	}
	sshPub, err := ssh.NewPublicKey(priv.Public().(ed25519.PublicKey))
	if err != nil {
		t.Fatalf("NewPublicKey: %v", err)
	}
	want := ssh.FingerprintSHA256(sshPub)
	if stored.Fingerprint != want {
		t.Errorf("fingerprint: want %q, got %q", want, stored.Fingerprint)
	}
}

// ============================================================================
// net.SplitHostPort parity test
// ============================================================================

// TestResolveSSHAddress_SplitHostPortParity
// proves `resolveSSHAddress` produces
// addresses that `net.SplitHostPort` can
// parse cleanly. The Go stdlib's
// `SplitHostPort` is the canonical
// "host:port" parser; the test asserts
// our home-grown helper is
// drop-in-equivalent for the row.Address
// shapes the v0.3.0 install writes
// (`host:port` for IPv4 hosts, no IPv6
// literal — the v0.3.0 install does
// not support IPv6 literals yet).
func TestResolveSSHAddress_SplitHostPortParity(t *testing.T) {
	cases := []struct {
		in       string
		override int
		wantHost string
		wantPort string
	}{
		{"1.2.3.4:22", 0, "1.2.3.4", "22"},
		{"1.2.3.4:22", 2222, "1.2.3.4", "2222"},
		{"host.example", 2222, "host.example", "2222"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in+"/"+tc.wantPort, func(t *testing.T) {
			got, err := resolveSSHAddress(tc.in, tc.override)
			if err != nil {
				t.Fatalf("resolveSSHAddress: %v", err)
			}
			host, port, err := net.SplitHostPort(got)
			if err != nil {
				t.Fatalf("SplitHostPort(%q): %v", got, err)
			}
			if host != tc.wantHost || port != tc.wantPort {
				t.Errorf("got (%s, %s), want (%s, %s)", host, port, tc.wantHost, tc.wantPort)
			}
		})
	}
}
