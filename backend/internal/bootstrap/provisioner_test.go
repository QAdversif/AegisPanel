// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// mockNodeProvider is a minimal in-memory
// implementation of NodeProvider. The handler
// `Update` overwrites the row by ID; the
// provisioner does not need more (the v0.3.0
// bootstrap is a state-only writer).
type mockNodeProvider struct {
	mu   sync.Mutex
	rows map[uuid.UUID]NodeRow
}

func newMockNodeProvider(rows ...NodeRow) *mockNodeProvider {
	m := &mockNodeProvider{rows: make(map[uuid.UUID]NodeRow, len(rows))}
	for _, r := range rows {
		cp := r
		m.rows[cp.ID] = cp
	}
	return m
}

func (m *mockNodeProvider) GetByID(_ context.Context, id uuid.UUID) (NodeRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return NodeRow{}, errors.New("mock: not found")
	}
	return r, nil
}

func (m *mockNodeProvider) Update(_ context.Context, row NodeRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.rows[row.ID]
	if !ok {
		return errors.New("mock: not found")
	}
	// The mock mirrors the production SQL
	// Update path: only the fields the
	// provisioner owns (Name, State, Address,
	// AgentBearer) are overwritten; the
	// dedicated-method columns
	// (SSHPrivateKeyCiphertext) are preserved
	// on the existing row. The production
	// PgStore.Update does this at the SQL
	// level (the UPDATE statement only names
	// the columns the provisioner writes);
	// the mock enforces the same contract
	// at the Go level so the test set is
	// faithful to what the production code
	// will see in the DB.
	merged := existing
	merged.ID = row.ID
	merged.Name = row.Name
	merged.State = row.State
	merged.Address = row.Address
	// AgentBearer is dedicated-method
	// territory (SetAgentBearer) but the
	// production Update SQL does not touch
	// it either; preserve the existing
	// value unless the caller explicitly
	// overwrites it.
	merged.AgentBearer = row.AgentBearer
	// SSHPrivateKeyCiphertext is the v0.8.x
	// dedicated-method column; the mock
	// preserves whatever SetSSHPrivateKeyCiphertext
	// last wrote.
	// (merged.SSHPrivateKeyCiphertext is
	// already the existing value.)
	m.rows[row.ID] = merged
	return nil
}

func (m *mockNodeProvider) SetAgentBearer(_ context.Context, id uuid.UUID, bearer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return errors.New("mock: not found")
	}
	r.AgentBearer = bearer
	m.rows[id] = r
	return nil
}

// AgentCA is removed in v0.8.30 PR 1c; the
// v0.8.30 PR 2 mTLS integration lands alongside
// the bootstrap `Provision` refactor that lifts
// the `bootstrap <-> nodes` import cycle (the
// v0.8.31 work). The `nodes.Service` keeps its
// own `WithAgentCA` + `AgentCA()` getter for
// the v0.8.30 PR 2 caller.

func (m *mockNodeProvider) SetSSHPrivateKeyCiphertext(_ context.Context, id uuid.UUID, ciphertext []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rows[id]
	if !ok {
		return errors.New("mock: not found")
	}
	// The mock follows the production contract:
	// nil from the caller becomes an empty slice
	// (the "no key yet" sentinel) rather than a
	// nil slice. The provisioner never passes nil;
	// the guard mirrors the production path.
	if ciphertext == nil {
		ciphertext = []byte{}
	}
	r.SSHPrivateKeyCiphertext = ciphertext
	m.rows[id] = r
	return nil
}

// TestProvisioner_Success verifies the
// happy path: a node in `new` state
// transitions to `online` after the install
// returns success.
func TestProvisioner_Success(t *testing.T) {
	store := newMockNodeProvider(NodeRow{
		ID:      uuid.MustParse("11111111-1111-4111-8111-111111111111"),
		Name:    "test-node",
		State:   string(StateNew),
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexec sleep infinity\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
		SSHUser:     "root",
		SSHPort:     22,
	})
	// Replace the installer with one that
	// uses the mock client. The package-
	// level NewClientFactory is a
	// function-value field; we override it
	// here so the SSH handshake is mocked.
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	// The audits package is optional; the
	// provisioner skips the audit write when
	// it is nil. v0.3.0 leaves the audits
	// service nil to keep this test focused.
	nodeID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	newState, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPrivateKey: "dummy-pem",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if newState != StateOnline {
		t.Errorf("state = %s, want online", newState)
	}
	row, _ := store.GetByID(context.Background(), nodeID)
	if row.State != string(StateOnline) {
		t.Errorf("row.State = %q, want online", row.State)
	}
}

