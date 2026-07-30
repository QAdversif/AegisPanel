// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Admin HTTP handler for the plan CRUD surface
// (v0.6.0). Mounted at /api/v1/plans by router.go.
//
// Endpoints (all require auth.RequireScope(ScopePlans)
// AND a valid JWT — the auth middleware takes care of
// the JWT part):
//
//	GET    /         -> list every plan
//	GET    /{id}     -> get a single plan
//	POST   /         -> create a plan
//	PATCH  /{id}     -> partial update
//	DELETE /{id}     -> hard delete
//
// # Why DELETE is allowed
//
// Unlike the users admin surface (#113) — which
// intentionally does NOT expose DELETE because the
// v0.2.x design uses Status = 'deleted' as a
// soft-delete — the plans table is a simple
// catalog. There is no per-row state machine, no
// traffic counter, no per-user override. A plan
// either exists or it does not. The
// `users.plan_id` column has no FK constraint
// (migration 0001), so a hard delete leaves
// users with a dangling plan_id; the
// subscription package's ListPoolsForUser
// handles dangling plan IDs by returning an
// empty pool list, so the user silently loses
// access to the plan's pools. The UI shows a
// confirm dialog that lists the affected user
// count before DELETE; v0.6.x adds a
// `?cascade=true` query param to bulk-unlink
// users on delete.
//
// # Why no audit log writes here
//
// The audit log write path lives in
// `internal/audits`; every mutating handler is
// expected to call `audits.RecordFromRequest`
// after a successful write. The v0.2.0 release
// shipped the helper but not the call sites —
// the call-site wiring is a v0.3 follow-up.
// v0.6.0 follows the same convention: the
// audit-log wiring lands in a single batch
// across all admin handlers (nodes, hosts,
// inbounds, users, plans, panelcfg).
//
// # JSON wire format
//
// Every request / response uses snake_case JSON
// keys to match the existing admin surface
// (`users.User`, `nodes.Node`, `hosts.Host`,
// …). The `Plan` struct tags are already
// snake_case; the request / response shapes
// here mirror them.

package plans

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// AdminRouter returns a chi subrouter for the plan
// admin surface:
//
//	r.Mount("/plans", plans.AdminRouter(svc, authSvc.Middleware()))
//
// The mounted subrouter applies ScopePlans to every
// route. Read endpoints are still guarded by
// ScopePlans — the scope gates "can this operator
// see plan data at all", not "can this operator
// edit it". A read-only scope variant is not in
// v0.6.0; the v1.0 panel will introduce a
// ScopePlansRead.
func AdminRouter(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopePlans))

	r.Get("/", svc.handleListPlans())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGetPlan())
		r.Patch("/", svc.handleUpdatePlan())
		r.Delete("/", svc.handleDeletePlan())
	})
	r.Post("/", svc.handleCreatePlan())
	return r
}

// --- request / response shapes ----------------------------------------

// createPlanRequest is the POST / body. The ID,
// CreatedAt, and UpdatedAt are NOT accepted from
// the caller — the Service generates them.
type createPlanRequest struct {
	Name              string `json:"name"`
	TrafficLimitBytes int64  `json:"traffic_limit_bytes,omitempty"`
	// Duration is in nanoseconds. The front-end
	// converts a human-readable "30 days" string
	// to ns before sending. The Service validates
	// the [MinDuration, MaxDuration] range and
	// returns 400 on out-of-range.
	Duration    int64  `json:"duration_ns"`
	DeviceLimit int    `json:"device_limit,omitempty"`
	ResetPeriod string `json:"reset_period,omitempty"` // defaults to "monthly"
	PriceCents  int64  `json:"price_cents,omitempty"`
}

