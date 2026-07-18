// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the panelcfg package. The
// surface is admin-only and is mounted by
// `internal/router/router.go` at
// `/api/v1/panelcfg`:
//
//	GET  /             -> current active sub_path config
//	POST /rotate       -> rotate to a fresh random sub_path
//	POST /rotate-to    -> rotate to an explicit sub_path
//	POST /reset        -> back to the default empty sub_path
//
// Every endpoint requires the `admin` scope. The
// sub_path table is the only mutable global config
// the v0.2.0 surface exposes, and rotating it is
// an operation that breaks every existing
// subscriber's URL вЂ” the operator must own the
// consent that the admin scope represents.

package panelcfg

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Router returns a chi subrouter for the panelcfg
// admin surface:
//
//	r.Mount("/panelcfg", panelcfg.Router(svc, authSvc.Middleware()))
//
// All routes are admin-only in Phase 0; we apply
// the ScopeAdmin requirement in front of every
// handler. The package depends on `auth` for the
// scope machinery but not for the JWT internals
// (the caller passes the middleware).
func Router(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeAdmin))

	r.Get("/", svc.handleGet())
	r.Post("/rotate", svc.handleRotate())
	r.Post("/rotate-to", svc.handleRotateTo())
	r.Post("/reset", svc.handleReset())
	return r
}

// --- request / response shapes ----------------------------------------

// rotateRequest is the body for POST /rotate. The
// optional `grace_window_seconds` field lets the
// operator keep the old sub_path alive for a
// transition window (the 3X-UI convention). A
// missing / zero field means "the old path stops
// working immediately".
type rotateRequest struct {
	GraceWindowSeconds *int `json:"grace_window_seconds,omitempty"`
}

// rotateToRequest is the body for POST /rotate-to.
// `sub_path` is required and is validated by the
// Service (length 4-64, [a-z0-9-] charset).
type rotateToRequest struct {
	SubPath            string `json:"sub_path"`
	GraceWindowSeconds *int   `json:"grace_window_seconds,omitempty"`
}

// --- handlers ---------------------------------------------------------

// handleGet returns the currently-active sub_path
// config. The empty sub_path is the default (the
// operator has not rotated yet) and is the expected
// "first install" response.
func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.GetActive(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, cfg)
	}
}

// handleRotate generates a fresh random sub_path
// and makes it the active row. The old active row
// is deactivated; an optional `grace_window_seconds`
// keeps it alive for the operator-specified window.
func (s *Service) handleRotate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req rotateRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "malformed request body")
				return
			}
		}
		grace := graceFromSeconds(req.GraceWindowSeconds)
		cfg, err := s.Rotate(r.Context(), grace)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, cfg)
	}
}

// handleRotateTo rotates to an explicit sub_path
// (operator-supplied). The path is validated by the
// Service before the write.
func (s *Service) handleRotateTo() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req rotateToRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.SubPath == "" {
			writeError(w, http.StatusBadRequest, "sub_path is required")
			return
		}
		grace := graceFromSeconds(req.GraceWindowSeconds)
		cfg, err := s.RotateTo(r.Context(), req.SubPath, grace)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, cfg)
	}
}

// handleReset deactivates the current rotated path
// and restores the default empty sub_path. The
// operator uses this to "go back to the documented
// /api/v1/sub/<token> path" after a rotation
// experiment.
func (s *Service) handleReset() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg, err := s.Reset(r.Context())
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, cfg)
	}
}

// graceFromSeconds converts a *int (seconds) to a
// time.Duration. Nil / zero / negative all return 0
// (the "no grace" default). The cap at 1h prevents
// a fat-finger entry (e.g. 86400) from keeping the
// old path alive for a day.
func graceFromSeconds(s *int) time.Duration {
	if s == nil || *s <= 0 {
		return 0
	}
	if *s > 3600 {
		return time.Hour
	}
	return time.Duration(*s) * time.Second
}

// writeStoreError maps the well-known Store /
// Service errors to HTTP status codes. Anything
// else is a 500.
func writeStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrEmpty):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	default:
		writeError(w, http.StatusInternalServerError, err.Error())
	}
}

// writeJSON serialises v as JSON with the
// application/json content type and a 200 OK
// status. Every endpoint in this package returns
// 200; error responses go through writeError
// instead, which sets the non-200 status
// explicitly. A future endpoint that needs a
// different success code (e.g. 201 Created on
// insert) can add a new helper rather than
// growing a status param on this one.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	// Hand-rolled JSON envelope (`{"error":"..."}`) to
	// stay consistent with the auth / nodes / hosts
	// / inbounds packages. The frontend's `toApiError`
	// reads this verbatim.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion
// in a JSON string literal. Non-ASCII runes round-
// trip through a hex escape rather than a direct
// byte cast (gosec G103 / G105 flags the cast as
// a potential integer-overflow conversion).
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
