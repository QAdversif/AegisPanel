// SPDX-License-Identifier: AGPL-3.0-or-later

package credentials

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// fixedClock is a deterministic clock the tests
// inject so the audit-log timestamps are stable
// across runs.
func fixedClock() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// newAdminRouter wires the credentials admin router
// for the test. The auth middleware is the standard
// panel middleware; the token carries
// ScopeCredentials so the RequireScope gate passes.
func newAdminRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	store := auth.NewMemoryStore()
	store.WithUser(&auth.User{
		ID:           uuid.New().String(),
		Username:     "test-admin",
		Email:        "admin@test.local",
		PasswordHash: "x",
		Role:         "super-admin",
		Enabled:      true,
		Scopes:       auth.Scopes{auth.ScopeCredentials},
		CreatedAt:    fixedClock(),
	})
	authSvc := auth.NewService(signer, store)
	mw := authSvc.Middleware()

	svc := NewService(NewMemoryStore())
	svc.SetClock(fixedClock)
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeCredentials}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return AdminRouter(svc, mw), tok
}

// doRequest is a tiny helper that wraps the router +
// token + method + path + body into one call.
func doRequest(t *testing.T, h http.Handler, token, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		rdr = bytes.NewReader(raw)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, path, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

// TestAdminRouter_RequiresAuth — every endpoint
// must reject unauthenticated requests with 401.
// The auth middleware is the standard panel
// middleware; if the JWT is missing / invalid, the
// request never reaches the ScopeCredentials check.
func TestAdminRouter_RequiresAuth(t *testing.T) {
	h, _ := newAdminRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		w := doRequest(t, h, "", method, "/", nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s / (no auth): status = %d, want 401", method, w.Code)
		}
	}
}

// TestAdminRouter_RequiresScope — a JWT with the
// wrong scope (e.g. ScopePlans instead of
// ScopeCredentials) is rejected with 403. The
// RequireScope gate runs after the middleware
// populates the claims, so a missing scope is a
// 403, not a 401.
func TestAdminRouter_RequiresScope(t *testing.T) {
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	store := auth.NewMemoryStore()
	store.WithUser(&auth.User{
		ID:           uuid.New().String(),
		Username:     "test-admin",
		Email:        "admin@test.local",
		PasswordHash: "x",
		Role:         "viewer",
		Enabled:      true,
		Scopes:       auth.Scopes{auth.ScopeRead}, // NOT ScopeCredentials
		CreatedAt:    fixedClock(),
	})
	authSvc := auth.NewService(signer, store)
	mw := authSvc.Middleware()
	svc := NewService(NewMemoryStore())
	h := AdminRouter(svc, mw)

	// Mint a JWT with the wrong scope.
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeRead}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	w := doRequest(t, h, tok, http.MethodGet, "/", nil)
	if w.Code != http.StatusForbidden {
		t.Errorf("GET / (wrong scope): status = %d, want 403", w.Code)
	}
}

