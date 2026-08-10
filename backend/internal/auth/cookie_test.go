// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newCookieRouter wires a fresh auth subrouter. Mirrors
// newChangePasswordRouter in handler_test.go but seeds a
// service in production-Secure mode so the test can
// assert the Secure flag actually fires when toggled.
func newCookieRouter(t *testing.T, secure bool) http.Handler {
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
	svc.SetCookieSecure(secure)
	return svc.Mount()
}

// doLogin hits /auth/login and returns (refreshToken, the
// Set-Cookie header value). The access token is the same on
// every call (test-only fixture) so we don't bother
// returning it. Tests that need an access token can use
// the canned `login(t, r)` helper from handler_test.go.
func doLogin(t *testing.T, r http.Handler) (string, *http.Cookie) {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/login",
		loginRequestBody(t, "admin", "hunter2-correct-horse"))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("login: status = %d, body = %s", w.Code, w.Body.String())
	}
	var resp loginResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode login: %v", err)
	}
	cookies := w.Result().Cookies()
	var refresh *http.Cookie
	for _, c := range cookies {
		if c.Name == refreshCookieName {
			refresh = c
			break
		}
	}
	return resp.RefreshToken, refresh
}

// TestHandleLogin_SetsCookie pins the v0.8.13+ contract:
// a successful login MUST set a HttpOnly + SameSite=Strict
// refresh-token cookie. The body still carries the refresh
// for one release (backwards compat); the cookie is the
// authoritative channel the frontend reads.
func TestHandleLogin_SetsCookie(t *testing.T) {
	r := newCookieRouter(t, true)
	_, c := doLogin(t, r)
	if c == nil {
		t.Fatal("expected Set-Cookie: aegis_rt=...; got no cookie")
	}
	if !c.HttpOnly {
		t.Error("cookie must be HttpOnly (no JS access)")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
	if !c.Secure {
		t.Error("Secure flag must be set when SetCookieSecure(true)")
	}
	if c.Path != refreshCookiePath {
		t.Errorf("Path = %q, want %q", c.Path, refreshCookiePath)
	}
	if c.Value == "" {
		t.Error("cookie value is empty")
	}
	if c.MaxAge != int(refreshCookieMaxAge.Seconds()) {
		t.Errorf("MaxAge = %d, want %d", c.MaxAge, int(refreshCookieMaxAge.Seconds()))
	}
}

// TestHandleLogin_DevModeCookieIsNotSecure verifies the
// dev path: SetCookieSecure(false) means the cookie
// survives over plain http://localhost, which is the
// standard dev URL. A Secure cookie would be silently
// dropped by the browser in that environment.
func TestHandleLogin_DevModeCookieIsNotSecure(t *testing.T) {
	r := newCookieRouter(t, false)
	_, c := doLogin(t, r)
	if c == nil {
		t.Fatal("expected Set-Cookie: aegis_rt=...; got no cookie")
	}
	if c.Secure {
		t.Error("dev mode must NOT set Secure (browser drops it over http://)")
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must still be set in dev mode")
	}
	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict", c.SameSite)
	}
}

// TestHandleRefresh_AcceptsCookie pins the v0.8.13+
// happy path: the refresh request carries the cookie,
// the response is 200 with a new access + a fresh cookie
// (the refresh is rotated on every use).
func TestHandleRefresh_AcceptsCookie(t *testing.T) {
	r := newCookieRouter(t, true)
	oldRT, _ := doLogin(t, r)

	// Refresh via the cookie path (no body).
	req := httptest.NewRequest(http.MethodPost, "/refresh", nil)
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: oldRT})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	// The rotated refresh MUST be a new cookie, not
	// the same value the browser just sent.
	var newRT string
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName {
			newRT = c.Value
		}
	}
	if newRT == "" {
		t.Fatal("refresh response did not set a new cookie")
	}
	if newRT == oldRT {
		t.Error("rotated refresh is the same value (one-time-claim broken)")
	}
}

// TestHandleRefresh_FallsBackToBody pins the v0.8.13
// backwards-compat: a pre-cookie client can still send
// the refresh in the body, and the server still rotates
// the cookie (so the next refresh uses the cookie path).
func TestHandleRefresh_FallsBackToBody(t *testing.T) {
	r := newCookieRouter(t, true)
	oldRT, _ := doLogin(t, r)

	// Refresh via the body path (no cookie).
	body, _ := json.Marshal(refreshRequest{RefreshToken: oldRT})
	req := httptest.NewRequest(http.MethodPost, "/refresh", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	// The response MUST set a cookie so the next
	// request can use the cookie-only path. The
	// v0.8.13 client ignores the body and reads the
	// cookie; the v0.8.12 client continues to use
	// the body.
	var hasCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("refresh via body did not set a cookie (next request cannot use the cookie path)")
	}
}

