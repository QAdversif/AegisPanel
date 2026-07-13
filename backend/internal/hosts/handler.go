// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Router returns a chi subrouter for /api/v1/hosts. The
// caller supplies the auth middleware so this package does
// not have to know about JWT internals.
//
// Mounting convention:
//
//	r.Mount("/hosts", hosts.Router(svc, authSvc.Middleware()))
//
// All routes are admin-only in Phase 0; we apply the
// ScopeHosts requirement in front of every handler.
func Router(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeHosts))

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
// wire format. Endpoint IDs are optional: the service
// assigns a UUID when the caller leaves them zero.
type createRequest struct {
	ID           *uuid.UUID       `json:"id,omitempty"`
	Remark       string           `json:"remark"`
	DisplayName  string           `json:"display_name,omitempty"`
	Type         HostType         `json:"type"`
	Enabled      *bool            `json:"enabled,omitempty"`
	Priority     *int             `json:"priority,omitempty"`
	StatusFilter []UserStatus     `json:"status_filter,omitempty"`
	Country      string           `json:"country,omitempty"`
	City         string           `json:"city,omitempty"`
	Tags         []string         `json:"tags,omitempty"`
	Endpoints    []createEndpoint `json:"endpoints"`
	Balancer     *Balancer        `json:"balancer,omitempty"`
}

// createEndpoint is the JSON form of Endpoint for the
// create body. The Service normalises the IDs and
// weights; we accept zero values here.
type createEndpoint struct {
	ID       *uuid.UUID `json:"id,omitempty"`
	NodeID   uuid.UUID  `json:"node_id"`
	Protocol string     `json:"protocol"`
	Weight   int        `json:"weight,omitempty"`
	Address  []string   `json:"address,omitempty"`
	Port     *int       `json:"port,omitempty"`
	SNI      []string   `json:"sni,omitempty"`
	Host     []string   `json:"host,omitempty"`
	Path     string     `json:"path,omitempty"`
}

type updateRequest struct {
	Remark       *string           `json:"remark,omitempty"`
	DisplayName  *string           `json:"display_name,omitempty"`
	Type         *HostType         `json:"type,omitempty"`
	Enabled      *bool             `json:"enabled,omitempty"`
	Priority     *int              `json:"priority,omitempty"`
	StatusFilter *[]UserStatus     `json:"status_filter,omitempty"`
	Country      *string           `json:"country,omitempty"`
	City         *string           `json:"city,omitempty"`
	Tags         *[]string         `json:"tags,omitempty"`
	Endpoints    *[]updateEndpoint `json:"endpoints,omitempty"`
	Balancer     *Balancer         `json:"balancer,omitempty"`
}

type updateEndpoint struct {
	ID       *uuid.UUID `json:"id,omitempty"`
	NodeID   uuid.UUID  `json:"node_id"`
	Protocol string     `json:"protocol"`
	Weight   int        `json:"weight,omitempty"`
	Address  []string   `json:"address,omitempty"`
	Port     *int       `json:"port,omitempty"`
	SNI      []string   `json:"sni,omitempty"`
	Host     []string   `json:"host,omitempty"`
	Path     string     `json:"path,omitempty"`
}

// --- handlers -----------------------------------------------------------

func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hs, err := s.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("list: %v", err))
			return
		}
		// Always return a JSON array, never null, so the
		// frontend can iterate without a guard.
		if hs == nil {
			hs = []*Host{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"hosts": hs})
	}
}

func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		h, err := s.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
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
			ID:           zeroOrValue(req.ID),
			Remark:       req.Remark,
			DisplayName:  req.DisplayName,
			Type:         req.Type,
			Enabled:      req.Enabled,
			Priority:     req.Priority,
			StatusFilter: req.StatusFilter,
			Country:      req.Country,
			City:         req.City,
			Tags:         req.Tags,
			Endpoints:    toServiceEndpoints(req.Endpoints),
			Balancer:     req.Balancer,
		}
		h, err := s.Create(r.Context(), in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, h)
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
		in := UpdateInput{
			Remark:       req.Remark,
			DisplayName:  req.DisplayName,
			Type:         req.Type,
			Enabled:      req.Enabled,
			Priority:     req.Priority,
			StatusFilter: req.StatusFilter,
			Country:      req.Country,
			City:         req.City,
			Tags:         req.Tags,
			Balancer:     req.Balancer,
		}
		if req.Endpoints != nil {
			eps := toServiceEndpointsFromUpdate(*req.Endpoints)
			in.Endpoints = &eps
		}
		h, err := s.Update(r.Context(), id, in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, h)
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

// --- helpers ------------------------------------------------------------

// toServiceEndpoints converts the wire-level create
// endpoints to the Service's Endpoint type. The Service
// sees only Endpoint, never the JSON variant, so the
// request shape can evolve without breaking the Service.
func toServiceEndpoints(in []createEndpoint) []Endpoint {
	if in == nil {
		return nil
	}
	out := make([]Endpoint, 0, len(in))
	for _, e := range in {
		out = append(out, endpointFromCreate(e))
	}
	return out
}

// toServiceEndpointsFromUpdate is the update-request
// twin of toServiceEndpoints. Keeping it as a separate
// function (instead of a generic over the two structs)
// keeps the wire format self-documenting: each handler
// touches its own type.
func toServiceEndpointsFromUpdate(in []updateEndpoint) []Endpoint {
	if in == nil {
		return nil
	}
	out := make([]Endpoint, 0, len(in))
	for _, e := range in {
		out = append(out, endpointFromUpdate(e))
	}
	return out
}

func endpointFromCreate(e createEndpoint) Endpoint {
	ep := Endpoint{
		NodeID:   e.NodeID,
		Protocol: e.Protocol,
		Weight:   e.Weight,
		Address:  e.Address,
		Port:     e.Port,
		SNI:      e.SNI,
		Host:     e.Host,
		Path:     e.Path,
	}
	if e.ID != nil {
		ep.ID = *e.ID
	}
	return ep
}

func endpointFromUpdate(e updateEndpoint) Endpoint {
	ep := Endpoint{
		NodeID:   e.NodeID,
		Protocol: e.Protocol,
		Weight:   e.Weight,
		Address:  e.Address,
		Port:     e.Port,
		SNI:      e.SNI,
		Host:     e.Host,
		Path:     e.Path,
	}
	if e.ID != nil {
		ep.ID = *e.ID
	}
	return ep
}

// parseID pulls the {id} URL parameter and validates it.
// On failure it writes a 400 response and returns
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
	// Hand-rolled JSON to stay consistent with the
	// nodes package and to keep this layer free of a
	// project-wide JSON helper dependency. The format
	// is the same {"error":"..."} envelope the auth
	// package emits.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion in a
// JSON string literal. ASCII letters and digits are
// written verbatim; control characters and JSON-meaningful
// punctuation are escaped with the standard backslash
// forms. Non-ASCII runes round-trip through a \uXXXX hex
// escape (rather than a direct byte cast, which gosec
// flags as a potential integer-overflow conversion).
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
				// Skip control characters —
				// they have no business in
				// an error message.
				continue
			}
			if r < 0x80 {
				// ASCII printable; write as-is.
				b = append(b, byte(r))
				continue
			}
			// Non-ASCII: \uXXXX hex escape.
			b = append(b, []byte(fmt.Sprintf(`\u%04X`, r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
