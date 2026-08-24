// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/QAdversif/AegisPanel/internal/httpjson"
)

// Cookie name + path for the refresh-token cookie. The
// name is intentionally NOT the JWT-typical
// "refresh_token" — that would be a hint to an attacker
// (and to log parsers) that the cookie is a credential.
// "aegis_rt" is opaque and only meaningful to the panel's
// own handler.
const (
	refreshCookieName = "aegis_rt"
	// refreshCookiePath is "/" (not "/api/v1/auth") because
	// the panel is mounted under a rotated sub-path
	// (e.g. "/<panel-sub-path>/api/v1/auth/login"); the
	// request URL the panel sees INCLUDES the sub-path, so
	// a cookie scoped to "/api/v1/auth" would not match
	// the request URL and the browser would drop it. "/"
	// matches every request to the same origin (the decoy
	// site is a different Caddy vhost with its own cookie
	// jar, so the cookie is not leaked there).
	refreshCookiePath = "/"
	// refreshCookieMaxAge matches RefreshTokenTTL. The
	// browser is the source of truth for the idle
	// expiry — the server's `admin_refresh_tokens.expires_at`
	// is the authoritative cap that the rotation+chain-
	// revocation path enforces.
	refreshCookieMaxAge = 30 * 24 * time.Hour
)

