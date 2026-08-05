// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler tests for the v0.8.7
// `POST /api/v1/nodes/{id}/refresh-agent-bearer`
// endpoint. The pattern mirrors the v0.8.5
// `handleGetStoredKey` tests in
// `stored_key_test.go`: a chi router with the
// handler mounted, an `httptest.NewRecorder`
// for the response, and a fake `audits.Store`
// (the `auditCaptureStore` from
// `stored_key_test.go`) to verify the audit
// row shape.
//
// The tests live in their own file (not
// appended to `refresh_bearer_test.go`) so
// the imports stay minimal: the handler
// tests need `httptest` + `chi` + the audit
// capture store; the Service tests need
// only the in-memory store + envelope. The
// split is the v0.8.5 precedent.

package nodes

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
)

// newRefreshHTTPFixture builds the same
// shape as the v0.8.5 `storedKeyTestFixture`
// helper: a `*Service` with a fake
// `audits.Store` (the `auditCaptureStore`
// from `stored_key_test.go` is reused — the
// test file is in the same package so the
// unexported type is accessible) and a
// `*fakeSSHClient` ready to be swapped into
// the factory.
func newRefreshHTTPFixture(t *testing.T) (*Service, *auditCaptureStore, *fakeSSHClient) {
	t.Helper()
	cap := &auditCaptureStore{}
	auditsSvc := audits.NewService(cap)
	svc := newServiceWithStore(t)
	svc.WithAudits(auditsSvc)
	fake := &fakeSSHClient{
		RunOutput: "AEGIS_AGENT_BEARER=fresh-bearer-abc123\n",
	}
	svc.WithSSHClientFactory(makeFactory(fake))
	svc.WithKnownHosts("/var/known_hosts")
	svc.WithSSHUser("root")
	return svc, cap, fake
}

// serveRefreshRequest fires a POST
// against the supplied chi router. The
// body is optional (empty body = "use
// defaults"). The helper attaches fake
// `auth.Claims` so the audit row has an
// actor id (the v0.8.5 test helper does
// the same; the production
// `auth.ClaimsFromContext` reads off the
// same key).
func serveRefreshRequest(t *testing.T, r http.Handler, url string, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reqBody *strings.Reader
	if body == "" {
		reqBody = strings.NewReader("")
	} else {
		reqBody = strings.NewReader(body)
	}
	httpReq := httptest.NewRequest(http.MethodPost, url, reqBody)
	if body != "" {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	httpReq = httpReq.WithContext(auth.WithClaims(httpReq.Context(), &auth.Claims{
		Scopes: auth.Scopes{auth.ScopeNodes},
	}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, httpReq)
	return rr
}

// errorBody decodes the `{"error":"..."}`
// envelope that `writeError` emits. The
// v0.8.7 error bodies are JSON-escaped
// (the `writeError` helper runs the
// message through `jsonString` to
// HTML-safe-encode control characters);
// comparing the raw body string is a
// footgun. The helper decodes once and
// returns the message verbatim so the
// tests can `strings.Contains` on the
// unescaped form.
func errorBody(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error body: %v (raw = %q)", err, rr.Body.String())
	}
	return env.Error
}

// lastAuditEntry returns the LAST
// `*AuditEntry` in the capture store.
// The seedNode + seedStoredKey fixtures
// each record their own audit rows
// (`node.create` + `node.update`); the
// `node.agent-bearer.refresh` row is
// always the LAST one in the test flow
// (the fixtures run first, the
// RefreshAgentBearer call runs after).
// The helper is the v0.8.7 test-side
// equivalent of the v0.8.5 audit-row
// assertion pattern.
func lastAuditEntry(t *testing.T, cap *auditCaptureStore) *audits.AuditEntry {
	t.Helper()
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.entries) == 0 {
		t.Fatal("no audit entries")
	}
	return cap.entries[len(cap.entries)-1]
}