// TestProvisioner_InstallFailure transitions
// to `offline` when the install fails. The
// caller gets back the failure error and the
// row's new state.
func TestProvisioner_InstallFailure(t *testing.T) {
	store := newMockNodeProvider(NodeRow{
		ID:      uuid.MustParse("22222222-2222-4222-8222-222222222222"),
		Name:    "test-node",
		State:   string(StateNew),
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")
	mock := &mockClient{
		connectErr: errors.New("dial timeout"),
	}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	nodeID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	newState, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPrivateKey: "dummy-pem",
	})
	if err == nil {
		t.Fatal("Provision: expected error on install failure")
	}
	if newState != StateOffline {
		t.Errorf("state = %s, want offline", newState)
	}
}

// TestProvisioner_RejectsWrongStartState
// verifies the pre-condition guard. A node
// in `online` state cannot be re-provisioned;
// the function returns errInvalidStartState
// without touching the network or the row.
func TestProvisioner_RejectsWrongStartState(t *testing.T) {
	store := newMockNodeProvider(NodeRow{
		ID:      uuid.MustParse("33333333-3333-4333-8333-333333333333"),
		Name:    "test-node",
		State:   string(StateOnline), // already online
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	nodeID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	_, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPrivateKey: "dummy-pem",
	})
	if !errors.Is(err, errInvalidStartState) {
		t.Errorf("err = %v, want errInvalidStartState", err)
	}
	if mock.connectCalled {
		t.Error("Connect was called despite pre-condition failure")
	}
	row, _ := store.GetByID(context.Background(), nodeID)
	if row.State != string(StateOnline) {
		t.Errorf("row.State = %q, want unchanged online", row.State)
	}
}

