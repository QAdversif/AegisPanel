// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Admin HTTP handler for the credentials CRUD surface
// (v0.8.2). Mounted at /api/v1/credentials by router.go.
//
// Endpoints (all require auth.RequireScope(ScopeCredentials)
// AND a valid JWT — the auth middleware takes care of the
// JWT part):
//
//	GET    /                     -> list every credential
//	GET    /{id}                 -> get a single credential
//	POST   /                     -> create a credential
//	PATCH  /{id}                 -> rotate the credential_value
//	DELETE /{id}                 -> hard delete
//
//	GET    /by-user/{userId}     -> list every credential for a user
//	GET    /by-inbound/{ibId}    -> list every credential for an inbound
//
// # Why query-param filtering is on the list endpoints
//
// The two cross-cut reads (ListByUser, ListByInbound) get
// dedicated routes (`/by-user/{userId}`, `/by-inbound/{ibId}`)
// in addition to the catch-all GET / (which lists every
// credential). The dedicated routes are the access patterns
// the future multi-user sing-box renderer will use
// internally, but the admin UI also benefits from them: the
// "View credentials" dropdown action on a user row hits
// /by-user/{userId}, the "Who can use this inbound?" panel
// on an inbound row hits /by-inbound/{ibId}. The dedicated
// routes keep the UI off the "list everything and filter
// client-side" path that does not scale to a few thousand
// rows.
//
// # Audit log writes
//
// Every mutating handler delegates to the Service, which
// already records `credential.create` / `credential.rotate`
// / `credential.delete` audit entries via
// `audits.RecordFromContext`. The wiring was added in
// PR #166; this PR just exposes the Service over HTTP.
//
// # JSON wire format
//
// Every request / response uses snake_case JSON keys to
// match the existing admin surface (`users.User`,
// `plans.Plan`, `nodes.Node`, `webhooks.Endpoint`, etc.).
// The `Credential` struct tags are already snake_case; the
// request / response shapes here mirror them.

package credentials

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// AdminRouter returns a chi subrouter for the credentials
// admin surface:
//
//	r.Mount("/credentials", credentials.AdminRouter(svc, authSvc.Middleware()))
//
// The mounted subrouter applies ScopeCredentials to every
// route. Read endpoints are still guarded by ScopeCredentials
// — the scope gates "can this operator see credential data
// at all", not "can this operator edit it".
func AdminRouter(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeCredentials))

	r.Get("/", svc.handleListCredentials())
	r.Post("/", svc.handleCreateCredential())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGetCredential())
		r.Patch("/", svc.handleRotateCredential())
		r.Delete("/", svc.handleDeleteCredential())
	})
	// Cross-cut reads: per-user and per-inbound lists. These
	// are the access patterns the future multi-user sing-box
	// renderer will use, and the dedicated routes keep the
	// admin UI off the "list everything" path.
	r.Get("/by-user/{userId}", svc.handleListByUser())
	r.Get("/by-inbound/{ibId}", svc.handleListByInbound())
	return r
}

// --- request / response shapes ----------------------------------------

// createCredentialRequest is the POST / body. The ID,
// CreatedAt, and UpdatedAt are NOT accepted from the
// caller — the Service generates them.
type createCredentialRequest struct {
	UserID          string `json:"user_id"`
	InboundID       string `json:"inbound_id"`
	CredentialValue string `json:"credential_value"`
}

// rotateCredentialRequest is the PATCH /{id} body. The
// rotation only takes the new credential_value; the
// (user_id, inbound_id) pair is fixed by the existing row.
type rotateCredentialRequest struct {
	CredentialValue string `json:"credential_value"`
}

// --- handlers ---------------------------------------------------------

func (s *Service) handleListCredentials() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// List every credential. The admin UI uses this
		// for the cross-user table; the per-user /
		// per-inbound routes below are the more
		// selective access paths.
		rows, err := s.ListAll(r.Context())
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if rows == nil {
			rows = []*Credential{}
		}
		writeCredentialJSON(w, http.StatusOK, map[string]any{"credentials": rows})
	}
}