// TestHandleRefreshAgentBearer_HappyPath is
// the wire-level happy path: 200 status,
// correct body shape (node_id, bearer,
// key_fingerprint), audit row recorded
// with `node.agent-bearer.refresh`.
func TestHandleRefreshAgentBearer_HappyPath(t *testing.T) {
	svc, cap, fake := newRefreshHTTPFixture(t)
	nodeID := seedNode(t, svc, "httphappy")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "httphappy")
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rr.Code, rr.Body.String())
	}
	var resp refreshAgentBearerResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.NodeID != nodeID.String() {
		t.Errorf("node_id: want %q, got %q", nodeID, resp.NodeID)
	}
	if resp.Bearer != "fresh-bearer-abc123" {
		t.Errorf("bearer: want %q, got %q", "fresh-bearer-abc123", resp.Bearer)
	}
	if !strings.HasPrefix(resp.KeyFingerprintSHA256, "SHA256:") {
		t.Errorf("key_fingerprint: want SHA256: prefix, got %q", resp.KeyFingerprintSHA256)
	}
	e := lastAuditEntry(t, cap)
	if e.Action != "node.agent-bearer.refresh" {
		t.Errorf("audit Action: want node.agent-bearer.refresh, got %q", e.Action)
	}
	if e.ResourceID != nodeID.String() {
		t.Errorf("audit ResourceID: want %q, got %q", nodeID, e.ResourceID)
	}
	// The bearer is NOT in the audit row.
	after, ok := e.After.(map[string]any)
	if !ok {
		t.Fatalf("audit After must be map[string]any, got %T", e.After)
	}
	if _, ok := after["bearer"]; ok {
		t.Error("bearer must NOT be in the audit row")
	}
	// The fingerprint IS in the audit row
	// (so the operator can correlate the
	// refresh with a specific key).
	if got := after["key_fingerprint"]; got != resp.KeyFingerprintSHA256 {
		t.Errorf("audit key_fingerprint: want %q, got %v", resp.KeyFingerprintSHA256, got)
	}
	// The SSH lifecycle assertions are
	// the same as the Service tests
	// (Connect + Run + Close each
	// called once).
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

// TestHandleRefreshAgentBearer_NoStoredKey_Returns409
// pins the v0.8.7 "rotate first" hint. The
// status is 409 (Conflict), not 404 —
// the row exists, the issue is "no
// stored key, can't refresh, must
// rotate first". The body carries the
// hint string so the operator UI can
// surface it verbatim.
func TestHandleRefreshAgentBearer_NoStoredKey_Returns409(t *testing.T) {
	svc, cap, fake := newRefreshHTTPFixture(t)
	nodeID := seedNode(t, svc, "httpnokey") // no SetSSHPrivateKeyCiphertext
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusConflict {
		t.Fatalf("status: want 409, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(errorBody(t, rr), "rotate-panel-key first") {
		t.Errorf("body must include the rotate-first hint: %q", rr.Body.String())
	}
	// The SSH client was never
	// instantiated (the no-stored-key
	// check fires before the factory).
	if fake.ConnectCalls != 0 {
		t.Errorf("ConnectCalls: want 0, got %d", fake.ConnectCalls)
	}
	// No audit row on the no-stored-key
	// path: the operation never reached
	// the audit-recording code. The
	// fixtures (seedNode + the
	// `SetSSHPrivateKeyCiphertext`
	// variant of seedStoredKey that
	// this test does NOT call) record
	// their own rows. The assertion
	// is: the LAST entry is NOT
	// `node.agent-bearer.refresh`
	// (because the no-stored-key path
	// exits before the audit hook).
	last := lastAuditEntry(t, cap)
	if last.Action == "node.agent-bearer.refresh" {
		t.Errorf("no-stored-key must not record node.agent-bearer.refresh; got last action %q", last.Action)
	}
}

// TestHandleRefreshAgentBearer_MalformedIDReturns400
// is the URL-level validation. The chi
// router validates the {id} as a UUID via
// `uuid.Parse` inside `parseID`.
func TestHandleRefreshAgentBearer_MalformedIDReturns400(t *testing.T) {
	svc, _, _ := newRefreshHTTPFixture(t)
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	// Note: chi matches the {id} route
	// pattern with a single segment, so
	// `/nodes/not-a-uuid/refresh-agent-bearer`
	// hits the {id} handler with a
	// non-UUID value. The `parseID`
	// helper returns 400.
	rr := serveRefreshRequest(t, r, "/nodes/not-a-uuid/refresh-agent-bearer", "")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rr.Code)
	}
}

