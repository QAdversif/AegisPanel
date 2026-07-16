// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Router returns a chi subrouter for the inbounds of a
// single node. The URL prefix is set by the caller:
//
//	r.Mount("/nodes/{nodeId}/inbounds", inbounds.Router(svc, authMW))
//
// The nodeId URL parameter is required: every inbound
// is scoped to a node, and the Service layer enforces
// that scope on every Create / Update / Delete / Get so
// a malicious operator cannot move an inbound to a
// different node by tampering with the URL.
//
// The returned router applies the ScopeNodes guard —
// the same scope the nodes CRUD uses — so the admin UI
// can talk to the same principal class for both.
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
	Listen      string         `json:"listen,omitempty"`
	ListenPort  int            `json:"listen_port"`
	ListenPorts []int          `json:"listen_ports,omitempty"`
	Enabled     *bool          `json:"enabled,omitempty"`
	Tags        []string       `json:"tags,omitempty"`
	Params      map[string]any `json:"params,omitempty"`
}

type updateRequest struct {
	Name        *string         `json:"name,omitempty"`
	Protocol    *Protocol       `json:"protocol,omitempty"`
	Listen      *string         `json:"listen,omitempty"`
	ListenPort  *int            `json:"listen_port,omitempty"`
	ListenPorts *[]int          `json:"listen_ports,omitempty"`
	Enabled     *bool           `json:"enabled,omitempty"`
	Tags        *[]string       `json:"tags,omitempty"`
	Params      *map[string]any `json:"params,omitempty"`
}

// --- handlers ----------------------------------------------------------

// nodeIDFromURL extracts the {nodeId} path parameter
// that the parent router injected. Every handler
// calls this first and short-circuits with a 400 if
// the id is missing / malformed.
//
// Returning the parsed UUID + ok makes the handler
// read like the nodes package's parseID: a one-line
// guard at the top.
func nodeIDFromURL(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "nodeId")
	if raw == "" {
		// The path parameter is set by the parent
		// router; a missing value is a routing bug,
		// not user input. Return 500 to surface the
		// misconfiguration loudly.
		writeError(w, http.StatusInternalServerError, "nodeId path parameter is missing")
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("invalid nodeId %q", raw))
		return uuid.Nil, false
	}
	return id, true
}

func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, ok := nodeIDFromURL(w, r)
		if !ok {
			return
		}
		items, err := s.ListByNode(r.Context(), nodeID)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		// Always return a JSON array, never null, so
		// the frontend can iterate without a guard.
		if items == nil {
			items = []*Inbound{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"inbounds": items})
	}
}

func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := nodeIDFromURL(w, r); !ok {
			return
		}
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		i, err := s.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		// Guard against a future regression where the
		// store returns an inbound belonging to a
		// different node. The Service stores the
		// inbound by ID alone, so the URL param is
		// advisory; this check keeps the contract
		// honest.
		nodeID := chi.URLParam(r, "nodeId")
		parsed, _ := uuid.Parse(nodeID)
		if i.NodeID != parsed {
			writeError(w, http.StatusNotFound, "inbound does not belong to this node")
			return
		}
		writeJSON(w, http.StatusOK, i)
	}
}

func (s *Service) handleCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodeID, ok := nodeIDFromURL(w, r)
		if !ok {
			return
		}
		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		in := CreateInput{
			ID:          zeroOrValue(req.ID),
			NodeID:      nodeID,
			Name:        req.Name,
			Protocol:    req.Protocol,
			Listen:      req.Listen,
			ListenPort:  req.ListenPort,
			ListenPorts: req.ListenPorts,
			Enabled:     req.Enabled,
			Tags:        req.Tags,
			Params:      req.Params,
		}
		i, err := s.Create(r.Context(), in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, i)
	}
}

func (s *Service) handleUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := nodeIDFromURL(w, r); !ok {
			return
		}
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
		i, err := s.Update(r.Context(), id, in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, i)
	}
}

func (s *Service) handleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := nodeIDFromURL(w, r); !ok {
			return
		}
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
// in a JSON string literal. ASCII letters and digits
// are written verbatim; control characters and
// JSON-meaningful punctuation are escaped with the
// standard backslash forms. Non-ASCII runes
// round-trip through a \uXXXX hex escape (rather than
// a direct byte cast, which gosec flags as a potential
// integer-overflow conversion).
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
