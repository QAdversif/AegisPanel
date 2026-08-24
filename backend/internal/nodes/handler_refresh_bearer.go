// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the v0.8.7
// `POST /api/v1/nodes/{id}/refresh-agent-bearer`
// endpoint. The handler is the operator-
// side entry point for the
// `nodes.Service.RefreshAgentBearer` flow
// (see `refresh_bearer.go` for the
// Service-level docstring; the
// security shape, error mapping, and
// audit-row shape are documented there).
//
// # Wire format
//
//	POST /api/v1/nodes/{id}/refresh-agent-bearer
//	{
//	  "ssh_port": 22,        // optional, default = node.Address port
//	  "ssh_user": "root"     // optional, default = service-wide
//	}
//
//	200 OK
//	{
//	  "node_id":              "...",
//	  "bearer":               "<hex>",
//	  "key_fingerprint":      "SHA256:..."
//	}
//
// # Auth
//
// The parent nodes router already enforces
// `auth.RequireScope(auth.ScopeNodes)` so the
// handler does not re-check. The same
// scope that grants the v0.8.5 GET
// `/stored-key` (read) and the v0.8.4
// rotate-panel-key (write) also grants this
// recovery write — there is no separate
// "refresh" scope because the operation is
// logically a "the agent bearer I have
// stored is wrong; give me the one that's
// actually on the node" sync.
//
// # Error mapping
//
//   - 400: malformed body
//   - 404: node not found (`ErrNotFound` from
//     the underlying Store)
//   - 409: no stored key
//     (`ErrNoStoredKey` from
//     `GetStoredKeyForUse`). The HTTP layer
//     surfaces this as a specific status
//     with a "rotate-panel-key first" hint
//     in the body so the operator UI can
//     show the right next-step button.
//   - 500: envelope not configured,
//     known_hosts path not configured,
//     generic Service error
//   - 502: SSH connect failure, SSH run
//     failure, agent.env parse failure,
//     SetAgentBearer failure. The 502
//     signals "the panel made the call but
//     the remote node rejected it" — the
//     same code class the v0.8.4
//     rotate-panel-key handler uses for
//     "the SSH session died before we
//     could finish".
//
// # Audit
//
// The audit row is recorded by the Service
// (via `audits.RecordFromContext`) AFTER the
// `SetAgentBearer` call. The handler does
// not record its own audit row; the
// Service-level audit is the single
// source of truth. The HTTP handler's
// 200 response is the "operator saw the
// result" signal; the audit row is the
// "operator triggered a refresh" signal.

package nodes

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/httpjson"
)

// refreshAgentBearerRequest is the POST
// body. The fields are operator-supplied
// per-call overrides; the service-level
// defaults (and the row's stored
// `Address`) fill in any omitted fields.
// The body is also accepted as empty
// (`{}` or no body at all) for the
// "use defaults" case.
type refreshAgentBearerRequest struct {
	// SSHPort is the per-call override.
	// Zero (omitted) means "use the
	// node's stored address (port
	// component of `Address`)".
	SSHPort int `json:"ssh_port,omitempty"`
	// SSHUser is the per-call override.
	// Empty (omitted) means "use the
	// service-wide default
	// (cfg.AgentSSHUser)".
	SSHUser string `json:"ssh_user,omitempty"`
}

// refreshAgentBearerResponse is the 200
// body. The `Bearer` field carries the
// new agent bearer (the value the panel
// will use for subsequent POST /v1/apply
// calls). The `KeyFingerprintSHA256` is
// the SHA-256 of the public key derived
// from the stored private key — same
// string `ssh-keygen -lf` reports, so
// the operator can verify the refresh
// used the key they expect.
type refreshAgentBearerResponse struct {
	NodeID               string `json:"node_id"`
	Bearer               string `json:"bearer"`
	KeyFingerprintSHA256 string `json:"key_fingerprint"`
}

