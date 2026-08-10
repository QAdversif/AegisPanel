// SPDX-License-Identifier: AGPL-3.0-or-later

package inboundtemplates

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Router returns a chi subrouter for the panel-wide
// inbound templates. The URL prefix is set by the
// caller:
//
//	r.Mount("/inbound-templates", inboundtemplates.Router(svc, authMW))
//
// Templates are global (not per-node), so the URL
// does not carry a nodeId — unlike the inbounds
// subrouter, which lives at
// `/nodes/{nodeId}/inbounds`. The same ScopeNodes
// guard backs both: managing templates is a
// panel-level operation that affects per-node
// inbounds, and the admin UI keeps them in the same
// permissions envelope.
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

// --- request / response shapes -----------------------------------------

// createRequest mirrors CreateInput but is JSON-only —
// the HTTP layer never sees the Service struct, so a
// future refactor of CreateInput does not break the
// wire format. ID is optional: the service assigns a
// UUID when the caller leaves it zero.
type createRequest struct {
	ID          *uuid.UUID     `json:"id,omitempty"`
	Name        string         `json:"name"`
	Protocol    Protocol       `json:"protocol"`
	Params      map[string]any `json:"params,omitempty"`
	Description string         `json:"description,omitempty"`
}

type updateRequest struct {
	Name        *string         `json:"name,omitempty"`
	Protocol    *Protocol       `json:"protocol,omitempty"`
	Params      *map[string]any `json:"params,omitempty"`
	Description *string         `json:"description,omitempty"`
}

// --- handlers ----------------------------------------------------------

func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := s.List(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		// Always return a JSON array, never null, so
		// the frontend can iterate without a guard.
		if items == nil {
			items = []*InboundTemplate{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"templates": items})
	}
}

func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		t, err := s.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
	}
}

func (s *Service) handleCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		in := CreateInput{
			ID:          zeroOrValue(req.ID),
			Name:        req.Name,
			Protocol:    req.Protocol,
			Params:      req.Params,
			Description: req.Description,
		}
		t, err := s.Create(r.Context(), in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, t)
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
		in := UpdateInput(req)
		t, err := s.Update(r.Context(), id, in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, t)
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

// --- helpers -----------------------------------------------------------

// parseID pulls the {id} URL parameter and validates
// it. On failure it writes a 400 response and returns
// ok=false so the caller can early-return.
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

// writeStoreError maps the well-known Store / Service
// errors to HTTP status codes. Anything else is a 500.
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion
// in a JSON string literal. Same convention as the
// inbounds package's helper.
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"':
			b = append(b, '\\', '"')
		case '\\':
			b = append(b, '\\', '\\')
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
			if r < 0x80 {
				b = append(b, byte(r))
				continue
			}
			b = append(b, []byte(fmt.Sprintf(`\u%04X`, r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