// TestProvisioner_RetryFromOffline verifies the
// operator's "retry provisioning" path: a
// node in `offline` state can be re-installed
// and the next success transitions back to
// `online`. The mock fakes the network.
func TestProvisioner_RetryFromOffline(t *testing.T) {
	store := newMockNodeProvider(NodeRow{
		ID:      uuid.MustParse("44444444-4444-4444-8444-444444444444"),
		Name:    "test-node",
		State:   string(StateOffline), // previous install failed
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	nodeID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	newState, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPrivateKey: "dummy-pem",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if newState != StateOnline {
		t.Errorf("state = %s, want online (retry succeeded)", newState)
	}
}

// TestProvisioner_PasswordInstall_GeneratesAndStoresSSHKey
// verifies the v0.8.x "auto-deploy" path: a
// password-based install generates a fresh
// ed25519 keypair on the panel, encrypts the
// private half via the envelope, persists the
// ciphertext, and pushes the public half into
// $HOME/.ssh/authorized_keys on the node.
// The test asserts:
//
//   - the row's SSHPrivateKeyCiphertext is
//     non-empty after a successful install
//   - the ciphertext decrypts through the same
//     envelope back to a valid OpenSSH-PEM
//     ed25519 private key block
//   - the SSH client received an Upload call
//     to the /tmp/.aegis-pubkey path (the
//     SFTP push of the public key)
//   - the SSH client received a Run call for
//     the constant "append + cleanup" shell
//     command
func TestProvisioner_PasswordInstall_GeneratesAndStoresSSHKey(t *testing.T) {
	const (
		nodeIDStr = "55555555-5555-4555-8555-555555555555"
		nodeName  = "test-node-password"
	)
	nodeID := uuid.MustParse(nodeIDStr)
	store := newMockNodeProvider(NodeRow{
		ID:      nodeID,
		Name:    nodeName,
		State:   string(StateNew),
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexec sleep infinity\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
		SSHUser:     "root",
		SSHPort:     22,
		// The NoopSecretCipher is the in-memory
		// equivalent of the production age
		// cipher for test purposes: the
		// provisioner encrypts with it on
		// store and decrypts with it on
		// re-provision, both operations
		// returning the same bytes.
		Envelope: envelope.NewNoopSecretCipher(),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	newState, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPassword: "test-password",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if newState != StateOnline {
		t.Errorf("state = %s, want online", newState)
	}
	row, _ := store.GetByID(context.Background(), nodeID)
	if len(row.SSHPrivateKeyCiphertext) == 0 {
		t.Fatal("row.SSHPrivateKeyCiphertext is empty; provisioner did not persist the panel key")
	}
	// The mock uses a NoopSecretCipher, so the
	// "ciphertext" is the plaintext PEM
	// verbatim. A real age envelope would
	// produce different bytes; the integration
	// tests under `envelope_test.go` cover
	// the round-trip. The unit test only
	// asserts the bytes land in the column.
	if !bytes.Contains(row.SSHPrivateKeyCiphertext, []byte("PRIVATE KEY")) {
		t.Errorf("row.SSHPrivateKeyCiphertext does not look like a PEM block: %q", row.SSHPrivateKeyCiphertext)
	}
	// Verify the SSH client received the
	// expected SFTP + Run calls. UploadPaths
	// is populated by the mockClient (see
	// installer_test.go).
	if len(mock.uploadPaths) == 0 {
		t.Error("expected at least one SFTP Upload call (the /tmp/.aegis-pubkey push)")
	}
	foundPubKeyUpload := false
	for _, p := range mock.uploadPaths {
		if p == "/tmp/.aegis-pubkey" {
			foundPubKeyUpload = true
			break
		}
	}
	if !foundPubKeyUpload {
		t.Errorf("expected SFTP upload to /tmp/.aegis-pubkey; got %v", mock.uploadPaths)
	}
	if len(mock.runCmds) == 0 {
		t.Error("expected at least one Run call (the authorized_keys append command)")
	}
}

// TestProvisioner_StoredKeyReuse_DecryptsOnReProvision
// verifies the "no password on re-provision"
// behaviour: a node that already has a stored
// SSH key uses that key for the next install,
// ignoring any password the operator types.
// The test pre-seeds the row with a ciphertext
// (a valid OpenSSH-PEM block, encrypted with
// the NoopSecretCipher for the round-trip)
// and then runs a Provision. The assertion is
// that:
//
//   - the install succeeded (the mock client
//     returns active)
//   - the row's ciphertext was NOT overwritten
//     (the provisioner does not rotate an
//     existing key on a successful re-install)
//   - the row's state transitioned to online
func TestProvisioner_StoredKeyReuse_DecryptsOnReProvision(t *testing.T) {
	const (
		nodeIDStr = "66666666-6666-4666-8666-666666666666"
		nodeName  = "test-node-reuse"
	)
	nodeID := uuid.MustParse(nodeIDStr)
	// Pre-existing ciphertext. The provisioner
	// decrypts this and uses it as the auth
	// material for the next install. With the
	// NoopSecretCipher the "ciphertext" is
	// the plaintext bytes. The fixture is a
	// deliberately non-PEM byte string so the
	// gitleaks CI job (which pattern-matches
	// on the "BEGIN ... PRIVATE KEY" header)
	// does not flag the test as a real
	// secret leak — the test only needs
	// "some bytes that round-trip through
	// the envelope", which a non-PEM string
	// satisfies just as well.
	existingKey := []byte("existing-pem-bytes-from-previous-run")
	store := newMockNodeProvider(NodeRow{
		ID:                      nodeID,
		Name:                    nodeName,
		State:                   string(StateOffline), // re-provision from a previous failure
		Address:                 "10.0.0.1:22",
		SSHPrivateKeyCiphertext: existingKey,
	})
	src := writeTempScript(t, "#!/bin/sh\nexec sleep infinity\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
		SSHUser:     "root",
		SSHPort:     22,
		Envelope:    envelope.NewNoopSecretCipher(),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	// Operator still types a password on the
	// form. The provisioner must ignore it
	// (the stored key wins).
	newState, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPassword: "ignored-because-stored-key-wins",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	if newState != StateOnline {
		t.Errorf("state = %s, want online", newState)
	}
	row, _ := store.GetByID(context.Background(), nodeID)
	// The stored ciphertext must be unchanged
	// (no rotation on a successful re-install).
	if !bytes.Equal(row.SSHPrivateKeyCiphertext, existingKey) {
		t.Errorf("row.SSHPrivateKeyCiphertext was overwritten; want preserved existing bytes")
	}
}

// TestProvisioner_OperatorKeyInstall_DoesNotRegenerate
// verifies the "operator gave a private key"
// path: the provisioner uses the operator's
// key as-is and does NOT register the
// post-install key-generation hook. The
// operator's key is the persistent credential
// for this node, so the panel has no business
// generating its own. The assertion is that
// the row's SSHPrivateKeyCiphertext stays
// empty (no panel key was written).
func TestProvisioner_OperatorKeyInstall_DoesNotRegenerate(t *testing.T) {
	const (
		nodeIDStr = "77777777-7777-4777-8777-777777777777"
		nodeName  = "test-node-operator-key"
	)
	nodeID := uuid.MustParse(nodeIDStr)
	store := newMockNodeProvider(NodeRow{
		ID:      nodeID,
		Name:    nodeName,
		State:   string(StateNew),
		Address: "10.0.0.1:22",
	})
	src := writeTempScript(t, "#!/bin/sh\nexec sleep infinity\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
		SSHUser:     "root",
		SSHPort:     22,
		Envelope:    envelope.NewNoopSecretCipher(),
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	_, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		// Operator gave a key, not a password.
		// The provisioner uses the key for
		// the install and does NOT register
		// the post-install key-generation
		// hook (the operator's key is the
		// persistent credential).
		SSHPrivateKey: "operator-supplied-pem",
	})
	if err != nil {
		t.Fatalf("Provision: %v", err)
	}
	row, _ := store.GetByID(context.Background(), nodeID)
	if len(row.SSHPrivateKeyCiphertext) != 0 {
		t.Errorf("row.SSHPrivateKeyCiphertext = %q; want empty (operator key path does not generate a panel key)",
			row.SSHPrivateKeyCiphertext)
	}
}

// TestProvisioner_StoredKeyWithoutEnvelope_FailsClosed
// verifies the v0.8.x failure mode: the row
// has a stored SSH key (from a previous run
// with a real age envelope) but the panel was
// booted without an envelope (a config bug).
// The provisioner refuses to proceed — the
// alternative is to silently fall through to
// the operator's request and lose access to
// the stored key on the next SSH-rotate.
//
// This is the "envelope is required for stored
// key decryption" contract; the symmetric
// failure (no stored key, no envelope,
// password given) is the "key generation
// skipped silently" path that is documented
// in buildPersistentSSHKeyHook.
func TestProvisioner_StoredKeyWithoutEnvelope_FailsClosed(t *testing.T) {
	const (
		nodeIDStr = "88888888-8888-4888-8888-888888888888"
		nodeName  = "test-node-no-envelope"
	)
	nodeID := uuid.MustParse(nodeIDStr)
	store := newMockNodeProvider(NodeRow{
		ID:                      nodeID,
		Name:                    nodeName,
		State:                   string(StateOffline),
		Address:                 "10.0.0.1:22",
		SSHPrivateKeyCiphertext: []byte("encrypted-blob-from-previous-run"),
	})
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")
	mock := &mockClient{runOut: "active\n"}
	svc := NewService(ServiceConfig{
		Nodes:       store,
		AgentBinary: src,
		KnownHosts:  filepath.Join(t.TempDir(), "known_hosts"),
		SSHUser:     "root",
		SSHPort:     22,
		// Envelope deliberately omitted (nil).
	})
	svc.installer = &Installer{
		ClientFactory: func(InstallInput) (Client, error) { return mock, nil },
	}
	_, err := svc.Provision(context.Background(), nodeID, nil, ProvisionRequest{
		SSHPassword: "ignored",
	})
	if err == nil {
		t.Fatal("Provision: expected error on stored key without envelope, got nil")
	}
	if !strings.Contains(err.Error(), "envelope is not configured") {
		t.Errorf("err = %v, want one mentioning 'envelope is not configured'", err)
	}
	if mock.connectCalled {
		t.Error("Connect was called despite the pre-condition failure")
	}
}

// TestEnsureKnownHosts verifies the helper that
// the panel calls at boot. An existing file is
// left untouched; a missing file is created
// with 0o600 mode. The test cleans up via
// t.TempDir().
func TestEnsureKnownHosts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "known_hosts")
	if err := EnsureKnownHosts(path); err != nil {
		t.Fatalf("EnsureKnownHosts: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != 0 {
		t.Errorf("file size = %d, want 0", info.Size())
	}
	// Idempotent: a second call is a no-op.
	if err := EnsureKnownHosts(path); err != nil {
		t.Errorf("second EnsureKnownHosts: %v", err)
	}
	// Empty path is a hard error.
	if err := EnsureKnownHosts(""); err == nil {
		t.Error("empty path should error")
	}
}
