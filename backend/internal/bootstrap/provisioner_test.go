// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/google/uuid"
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
	m.rows[row.ID] = row
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
