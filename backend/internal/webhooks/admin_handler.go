// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Admin HTTP handler for the webhook CRUD surface
// (v0.7.0). Mounted at /api/v1/webhooks by
// router.go.
//
// Endpoints (all require auth.RequireScope(ScopeWebhooks)
// AND a valid JWT — the auth middleware takes care of
// the JWT part):
//
//	GET    /                         -> list every endpoint
//	GET    /{id}                     -> get a single endpoint
//	POST   /                         -> create an endpoint
//	PATCH  /{id}                     -> partial update
//	DELETE /{id}                     -> hard delete (cascades delivery history)
//
//	GET    /{id}/deliveries          -> delivery history for the endpoint
//	GET    /{id}/deliveries/{did}/retry
//	                                 -> manually trigger the next retry for a
//	                                    failed delivery
//
//	GET    /dlq                      -> every DLQ entry (cross-endpoint)
//	GET    /dlq/{did}                -> single DLQ entry
//	POST   /dlq/{did}/replay         -> take the entry off the queue and
//	                                    dispatch a fresh attempt
//	DELETE /dlq/{did}                -> drop a DLQ entry
//
//	POST   /{id}/test                -> send a synthetic `webhook.test` event
//	                                    to the endpoint (operator-driven
//	                                    end-to-end check)
//
// # Secret redaction
//
// The `secret` field is shown VERBATIM on Create (so
// the operator can copy it to their receiver) and
// as SecretRedacted on every other read. The
// handler enforces the policy so the Store and the
// Service do not have to.
//
// # Why no audit log writes here
//
// The audit log write path lives in
// `internal/audits`; every mutating handler is
// expected to call `audits.RecordFromRequest` after
// a successful write. The v0.2.0 release shipped
// the helper but not the call sites — the call-site
// wiring is a v0.6.x follow-up that lands in a
// single batch across all admin handlers. v0.7.0
// follows the same convention.
//
// # JSON wire format
//
// Every request / response uses snake_case JSON
// keys to match the existing admin surface
// (`users.User`, `plans.Plan`, `nodes.Node`, etc.).
// The `Endpoint` struct tags are already
// snake_case; the request / response shapes here
// mirror them.

package webhooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// AdminRouter returns a chi subrouter for the
// webhook admin surface:
//
//	r.Mount("/webhooks", webhooks.AdminRouter(svc, authSvc.Middleware()))
//
// The mounted subrouter applies ScopeWebhooks to
// every route. Read endpoints are still guarded by
// ScopeWebhooks — the scope gates "can this
// operator see webhook data at all", not "can this
// operator edit it". A read-only scope variant is
// not in v0.7.0.
func AdminRouter(svc *Service, authMiddleware func(http.Handler) http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeWebhooks))

	r.Get("/", svc.handleListEndpoints())
	r.Post("/", svc.handleCreateEndpoint())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGetEndpoint())
		r.Patch("/", svc.handleUpdateEndpoint())
		r.Delete("/", svc.handleDeleteEndpoint())
		r.Get("/deliveries", svc.handleListDeliveries())
		r.Post("/test", svc.handleTestEndpoint())
	})
	// DLQ is cross-endpoint, so it lives at the top
	// level alongside the endpoints list.
	r.Route("/dlq", func(r chi.Router) {
		r.Get("/", svc.handleListDLQ())
		r.Route("/{did}", func(r chi.Router) {
			r.Get("/", svc.handleGetDLQ())
			r.Delete("/", svc.handleDeleteDLQ())
			r.Post("/replay", svc.handleReplayDLQ())
		})
	})
	return r
}

// --- request / response shapes ----------------------------------------

// createEndpointRequest is the POST / body. The
// ID, CreatedAt, and UpdatedAt are NOT accepted
// from the caller — the Service generates them.
type createEndpointRequest struct {
	URL     string   `json:"url"`
	Secret  string   `json:"secret"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"` // defaults to true
}

// updateEndpointRequest is the PATCH /{id} body.
// Every field is a pointer so the handler can
// distinguish "leave alone" (nil) from "set to
// zero / empty" (non-nil & zero).
type updateEndpointRequest struct {
	URL     *string   `json:"url,omitempty"`
	Secret  *string   `json:"secret,omitempty"`
	Events  *[]string `json:"events,omitempty"`
	Enabled *bool     `json:"enabled,omitempty"`
}

