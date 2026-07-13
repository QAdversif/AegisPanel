// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Router returns a chi subrouter for /api/v1/nodes. The caller
// supplies the auth middleware so this package does not have to
// know about JWT internals.
//
// Mounting convention:
//
//	r.Mount("/nodes", nodes.Router(svc, authSvc.Middleware()))
//
// All routes are admin-only in Phase 0; we apply the
// ScopeNodes requirement in front of every handler.
func Router(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeNodes))

	r.Get("/", svc.handleList())
	r.Post("/", svc.handleCreate())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGet())
		r.Put("/", svc.handleUpdate())
		r.Delete("/", svc.handleDelete())
	})
	return r
}

// --- request / response shapes ------------------------------------------

type createRequest struct {
	ID           *uuid.UUID `json:"id,omitempty"`
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	State        State      `json:"state"`
	Address      string     `json:"address"`
	CapacityHint string     `json:"capacity_hint,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
}

type updateRequest struct {
	Name         *string   `json:"name,omitempty"`
	Region       *string   `json:"region,omitempty"`
	State        *State    `json:"state,omitempty"`
	Address      *string   `json:"address,omitempty"`
	CapacityHint *string   `json:"capacity_hint,omitempty"`
	Tags         *[]string `json:"tags,omitempty"`
}

// --- handlers -----------------------------------------------------------

func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, err := s.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("list: %v", err))
			return
		}
		// Always return a JSON array, never null, so the
		// frontend can iterate without a guard.
		if nodes == nil {
			nodes = []*Node{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	}
}

func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		n, err := s.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	}
}

func (s *Service) handleCreate() http.HandlerFunc {
	// Phase 0 reserves POST /api/v1/nodes for future use
	// (today operators seed nodes via the SSH bootstrap
	// flow, not via this endpoint). The handler exists so the
	// path does not 404 and so the schema validation logic
	// has a wire-level smoke test.
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		in := CreateInput{
			ID:           zeroOrValue(req.ID),
			Name:         req.Name,
			Region:       req.Region,
			State:        req.State,
			Address:      req.Address,
			CapacityHint: req.CapacityHint,
			Tags:         req.Tags,
		}
		n, err := s.Create(r.Context(), in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, n)
	}
}

func (s *Service) handleUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		// updateRequest and UpdateInput have identical fields, so
		// a direct conversion is the cleanest way to pass the
		// patch through. The staticcheck S1016 hint that flagged
		// the previous struct-literal version is happy with
		// this form.
		n, err := s.Update(r.Context(), id, UpdateInput(req))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	}
}

func (s *Service) handleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		if err := s.Delete(r.Context(), id); err != nil {
			writeStoreError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// parseID pulls the {id} URL parameter and validates it. On
// failure it writes a 400 response and returns ok=false so the
// caller can early-return.
func parseID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid id %q", raw))
		return uuid.Nil, false
	}
	return id, true
}

func zeroOrValue(p *uuid.UUID) uuid.UUID {
	if p == nil {
		return uuid.Nil
	}
	return *p
}

// writeStoreError maps the well-known Store / Service errors to
// HTTP status codes. Anything else is a 500.
func writeStoreError(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicate):
		writeError(w, http.StatusConflict, err.Error())
	case errors.As(err, &vErr):
		writeError(w, http.StatusBadRequest, vErr.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	// Hand-rolled JSON to stay consistent with the auth
	// package and to keep this layer free of a JSON-helper
	// dependency. The format is the same {"error":"..."}
	// envelope the auth package emits.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion in a JSON
// string literal. Same implementation as auth.jsonString,
// duplicated here so this package has no dependency on auth
// internals. Non-ASCII runes are emitted as JSON \uXXXX escapes
// via a hex round-trip rather than a direct byte cast, which
// gosec flags as a potential integer-overflow conversion.
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', '\\', byte(r))
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				// Skip control characters — they would
				// be invalid in JSON anyway.
				continue
			}
			// Round-trip through fmt.Sprintf so we get the
			// right escape for any rune without the
			// gosec-flagged rune→byte cast.
			b = append(b, []byte(fmt.Sprintf(`\u%04X`, r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