// TestHandleRefreshAgentBearer_NodeNotFoundReturns404
// pins the 404 for a non-existent node.
func TestHandleRefreshAgentBearer_NodeNotFoundReturns404(t *testing.T) {
	svc, _, _ := newRefreshHTTPFixture(t)
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+uuid.New().String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status: want 404, got %d", rr.Code)
	}
}

// TestHandleRefreshAgentBearer_NilEnvelopeReturns500
// pins the 500 for the "panel was booted
// without the age envelope" case. The
// `parseID` is reached first; the
// envelope check fires inside the
// Service. The HTTP layer maps the
// "envelope is not configured" error
// string to 500.
func TestHandleRefreshAgentBearer_NilEnvelopeReturns500(t *testing.T) {
	cap := &auditCaptureStore{}
	svc := NewService(NewMemoryStore()) // no WithEnvelope
	svc.WithAudits(audits.NewService(cap))
	fake := &fakeSSHClient{}
	svc.WithSSHClientFactory(makeFactory(fake))
	svc.WithKnownHosts("/kh")
	svc.WithSSHUser("root")
	nodeID := seedNode(t, svc, "noenvelope")
	_ = seedStoredKey(t, svc, mustMakeEnvelope(t), nodeID, "noenvelope")
	// Note: the seedStoredKey call
	// above used a fresh envelope that
	// is NOT wired into the Service. The
	// resulting row's
	// `ssh_private_key_ciphertext` is
	// non-empty but encrypted with a
	// key the Service can't read. The
	// `RefreshAgentBearer` flow will
	// fail at the envelope check
	// BEFORE the decrypt attempt, so
	// the row content is irrelevant.
	// We seed with a random envelope to
	// ensure the row is non-empty
	// (otherwise the no-stored-key
	// check fires first and the test
	// would assert 409, not 500).
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(errorBody(t, rr), "envelope is not configured") {
		t.Errorf("body must name the missing wiring: %q", rr.Body.String())
	}
}

// TestHandleRefreshAgentBearer_SSHConnectFailure_Returns502
// pins the 502 for the "remote node
// rejected the SSH handshake" case. The
// status is 502, not 500 — same as the
// v0.8.4 rotate-panel-key handler.
func TestHandleRefreshAgentBearer_SSHConnectFailure_Returns502(t *testing.T) {
	svc, _, _ := newRefreshHTTPFixture(t)
	// Override the fake with one that
	// fails on Connect.
	connectFail := &fakeSSHClient{
		ConnectErr: errors.New("ssh: handshake failed"),
	}
	svc.sshClientFactory = makeFactory(connectFail)
	nodeID := seedNode(t, svc, "httpconnfail")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "httpconnfail")
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(errorBody(t, rr), "SSH connect") {
		t.Errorf("body must name the failing stage: %q", rr.Body.String())
	}
}

// TestHandleRefreshAgentBearer_AgentEnvParseFailure_Returns502
// pins the 502 for the "agent.env exists
// but does not contain AEGIS_AGENT_BEARER"
// case. Same status as the SSH failure
// (the remote node responded, but the
// response is malformed).
func TestHandleRefreshAgentBearer_AgentEnvParseFailure_Returns502(t *testing.T) {
	svc, _, _ := newRefreshHTTPFixture(t)
	badEnv := &fakeSSHClient{
		RunOutput: "OTHER_KEY=foo\n",
	}
	svc.sshClientFactory = makeFactory(badEnv)
	nodeID := seedNode(t, svc, "httpparsefail")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "httpparsefail")
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status: want 502, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(errorBody(t, rr), "parse agent.env") {
		t.Errorf("body must name the failing stage: %q", rr.Body.String())
	}
}

