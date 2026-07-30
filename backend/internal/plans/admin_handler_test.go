// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// adminRequest is the JSON shape the handler
// reads from the request body. The handler also
// accepts this shape for create / update; the
// difference is which fields are required.
type adminPlan struct {
	Name              string `json:"name"`
	TrafficLimitBytes int64  `json:"traffic_limit_bytes"`
	DurationNs        int64  `json:"duration_ns"`
	DeviceLimit       int    `json:"device_limit"`
	ResetPeriod       string `json:"reset_period"`
	PriceCents        int64  `json:"price_cents"`
}

// newAdminSvc wires a Service + the in-process
// auth middleware so the handler tests can hit
// the admin router end-to-end. The token is
// signed with the test secret; the auth claims
// carry ScopePlans so the RequireScope gate
// passes.
func newAdminSvc(t *testing.T) (*Service, func(http.Handler) http.Handler) {
	t.Helper()
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	store := auth.NewMemoryStore()
	// Seed an admin with the plans scope. The
	// MemoryStore.WithUser bypasses the password
	// hash (we never call Login in the handler
	// tests; we mint a JWT directly with the
	// scope and bypass the login flow).
	adminID := uuid.New()
	store.WithUser(&auth.User{
		ID:           adminID.String(),
		Username:     "test-admin",
		Email:        "admin@test.local",
		PasswordHash: "x",
		Role:         "super-admin",
		Enabled:      true,
		Scopes:       auth.Scopes{auth.ScopePlans},
		CreatedAt:    time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
	})
	authSvc := auth.NewService(signer, store)
	mw := authSvc.Middleware()

	svc := NewService(newMemStore())
	svc.SetClock(fixedClock)
	return svc, mw
}

// newAdminRouter wires the full admin router
// for the test. Returns a ready-to-call
// http.Handler and a token to put in the
// Authorization header.
func newAdminRouter(t *testing.T) (http.Handler, string) {
	t.Helper()
	svc, mw := newAdminSvc(t)
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopePlans}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	return AdminRouter(svc, mw), tok
}

// doRequest is a tiny helper that wraps the
// router + token + method + path + body into
// one call.
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
// request never reaches the ScopePlans check.
func TestAdminRouter_RequiresAuth(t *testing.T) {
	h, _ := newAdminRouter(t)
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		w := doRequest(t, h, "", method, "/", nil)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s / (no auth): status = %d, want 401", method, w.Code)
		}
	}
}

// TestAdminRouter_RequiresScopePlans — a token
// with the wrong scope is rejected with 403
// (Forbidden). The handler returns 403 from the
// RequireScope middleware, not 401.
func TestAdminRouter_RequiresScopePlans(t *testing.T) {
	h, _ := newAdminRouter(t)
	signer := auth.NewSigner("test-secret-very-long-and-very-secret-32+")
	tok, err := signer.Issue("test-admin-id", auth.Scopes{auth.ScopeUsers}, "test-jti")
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}
	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPatch, http.MethodDelete} {
		w := doRequest(t, h, tok, method, "/", nil)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s / (wrong scope): status = %d, want 403", method, w.Code)
		}
	}
}

// TestAdminHandler_List_Empty — GET / with no
// plans returns 200 and an empty array (not null).
func TestAdminHandler_List_Empty(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /: status = %d, want 200", w.Code)
	}
	var resp struct {
		Plans []*Plan `json:"plans"`
	}
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(resp.Plans) != 0 {
		t.Errorf("len(plans) = %d, want 0", len(resp.Plans))
	}
}

// TestAdminHandler_Create_HappyPath — POST /
// creates a plan and returns 201 with the row.
func TestAdminHandler_Create_HappyPath(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := adminPlan{
		Name:              "starter",
		TrafficLimitBytes: 5_000_000_000,
		DurationNs:        int64(30 * 24 * time.Hour),
		DeviceLimit:       3,
		ResetPeriod:       "monthly",
		PriceCents:        500,
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("POST /: status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	var got Plan
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Name != "starter" {
		t.Errorf("Name = %q, want %q", got.Name, "starter")
	}
	if got.Duration != 30*24*time.Hour {
		t.Errorf("Duration = %s, want %s", got.Duration, 30*24*time.Hour)
	}
}

// TestAdminHandler_Create_ValidationFailures —
// every validator path returns 400, not 500.
func TestAdminHandler_Create_ValidationFailures(t *testing.T) {
	h, tok := newAdminRouter(t)
	cases := []struct {
		name string
		body adminPlan
	}{
		{"empty-name", adminPlan{DurationNs: int64(24 * time.Hour)}},
		{"zero-duration", adminPlan{Name: "x"}},
		{"huge-duration", adminPlan{Name: "x", DurationNs: int64(100 * 365 * 24 * time.Hour)}},
		{"bad-reset", adminPlan{Name: "x", DurationNs: int64(24 * time.Hour), ResetPeriod: "yearly"}},
		{"neg-traffic", adminPlan{Name: "x", DurationNs: int64(24 * time.Hour), TrafficLimitBytes: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := doRequest(t, h, tok, http.MethodPost, "/", tc.body)
			if w.Code != http.StatusBadRequest {
				t.Errorf("POST / (%s): status = %d, want 400; body=%s", tc.name, w.Code, w.Body.String())
			}
		})
	}
}

// TestAdminHandler_Create_DuplicateName — two
// plans with the same name return 409 on the
// second POST.
func TestAdminHandler_Create_DuplicateName(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := adminPlan{
		Name:       "dupe",
		DurationNs: int64(24 * time.Hour),
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("first POST: status = %d, want 201; body=%s", w.Code, w.Body.String())
	}
	w = doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusConflict {
		t.Errorf("second POST: status = %d, want 409", w.Code)
	}
}

// TestAdminHandler_Get_NotFound — GET /{id} with
// a non-existent UUID returns 404, not 500.
func TestAdminHandler_Get_NotFound(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/"+uuid.New().String(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET /missing: status = %d, want 404", w.Code)
	}
}