// endpointView is the response shape. It is the
// Service's Endpoint struct with the secret
// redacted on every read EXCEPT the immediate
// Create response. The handler enforces the
// redaction policy here so the Store and the
// Service do not have to.
type endpointView struct {
	ID             uuid.UUID   `json:"id"`
	URL            string      `json:"url"`
	Secret         string      `json:"secret"`
	Events         []EventType `json:"events"`
	Enabled        bool        `json:"enabled"`
	LastDeliveryAt *string     `json:"last_delivery_at,omitempty"`
	LastStatusCode *int        `json:"last_status_code,omitempty"`
	CreatedAt      string      `json:"created_at"`
	UpdatedAt      string      `json:"updated_at"`
}

// newEndpointView wraps a Service-returned
// Endpoint in the redacting view. The `redact`
// flag controls whether the secret is shown
// verbatim or replaced with the placeholder.
func newEndpointView(e *Endpoint, redact bool) endpointView {
	v := endpointView{
		ID:        e.ID,
		URL:       e.URL,
		Events:    append([]EventType(nil), e.Events...),
		Enabled:   e.Enabled,
		CreatedAt: e.CreatedAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
		UpdatedAt: e.UpdatedAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00"),
	}
	if !redact {
		v.Secret = e.Secret
	} else {
		v.Secret = SecretRedacted
	}
	if e.LastDeliveryAt != nil {
		s := e.LastDeliveryAt.UTC().Format("2006-01-02T15:04:05.000000Z07:00")
		v.LastDeliveryAt = &s
	}
	if e.LastStatusCode != nil {
		v.LastStatusCode = e.LastStatusCode
	}
	return v
}

// --- handlers ---------------------------------------------------------

// handleListEndpoints serves GET /.
func (s *Service) handleListEndpoints() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := s.List(r.Context())
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		views := make([]endpointView, 0, len(rows))
		for _, e := range rows {
			views = append(views, newEndpointView(e, true))
		}
		writeWebhookJSON(w, http.StatusOK, map[string]any{"endpoints": views})
	}
}

// handleGetEndpoint serves GET /{id}.
func (s *Service) handleGetEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseEndpointID(w, r)
		if !ok {
			return
		}
		e, err := s.Get(r.Context(), id)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeWebhookJSON(w, http.StatusOK, newEndpointView(e, true))
	}
}

// handleCreateEndpoint serves POST /.
func (s *Service) handleCreateEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createEndpointRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebhookError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		enabled := true
		if req.Enabled != nil {
			enabled = *req.Enabled
		}
		events := make([]EventType, 0, len(req.Events))
		for _, e := range req.Events {
			events = append(events, EventType(e))
		}
		in := CreateInput{
			URL:     req.URL,
			Secret:  req.Secret,
			Events:  events,
			Enabled: enabled,
		}
		e, err := s.Create(r.Context(), in)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		// Create returns the secret verbatim. The
		// view-layer policy says: show once on
		// Create, redact forever after.
		writeWebhookJSON(w, http.StatusCreated, newEndpointView(e, false))
	}
}

// handleUpdateEndpoint serves PATCH /{id}.
func (s *Service) handleUpdateEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseEndpointID(w, r)
		if !ok {
			return
		}
		var req updateEndpointRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeWebhookError(w, &ValidationError{Field: "body", Message: "malformed JSON"})
			return
		}
		in := UpdateInput{
			URL:     req.URL,
			Secret:  req.Secret,
			Enabled: req.Enabled,
		}
		if req.Events != nil {
			events := make([]EventType, 0, len(*req.Events))
			for _, e := range *req.Events {
				events = append(events, EventType(e))
			}
			in.Events = &events
		}
		e, err := s.Update(r.Context(), id, in)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeWebhookJSON(w, http.StatusOK, newEndpointView(e, true))
	}
}

// handleDeleteEndpoint serves DELETE /{id}.
func (s *Service) handleDeleteEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseEndpointID(w, r)
		if !ok {
			return
		}
		if err := s.Delete(r.Context(), id); err != nil {
			writeWebhookError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleListDeliveries serves GET /{id}/deliveries.
func (s *Service) handleListDeliveries() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseEndpointID(w, r)
		if !ok {
			return
		}
		limit := parseLimit(r, 0)
		rows, err := s.ListDeliveries(r.Context(), id, limit)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		if rows == nil {
			rows = []*Delivery{}
		}
		writeWebhookJSON(w, http.StatusOK, map[string]any{"deliveries": rows})
	}
}