// TestHandleRefreshAgentBearer_BodyOverride
// exercises the per-call `ssh_port` +
// `ssh_user` overrides via the request
// body. The wire shape is
// `{"ssh_port": 2222, "ssh_user": "aegis"}`;
// the resulting ClientConfig carries
// those values.
func TestHandleRefreshAgentBearer_BodyOverride(t *testing.T) {
	svc, _, fake := newRefreshHTTPFixture(t)
	nodeID := seedNode(t, svc, "httpoverride")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "httpoverride")
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	body := `{"ssh_port": 2222, "ssh_user": "aegis"}`
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", body)
	if rr.Code != http.StatusOK {
		t.Fatalf("status: want 200, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if fake.LastConfig.Address != "1.2.3.4:2222" {
		t.Errorf("factory Address: want %q, got %q", "1.2.3.4:2222", fake.LastConfig.Address)
	}
	if fake.LastConfig.User != "aegis" {
		t.Errorf("factory User: want %q, got %q", "aegis", fake.LastConfig.User)
	}
}

// TestHandleRefreshAgentBearer_MalformedBodyReturns400
// covers a non-JSON body. The 400 is the
// same shape as the v0.8.5 GET
// `/stored-key` handler's body-parsing
// edge case (the v0.8.5 endpoint takes
// no body, so the v0.8.7 case is a
// strictly new test).
func TestHandleRefreshAgentBearer_MalformedBodyReturns400(t *testing.T) {
	svc, _, _ := newRefreshHTTPFixture(t)
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+uuid.New().String()+"/refresh-agent-bearer", "{not-json")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status: want 400, got %d", rr.Code)
	}
}

// TestHandleRefreshAgentBearer_NilSSHClientFactory_Returns500
// pins the fail-closed wiring check: a
// Service without `WithSSHClientFactory`
// rejects the call before touching the
// store. The status is 500 (panel
// wiring missing) — same shape as the
// nil-envelope case.
func TestHandleRefreshAgentBearer_NilSSHClientFactory_Returns500(t *testing.T) {
	cap := &auditCaptureStore{}
	svc := newServiceWithStore(t)
	svc.WithAudits(audits.NewService(cap))
	// No WithSSHClientFactory call.
	svc.WithKnownHosts("/kh")
	svc.WithSSHUser("root")
	nodeID := seedNode(t, svc, "httpnofactory")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "httpnofactory")
	r := chi.NewRouter()
	r.Post("/nodes/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())
	rr := serveRefreshRequest(t, r, "/nodes/"+nodeID.String()+"/refresh-agent-bearer", "")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status: want 500, got %d; body = %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(errorBody(t, rr), "SSH client factory is not configured") {
		t.Errorf("body must name the missing wiring: %q", rr.Body.String())
	}
}

// mustMakeEnvelope is a small fixture
// helper for tests that need a
// throwaway envelope (e.g. the
// "wrong-key envelope" test above
// which seeds a row with a ciphertext
// the Service cannot read). The
// envelope is `xorCipher` (the same
// shape the Service tests use).
func mustMakeEnvelope(t *testing.T) envelopeLike {
	t.Helper()
	return &xorCipher{key: []byte("throwaway-key-for-row-seed-only")}
}

// envelopeLike is the minimal interface
// the test fixture needs. The full
// `envelope.SecretCipher` interface is
// not imported here because the helper
// is used to seed a row, not to wire
// into the Service.
type envelopeLike interface {
	Encrypt(plain []byte) ([]byte, error)
	Decrypt(cipher []byte) ([]byte, error)
}

// ============================================================================
// SSH client factory smoke (proves the
// factory pattern is wired correctly in
// the production-style path)
// ============================================================================