// handleRefreshAgentBearer returns the
// HTTP handler for the v0.8.7
// refresh-agent-bearer endpoint. The
// function is called by the nodes router
// and is mounted as
// `POST /{id}/refresh-agent-bearer`
// (the parent subrouter already validated
// the {id} as a UUID via chi.URLParam).
//
// The signature is `func(...) http.HandlerFunc`
// (not `func(...) http.Handler`) so the
// caller can keep its existing
// `r.Post("/{id}/refresh-agent-bearer", svc.handleRefreshAgentBearer())`
// pattern (matches the v0.8.5 GET
// `/stored-key` and the v0.8.4
// rotate-panel-key POST).
func (s *Service) handleRefreshAgentBearer() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		// The body is optional (an empty
		// body means "use defaults"). The
		// `json.NewDecoder` on an empty
		// reader returns `io.EOF`, which
		// is NOT a 400 — it's "no body
		// was supplied, that's fine, use
		// defaults". The decoder also
		// silently ignores unknown fields
		// by default, which is what we
		// want for forward-compat.
		var req refreshAgentBearerRequest
		if r.ContentLength > 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				// `io.EOF` is the
				// "empty body" case
				// — also a 400 to
				// keep the surface
				// explicit.
				httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
				return
			}
		}
		// Pre-flight scope check (defence
		// in depth: the parent router
		// already enforced `ScopeNodes`,
		// but the v0.8.7 audit row
		// should record the operator
		// id; if the auth context is
		// missing, fail closed).
		claims := auth.ClaimsFromContext(r.Context())
		if claims == nil {
			httpjson.WriteError(w, http.StatusUnauthorized, "missing auth claims")
			return
		}
		// Delegate to the Service. The
		// Service records the audit row
		// via `audits.RecordFromContext`
		// (which pulls the operator id
		// from the same `claims`).
		out, err := s.RefreshAgentBearer(r.Context(), id, RefreshBearerOptions{
			SSHPort: req.SSHPort,
			SSHUser: req.SSHUser,
		})
		if err != nil {
			// Differentiate the error
			// classes for the operator
			// UI. The shape mirrors the
			// v0.8.5 GET `/stored-key`
			// handler and the v0.8.4
			// rotate-panel-key POST.
			if errors.Is(err, ErrNotFound) {
				httpjson.WriteError(w, http.StatusNotFound, "node not found")
				return
			}
			if errors.Is(err, ErrNoStoredKey) {
				// 409: the row
				// exists but has
				// no stored key.
				// The operator
				// must
				// rotate-panel-key
				// first.
				httpjson.WriteError(w, http.StatusConflict, "no stored panel SSH key (rotate-panel-key first)")
				return
			}
			msg := err.Error()
			switch {
			case strings.Contains(msg, "SSH client factory is not configured"),
				strings.Contains(msg, "known_hosts path is not configured"):
				// 500: panel
				// wiring
				// missing.
				httpjson.WriteError(w, http.StatusInternalServerError, msg)
			case strings.Contains(msg, "envelope is not configured"):
				httpjson.WriteError(w, http.StatusInternalServerError, msg)
			case strings.Contains(msg, "SSH connect"),
				strings.Contains(msg, "read agent.env"),
				strings.Contains(msg, "parse agent.env"),
				strings.Contains(msg, "persist bearer"):
				// 502: the
				// remote
				// node
				// rejected
				// the
				// call
				// (SSH
				// handshake,
				// file
				// read,
				// parse,
				// or
				// DB
				// update
				// failed).
				httpjson.WriteError(w, http.StatusBadGateway, msg)
			default:
				httpjson.WriteError(w, http.StatusInternalServerError, msg)
			}
			return
		}
		// Success. The 200 body carries
		// the new bearer + key
		// fingerprint so the operator UI
		// can surface them in a
		// "refresh result" card (same
		// UX pattern as the v0.8.4
		// rotate-panel-key).
		httpjson.WriteJSON(w, http.StatusOK, refreshAgentBearerResponse{
			NodeID:               out.NodeID.String(),
			Bearer:               out.Bearer,
			KeyFingerprintSHA256: out.KeyFingerprintSHA256,
		})
	}
}
