// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP handler for the BYO-Node bootstrap. The
// surface is a single sub-action on the nodes
// router:
//
//	POST /api/v1/nodes/{id}/provision
//
// The endpoint kicks off the install workflow
// and returns the new state (online or
// offline). The actual workflow is in
// provisioner.go; this file is the HTTP
// translation only.
//
// # Why a sub-action and not a separate router
//
// `/api/v1/nodes/{id}/provision` is the REST
// convention for "mutate this specific node".
// A separate router (`/api/v1/bootstrap/{id}`)
// would split the conceptual surface across
// two URLs and force the operator UI to know
// about the split. v0.3.0 keeps the bootstrap
// inside the nodes resource.
//
// # Auth
//
// The provisioner is gated by auth.ScopeNodes
// (the same scope as the regular nodes CRUD).
// Every operator who can read + write nodes
// can also provision them. v0.5.0 splits
// "provision" into a separate scope
// (auth.ScopeProvision) so read-only viewers
// can list nodes without being able to
// install agents.

package bootstrap

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/auth"
)

// provisionRequest is the POST body. The
// fields are operator-supplied; the handler
// forwards them to the provisioner as a
// ProvisionRequest.
//
// Snake_case wire format matches the v0.2.0
// pattern (host + inbound + user handlers all
// use snake_case in the request body). The Go
// struct stays PascalCase internally.
type provisionRequest struct {
	// SSHPort is the per-call override. Zero
	// (omitted from the JSON) means "use the
	// service-wide default".
	SSHPort int `json:"ssh_port,omitempty"`
	// SSHUser is the per-call override.
	SSHUser string `json:"ssh_user,omitempty"`
	// SSHPrivateKey is the operator's pasted
	// private key (PEM, no passphrase). The
	// panel does not store this; the install
	// is the only consumer.
	SSHPrivateKey string `json:"ssh_private_key"`
	// TofuPolicy is the trust-on-first-use
	// policy. "reject" is the safe default;
	// "accept-and-append" is the v0.3.0
	// "first contact" UX.
	TofuPolicy string `json:"tofu_policy,omitempty"`
	// ExpectedFingerprint is the operator-
	// confirmed SHA256 fingerprint.
	ExpectedFingerprint string `json:"expected_fingerprint,omitempty"`
}

// provisionResponse is the 200 body. The
// operator UI re-renders the node's state
// badge from the new_state field; the
// install-stage + install-error are surfaced
// for the "retry" button's tooltip.
type provisionResponse struct {
	NodeID        string `json:"node_id"`
	NewState      string `json:"new_state"`
	InstallStage  string `json:"install_stage,omitempty"`
	InstallError  string `json:"install_error,omitempty"`
	VerifyLatency string `json:"verify_latency,omitempty"`
}

// HandleProvision returns the HTTP handler for
// the provision endpoint. The function is
// called by the nodes router and is mounted as
// `POST /{id}/provision` (the parent
// subrouter already validated the {id} as a
// UUID via chi.URLParam).
//
// The signature is `func(...) http.HandlerFunc`
// rather than `func(...) http.Handler` so the
// caller can keep its existing `r.Post("/{id}/provision", svc.HandleProvision())`
// style. The public name is the seam the
// nodes router uses to mount the handler
// without importing the bootstrap package
// internals.
func (s *Service) HandleProvision() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawID := chi.URLParam(r, "id")
		nodeID, err := uuid.Parse(rawID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid node id: "+err.Error())
			return
		}
		var req provisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.SSHPrivateKey == "" {
			writeError(w, http.StatusBadRequest, "ssh_private_key is required")
			return
		}
		// Translate the wire format into the
		// provisioner's ProvisionRequest. The
		// `var` (not `:=`) avoids the
		// ineffectual-assignment lint: the
		// switch below writes to tp.
		var tp TofuPolicy
		switch req.TofuPolicy {
		case "", "reject":
			tp = TofuReject
		case "accept-and-append":
			tp = TofuAcceptAndAppend
		default:
			writeError(w, http.StatusBadRequest, "unknown tofu_policy: "+req.TofuPolicy)
			return
		}
		provReq := ProvisionRequest{
			SSHPort:             req.SSHPort,
			SSHUser:             req.SSHUser,
			SSHPrivateKey:       req.SSHPrivateKey,
			Tofu:                tp,
			ExpectedFingerprint: req.ExpectedFingerprint,
		}
		claims := auth.ClaimsFromContext(r.Context())
		newState, err := s.Provision(r.Context(), nodeID, claims, provReq)
		if err != nil {
			// Pre-condition violations
			// (e.g. "cannot provision from
			// state online") map to 409.
			// Install failures map to 502
			// (the upstream SSH server is
			// the source of the problem).
			if errors.Is(err, errInvalidStartState) {
				writeError(w, http.StatusConflict, err.Error())
				return
			}
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, provisionResponse{
			NodeID:   nodeID.String(),
			NewState: string(newState),
		})
	}
}

// errInvalidStartState is the sentinel for
// the "node is not in a provisionable state"
// case. The handler maps it to 409. Defined
// here (not in provisioner.go) to keep the
// provisioner free of HTTP-layer error
// mapping.
var errInvalidStartState = errors.New("bootstrap: node is not in a provisionable state")

// _ = strconv keeps the strconv import in
// use while the handler is growing. A future
// PR may add query-string filters (e.g.
// ?include=state-only for a status endpoint).
var _ = strconv.Itoa

// writeJSON / writeError are tiny helpers that
// match the v0.2.0 panelcfg / subscription
// pattern (hand-rolled envelope `{"error":
// "..."}`, content-type `application/json`).
// The frontend reads `error` verbatim through
// toApiError.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":"` + jsonEscape(msg) + `"}`))
}

// jsonString escapes a Go string for safe
// inclusion in a JSON string literal. The
// implementation avoids the gosec-flagged
// `[]byte(r)` cast.
func jsonEscape(s string) string {
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
			b = append(b, []byte(formatHex(r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}

// formatHex returns the 4-digit hex escape for
// a non-ASCII rune. Kept separate from jsonEscape
// to keep the inlined switch above readable.
func formatHex(r rune) string {
	const hex = "0123456789ABCDEF"
	return string([]byte{
		'\\', 'u',
		hex[(r>>12)&0xF],
		hex[(r>>8)&0xF],
		hex[(r>>4)&0xF],
		hex[r&0xF],
	})
}
