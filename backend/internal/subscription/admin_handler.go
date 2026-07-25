// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Admin HTTP handler for the user CRUD surface.
// Mounted at /api/v1/users by the router; every
// endpoint requires the `users` scope (so a
// read-only operator can list + read but not
// create / edit / rotate sub_tokens).
//
// The endpoint shape:
//
//	GET  /                  -> list every user
//	GET  /{id}              -> get a single user
//	POST /                  -> create a user
//	PATCH /{id}             -> partial update
//	POST /{id}/rotate-token -> rotate the sub_token
//
// The rotate-token endpoint is separate from the
// panel-wide sub_path rotation (panelcfg). The
// per-user sub_token is the credential the end
// user pastes into a VPN client; the sub_path is
// the URL prefix. The two are independent — an
// operator can rotate the user's token without
// rotating the panel path, and vice versa.
//
// # d-refactor.2 split
//
// As of d-refactor.2 the user-CRUD surface has moved
// to `internal/users`. The handler here talks to the
// users package directly for the two read/mutate
// paths (Get / Update), and to subscription.Service's
// thin wrappers for the four admin actions that
// already had a thin-wrapper path (Create / List /
// Rotate / future Read-by-token). d-refactor.3 will
// move this entire file to `internal/users/admin_handler.go`
// and replace it with a one-line re-export alias
// here (kept for the legacy mount point).

package subscription

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/users"
)

// AdminRouter returns a chi subrouter for the user
// admin surface:
//
//	r.Mount("/users", subscription.AdminRouter(svc, authSvc.Middleware()))
//
// The mounted subrouter applies ScopeUsers to every
// route. Read endpoints are still guarded by
// ScopeUsers — the scope gates "can this operator
// see user data at all", not "can this operator
// edit it". A read-only scope variant is not in
// v0.2.0 (the panel's access model is admin-or-not);
// the v1.0 panel will introduce a ScopeUsersRead.
//
// The d-refactor.2 signature takes a
// *subscription.Service; the user-CRUD thin
// wrappers on Service (Create / List / Rotate) and
// the direct s.users access for Get / Update keep
// the handler internal to the subscription package
// for now. d-refactor.3 will move this file to
// `internal/users` and the signature will become
// `users.AdminRouter(usersSvc, ...)`; the call
// site in `router.Build` updates in the same PR.
func AdminRouter(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeUsers))

	r.Get("/", svc.handleListUsers())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGetUser())
		r.Patch("/", svc.handleUpdateUser())
		r.Post("/rotate-token", svc.handleRotateSubToken())
	})
	r.Post("/", svc.handleCreateUser())
	return r
}

// --- request / response shapes ----------------------------------------

// createUserRequest is the POST / body. The
// sub_token is NOT accepted from the caller — the
// Service generates one.
type createUserRequest struct {
	Username          string      `json:"username"`
	Status            UserStatus  `json:"status,omitempty"`
	PlanID            *uuid.UUID  `json:"plan_id,omitempty"`
	ExpireAt          *string     `json:"expire_at,omitempty"`
	TrafficLimitBytes int64       `json:"traffic_limit_bytes,omitempty"`
	DeviceLimit       int         `json:"device_limit,omitempty"`
	HostsAllowlist    []uuid.UUID `json:"hosts_allowlist,omitempty"`
	HostsBlocklist    []uuid.UUID `json:"hosts_blocklist,omitempty"`
}

// updateUserRequest is the PATCH /{id} body. All
// fields are optional; the absence of a key means
// "leave unchanged".
type updateUserRequest struct {
	Username          *string      `json:"username,omitempty"`
	Status            *UserStatus  `json:"status,omitempty"`
	PlanID            *uuid.UUID   `json:"plan_id,omitempty"`
	ClearPlanID       bool         `json:"clear_plan_id,omitempty"`
	ExpireAt          *string      `json:"expire_at,omitempty"`
	ClearExpireAt     bool         `json:"clear_expire_at,omitempty"`
	TrafficLimitBytes *int64       `json:"traffic_limit_bytes,omitempty"`
	DeviceLimit       *int         `json:"device_limit,omitempty"`
	HostsAllowlist    *[]uuid.UUID `json:"hosts_allowlist,omitempty"`
	HostsBlocklist    *[]uuid.UUID `json:"hosts_blocklist,omitempty"`
}

// rotateTokenRequest is the POST /{id}/rotate-token
// body. Both fields are optional.
type rotateTokenRequest struct {
	GraceWindowSeconds *int `json:"grace_window_seconds,omitempty"`
}

// --- handlers ---------------------------------------------------------

func (s *Service) handleListUsers() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.ListUsers(r.Context())
		if err != nil {
			writeUserError(w, err)
			return
		}
		if rows == nil {
			rows = []*User{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"users": rows})
	}
}

func (s *Service) handleGetUser() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUserID(w, r)
		if !ok {
			return
		}
		// As of d-refactor.2 the user-CRUD reads go
		// through the users package directly; the
		// subscription.Service does not have a
		// GetByID thin wrapper yet. d-refactor.3
		// will introduce one (or move this handler
		// out of this file).
		if s.users == nil {
			writeUserError(w, &ValidationError{Field: "users", Message: "users store is not wired"})
			return
		}
		u, err := s.users.GetByID(r.Context(), id)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	}
}

