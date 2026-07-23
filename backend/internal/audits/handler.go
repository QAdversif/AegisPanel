// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the audit log. Mounted at
// /api/v1/audits by router.go:
//
//	GET /                -> list entries (with optional
//	                       actor_id / action /
//	                       resource_type / resource_id /
//	                       since / until / limit
//	                       query params)
//	GET /{id}            -> single entry (with full
//	                       before / after)
//
// Every endpoint requires the `audits` scope.
// The audit log is admin-only — viewer (read-only)
// principals can read other entities' data but
// the audit log is the one surface that contains
// the IP + User-Agent of the actor and the
// before/after diff of the mutation, both of
// which are operationally sensitive.

package audits

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/go-chi/chi/v5/middleware"
)

// Router returns a chi subrouter for the audit
// log read surface:
//
//	r.Mount("/audits", audits.Router(svc, authSvc.Middleware()))
//
// The mounted subrouter applies ScopeAudits to
// every route. The write path is not exposed over
// HTTP — internal packages call Service.Record
// directly after a successful mutation. A future
// "out-of-band" import path (e.g. a webhook from
// the agent) would land as a separate
// admin-scoped POST endpoint, NOT a public
// `audits:write` scope.
func Router(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeAudits))

	r.Get("/", svc.handleList())
	r.Get("/{id}", svc.handleGet())
	return r
}

// --- request / response shapes ----------------------------------------

// listResponse is the GET / envelope. The v0.2.0
// surface uses the {"audits": [...]} envelope
// (the same shape the GET /users handler uses) so
// the frontend can read the field the same way
// across both endpoints.
type listResponse struct {
	Audits []*AuditEntry `json:"audits"`
}

// --- handlers ---------------------------------------------------------

// handleList parses the query string into a
// ListFilter and delegates to the Service. The
// filter is intentionally permissive — an empty
// filter returns the most recent N entries.
func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		filter, err := parseListFilter(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		entries, err := s.List(r.Context(), filter)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, listResponse{Audits: entries})
	}
}

// handleGet returns a single entry by id. The
// full Before / After blobs are included (the
// list path elides them).
func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "id")
		if raw == "" {
			writeError(w, http.StatusBadRequest, "id is required")
			return
		}
		entry, err := s.GetByID(r.Context(), raw)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, entry)
	}
}

// parseListFilter maps the GET / query string to
// a ListFilter. The handler is the only place
// where the HTTP query string meets the typed
// filter; the Service is HTTP-agnostic.
//
// The bounds (since / until) accept RFC3339
// timestamps. The limit accepts a non-negative
// integer; values above MaxListLimit are clamped
// to the max.
func parseListFilter(r *http.Request) (ListFilter, error) {
	q := r.URL.Query()
	filter := ListFilter{
		ActorID:      q.Get("actor_id"),
		Action:       q.Get("action"),
		ResourceType: q.Get("resource_type"),
		ResourceID:   q.Get("resource_id"),
	}
	if raw := q.Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return filter, fmt.Errorf("invalid since: %w", err)
		}
		filter.Since = t.UTC()
	}
	if raw := q.Get("until"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return filter, fmt.Errorf("invalid until: %w", err)
		}
		filter.Until = t.UTC()
	}
	if raw := q.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			return filter, fmt.Errorf("invalid limit: %w", err)
		}
		filter.Limit = n
	}
	if !filter.Since.IsZero() && !filter.Until.IsZero() && filter.Until.Before(filter.Since) {
		return filter, fmt.Errorf("until must be at or after since")
	}
	return filter, nil
}

// --- shared helpers ---------------------------------------------------

// writeJSON serialises v as JSON with a 200 (or
// caller-chosen) status. Hand-rolled to keep
// the package dependency-light; the v0.2.0
// panels all use the same {"error": "..."}
// envelope so the frontend can read any of them
// through toApiError.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe
// inclusion in a JSON string literal. Same shape
// as the rest of the v0.2.0 packages. The
// fmt.Sprintf hex-escape avoids gosec-flagged
// rune-to-byte casts.
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', byte(r))
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				continue
			}
			b = append(b, []byte(fmt.Sprintf(`\u%04X`, r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}

// --- context helpers (used by other packages) ------------------------

// RecordFromRequest is a convenience wrapper
// around Service.Record that pulls the IP,
// User-Agent, and (if the request is
// authenticated) the actor id off the request
// and the context. The function lives in the
// audits package so other handlers don't have
// to import the `net` / `net/http` headers
// themselves; the v0.3+ call-site is
//
//	defer func() {
//	    if err == nil {
//	        audits.RecordFromRequest(svc, r, audits.Entry{
//	            Action: "user.create",
//	            ...
//	        })
//	    }
//	}()
//
// Note: the action / resource_type / resource_id
// are NOT derived automatically — the caller
// knows them. The helper only fills the
// request-shaped fields (ip, ua, actor).
func RecordFromRequest(svc *Service, r *http.Request, e Entry) {
	if svc == nil || r == nil {
		return
	}
	e.IP = clientIP(r)
	e.UserAgent = r.UserAgent()
	if claims := auth.ClaimsFromContext(r.Context()); claims != nil {
		e.ActorID = claims.Subject
		// The username is not on the claims; the
		// caller can pass it explicitly. The
		// change-password handler does this; the
		// other v0.3+ call-sites will too.
	}
	_, _ = svc.Record(r.Context(), e)
}

// clientIP returns the best-effort client IP for
// an http.Request. The IP is resolved by the chi
// v5.3 ClientIPFrom* middleware family, which is
// mounted in router.go before this handler is
// reached. The previous local implementation
// re-parsed X-Forwarded-For / X-Real-IP itself,
// which duplicated the (now-deprecated) chi
// `middleware.RealIP` behaviour and made the trust
// boundary implicit. Routing through GetClientIP
// keeps a single source of truth for IP extraction
// and lets the chi middleware decide which proxy
// headers to honour (e.g. trusting only the
// X-Real-IP that Caddy overwrites on every
// request, falling back to the TCP peer for the
// dev-mode direct-exposure path).
//
// Returns "" if no IP was set (e.g. the request
// path bypasses the router's middleware chain —
// tests that construct a bare *http.Request).
func clientIP(r *http.Request) string {
	return middleware.GetClientIP(r.Context())
}
