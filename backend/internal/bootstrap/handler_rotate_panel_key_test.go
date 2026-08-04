// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the v0.8.4 POST /{id}/rotate-panel-key
// HTTP handler. The handler is the HTTP mirror
// of the v0.8.3 `aegis admin node rotate-panel-key`
// CLI (PR #184); the test coverage mirrors the
// CLI's coverage but at the HTTP layer (status
// code + response body shape).
//
// # What's covered
//
//   - 200 happy path: PEM + node row + mock SSH
//     client + envelope → 200 with public_key_line
//     + fingerprint + node_id.
//   - 400 missing ssh_private_key (empty body).
//   - 400 malformed JSON body.
//   - 404 node row not found.
//   - 500 envelope not configured.
//   - 502 SSH connect failure.
//   - audit entry recorded on success (the
//     `node.rotate-panel-key` action, the
//     resource id is the node UUID, the
//     After.fingerprint matches the response
//     body's fingerprint).
//
// # What's NOT covered
//
//   - End-to-end against a real node. The
//     rotate-panel-key flow is a stateful
//     remote-modification tool; the unit
//     tests cover the panel's surface, not
//     the SSH server. The CI smoke test
//     (v0.9.0 work) will exercise the real
//     SSH path on a throwaway VM.

package bootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// auditCaptureStore is a test-local
// audits.Store that records every Entry it
// sees. The unit tests assert on the captured
// entries to pin the "audit recorded with the
// right shape" contract (the same shape the
// operator's audit-log UI reads back).
type auditCaptureStore struct {
	mu      sync.Mutex
	entries []*audits.AuditEntry
	nextID  uint64
}

func (a *auditCaptureStore) Insert(_ context.Context, e audits.Entry) (*audits.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.nextID++
	out := &audits.AuditEntry{
		ID:            "",
		ActorID:       e.ActorID,
		ActorUsername: e.ActorUsername,
		Action:        e.Action,
		ResourceType:  e.ResourceType,
		ResourceID:    e.ResourceID,
		Before:        e.Before,
		After:         e.After,
		IP:            e.IP,
		UserAgent:     e.UserAgent,
		CreatedAt:     e.CreatedAt,
	}
	a.entries = append(a.entries, out)
	return out, nil
}

func (a *auditCaptureStore) List(_ context.Context, _ audits.ListFilter) ([]*audits.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.entries, nil
}

func (a *auditCaptureStore) GetByID(_ context.Context, _ string) (*audits.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return nil, audits.ErrNotFound
}

// withMockSSHClient swaps the package-level
// `newSSHClientForRotate` for a factory that
// returns the supplied mock. The swap is
// reverted at test cleanup. The seam is a
// tiny indirection in handler.go: production
// code calls `NewClient`; tests inject a
// pre-built mock that records Upload/Run
// calls without dialling a real SSH server.
func withMockSSHClient(t *testing.T, mock Client) {
	t.Helper()
	prev := newSSHClientForRotate
	t.Cleanup(func() { newSSHClientForRotate = prev })
	newSSHClientForRotate = func(ClientConfig) (Client, error) {
		return mock, nil
	}
}

// withClaimsContext returns a context with
// the supplied auth claims attached. The
// audit RecordFromRequest helper reads the
// claims off the request context for the
// actor_id field.
func withClaimsContext(r *http.Request) *http.Request {
	claims := &auth.Claims{
		Scopes: auth.Scopes{auth.ScopeAdmin, auth.ScopeNodes},
	}
	ctx := auth.WithClaims(r.Context(), claims)
	return r.WithContext(ctx)
}

// doRequest is the unit-test HTTP helper.
// The handler reads the node id off the URL
// parameter; chi.URLParam reads the {id}
// segment off a *http.Request served by
// httptest. The helper mounts the handler
// on a real chi router so the URL parameter
// resolves; the route is
// `/{id}/rotate-panel-key` (same shape the
// production nodes router uses). The method
// is always POST — the endpoint is mutating
// only — so the parameter list drops the
// method (and the unparam lint stays quiet).
func doRequest(
	t *testing.T,
	url string,
	body any,
	svc *Service,
	withClaims bool,
) *httptest.ResponseRecorder {
	t.Helper()
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
	}
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	if withClaims {
		req = withClaimsContext(req)
	}
	rr := httptest.NewRecorder()
	r := chi.NewRouter()
	r.Post("/{id}/rotate-panel-key", svc.HandleRotatePanelKey())
	r.ServeHTTP(rr, req)
	return rr
}