func (s *Service) handleCreateUser() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeUserError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		expireAt, err := parseISO8601Ptr(req.ExpireAt)
		if err != nil {
			writeUserError(w, &ValidationError{Field: "expire_at", Message: err.Error()})
			return
		}
		u, err := s.CreateUser(r.Context(), CreateUserInput{
			Username:          req.Username,
			Status:            req.Status,
			PlanID:            req.PlanID,
			ExpireAt:          expireAt,
			TrafficLimitBytes: req.TrafficLimitBytes,
			DeviceLimit:       req.DeviceLimit,
			HostsAllowlist:    req.HostsAllowlist,
			HostsBlocklist:    req.HostsBlocklist,
		})
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, u)
	}
}

func (s *Service) handleUpdateUser() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUserID(w, r)
		if !ok {
			return
		}
		var req updateUserRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeUserError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		expireAt, err := parseISO8601Ptr(req.ExpireAt)
		if err != nil {
			writeUserError(w, &ValidationError{Field: "expire_at", Message: err.Error()})
			return
		}
		// Translate the legacy updateUserRequest
		// into users.UpdateInput. The clear_plan_id
		// / clear_expire_at flags become a uuid.Nil
		// / nil pointer respectively. As of
		// d-refactor.2 the translation lives here
		// (not in Service) because d-refactor.3 will
		// move this whole file to users and the
		// translation becomes a direct
		// users.UpdateInput construction.
		// `req.Status` is `*UserStatus`, and
		// `UserStatus` is a type alias for
		// `users.Status` (see subscription.go), so
		// the pointer types are interchangeable
		// without a cast. The assignment is the
		// shape the users.Service.Update expects.
		in := users.UpdateInput{
			Username:          req.Username,
			Status:            req.Status,
			PlanID:            req.PlanID,
			ExpireAt:          expireAt,
			TrafficLimitBytes: req.TrafficLimitBytes,
			DeviceLimit:       req.DeviceLimit,
			HostsAllowlist:    req.HostsAllowlist,
			HostsBlocklist:    req.HostsBlocklist,
		}
		if req.ClearPlanID && req.PlanID == nil {
			nilUUID := uuid.Nil
			in.PlanID = &nilUUID
		}
		if req.ClearExpireAt {
			in.ExpireAt = nil
		}
		// As of d-refactor.2 the user-CRUD updates
		// go through the users package directly.
		if s.usersSvc == nil {
			writeUserError(w, &ValidationError{Field: "users", Message: "users service is not wired"})
			return
		}
		u, err := s.usersSvc.Update(r.Context(), id, in)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	}
}

func (s *Service) handleRotateSubToken() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseUserID(w, r)
		if !ok {
			return
		}
		var req rotateTokenRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeUserError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
				return
			}
		}
		grace := graceFromSeconds(req.GraceWindowSeconds)
		u, err := s.RotateSubToken(r.Context(), id, grace)
		if err != nil {
			writeUserError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, u)
	}
}

// parseUserID pulls the {id} URL parameter and
// validates it. On failure it writes a 400 response
// and returns ok=false so the caller can early-return.
func parseUserID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeUserError(w, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid uuid %q", raw)})
		return uuid.Nil, false
	}
	return id, true
}

// parseISO8601Ptr parses a *string ISO-8601 timestamp
// into a *time.Time. Nil input returns nil (the
// "field was absent" case). Empty-string input also
// returns nil. Otherwise the input is parsed with
// time.RFC3339Nano; a parse error is returned so
// the caller can surface a 400.
func parseISO8601Ptr(s *string) (*time.Time, error) {
	if s == nil || *s == "" {
		return nil, nil
	}
	t, err := time.Parse(time.RFC3339Nano, *s)
	if err != nil {
		// Try the older RFC3339 form (no fractional
		// seconds) as a fallback — many clients
		// emit it.
		t, err2 := time.Parse(time.RFC3339, *s)
		if err2 != nil {
			return nil, fmt.Errorf("invalid ISO-8601 timestamp %q", *s)
		}
		return &t, nil
	}
	return &t, nil
}

// graceFromSeconds is a tiny helper to keep the
// handler small. Nil / zero / negative means "no
// grace" (the 3X-UI convention); the server caps at
// 1h to prevent a fat-finger entry.
func graceFromSeconds(s *int) time.Duration {
	if s == nil || *s <= 0 {
		return 0
	}
	if *s > 3600 {
		return time.Hour
	}
	return time.Duration(*s) * time.Second
}

// writeUserError maps the well-known Store /
// Service errors to HTTP status codes. As of
// d-refactor.2 the error space is the union of the
// subscription package's sentinels (the thin
// wrappers return them) and the users package's
// sentinels (the direct s.users.* and s.usersSvc.*
// calls in handleGetUser / handleUpdateUser return
// them). The mapper knows both shapes. Anything
// else is a 500.
func writeUserError(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	var usersVErr *users.ValidationError
	var nferr *NotFoundError
	switch {
	case errors.As(err, &nferr):
		writeJSONError(w, http.StatusNotFound, nferr.Error())
	case errors.Is(err, ErrNotFound), errors.Is(err, users.ErrNotFound):
		writeJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicate), errors.Is(err, users.ErrDuplicate):
		writeJSONError(w, http.StatusConflict, err.Error())
	case errors.As(err, &vErr):
		writeJSONError(w, http.StatusBadRequest, vErr.Error())
	case errors.As(err, &usersVErr):
		// users.ValidationError satisfies errors.As
		// even when wrapped, so this branch covers
		// the users package's per-field validation
		// errors.
		writeJSONError(w, http.StatusBadRequest, usersVErr.Error())
	default:
		writeJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion
// in a JSON string literal. Same shape as the
// other handlers in this repo; the round-trip via
// fmt.Sprintf avoids gosec-flagged rune→byte casts.
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
