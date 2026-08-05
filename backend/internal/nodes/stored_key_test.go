// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the v0.8.5
// `GET /api/v1/nodes/{id}/stored-key` debug
// surface. The tests cover the Service-level
// `GetStoredKey` method (the pure logic) and
// the HTTP `handleGetStoredKey` handler (the
// wire-level shape: status codes, audit row,
// response body).
//
// # What's covered
//
// Service.GetStoredKey:
//
//   - happy path with a real ed25519 key
//     (generated, encrypted, persisted,
//     decrypted, public key derived, fields
//     populated, fingerprint matches the
//     `ssh-keygen -lf` shape)
//   - row with no stored ciphertext
//     (`HasStoredKey: false`, no decrypt
//     attempt)
//   - nil envelope (fail-closed, no decrypt
//     attempt)
//   - row not found (the underlying
//     `ErrNotFound` propagates)
//
// HTTP handleGetStoredKey:
//
//   - 200 with the stored key surface
//   - 200 with `has_stored_key: false` for
//     a row that has no stored ciphertext
//   - 400 malformed UUID
//   - 404 node not found
//   - 500 envelope not configured
//   - 502 decrypt failure (simulated by
//     storing random non-PEM bytes in the
//     ciphertext column)
//   - audit row recorded on success with
//     `node.stored-key.read` action

package nodes

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
)

// noopCipher is a test-local
// envelope.SecretCipher. The store is
// happy to accept any Encrypt / Decrypt
// pair, so a struct that round-trips the
// bytes verbatim is sufficient. The real
// production cipher is
// `envelope.NewAgeSecretCipher` (used by
// the panel main) or `envelope.NewNoopSecretCipher`
// (used in memory-mode dev); both are out
// of scope for unit tests because the age
// cipher requires a real key file.
type noopCipher struct{}

func (noopCipher) Encrypt(plain []byte) ([]byte, error) {
	out := make([]byte, len(plain))
	copy(out, plain)
	return out, nil
}
func (noopCipher) Decrypt(cipher []byte) ([]byte, error) {
	out := make([]byte, len(cipher))
	copy(out, cipher)
	return out, nil
}

// makeStoredPrivateKey returns a freshly-
// generated ed25519 private key in the
// OpenSSH-PEM format the v0.8.1 provisioner
// produces. The `ssh.MarshalPrivateKey` wire
// shape is the same call the production
// code uses in `bootstrap.generateAndPushKey`
// (PR #179); the test imports the same
// `golang.org/x/crypto/ssh` package.
func makeStoredPrivateKey(t *testing.T, comment string) []byte {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	block, err := ssh.MarshalPrivateKey(priv, comment)
	if err != nil {
		t.Fatalf("MarshalPrivateKey: %v", err)
	}
	return pem.EncodeToMemory(block)
}

// TestGetStoredKey_HappyPath_PopulatesAllFields
// is the round-trip test: generate a real
// ed25519 key, encrypt it with the noop
// cipher, persist, read back via the
// Service, assert every field is populated
// and the fingerprint matches the OpenSSH
// public-key form.
func TestGetStoredKey_HappyPath_PopulatesAllFields(t *testing.T) {
	rowID := uuid.MustParse("11111111-1111-4111-8111-111111111111")
	now := time.Date(2026, 8, 4, 17, 30, 0, 0, time.UTC)
	privPEM := makeStoredPrivateKey(t, "aegis-panel@node-test-rotate")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-rotate",
		State:                   StateOnline,
		Address:                 "10.0.0.1:22",
		SSHPrivateKeyCiphertext: privPEM,
		UpdatedAt:               now,
	})
	svc := NewService(store)
	svc.WithEnvelope(noopCipher{})
	sk, err := svc.GetStoredKey(context.Background(), rowID)
	if err != nil {
		t.Fatalf("GetStoredKey: %v", err)
	}
	if !sk.HasStoredKey {
		t.Fatal("HasStoredKey = false, want true")
	}
	if sk.PublicKeyLine == "" {
		t.Error("PublicKeyLine is empty")
	}
	if sk.Fingerprint == "" {
		t.Error("Fingerprint is empty")
	}
	if len(sk.Fingerprint) < 7 || sk.Fingerprint[:6] != "SHA256" {
		t.Errorf("Fingerprint = %q, want SHA256: prefix", sk.Fingerprint)
	}
	if sk.Algorithm != "ssh-ed25519" {
		t.Errorf("Algorithm = %q, want ssh-ed25519", sk.Algorithm)
	}
	if !sk.KeyUpdatedAt.Equal(now) {
		t.Errorf("KeyUpdatedAt = %v, want %v", sk.KeyUpdatedAt, now)
	}
}