// TestSSHClientFactory_PassesKnownHosts proves
// the `WithKnownHosts` setter survives
// a `RefreshAgentBearer` call (the v0.8.5
// `WithEnvelope` set a precedent; the
// v0.8.7 `WithKnownHosts` mirrors it).
func TestSSHClientFactory_PassesKnownHosts(t *testing.T) {
	svc, _, fake := newRefreshHTTPFixture(t)
	// Override the known_hosts path to
	// confirm the setter is wired.
	svc.knownHosts = "/tmp/some-other-known-hosts"
	nodeID := seedNode(t, svc, "kh")
	_ = seedStoredKey(t, svc, svc.envelope, nodeID, "kh")
	if _, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{}); err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	if fake.LastConfig.KnownHosts != "/tmp/some-other-known-hosts" {
		t.Errorf("KnownHosts: want %q, got %q", "/tmp/some-other-known-hosts", fake.LastConfig.KnownHosts)
	}
}

// TestSSHClientFactory_PrivateKeyMatchesStored
// is a sanity check: the bytes the
// factory received in
// `ClientConfig.PrivateKey` round-trip
// through `ssh.ParseRawPrivateKey` to
// the same public key the original
// `seedStoredKey` call encrypted.
func TestSSHClientFactory_PrivateKeyMatchesStored(t *testing.T) {
	svc, _, fake := newRefreshHTTPFixture(t)
	nodeID := seedNode(t, svc, "pkmatch")
	priv := seedStoredKey(t, svc, svc.envelope, nodeID, "pkmatch")
	if _, err := svc.RefreshAgentBearer(context.Background(), nodeID, RefreshBearerOptions{}); err != nil {
		t.Fatalf("RefreshAgentBearer: %v", err)
	}
	// Parse the bytes the factory got.
	parsed, err := ssh.ParseRawPrivateKey(fake.LastConfig.PrivateKey)
	if err != nil {
		t.Fatalf("ParseRawPrivateKey: %v", err)
	}
	gotEd, ok := parsed.(*ed25519.PrivateKey)
	if !ok {
		t.Fatalf("parsed is not *ed25519.PrivateKey: %T", parsed)
	}
	gotPub := gotEd.Public().(ed25519.PublicKey)
	wantPub := priv.Public().(ed25519.PublicKey)
	if !gotPub.Equal(wantPub) {
		t.Errorf("public key mismatch: got %x, want %x", gotPub, wantPub)
	}
}

// sshPrivateKey alias removed — the
// test imports `crypto/ed25519`
// directly. The alias was a stub
// from an earlier draft that is no
// longer needed (the
// `parsed.(*ed25519.PrivateKey)`
// cast is now spelled out in the
// test body).

// ============================================================================
// Test-only envelope wiring (the
// `mustMakeEnvelope` helper above
// returns a `*xorCipher` cast as
// `envelopeLike`; the cast is intentional
// — the `seedStoredKey` helper expects
// the production `envelope.SecretCipher`
// shape, but the helper is only used to
// populate a row, not to decrypt it).
// ============================================================================

// TestXorCipherFixture_RoundTrip is a
// smoke test on the test fixture's
// `xorCipher` (the same shape the
// Service tests use). The cipher
// inverts under Encrypt/Decrypt; the
// test pins that contract so the
// fixture doesn't drift.
func TestXorCipherFixture_RoundTrip(t *testing.T) {
	c := &xorCipher{key: []byte("test-key-32-bytes-padded-1234")}
	plain := []byte("hello world")
	cipher, err := c.Encrypt(plain)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if string(cipher) == string(plain) {
		t.Fatal("ciphertext equals plaintext (XOR is a no-op?)")
	}
	got, err := c.Decrypt(cipher)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("round-trip: want %q, got %q", plain, got)
	}
}

// ============================================================================
// Reference to bootstrap package
// (silences unused-import errors when
// the handler tests are run in
// isolation; the v0.8.4 handler tests
// have the same import-only-for-types
// pattern).
// ============================================================================

var _ = bootstrap.TofuReject
