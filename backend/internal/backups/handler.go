// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the panel-side backup surface
// (v0.5.0). Mounted at /api/v1/backups by router.go.
//
// Endpoints (all require auth.RequireScope(ScopeBackups)
// AND a valid JWT — the auth middleware takes care of
// the JWT part):
//
//	POST   /                        -> create (202 + body)
//	GET    /                        -> list   (200 + array)
//	GET    /{id}                    -> get    (200 + body)
//	GET    /{id}/download           -> stream (200 + application/gzip)
//	DELETE /{id}                    -> delete (204)
//	POST   /{id}/restore            -> restore (202)
//
// Restore is gated by Config.AllowUIRestore; the
// default is OFF in production. The CLI binary
// (cmd/aegis-pg-restore, future PR) bypasses the
// HTTP path entirely and calls Service.Restore
// directly after a separate process-level check
// (the CLI is the only thing trusted to drop the
// panel DB).
//
// # Why the 202 (Accepted) on create
//
// The Create call is single-flight; the second
// concurrent caller gets ErrBackupInProgress which
// the handler maps to 409 (Conflict). The first
// caller returns 202 to indicate "I accepted your
// request; the work is in progress; poll GET /{id}
// to see when it finishes".

package backups

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// Handler is the http.Handler for the backups
// surface. The Service is the orchestrator; the
// Handler is a thin HTTP wrapper.
type Handler struct {
	svc  *Service
	auth func(http.Handler) http.Handler // pre-validated JWT middleware
}

// NewHandler returns a Handler bound to svc. The
// auth argument is the standard panel auth
// middleware (jwt verify + claims on context); the
// Handler also stacks the `ScopeBackups` requirement
// on top.
func NewHandler(svc *Service, authMW func(http.Handler) http.Handler) *Handler {
	return &Handler{svc: svc, auth: authMW}
}

// Mount returns an http.Handler to be r.Mount()'d at
// `/api/v1/backups` by the router. The shape mirrors
// `users.AdminRouter` and `nodes.Router`.
func (h *Handler) Mount() http.Handler {
	r := chi.NewRouter()
	r.Use(h.auth)
	r.Use(auth.RequireScope(auth.ScopeBackups))

	r.Post("/", h.handleCreate())
	r.Get("/", h.handleList())
	// /schedule is mounted BEFORE the /{id} route so
	// chi's path matcher does not try to bind
	// "schedule" as a backup id. (The order matters
	// in chi: a `r.Get("/{id}", ...)` registered
	// before `r.Get("/schedule", ...)` would
	// greedily match /schedule.)
	r.Get("/schedule", h.handleGetSchedule())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", h.handleGet())
		r.Get("/download", h.handleDownload())
		r.Delete("/", h.handleDelete())
		r.Post("/restore", h.handleRestore())
	})
	return r
}

type createRequest struct {
	Trigger Trigger `json:"trigger,omitempty"`
}

func (h *Handler) handleCreate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		// Body is optional; an empty body defaults
		// to TriggerManual.
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
				return
			}
		}
		if req.Trigger == "" {
			req.Trigger = TriggerManual
		}
		if req.Trigger != TriggerManual && req.Trigger != TriggerScheduled {
			writeError(w, http.StatusBadRequest, "trigger must be 'manual' or 'scheduled'")
			return
		}
		row, err := h.svc.Create(r.Context(), req.Trigger)
		if err != nil {
			if errors.Is(err, ErrBackupInProgress) {
				writeError(w, http.StatusConflict, "another backup is in progress")
				return
			}
			// Create returns the (failed) row + an
			// error; surface the row in the body so
			// the operator can see the failure
			// immediately.
			if row != nil {
				writeJSON(w, http.StatusInternalServerError, row)
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, row)
	}
}

func (h *Handler) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rows, err := h.svc.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, rows)
	}
}

func (h *Handler) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		row, err := h.svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, "backup not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, row)
	}
}

func (h *Handler) handleDownload() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		row, err := h.svc.Get(r.Context(), id)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, "backup not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		f, err := h.svc.Open(r.Context(), id)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		defer closeQuiet(f)
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename=%q`, row.Path))
		w.WriteHeader(http.StatusOK)
		if _, err := io.Copy(w, f); err != nil {
			log.Warn().Err(err).Str("backup_id", id).Msg("backups: client disconnected mid-download")
		}
	}
}

func (h *Handler) handleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := h.svc.Delete(r.Context(), id); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func (h *Handler) handleRestore() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		if err := h.svc.Restore(r.Context(), id); err != nil {
			if errors.Is(err, ErrBackupDisabled) {
				writeError(w, http.StatusForbidden, err.Error())
				return
			}
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, "backup not found")
				return
			}
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}
}

// handleGetSchedule returns the current backup
// schedule + retention policy. The endpoint is
// read-only — the operator edits the env var
// (`AEGIS_BACKUPS_CRON`) and restarts the panel to
// apply changes. A POST endpoint for hot-reload
// is deferred to v0.9.1 per the Tier 1 #3 plan.
//
// Response shape (v0.9.x):
//
//	{
//	  "cron":           "0 2 * * *",  // 5-field Vixie expression; "" = manual-only
//	  "retentionDays":  30,            // 0 = unlimited
//	  "maxCount":       0,             // 0 = unlimited
//	  "scheduleActive": true           // false when no scheduler is running
//	}
//
// The cron field reflects the live scheduler
// expression (a hot-reload is visible immediately
// without a restart), not the boot-time config
// value. retentionDays + maxCount are read off
// cfg at request time (the panel does not support
// hot-reloading retention).
func (h *Handler) handleGetSchedule() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		expr, _, active := h.svc.Schedule()
		writeJSON(w, http.StatusOK, map[string]any{
			"cron":           expr,
			"retentionDays":  h.svc.Cfg().RetentionDays,
			"maxCount":       h.svc.Cfg().MaxCount,
			"scheduleActive": active,
		})
	}
}

// writeJSON serialises v as JSON and writes it to
// w with the given status. Errors during write are
// logged but not surfaced (the response is already
// partially sent).
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Warn().Err(err).Msg("backups: write JSON response")
	}
}

// writeError writes a small JSON error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	body := map[string]string{"error": msg}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// hashHex is a tiny helper used by the tests to
// produce deterministic checksums. Production code
// uses hashFile directly.
func hashHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}