// TestGetStoredKey_RowWithoutCiphertext_ReportsNoKey
// pins the "row exists, no key" path. The
// endpoint returns 200 with
// `has_stored_key: false`; the handler
// does NOT 404.
func TestGetStoredKey_RowWithoutCiphertext_ReportsNoKey(t *testing.T) {
	rowID := uuid.MustParse("22222222-2222-4222-8222-222222222222")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-no-key",
		State:                   StateNew,
		Address:                 "10.0.0.2:22",
		SSHPrivateKeyCiphertext: nil, // explicit: never installed via v0.8.1
	})
	svc := NewService(store)
	svc.WithEnvelope(noopCipher{})
	sk, err := svc.GetStoredKey(context.Background(), rowID)
	if err != nil {
		t.Fatalf("GetStoredKey: %v", err)
	}
	if sk.HasStoredKey {
		t.Error("HasStoredKey = true, want false")
	}
	if sk.PublicKeyLine != "" || sk.Fingerprint != "" {
		t.Errorf("expected empty public-key fields, got PublicKeyLine=%q Fingerprint=%q", sk.PublicKeyLine, sk.Fingerprint)
	}
}

// TestGetStoredKey_NilEnvelope_FailsClosed
// pins the "panel booted without envelope"
// failure mode. The function returns
// without touching the row.
func TestGetStoredKey_NilEnvelope_FailsClosed(t *testing.T) {
	rowID := uuid.MustParse("33333333-3333-4333-8333-333333333333")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-nil-env",
		State:                   StateOnline,
		Address:                 "10.0.0.3:22",
		SSHPrivateKeyCiphertext: []byte("does-not-matter"),
	})
	svc := NewService(store)
	// no WithEnvelope — s.envelope is nil
	if _, err := svc.GetStoredKey(context.Background(), rowID); err == nil {
		t.Fatal("GetStoredKey with nil envelope should fail, got nil error")
	}
	// Row state must be unchanged (no
	// mutate-side-effect path; this is a
	// read).
	row, _ := store.GetByID(context.Background(), rowID)
	if len(row.SSHPrivateKeyCiphertext) == 0 {
		t.Error("GetStoredKey cleared the row's ciphertext")
	}
}

// TestGetStoredKey_NodeNotFound_Propagates
// pins the "row not in the store" failure
// mode. The Service returns the underlying
// store's ErrNotFound; the HTTP handler
// maps that to 404.
func TestGetStoredKey_NodeNotFound_Propagates(t *testing.T) {
	store := newMemoryStoreWithRows()
	svc := NewService(store)
	svc.WithEnvelope(noopCipher{})
	_, err := svc.GetStoredKey(context.Background(), uuid.New())
	if err == nil {
		t.Fatal("GetStoredKey with unknown id should fail, got nil error")
	}
}

// --- HTTP handler tests -------------------------------------------------

// auditCaptureStore is a test-local
// audits.Store that records every Entry it
// sees. The unit tests assert on the captured
// entries to pin the "audit recorded with the
// right shape" contract (the same shape the
// operator's audit-log UI reads back).
type auditCaptureStore struct {
	mu      sync.Mutex
	entries []*audits.AuditEntry
}

func (a *auditCaptureStore) Insert(_ context.Context, e audits.Entry) (*audits.AuditEntry, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := &audits.AuditEntry{
		ActorID:       e.ActorID,
		ActorUsername: e.ActorUsername,
		Action:        e.Action,
		ResourceType:  e.ResourceType,
		ResourceID:    e.ResourceID,
		Before:        e.Before,
		After:         e.After,
		IP:            e.IP,
		UserAgent:     e.UserAgent,
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

// TestHandleGetStoredKey_HappyPath verifies
// the HTTP-level happy path: 200 status,
// correct body shape, audit row recorded.
func TestHandleGetStoredKey_HappyPath(t *testing.T) {
	rowID := uuid.MustParse("44444444-4444-4444-8444-444444444444")
	now := time.Date(2026, 8, 4, 17, 30, 0, 0, time.UTC)
	privPEM := makeStoredPrivateKey(t, "aegis-panel@node-test-handler")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-handler",
		State:                   StateOnline,
		Address:                 "10.0.0.4:22",
		SSHPrivateKeyCiphertext: privPEM,
		UpdatedAt:               now,
	})
	cap := &auditCaptureStore{}
	auditsSvc := audits.NewService(cap)
	svc := NewService(store)
	svc.WithAudits(auditsSvc)
	svc.WithEnvelope(noopCipher{})
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	rr := serveStoredKeyRequest(t, r, "/nodes/"+rowID.String()+"/stored-key")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var sk StoredKey
	if err := json.Unmarshal(rr.Body.Bytes(), &sk); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !sk.HasStoredKey {
		t.Error("HasStoredKey = false, want true")
	}
	if sk.Fingerprint == "" {
		t.Error("Fingerprint is empty")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1", len(cap.entries))
	}
	e := cap.entries[0]
	if e.Action != "node.stored-key.read" {
		t.Errorf("Action = %q, want node.stored-key.read", e.Action)
	}
	if e.ResourceID != rowID.String() {
		t.Errorf("ResourceID = %q, want %q", e.ResourceID, rowID.String())
	}
}