// updatePlanRequest is the PATCH /{id} body. Every
// field is optional; the absence of a key means
// "leave unchanged". String pointers let us
// distinguish "leave alone" (nil) from "set to
// empty string" (non-nil & empty).
type updatePlanRequest struct {
	Name              *string `json:"name,omitempty"`
	TrafficLimitBytes *int64  `json:"traffic_limit_bytes,omitempty"`
	Duration          *int64  `json:"duration_ns,omitempty"`
	DeviceLimit       *int    `json:"device_limit,omitempty"`
	ResetPeriod       *string `json:"reset_period,omitempty"`
	PriceCents        *int64  `json:"price_cents,omitempty"`
}

// --- handlers ---------------------------------------------------------

func (s *Service) handleListPlans() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.List(r.Context())
		if err != nil {
			writePlanError(w, err)
			return
		}
		if rows == nil {
			rows = []*Plan{}
		}
		writePlanJSON(w, http.StatusOK, map[string]any{"plans": rows})
	}
}

func (s *Service) handleGetPlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePlanID(w, r)
		if !ok {
			return
		}
		p, err := s.Get(r.Context(), id)
		if err != nil {
			writePlanError(w, err)
			return
		}
		writePlanJSON(w, http.StatusOK, p)
	}
}

func (s *Service) handleCreatePlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createPlanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writePlanError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		in := CreateInput{
			Name:              req.Name,
			TrafficLimitBytes: req.TrafficLimitBytes,
			Duration:          time.Duration(req.Duration),
			DeviceLimit:       req.DeviceLimit,
			ResetPeriod:       ResetPeriod(req.ResetPeriod),
			PriceCents:        req.PriceCents,
		}
		p, err := s.Create(r.Context(), in)
		if err != nil {
			writePlanError(w, err)
			return
		}
		writePlanJSON(w, http.StatusCreated, p)
	}
}

func (s *Service) handleUpdatePlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePlanID(w, r)
		if !ok {
			return
		}
		var req updatePlanRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writePlanError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		in := UpdateInput{
			Name:              req.Name,
			TrafficLimitBytes: req.TrafficLimitBytes,
			DeviceLimit:       req.DeviceLimit,
			PriceCents:        req.PriceCents,
		}
		if req.Duration != nil {
			d := time.Duration(*req.Duration)
			in.Duration = &d
		}
		if req.ResetPeriod != nil {
			rp := ResetPeriod(*req.ResetPeriod)
			in.ResetPeriod = &rp
		}
		p, err := s.Update(r.Context(), id, in)
		if err != nil {
			writePlanError(w, err)
			return
		}
		writePlanJSON(w, http.StatusOK, p)
	}
}

func (s *Service) handleDeletePlan() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parsePlanID(w, r)
		if !ok {
			return
		}
		if err := s.Delete(r.Context(), id); err != nil {
			writePlanError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// --- helpers ----------------------------------------------------------

// parsePlanID pulls the {id} URL parameter and
// validates it. On failure it writes a 400 response
// and returns ok=false so the caller can early-return.
func parsePlanID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writePlanError(w, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid uuid %q", raw)})
		return uuid.Nil, false
	}
	return id, true
}

// writePlanError maps the well-known Service / Store
// errors to HTTP status codes. Mirrors
// users.writeUserError; the duplication is cheaper
// than a shared httpkit for 20 lines of code.
func writePlanError(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writePlanJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicate):
		writePlanJSONError(w, http.StatusConflict, err.Error())
	case errors.As(err, &vErr):
		writePlanJSONError(w, http.StatusBadRequest, vErr.Error())
	default:
		writePlanJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

// writePlanJSON / writePlanJSONError / planJSONString
// are the tiny shims the handler needs. They live
// in this file (not a shared httpkit) because every
// package has its own copy of these and the
// duplication is cheaper than a new package
// dependency for 20 lines of code.

func writePlanJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writePlanJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + planJSONString(msg) + `}`))
}

// planJSONString escapes a Go string for safe
// inclusion in a JSON string literal. Same shape
// as the other handlers in this repo; the
// round-trip via fmt.Sprintf avoids
// gosec-flagged rune→byte casts.
func planJSONString(s string) string {
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