// setRefreshCookie writes the refresh-token cookie on the
// response. HttpOnly (no JS access), SameSite=Strict (no
// cross-site send), Secure in production (dropped by the
// browser over plain HTTP — handled by the dev toggle in
// Service.SetCookieSecure). Max-Age mirrors the server
// RefreshTokenTTL so the browser expires the cookie in
// lock-step with the server's idle cap.
//
// main.go from cfg.Env == "production"; gosec's static
// analysis can't see that the production value is true.
//
//nolint:gosec // G124 — `Secure` is a Service field toggled by
func setRefreshCookie(w http.ResponseWriter, s *Service, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    token,
		Path:     refreshCookiePath,
		MaxAge:   int(refreshCookieMaxAge.Seconds()),
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// clearRefreshCookie expires the refresh-token cookie. Called
// on logout + on refresh failure (so a failed refresh wipes
// the stale cookie too). Max-Age=-1 is the canonical
// "delete now" — both Chrome and Firefox ignore the
// browser's clock and honour Max-Age / Expires.
//
//nolint:gosec // G124 — see setRefreshCookie.
func clearRefreshCookie(w http.ResponseWriter, s *Service) {
	http.SetCookie(w, &http.Cookie{
		Name:     refreshCookieName,
		Value:    "",
		Path:     refreshCookiePath,
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.cookieSecure,
		SameSite: http.SameSiteStrictMode,
	})
}

// readRefreshToken pulls the refresh token from the
// HttpOnly cookie. v0.8.14+ is cookie-only — the v0.8.13
// body-fallback path is gone. The v0.8.13 release kept
// the body parse as a one-release backwards-compat shim
// for v0.8.0-v0.8.12 clients; v0.8.14 closes that
// window. A client without the cookie gets a 400 on
// /auth/refresh and 204 (idempotent) on /auth/logout.
//
// Empty string is returned when the cookie is missing
// or empty; the caller is expected to treat that as
// "no token".
func readRefreshToken(r *http.Request) string {
	c, err := r.Cookie(refreshCookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	return c.Value
}

// loginRequest is the POST /auth/login body. Both fields are required.
type loginRequest struct {
	Username string `json:"username" example:"admin"`
	Password string `json:"password" example:"aegis-dev-password"`
}

// meResponse is the GET /auth/me body.
type meResponse struct {
	UserID   string   `json:"user_id" example:"u-1"`
	Username string   `json:"username" example:"admin"`
	Scopes   []string `json:"scopes" example:"admin,read,write"`
}

// loginResponse is the POST /auth/login body on success.
// v0.8.14+: the response ONLY carries the access token
// (and the standard `token_type` / `expires_at` /
// `scopes` envelope). The refresh token is NOT in the
// body — it is set as a `Set-Cookie: aegis_rt=...;
// HttpOnly; Secure; SameSite=Strict` header on the same
// response (see setRefreshCookie), and that cookie is
// the only authoritative channel the server reads on
// /auth/refresh. The body-field drop is the v0.8.14
// commitment that closes the v0.8.13 backwards-compat
// shim: from this release forward, the refresh token
// is unreachable from JavaScript (XSS mitigation) and
// the browser attaches it to /api/v1 requests
// automatically (no per-request boilerplate for the
// frontend).
type loginResponse struct {
	AccessToken string    `json:"access_token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	TokenType   string    `json:"token_type" example:"Bearer"`
	ExpiresAt   time.Time `json:"expires_at"`
	Scopes      []string  `json:"scopes" example:"admin,read,write"`
}

// handleLogin returns an http.HandlerFunc that authenticates a
// user and returns an access+refresh pair. Wrong credentials
// collapse to 401 with a generic message — never 404, never 200.
//
// v0.8.14+: the response sets a `Set-Cookie: aegis_rt=...;
// HttpOnly; Secure; SameSite=Strict; Path=/; Max-Age=2592000`
// header on the same response, and the body only carries the
// access token (no `refresh_token` field — that was the
// v0.8.13 backwards-compat shim, closed in v0.8.14). The
// frontend reads the refresh from the cookie, not the body.
// The cookie is unreachable from JS (XSS mitigation) and the
// browser attaches it to /api/v1 requests automatically.
func (s *Service) handleLogin() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req loginRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.Username == "" || req.Password == "" {
			writeJSONError(w, http.StatusBadRequest, "username and password are required")
			return
		}
		result, err := s.Login(r.Context(), req.Username, req.Password)
		if err != nil {
			if errUnauthorisedFor(err) {
				writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		setRefreshCookie(w, s, result.RefreshToken)
		httpjson.WriteJSON(w, http.StatusOK, loginResponse{
			AccessToken: result.AccessToken,
			TokenType:   "Bearer",
			ExpiresAt:   result.ExpiresAt,
			Scopes:      result.Scopes.Strings(),
		})
	}
}

// handleRefresh exchanges a refresh token for a new pair.
// v0.8.14+: the refresh token is read from the HttpOnly
// cookie (the only authoritative channel — the v0.8.13
// body-fallback is closed). The response sets a fresh
// cookie with the rotated refresh token (the server-side
// `ConsumeRefresh` is a one-time claim, so the new pair
// is mandatory). A request with no cookie returns 400.
func (s *Service) handleRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := readRefreshToken(r)
		if token == "" {
			writeJSONError(w, http.StatusBadRequest, "refresh token cookie is required")
			return
		}
		result, err := s.Refresh(r.Context(), token)
		if err != nil {
			// The cookie may be stale (rotated by a
			// different tab, or revoked by a chain
			// revocation). Clear it so a subsequent
			// 401-refresh-retry doesn't loop on the
			// same dead value.
			clearRefreshCookie(w, s)
			if errUnauthorisedFor(err) {
				writeJSONError(w, http.StatusUnauthorized, "invalid refresh token")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		setRefreshCookie(w, s, result.RefreshToken)
		httpjson.WriteJSON(w, http.StatusOK, loginResponse{
			AccessToken: result.AccessToken,
			TokenType:   "Bearer",
			ExpiresAt:   result.ExpiresAt,
			Scopes:      result.Scopes.Strings(),
		})
	}
}

// handleMe returns the current user's identity. Must be mounted
// behind Middleware().
func (s *Service) handleMe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			// Should be impossible behind the middleware.
			writeJSONError(w, http.StatusUnauthorized, "no claims")
			return
		}
		u, err := s.Me(r.Context(), claims)
		if err != nil {
			if errUnauthorisedFor(err) {
				writeJSONError(w, http.StatusUnauthorized, "user not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("me: %v", err))
			return
		}
		httpjson.WriteJSON(w, http.StatusOK, meResponse{
			UserID:   u.ID,
			Username: u.Username,
			Scopes:   u.Scopes.Strings(),
		})
	}
}

// changePasswordRequest is the POST /auth/me/password body.
// Both fields are required. The current password is
// verified to defend against a stolen access token being
// used to lock the operator out of their own account.
type changePasswordRequest struct {
	CurrentPassword string `json:"current_password" example:"aegis-dev-password"`
	NewPassword     string `json:"new_password" example:"new-secret-123"`
}

// changePasswordResponse is the POST /auth/me/password
// success body. The endpoint returns 200 with the
// refreshed `me` shape so the frontend can update the
// topbar's username / scope display without a separate
// round-trip.
type changePasswordResponse struct {
	UserID   string   `json:"user_id" example:"u-1"`
	Username string   `json:"username" example:"admin"`
	Scopes   []string `json:"scopes" example:"admin,read,write"`
}

// handleChangePassword rotates the current operator's
// password. The current password is verified to ensure
// the caller is not just a stolen bearer token — the
// security model is "an attacker with a stolen token
// must also know the password before they can change
// it". On success, the existing refresh tokens are
// kept (the user is not logged out); the operator's
// other browsers and devices stay authenticated.
//
// Must be mounted behind Middleware().
func (s *Service) handleChangePassword() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims := ClaimsFromContext(r.Context())
		if claims == nil {
			// Should be impossible behind the middleware.
			writeJSONError(w, http.StatusUnauthorized, "no claims")
			return
		}
		var req changePasswordRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.CurrentPassword == "" || req.NewPassword == "" {
			writeJSONError(w, http.StatusBadRequest, "current_password and new_password are required")
			return
		}
		if len(req.NewPassword) < 8 {
			writeJSONError(w, http.StatusBadRequest, "new_password is too short (min 8 chars)")
			return
		}
		if req.CurrentPassword == req.NewPassword {
			writeJSONError(w, http.StatusBadRequest, "new_password must differ from the current one")
			return
		}
		// Resolve the user from the claims, then verify
		// the supplied current password. The flow is
		// the same shape as the Login call: lookup ->
		// VerifyPassword.
		u, err := s.Me(r.Context(), claims)
		if err != nil {
			if errUnauthorisedFor(err) {
				writeJSONError(w, http.StatusUnauthorized, "user not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("me: %v", err))
			return
		}
		if err := u.VerifyPassword(req.CurrentPassword); err != nil {
			// Wrong current password — same code as
			// Login (401). No "current password
			// wrong" distinction; the UI is allowed
			// to retry.
			writeJSONError(w, http.StatusUnauthorized, "invalid credentials")
			return
		}
		if err := s.ChangePassword(r.Context(), u.ID, req.NewPassword); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("change password: %v", err))
			return
		}
		httpjson.WriteJSON(w, http.StatusOK, changePasswordResponse{
			UserID:   u.ID,
			Username: u.Username,
			Scopes:   u.Scopes.Strings(),
		})
	}
}