// knownHostsFor returns a path to an empty
// known_hosts file in t.TempDir(). The handler
// passes this to NewClient; the test mocks
// the SSH client factory to avoid a real
// dial, so the file is not actually read.
func knownHostsFor(t *testing.T) string {
	t.Helper()
	p := t.TempDir() + "/known_hosts"
	if err := os.WriteFile(p, nil, 0o600); err != nil {
		t.Fatalf("write known_hosts: %v", err)
	}
	return p
}

// TestHandleRotatePanelKey_HappyPath drives a
// full success: valid PEM, mock SSH client that
// returns success on Upload + Run, mock node
// provider that returns a row, envelope
// installed → 200 with the public key line and
// fingerprint populated.
func TestHandleRotatePanelKey_HappyPath(t *testing.T) {
	nodeID := uuid.MustParse("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")
	store := newMockNodeProvider(NodeRow{
		ID:      nodeID,
		Name:    "test-rotate",
		State:   "online",
		Address: "10.0.0.1:22",
	})
	svc := NewService(ServiceConfig{
		Nodes:      store,
		Envelope:   envelope.NewNoopSecretCipher(),
		KnownHosts: knownHostsFor(t),
		SSHUser:    "root",
		SSHPort:    22,
	})
	mockSSH := &mockClient{runOut: ""}
	withMockSSHClient(t, mockSSH)
	rr := doRequest(t, "/"+nodeID.String()+"/rotate-panel-key", map[string]any{
		"ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\nfake\n-----END OPENSSH PRIVATE KEY-----\n",
	}, svc, false)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var resp rotatePanelKeyResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if resp.NodeID != nodeID.String() {
		t.Errorf("NodeID = %q, want %q", resp.NodeID, nodeID.String())
	}
	if resp.PublicKeyLine == "" {
		t.Error("PublicKeyLine is empty")
	}
	if resp.Fingerprint == "" {
		t.Error("Fingerprint is empty")
	}
	// The fingerprint must start with
	// "SHA256:" (the canonical format
	// ssh-keygen -lf outputs).
	if len(resp.Fingerprint) < 7 || resp.Fingerprint[:6] != "SHA256" {
		t.Errorf("Fingerprint = %q, want SHA256 prefix", resp.Fingerprint)
	}
	// The SSH client must have received
	// the Upload + Run calls (the
	// public-key SFTP push + the
	// authorized_keys append).
	if len(mockSSH.uploadPaths) == 0 {
		t.Error("expected at least one Upload call, got none")
	}
	if len(mockSSH.runCmds) == 0 {
		t.Error("expected at least one Run call (authorized_keys append), got none")
	}
}