func (s *Service) handleGetCredential() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCredentialID(w, r)
		if !ok {
			return
		}
		c, err := s.Get(r.Context(), id)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		writeCredentialJSON(w, http.StatusOK, c)
	}
}

func (s *Service) handleCreateCredential() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCredentialError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		userID, err := uuid.Parse(req.UserID)
		if err != nil {
			writeCredentialError(w, &ValidationError{Field: "user_id", Message: fmt.Sprintf("invalid uuid %q", req.UserID)})
			return
		}
		inboundID, err := uuid.Parse(req.InboundID)
		if err != nil {
			writeCredentialError(w, &ValidationError{Field: "inbound_id", Message: fmt.Sprintf("invalid uuid %q", req.InboundID)})
			return
		}
		c, err := s.Create(r.Context(), userID, inboundID, req.CredentialValue)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		writeCredentialJSON(w, http.StatusCreated, c)
	}
}

func (s *Service) handleRotateCredential() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCredentialID(w, r)
		if !ok {
			return
		}
		var req rotateCredentialRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeCredentialError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		c, err := s.Rotate(r.Context(), id, req.CredentialValue)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		writeCredentialJSON(w, http.StatusOK, c)
	}
}

func (s *Service) handleDeleteCredential() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseCredentialID(w, r)
		if !ok {
			return
		}
		if err := s.Delete(r.Context(), id); err != nil {
			writeCredentialError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (s *Service) handleListByUser() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "userId")
		userID, err := uuid.Parse(raw)
		if err != nil {
			writeCredentialError(w, &ValidationError{Field: "userId", Message: fmt.Sprintf("invalid uuid %q", raw)})
			return
		}
		rows, err := s.ListByUser(r.Context(), userID)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if rows == nil {
			rows = []*Credential{}
		}
		writeCredentialJSON(w, http.StatusOK, map[string]any{"credentials": rows})
	}
}

func (s *Service) handleListByInbound() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		raw := chi.URLParam(r, "ibId")
		inboundID, err := uuid.Parse(raw)
		if err != nil {
			writeCredentialError(w, &ValidationError{Field: "ibId", Message: fmt.Sprintf("invalid uuid %q", raw)})
			return
		}
		rows, err := s.ListByInbound(r.Context(), inboundID)
		if err != nil {
			writeCredentialError(w, err)
			return
		}
		if rows == nil {
			rows = []*Credential{}
		}
		writeCredentialJSON(w, http.StatusOK, map[string]any{"credentials": rows})
	}
}

// --- helpers ----------------------------------------------------------

// parseCredentialID pulls the {id} URL parameter and
// validates it. On failure it writes a 400 response
// and returns ok=false so the caller can early-return.
func parseCredentialID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeCredentialError(w, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid uuid %q", raw)})
		return uuid.Nil, false
	}
	return id, true
}

// writeCredentialError maps the well-known Service /
// Store errors to HTTP status codes. Mirrors
// plans.writePlanError and webhooks.writeWebhookError;
// the duplication is cheaper than a shared httpkit for
// 20 lines of code.
func writeCredentialError(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeCredentialJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicate):
		writeCredentialJSONError(w, http.StatusConflict, err.Error())
	case errors.As(err, &vErr):
		writeCredentialJSONError(w, http.StatusBadRequest, vErr.Error())
	default:
		writeCredentialJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

// writeCredentialJSON / writeCredentialJSONError /
// credentialJSONString are the tiny shims the handler
// needs. They live in this file (not a shared httpkit)
// because every package has its own copy of these and
// the duplication is cheaper than a new package
// dependency for 20 lines of code.

func writeCredentialJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeCredentialJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + credentialJSONString(msg) + `}`))
}

// credentialJSONString escapes a Go string for safe
// inclusion in a JSON string literal. Same shape as
// the other handlers in this repo; the round-trip via
// fmt.Sprintf avoids gosec-flagged rune→byte casts.
func credentialJSONString(s string) string {
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