// handleLogout revokes the operator's current refresh
// token (server-side) and clears the cookie (client-side).
//
// Server-side revoke: the refresh token is read from the
// HttpOnly cookie (the v0.8.14+ authoritative channel —
// the v0.8.13 body-fallback is closed). The matching
// `admin_refresh_tokens` row is marked `used_at = NOW()`
// (the one-time-claim pattern), so even if the browser
// cookie lingers the token is now useless. The access
// token (15-min lifetime) is NOT revoked on the server —
// JWT access tokens are stateless by design, and the next
// 401-refresh-retry attempt would fail anyway because
// the refresh is now consumed.
//
// Client-side clear: `Set-Cookie: aegis_rt=; Max-Age=-1`
// expires the cookie. The browser drops it on the next
// page load. The access token the frontend holds in
// memory (Pinia ref) is also dropped by the frontend's
// `auth.logout()` Pinia action.
//
// 204 No Content on success — the caller doesn't need a
// body, only the status. The handler clears the cookie
// in every code path (including the no-cookie /
// already-cleared case) so a bot probing /logout cannot
// distinguish "never logged in" from "logged out".
func (s *Service) handleLogout() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := readRefreshToken(r)
		if token == "" {
			// Nothing to revoke server-side, but
			// still clear the cookie in case it's
			// set with an empty value (some
			// browsers do that on tab close).
			clearRefreshCookie(w, s)
			w.WriteHeader(http.StatusNoContent)
			return
		}
		// We do NOT surface an error to the operator
		// if the revoke fails — the cookie is still
		// cleared, the next refresh attempt will
		// fail anyway (rotated/revoked), and the
		// access token expires in 15 min. The
		// authoritative cleanup is the cookie,
		// not the response.
		if err := s.RevokeOne(r.Context(), token); err != nil {
			writeJSONError(w, http.StatusInternalServerError, fmt.Sprintf("revoke: %v", err))
			clearRefreshCookie(w, s)
			return
		}
		clearRefreshCookie(w, s)
		w.WriteHeader(http.StatusNoContent)
	}
}
