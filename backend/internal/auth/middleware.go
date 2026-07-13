// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
)

// ctxKey is unexported to prevent collisions in request contexts.
type ctxKey int

const (
	ctxKeyClaims ctxKey = iota
)

// ClaimsFromContext returns the verified claims attached by the
// middleware, or nil if the request was not authenticated.
func ClaimsFromContext(ctx context.Context) *Claims {
	v, ok := ctx.Value(ctxKeyClaims).(*Claims)
	if !ok {
		return nil
	}
	return v
}

// WithClaims attaches claims to a context. Intended for
// tests that want to exercise a downstream middleware
// (like RequireScope) without going through the real JWT
// verification path. Production code should not use this —
// Middleware() is the only safe way to put claims on a
// request.
func WithClaims(ctx context.Context, claims *Claims) context.Context {
	return context.WithValue(ctx, ctxKeyClaims, claims)
}

// Middleware returns a chi middleware that verifies a Bearer access
// token on the Authorization header and stashes the resulting
// claims on the request context. Public endpoints (login, refresh,
// health) must be mounted outside the protected group.
func (s *Service) Middleware() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := bearerToken(r)
			if raw == "" {
				writeJSONError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}
			claims, err := s.signer.Verify(raw)
			if err != nil {
				writeJSONError(w, http.StatusUnauthorized, "invalid token")
				return
			}
			ctx := context.WithValue(r.Context(), ctxKeyClaims, claims)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// RequireScope returns a middleware that rejects requests whose
// claims do not include the given scope. Use after Middleware().
func RequireScope(want Scope) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims := ClaimsFromContext(r.Context())
			if claims == nil || !claims.HasScope(want) {
				writeJSONError(w, http.StatusForbidden, "missing scope: "+string(want))
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// bearerToken pulls the access token out of an Authorization header
// of the form "Bearer <token>". Empty string if missing/malformed.
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if h == "" {
		return ""
	}
	const prefix = "Bearer "
	if !strings.HasPrefix(h, prefix) {
		return ""
	}
	return strings.TrimSpace(h[len(prefix):])
}

// Mount returns a chi router with the auth endpoints mounted at the
// given path. The returned router is a fresh subrouter — caller is
// expected to Mount it under a parent that handles /api/v1.
//
// Routes:
//
//	POST /auth/login    public
//	POST /auth/refresh  public
//	GET  /auth/me       Bearer-protected
func (s *Service) Mount() http.Handler {
	r := chi.NewRouter()

	r.Post("/login", s.handleLogin())
	r.Post("/refresh", s.handleRefresh())

	// Protected sub-group.
	r.Group(func(r chi.Router) {
		r.Use(s.Middleware())
		r.Get("/me", s.handleMe())
	})

	return r
}

// writeJSONError writes a minimal error envelope. We avoid
// importing a JSON library at the auth layer; the format is just
// {"error": "..."}. The frontend reads this verbatim.
func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// Hand-rolled JSON to keep the layer dependency-free.
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion in a JSON
// string literal. Sufficient for our error messages; not a
// general-purpose encoder.
func jsonString(s string) string {
	var b strings.Builder
	b.Grow(len(s) + 2)
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b.WriteByte('\\')
			b.WriteRune(r)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				// Skip control characters — they have no
				// business in an error message.
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// errUnauthorisedFor is a tiny helper so handler code reads cleanly.
func errUnauthorisedFor(err error) bool {
	return errors.Is(err, ErrUnauthorised) || errors.Is(err, ErrInvalidToken) || errors.Is(err, ErrInvalidRefreshToken)
}
