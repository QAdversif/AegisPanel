// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// loginRequest is the POST /auth/login body. Both fields are required.
type loginRequest struct {
	Username string `json:"username" example:"admin"`
	Password string `json:"password" example:"aegis-dev-password"`
}

// refreshRequest is the POST /auth/refresh body.
type refreshRequest struct {
	RefreshToken string `json:"refresh_token" example:"a3f1...64hex"`
}

// meResponse is the GET /auth/me body.
type meResponse struct {
	UserID   string   `json:"user_id" example:"u-1"`
	Username string   `json:"username" example:"admin"`
	Scopes   []string `json:"scopes" example:"admin,read,write"`
}

// loginResponse is the POST /auth/login body on success. Refresh
// token is returned in the body for now; Phase 1.1 will move it
// into an HttpOnly cookie.
type loginResponse struct {
	AccessToken  string    `json:"access_token" example:"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9..."`
	RefreshToken string    `json:"refresh_token" example:"a3f1...64hex"`
	TokenType    string    `json:"token_type" example:"Bearer"`
	ExpiresAt    time.Time `json:"expires_at"`
	Scopes       []string  `json:"scopes" example:"admin,read,write"`
}

// handleLogin returns an http.HandlerFunc that authenticates a
// user and returns an access+refresh pair. Wrong credentials
// collapse to 401 with a generic message — never 404, never 200.
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
		writeJSON(w, http.StatusOK, loginResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
			TokenType:    "Bearer",
			ExpiresAt:    result.ExpiresAt,
			Scopes:       result.Scopes.Strings(),
		})
	}
}

// handleRefresh exchanges a refresh token for a new pair.
func (s *Service) handleRefresh() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req refreshRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.RefreshToken == "" {
			writeJSONError(w, http.StatusBadRequest, "refresh_token is required")
			return
		}
		result, err := s.Refresh(r.Context(), req.RefreshToken)
		if err != nil {
			if errUnauthorisedFor(err) {
				writeJSONError(w, http.StatusUnauthorized, "invalid refresh token")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		writeJSON(w, http.StatusOK, loginResponse{
			AccessToken:  result.AccessToken,
			RefreshToken: result.RefreshToken,
			TokenType:    "Bearer",
			ExpiresAt:    result.ExpiresAt,
			Scopes:       result.Scopes.Strings(),
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
		writeJSON(w, http.StatusOK, meResponse{
			UserID:   u.ID,
			Username: u.Username,
			Scopes:   u.Scopes.Strings(),
		})
	}
}

// writeJSON writes v as a JSON object with the given status. Kept
// local to the auth package so we don't take on a project-wide
// JSON helper dependency.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// errInternal is a sentinel for tests; production code branches on
// errUnauthorisedFor and the response is determined by that.
var errInternal = errors.New("auth: internal error")