// TestHandleRefresh_RefreshFailure_ClearsCookie pins
// the recovery path: when the server rejects the
// refresh (token unknown / already used / expired),
// the response MUST clear the cookie so the browser
// stops sending the dead value on the next 401-retry
// (which would otherwise loop).
func TestHandleRefresh_RefreshFailure_ClearsCookie(t *testing.T) {
	r := newCookieRouter(t, true)
	// Log in to seed the store, then use a totally
	// unknown token to trigger the failure path.
	doLogin(t, r)

	garbage := strings.Repeat("0", 64) // valid 64-hex, just no row
	body, _ := json.Marshal(refreshRequest{RefreshToken: garbage})
	req := httptest.NewRequest(http.MethodPost, "/refresh", bytes.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; body = %s", w.Code, w.Body.String())
	}
	var clearCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName {
			clearCookie = c
		}
	}
	if clearCookie == nil {
		t.Fatal("refresh failure did not emit a clear-cookie header")
	}
	if clearCookie.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0 (delete-now)", clearCookie.MaxAge)
	}
}

// TestHandleLogout_ClearsCookie pins the v0.8.13+
// logout happy path: the operator clicks "sign out",
// the response clears the cookie (Max-Age=-1) and the
// refresh row is consumed server-side. 204 No Content.
func TestHandleLogout_ClearsCookie(t *testing.T) {
	r := newCookieRouter(t, true)
	oldRT, _ := doLogin(t, r)

	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	req.AddCookie(&http.Cookie{Name: refreshCookieName, Value: oldRT})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	var clearCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName {
			clearCookie = c
		}
	}
	if clearCookie == nil {
		t.Fatal("logout did not emit a clear-cookie header")
	}
	if clearCookie.MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want < 0 (delete-now)", clearCookie.MaxAge)
	}
}

// TestHandleLogout_NoToken_StillClears pins the
// idempotency promise: a user with a stale / unknown /
// no cookie MUST still get 204 + clear-cookie, so a
// bot probing /logout cannot distinguish "never logged
// in" from "logged out".
func TestHandleLogout_NoToken_StillClears(t *testing.T) {
	r := newCookieRouter(t, true)
	req := httptest.NewRequest(http.MethodPost, "/logout", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body = %s", w.Code, w.Body.String())
	}
	var hasClearCookie bool
	for _, c := range w.Result().Cookies() {
		if c.Name == refreshCookieName && c.MaxAge < 0 {
			hasClearCookie = true
		}
	}
	if !hasClearCookie {
		t.Error("logout with no token did not emit a clear-cookie header")
	}
}

// TestService_RevokeOne_Idempotent pins the logout-time
// Store contract: RevokeOne must NOT error on missing,
// already-consumed, or unknown tokens. The store
// promise is "best-effort; the cookie is the
// authoritative cleanup".
func TestService_RevokeOne_Idempotent(t *testing.T) {
	svc := newTestService(t)

	// 1. Unknown token — no error.
	if err := svc.RevokeOne(context.Background(), "deadbeef"); err != nil {
		t.Errorf("RevokeOne(unknown) error = %v, want nil", err)
	}
	// 2. Empty token — no error.
	if err := svc.RevokeOne(context.Background(), ""); err != nil {
		t.Errorf("RevokeOne(empty) error = %v, want nil", err)
	}
	// 3. Real token — succeeds, idempotent on re-call.
	loginResult, err := svc.Login(context.Background(), "admin", "hunter2-correct-horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	rt := loginResult.RefreshToken
	if err := svc.RevokeOne(context.Background(), rt); err != nil {
		t.Errorf("RevokeOne(real) error = %v, want nil", err)
	}
	if err := svc.RevokeOne(context.Background(), rt); err != nil {
		t.Errorf("RevokeOne(real, again) error = %v, want nil", err)
	}
	// 4. The next refresh attempt with the consumed
	// token MUST fail (one-time-claim is the audit
	// defense against theft; the consume-and-revoke
	// pattern is what makes logout work even after
	// rotation).
	if _, err := svc.Refresh(context.Background(), rt); err == nil {
		t.Error("refresh after logout succeeded — one-time-claim broken")
	}
}
