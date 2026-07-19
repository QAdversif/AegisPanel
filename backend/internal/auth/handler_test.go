// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// newChangePasswordRouter wires the auth subrouter
// against a fresh MemoryStore + Service. The
// `Mount` subrouter's real Middleware() is in
// place; the tests issue a real login first to
// mint a bearer token, then attach it to the
// change-password request. This is closer to the
// production path than a claims-stuffer and
// gives us coverage of the actual JWT plumbing.
func newChangePasswordRouter(t *testing.T) (http.Handler, *Service) {
	t.Helper()
	hash, err := HashPassword("hunter2-correct-horse")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	store := NewMemoryStore().WithUser(&User{
		ID:           "u-1",
		Username:     "admin",
		PasswordHash: hash,
		Role:         "super-admin",
		Scopes:       Scopes{ScopeAdmin, ScopeRead, ScopeWrite, ScopeAudits},
	})
	signer := NewSigner("0123456789abcdef0123456789abcdef")
	svc := NewService(signer, store)
	return svc.Mount(), svc
}

// loginRequestBody encodes the JSON body the
// /auth/login handler expects. Tiny helper to
// keep the tests readable.
func loginRequestBody(t *testing.T, username, password string) *bytes.Reader {
	t.Helper()
	body, err := json.Marshal(loginRequest{Username: username, Password: password})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return bytes.NewReader(body)
}

// login issues a /auth/login call for the seeded
// "admin" user and returns the resulting access
// token. The change-password tests attach the
// token as a bearer credential to the follow-up
// request. The username is hard-coded because the
// test router only seeds the one admin.
func login(t *testing.T, r http.Handler, password string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/login", loginRequestBody(t, "admin", password))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	return resp.AccessToken
}

func TestChangePassword_Success(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "hunter2-correct-horse",
		NewPassword:     "new-strong-passphrase",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", w.Code, w.Body.String())
	}
	var resp changePasswordResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.UserID != "u-1" {
		t.Errorf("user_id = %q, want u-1", resp.UserID)
	}
	if resp.Username != "admin" {
		t.Errorf("username = %q, want admin", resp.Username)
	}
}

func TestChangePassword_WrongCurrent(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "nope",
		NewPassword:     "new-strong-passphrase",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestChangePassword_TooShort(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "hunter2-correct-horse",
		NewPassword:     "short",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestChangePassword_SameAsCurrent(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "hunter2-correct-horse",
		NewPassword:     "hunter2-correct-horse",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestChangePassword_MissingFields(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(map[string]string{
		"current_password": "hunter2-correct-horse",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

// TestChangePassword_NewHashTakesEffect verifies the
// post-rotation password is what is now on disk —
// i.e. Login with the new password works, and Login
// with the old one does not.
func TestChangePassword_NewHashTakesEffect(t *testing.T) {
	r, svc := newChangePasswordRouter(t)
	token := login(t, r, "hunter2-correct-horse")
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "hunter2-correct-horse",
		NewPassword:     "new-strong-passphrase",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("change password: status = %d, body = %s", w.Code, w.Body.String())
	}

	// Old password should no longer work.
	if _, err := svc.Login(t.Context(), "admin", "hunter2-correct-horse"); err == nil {
		t.Error("old password still works after rotation")
	}
	// New password should work.
	if _, err := svc.Login(t.Context(), "admin", "new-strong-passphrase"); err != nil {
		t.Errorf("new password rejected: %v", err)
	}
}

// TestChangePassword_MissingToken rejects anonymous
// requests with 401. The auth middleware is in
// front of the route; no token = no claims = 401.
func TestChangePassword_MissingToken(t *testing.T) {
	r, _ := newChangePasswordRouter(t)
	body, _ := json.Marshal(changePasswordRequest{
		CurrentPassword: "hunter2-correct-horse",
		NewPassword:     "new-strong-passphrase",
	})
	req := httptest.NewRequest(http.MethodPost, "/me/password", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