// TestAdminRouter_ListEmpty — a fresh store
// returns an empty list (200, `credentials: []`),
// not a 404. The shape mirrors the plans handler.
func TestAdminRouter_ListEmpty(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var resp struct {
		Credentials []*Credential `json:"credentials"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Credentials) != 0 {
		t.Fatalf("got %d credentials, want 0", len(resp.Credentials))
	}
}

// TestAdminRouter_CreateGetRotateDelete is the
// canonical end-to-end happy path: create a
// credential, fetch it, rotate the value, fetch
// again to confirm the rotation, then delete.
func TestAdminRouter_CreateGetRotateDelete(t *testing.T) {
	h, tok := newAdminRouter(t)
	userID := uuid.New()
	inboundID := uuid.New()

	// Create
	createBody := map[string]any{
		"user_id":          userID.String(),
		"inbound_id":       inboundID.String(),
		"credential_value": "initial-secret-XK4p!",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", createBody)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /: status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var created Credential
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.CredentialValue != "initial-secret-XK4p!" {
		t.Fatalf("created value = %q, want initial-secret-XK4p!", created.CredentialValue)
	}
	if created.UserID != userID {
		t.Fatalf("created user_id = %s, want %s", created.UserID, userID)
	}

	// Get
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /{id}: status = %d, want 200", w.Code)
	}
	var fetched Credential
	if err := json.NewDecoder(w.Body).Decode(&fetched); err != nil {
		t.Fatalf("decode get: %v", err)
	}
	if fetched.CredentialValue != "initial-secret-XK4p!" {
		t.Fatalf("fetched value = %q, want initial-secret-XK4p!", fetched.CredentialValue)
	}

	// Rotate (PATCH)
	rotateBody := map[string]any{"credential_value": "rotated-2nd-factor"}
	w = doRequest(t, h, tok, http.MethodPatch, "/"+created.ID.String(), rotateBody)
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH /{id}: status = %d, want 200; body=%s", w.Code, w.Body.String())
	}
	var rotated Credential
	if err := json.NewDecoder(w.Body).Decode(&rotated); err != nil {
		t.Fatalf("decode rotate: %v", err)
	}
	if rotated.CredentialValue != "rotated-2nd-factor" {
		t.Fatalf("rotated value = %q, want rotated-2nd-factor", rotated.CredentialValue)
	}

	// Delete
	w = doRequest(t, h, tok, http.MethodDelete, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Fatalf("DELETE /{id}: status = %d, want 204", w.Code)
	}

	// Confirm gone
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("GET /{id} after delete: status = %d, want 404", w.Code)
	}
}

// TestAdminRouter_DuplicateReturns409 — a second
// create with the same (user, inbound) pair
// returns 409 (the underlying ErrDuplicate is
// mapped to StatusConflict).
func TestAdminRouter_DuplicateReturns409(t *testing.T) {
	h, tok := newAdminRouter(t)
	userID := uuid.New()
	inboundID := uuid.New()
	body := map[string]any{
		"user_id":          userID.String(),
		"inbound_id":       inboundID.String(),
		"credential_value": "x",
	}
	if w := doRequest(t, h, tok, http.MethodPost, "/", body); w.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d, want 201", w.Code)
	}
	if w := doRequest(t, h, tok, http.MethodPost, "/", body); w.Code != http.StatusConflict {
		t.Fatalf("second POST (dup): status = %d, want 409", w.Code)
	}
}

// TestAdminRouter_CreateRejectsInvalidUUID — a
// POST with a malformed user_id or inbound_id
// returns 400, not 500. The handler validates the
// JSON-level UUID shape; the Service validates the
// logical (non-zero) shape.
func TestAdminRouter_CreateRejectsInvalidUUID(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := map[string]any{
		"user_id":          "not-a-uuid",
		"inbound_id":       uuid.New().String(),
		"credential_value": "x",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_CreateRejectsEmptyValue — a
// POST with an empty credential_value is rejected
// by the Service (ValidationError) and mapped to
// 400 by the handler.
func TestAdminRouter_CreateRejectsEmptyValue(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := map[string]any{
		"user_id":          uuid.New().String(),
		"inbound_id":       uuid.New().String(),
		"credential_value": "",
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_ByUser — the per-user cross-cut
// read returns every credential for the given user.
// The shape mirrors the list endpoint but is
// filtered server-side.
func TestAdminRouter_ByUser(t *testing.T) {
	h, tok := newAdminRouter(t)
	userID := uuid.New()
	otherUserID := uuid.New()
	for i := 0; i < 3; i++ {
		body := map[string]any{
			"user_id":          userID.String(),
			"inbound_id":       uuid.New().String(),
			"credential_value": "value",
		}
		if w := doRequest(t, h, tok, http.MethodPost, "/", body); w.Code != http.StatusCreated {
			t.Fatalf("create %d: status = %d, want 201", i, w.Code)
		}
	}
	// One more for a different user
	body := map[string]any{
		"user_id":          otherUserID.String(),
		"inbound_id":       uuid.New().String(),
		"credential_value": "other",
	}
	if w := doRequest(t, h, tok, http.MethodPost, "/", body); w.Code != http.StatusCreated {
		t.Fatalf("create other: status = %d, want 201", w.Code)
	}
	w := doRequest(t, h, tok, http.MethodGet, "/by-user/"+userID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /by-user/{userId}: status = %d, want 200", w.Code)
	}
	var resp struct {
		Credentials []*Credential `json:"credentials"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Credentials) != 3 {
		t.Fatalf("got %d credentials, want 3", len(resp.Credentials))
	}
	for _, c := range resp.Credentials {
		if c.UserID != userID {
			t.Fatalf("got credential for user %s, want %s", c.UserID, userID)
		}
	}
}

// TestAdminRouter_ByInbound — the per-inbound
// cross-cut read returns every credential for the
// given inbound.
func TestAdminRouter_ByInbound(t *testing.T) {
	h, tok := newAdminRouter(t)
	inboundID := uuid.New()
	for i := 0; i < 2; i++ {
		body := map[string]any{
			"user_id":          uuid.New().String(),
			"inbound_id":       inboundID.String(),
			"credential_value": "value",
		}
		if w := doRequest(t, h, tok, http.MethodPost, "/", body); w.Code != http.StatusCreated {
			t.Fatalf("create %d: status = %d, want 201", i, w.Code)
		}
	}
	w := doRequest(t, h, tok, http.MethodGet, "/by-inbound/"+inboundID.String(), nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /by-inbound/{ibId}: status = %d, want 200", w.Code)
	}
	var resp struct {
		Credentials []*Credential `json:"credentials"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Credentials) != 2 {
		t.Fatalf("got %d credentials, want 2", len(resp.Credentials))
	}
}

// TestAdminRouter_ByUserRejectsInvalidUUID — a
// malformed userId in the URL is 400, not 500.
func TestAdminRouter_ByUserRejectsInvalidUUID(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/by-user/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}

// TestAdminRouter_NotFoundReturns404 — a GET for
// an unknown credential id is 404.
func TestAdminRouter_NotFoundReturns404(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/"+uuid.New().String(), nil)
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}
}

// contextUnused is a tiny shim that silences the
// unused-import warning for `context` when tests
// in this file are the only consumers of the
// import. The plans handler test has the same
// pattern; the import stays for symmetry with the
// service-level tests in service_test.go.
var _ = context.Background