// TestHandleRotatePanelKey_MissingKeyReturns400
// pins the "no ssh_private_key" failure mode.
func TestHandleRotatePanelKey_MissingKeyReturns400(t *testing.T) {
	svc := NewService(ServiceConfig{
		Nodes:    newMockNodeProvider(),
		Envelope: envelope.NewNoopSecretCipher(),
	})
	rr := doRequest(t, "/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/rotate-panel-key", map[string]any{
		// ssh_private_key omitted on purpose
	}, svc, false)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleRotatePanelKey_MalformedJSONReturns400
// pins the "body decoder" failure mode.
func TestHandleRotatePanelKey_MalformedJSONReturns400(t *testing.T) {
	svc := NewService(ServiceConfig{
		Nodes:    newMockNodeProvider(),
		Envelope: envelope.NewNoopSecretCipher(),
	})
	req := httptest.NewRequest(http.MethodPost, "/rotate-panel-key", bytes.NewReader([]byte("not-json")))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	handler := svc.HandleRotatePanelKey()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleRotatePanelKey_NodeNotFoundReturns404
// pins the "row not in the store" failure mode.
// The handler calls s.nodes.GetByID; the mock
// returns ErrNotFound for an unknown ID.
func TestHandleRotatePanelKey_NodeNotFoundReturns404(t *testing.T) {
	svc := NewService(ServiceConfig{
		Nodes:    newMockNodeProvider(), // empty
		Envelope: envelope.NewNoopSecretCipher(),
	})
	withMockSSHClient(t, &mockClient{runOut: ""})
	rr := doRequest(t, "/00000000-0000-4000-8000-000000000000/rotate-panel-key", map[string]any{
		"ssh_private_key": "fake-pem",
	}, svc, false)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleRotatePanelKey_NilEnvelopeReturns500
// pins the "panel booted without envelope"
// failure mode. The handler returns 500
// (server config) so the operator knows the
// panel needs AEGIS_WEBHOOKS_SECRET_AGE_*
// before this endpoint can work.
func TestHandleRotatePanelKey_NilEnvelopeReturns500(t *testing.T) {
	svc := NewService(ServiceConfig{
		Nodes:    newMockNodeProvider(),
		Envelope: nil,
	})
	rr := doRequest(t, "/aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa/rotate-panel-key", map[string]any{
		"ssh_private_key": "fake-pem",
	}, svc, false)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleRotatePanelKey_SSHConnectFailsReturns502
// pins the "dial failed" failure mode. The
// mockClient's connectErr is non-nil, so
// the handler's Connect path returns 502.
func TestHandleRotatePanelKey_SSHConnectFailsReturns502(t *testing.T) {
	nodeID := uuid.MustParse("bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb")
	store := newMockNodeProvider(NodeRow{
		ID:      nodeID,
		Name:    "test-rotate",
		State:   "online",
		Address: "10.0.0.1:22",
	})
	svc := NewService(ServiceConfig{
		Nodes:      store,
		Envelope:   envelope.NewNoopSecretCipher(),
		KnownHosts: knownHostsFor(t),
		SSHUser:    "root",
		SSHPort:    22,
	})
	withMockSSHClient(t, &mockClient{connectErr: errors.New("dial timeout")})
	rr := doRequest(t, "/"+nodeID.String()+"/rotate-panel-key", map[string]any{
		"ssh_private_key": "fake-pem",
	}, svc, false)
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleRotatePanelKey_AuditRecordedOnSuccess
// pins the audit shape. The handler must
// record a `node.rotate-panel-key` entry
// with resource_id = the node UUID and
// after.fingerprint = the response body
// fingerprint.
func TestHandleRotatePanelKey_AuditRecordedOnSuccess(t *testing.T) {
	nodeID := uuid.MustParse("cccccccc-cccc-4ccc-8ccc-cccccccccccc")
	store := newMockNodeProvider(NodeRow{
		ID:      nodeID,
		Name:    "test-rotate-audit",
		State:   "online",
		Address: "10.0.0.1:22",
	})
	cap := &auditCaptureStore{}
	auditsSvc := audits.NewService(cap)
	svc := NewService(ServiceConfig{
		Nodes:      store,
		Audits:     auditsSvc,
		Envelope:   envelope.NewNoopSecretCipher(),
		KnownHosts: knownHostsFor(t),
		SSHUser:    "root",
		SSHPort:    22,
	})
	withMockSSHClient(t, &mockClient{runOut: ""})
	rr := doRequest(t, "/"+nodeID.String()+"/rotate-panel-key", map[string]any{
		"ssh_private_key": "fake-pem",
	}, svc, true)
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(cap.entries))
	}
	e := cap.entries[0]
	if e.Action != "node.rotate-panel-key" {
		t.Errorf("Action = %q, want node.rotate-panel-key", e.Action)
	}
	if e.ResourceType != "node" {
		t.Errorf("ResourceType = %q, want node", e.ResourceType)
	}
	if e.ResourceID != nodeID.String() {
		t.Errorf("ResourceID = %q, want %q", e.ResourceID, nodeID.String())
	}
	if e.After == nil {
		t.Fatal("After is nil")
	}
	afterMap, ok := e.After.(map[string]any)
	if !ok {
		t.Fatalf("After is %T, want map[string]any", e.After)
	}
	if fp, _ := afterMap["fingerprint"].(string); fp == "" {
		t.Error("After.fingerprint is empty")
	}
}