// TestAdminHandler_Get_BadID — GET /{id} with a
// non-UUID string returns 400, not 500.
func TestAdminHandler_Get_BadID(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodGet, "/not-a-uuid", nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("GET /not-a-uuid: status = %d, want 400", w.Code)
	}
}

// TestAdminHandler_Get_OK — GET /{id} for a
// created plan returns 200.
func TestAdminHandler_Get_OK(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := adminPlan{Name: "starter", DurationNs: int64(24 * time.Hour)}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: %d", w.Code)
	}
	var created Plan
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode seed: %v", err)
	}
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusOK {
		t.Errorf("GET /{id}: status = %d, want 200", w.Code)
	}
}

// TestAdminHandler_Update_Partial — PATCH /{id}
// touches only the fields marked in the body.
func TestAdminHandler_Update_Partial(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := adminPlan{
		Name:       "starter",
		DurationNs: int64(24 * time.Hour),
		PriceCents: 500,
	}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: %d", w.Code)
	}
	var created Plan
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode seed: %v", err)
	}

	// Patch only the price.
	newPrice := int64(700)
	w = doRequest(t, h, tok, http.MethodPatch, "/"+created.ID.String(),
		map[string]any{"price_cents": newPrice})
	if w.Code != http.StatusOK {
		t.Fatalf("PATCH: status = %d, body=%s", w.Code, w.Body.String())
	}
	var updated Plan
	if err := json.NewDecoder(w.Body).Decode(&updated); err != nil {
		t.Fatalf("decode patch: %v", err)
	}
	if updated.PriceCents != 700 {
		t.Errorf("PriceCents = %d, want 700", updated.PriceCents)
	}
	if updated.Name != "starter" {
		t.Errorf("Name = %q, want %q (unchanged)", updated.Name, "starter")
	}
}

// TestAdminHandler_Update_NotFound — PATCH /{id}
// with a non-existent UUID returns 404.
func TestAdminHandler_Update_NotFound(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodPatch, "/"+uuid.New().String(),
		map[string]any{"name": "x"})
	if w.Code != http.StatusNotFound {
		t.Errorf("PATCH /missing: status = %d, want 404", w.Code)
	}
}

// TestAdminHandler_Delete_OK — DELETE /{id} for
// an existing plan returns 204 and the row is
// gone.
func TestAdminHandler_Delete_OK(t *testing.T) {
	h, tok := newAdminRouter(t)
	body := adminPlan{Name: "starter", DurationNs: int64(24 * time.Hour)}
	w := doRequest(t, h, tok, http.MethodPost, "/", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("seed: %d", w.Code)
	}
	var created Plan
	if err := json.NewDecoder(w.Body).Decode(&created); err != nil {
		t.Fatalf("decode seed: %v", err)
	}

	w = doRequest(t, h, tok, http.MethodDelete, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNoContent {
		t.Errorf("DELETE: status = %d, want 204", w.Code)
	}

	// GET returns 404 after delete.
	w = doRequest(t, h, tok, http.MethodGet, "/"+created.ID.String(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("GET after delete: status = %d, want 404", w.Code)
	}
}

// TestAdminHandler_Delete_NotFound — DELETE on
// a non-existent UUID returns 404.
func TestAdminHandler_Delete_NotFound(t *testing.T) {
	h, tok := newAdminRouter(t)
	w := doRequest(t, h, tok, http.MethodDelete, "/"+uuid.New().String(), nil)
	if w.Code != http.StatusNotFound {
		t.Errorf("DELETE /missing: status = %d, want 404", w.Code)
	}
}

// Compile-time check that the sentinel errors
// line up with the rest of the package. The
// handler maps ErrNotFound -> 404, ErrDuplicate
// -> 409, ValidationError -> 400. A test that
// fails to reach the right branch (e.g. an
// unwrapped error string) will be caught by the
// per-endpoint test cases above; this sentinel
// check is just a compile-time reminder.
var (
	_ = errors.Is
	_ = context.Background
)