// TestHandleGetStoredKey_NoStoredKey_Returns200
// pins the "row exists, no key" wire shape:
// 200 (NOT 404) with `has_stored_key: false`.
// The audit row still records the read (so
// "who looked at this row at time T" is
// answerable).
func TestHandleGetStoredKey_NoStoredKey_Returns200(t *testing.T) {
	rowID := uuid.MustParse("55555555-5555-4555-8555-555555555555")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-no-key-handler",
		State:                   StateNew,
		Address:                 "10.0.0.5:22",
		SSHPrivateKeyCiphertext: nil,
	})
	cap := &auditCaptureStore{}
	auditsSvc := audits.NewService(cap)
	svc := NewService(store)
	svc.WithAudits(auditsSvc)
	svc.WithEnvelope(noopCipher{})
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	rr := serveStoredKeyRequest(t, r, "/nodes/"+rowID.String()+"/stored-key")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}
	var sk StoredKey
	if err := json.Unmarshal(rr.Body.Bytes(), &sk); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if sk.HasStoredKey {
		t.Error("HasStoredKey = true, want false")
	}
	cap.mu.Lock()
	defer cap.mu.Unlock()
	if len(cap.entries) != 1 {
		t.Fatalf("audit entries = %d, want 1 (read was recorded)", len(cap.entries))
	}
}

// TestHandleGetStoredKey_MalformedIDReturns400
// pins the URL-level validation. The chi
// router validates the {id} as a UUID via
// uuid.Parse inside parseID.
func TestHandleGetStoredKey_MalformedIDReturns400(t *testing.T) {
	svc := NewService(newMemoryStoreWithRows())
	svc.WithEnvelope(noopCipher{})
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	// No {id} segment → chi.URLParam returns
	// "" → uuid.Parse fails → 400.
	rr := serveStoredKeyRequest(t, r, "/nodes//stored-key")
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleGetStoredKey_NodeNotFoundReturns404
// pins the wire shape: row missing → 404.
func TestHandleGetStoredKey_NodeNotFoundReturns404(t *testing.T) {
	svc := NewService(newMemoryStoreWithRows())
	svc.WithEnvelope(noopCipher{})
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	rr := serveStoredKeyRequest(t, r, "/nodes/"+uuid.New().String()+"/stored-key")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleGetStoredKey_NilEnvelopeReturns500
// pins the "panel booted without envelope"
// failure mode. The handler returns 500
// (server config), not 502 (upstream).
func TestHandleGetStoredKey_NilEnvelopeReturns500(t *testing.T) {
	rowID := uuid.MustParse("66666666-6666-4666-8666-666666666666")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-nil-env-handler",
		State:                   StateOnline,
		Address:                 "10.0.0.6:22",
		SSHPrivateKeyCiphertext: []byte("does-not-matter"),
	})
	svc := NewService(store)
	// no WithEnvelope
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	rr := serveStoredKeyRequest(t, r, "/nodes/"+rowID.String()+"/stored-key")
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", rr.Code, rr.Body.String())
	}
}

// TestHandleGetStoredKey_DecryptFailureReturns502
// pins the "stored ciphertext is unreadable"
// failure mode. We write random non-PEM
// bytes to the row; the noop cipher
// round-trips them verbatim; the parser
// then fails with "not a PEM block" → the
// handler maps to 502.
func TestHandleGetStoredKey_DecryptFailureReturns502(t *testing.T) {
	rowID := uuid.MustParse("77777777-7777-4777-8777-777777777777")
	store := newMemoryStoreWithRows(Node{
		ID:                      rowID,
		Name:                    "test-bad-cipher",
		State:                   StateOnline,
		Address:                 "10.0.0.7:22",
		SSHPrivateKeyCiphertext: []byte("not-a-pem-block"),
	})
	svc := NewService(store)
	svc.WithEnvelope(noopCipher{})
	r := chi.NewRouter()
	r.Get("/nodes/{id}/stored-key", svc.handleGetStoredKey())
	rr := serveStoredKeyRequest(t, r, "/nodes/"+rowID.String()+"/stored-key")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502; body=%s", rr.Code, rr.Body.String())
	}
}

// --- test helpers --------------------------------------------------------

// newMemoryStoreWithRows returns a
// *MemoryStore seeded with the supplied
// nodes. The MemoryStore is the v0.2.0+ test
// fixture for the nodes package; using it
// here keeps the test hermetic (no pg / no
// DSN).
func newMemoryStoreWithRows(rows ...Node) *MemoryStore {
	store := NewMemoryStore()
	for _, r := range rows {
		cp := r
		store.byID[cp.ID] = &cp
	}
	return store
}

// serveStoredKeyRequest fires a GET against
// the supplied chi router and returns the
// httptest.ResponseRecorder. The helper
// uses the supplied URL exactly (caller
// controls the path).
func serveStoredKeyRequest(t *testing.T, r http.Handler, url string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, url, nil)
	// Attach fake claims so the audit row
	// has an actor id (the production
	// `auth.ClaimsFromContext` reads off the
	// same key; the audit row is empty if
	// no claims are attached, but the
	// shape assertion still passes).
	req = req.WithContext(auth.WithClaims(req.Context(), &auth.Claims{
		Scopes: auth.Scopes{auth.ScopeNodes},
	}))
	rr := httptest.NewRecorder()
	r.ServeHTTP(rr, req)
	return rr
}