// handleTestEndpoint serves POST /{id}/test. The
// operator uses this to verify their receiver
// setup end-to-end without having to trigger a
// real panel event.
func (s *Service) handleTestEndpoint() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseEndpointID(w, r)
		if !ok {
			return
		}
		result, err := s.SendTestEvent(r.Context(), id)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeWebhookJSON(w, http.StatusOK, map[string]any{
			"endpoint_id": result.EndpointID,
			"delivery_id": result.DeliveryID,
			"status":      result.Status,
			"status_code": result.StatusCode,
			"error":       result.Error,
			"attempts":    result.Attempts,
		})
	}
}

// handleListDLQ serves GET /dlq.
func (s *Service) handleListDLQ() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		limit := parseLimit(r, 0)
		rows, err := s.ListDLQ(r.Context(), limit)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		if rows == nil {
			rows = []*DLQEntry{}
		}
		writeWebhookJSON(w, http.StatusOK, map[string]any{"dlq": rows})
	}
}

// handleGetDLQ serves GET /dlq/{did}.
func (s *Service) handleGetDLQ() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDLQID(w, r)
		if !ok {
			return
		}
		entry, err := s.GetDLQ(r.Context(), id)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeWebhookJSON(w, http.StatusOK, entry)
	}
}

// handleDeleteDLQ serves DELETE /dlq/{did}.
func (s *Service) handleDeleteDLQ() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDLQID(w, r)
		if !ok {
			return
		}
		if err := s.DeleteDLQ(r.Context(), id); err != nil {
			writeWebhookError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// handleReplayDLQ serves POST /dlq/{did}/replay.
func (s *Service) handleReplayDLQ() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseDLQID(w, r)
		if !ok {
			return
		}
		result, err := s.ReplayDLQEntry(r.Context(), id)
		if err != nil {
			writeWebhookError(w, err)
			return
		}
		writeWebhookJSON(w, http.StatusOK, map[string]any{
			"endpoint_id": result.EndpointID,
			"delivery_id": result.DeliveryID,
			"status":      result.Status,
			"status_code": result.StatusCode,
			"error":       result.Error,
			"attempts":    result.Attempts,
		})
	}
}

// --- helpers ----------------------------------------------------------

// parseEndpointID pulls the {id} URL parameter
// and validates it. On failure it writes a 400
// response and returns ok=false so the caller can
// early-return.
func parseEndpointID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "id")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeWebhookError(w, &ValidationError{Field: "id", Message: fmt.Sprintf("invalid uuid %q", raw)})
		return uuid.Nil, false
	}
	return id, true
}

// parseDLQID is the DLQEntry-id variant. The URL
// parameter is `did` to disambiguate from the
// endpoint-id in nested routes.
func parseDLQID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	raw := chi.URLParam(r, "did")
	id, err := uuid.Parse(raw)
	if err != nil {
		writeWebhookError(w, &ValidationError{Field: "did", Message: fmt.Sprintf("invalid uuid %q", raw)})
		return uuid.Nil, false
	}
	return id, true
}

// parseLimit extracts the optional `?limit=N`
// query parameter. 0 means "default" (the Store
// applies DefaultListLimit / MaxListLimit). The
// query value is parsed as a plain int; a
// malformed value is silently ignored (the Store
// clamps on its own).
func parseLimit(r *http.Request, _ int) int {
	raw := r.URL.Query().Get("limit")
	if raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0
	}
	return n
}

// writeWebhookError maps the well-known Service /
// Store errors to HTTP status codes. Mirrors
// plans.writePlanError and users.writeUserError;
// the duplication is cheaper than a shared
// httpkit for 20 lines of code.
func writeWebhookError(w http.ResponseWriter, err error) {
	var vErr *ValidationError
	switch {
	case errors.Is(err, ErrNotFound):
		writeWebhookJSONError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, ErrDuplicate):
		writeWebhookJSONError(w, http.StatusConflict, err.Error())
	case errors.As(err, &vErr):
		writeWebhookJSONError(w, http.StatusBadRequest, vErr.Error())
	default:
		writeWebhookJSONError(w, http.StatusInternalServerError, err.Error())
	}
}

// writeWebhookJSON / writeWebhookJSONError /
// webhookJSONString are the tiny shims the
// handler needs. They live in this file (not a
// shared httpkit) because every package has its
// own copy of these and the duplication is cheaper
// than a new package dependency for 20 lines of
// code.

func writeWebhookJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeWebhookJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + webhookJSONString(msg) + `}`))
}

// webhookJSONString escapes a Go string for safe
// inclusion in a JSON string literal. Same shape
// as the other handlers in this repo; the
// round-trip via fmt.Sprintf avoids gosec-flagged
// rune→byte casts.
func webhookJSONString(s string) string {
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
