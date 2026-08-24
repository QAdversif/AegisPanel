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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/httpjson"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
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
	// is the only consumer. Mutually
	// exclusive with SSHPassword — exactly
	// one of the two must be set.
	SSHPrivateKey string `json:"ssh_private_key,omitempty"`
	// SSHPassword is the operator's SSH login
	// password for first-time auth on a fresh
	// node. The panel does not store this; the
	// install is the only consumer. After the
	// install completes the agent switches
	// to bearer-token auth so the password is
	// never reused. Mutually exclusive with
	// SSHPrivateKey.
	SSHPassword string `json:"ssh_password,omitempty"`
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
			httpjson.WriteError(w, http.StatusBadRequest, "invalid node id: "+err.Error())
			return
		}
		var req provisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		// Exactly one auth method: key XOR password.
		// Both set → 400 (ambiguous). Neither set
		// → 400 (no auth). This is the same
		// contract the SSH client enforces; the
		// HTTP-layer check gives a clearer error
		// to the operator than the 502 the SSH
		// client would produce.
		hasKey := req.SSHPrivateKey != ""
		hasPassword := req.SSHPassword != ""
		if hasKey == hasPassword {
			// Both or neither: ambiguous.
			httpjson.WriteError(w, http.StatusBadRequest, "exactly one of ssh_private_key or ssh_password is required")
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
			httpjson.WriteError(w, http.StatusBadRequest, "unknown tofu_policy: "+req.TofuPolicy)
			return
		}
		provReq := ProvisionRequest{
			SSHPort:             req.SSHPort,
			SSHUser:             req.SSHUser,
			SSHPrivateKey:       req.SSHPrivateKey,
			SSHPassword:         req.SSHPassword,
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
				httpjson.WriteError(w, http.StatusConflict, err.Error())
				return
			}
			httpjson.WriteError(w, http.StatusBadGateway, err.Error())
			return
		}
		httpjson.WriteJSON(w, http.StatusOK, provisionResponse{
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

// newSSHClientForRotate is the package-level
// indirection the HTTP handler uses to
// construct an SSH client. The default is
// the production `NewClient`; the unit
// tests override it via `withMockSSHClient`
// in handler_rotate_panel_key_test.go to
// avoid a real SSH dial.
//
// The indirection is the lightest seam that
// keeps the handler testable: a
// `Service`-level factory would require a
// new field + a new constructor argument
// just to support unit tests, and the
// Installer.ClientFactory pattern does not
// apply here (the rotate-panel-key flow
// uses NewClient directly, not the
// Installer's wrapper).
var newSSHClientForRotate = NewClient

// rotatePanelKeyRequest is the POST
// /{id}/rotate-panel-key body. The endpoint
// is the v0.8.4 HTTP mirror of the v0.8.3
// `aegis admin node rotate-panel-key` CLI
// (PR #184): the operator pastes their
// existing SSH private key (the one they used
// to install the node on v0.3.0..v0.7.x), the
// panel opens an SSH session, generates a fresh
// ed25519 keypair, pushes the public half to
// $HOME/.ssh/authorized_keys, and seals the
// private half with the operator's age
// envelope.
//
// The endpoint only accepts a private key
// (NOT a password). The reason is the
// rotate-panel-key path is the v0.3.0..v0.7.x
// re-provision escape hatch: the node already
// has the operator's PEM authorised in
// $HOME/.ssh/authorized_keys from the
// original install. A password would only be
// meaningful on a brand-new node that does not
// yet have any keys — and that path is the
// `POST /{id}/provision` endpoint, not this
// one.
type rotatePanelKeyRequest struct {
	// SSHPrivateKey is the operator's existing
	// private key (PEM, no passphrase) that
	// the panel will use to SSH into the node
	// and append the new panel key to
	// authorized_keys. Required.
	SSHPrivateKey string `json:"ssh_private_key"`
	// SSHPort is the per-call override. Zero
	// (omitted from the JSON) means "use the
	// service-wide default".
	SSHPort int `json:"ssh_port,omitempty"`
	// SSHUser is the per-call override.
	// Empty (omitted from the JSON) means "use
	// the service-wide default".
	SSHUser string `json:"ssh_user,omitempty"`
}

// rotatePanelKeyResponse is the 200 body.
// The UI surfaces the public_key_line and
// fingerprint in a "rotation result" card so
// the operator can verify what is now in the
// node's authorized_keys (a sanity check
// against `ssh-add -L` after the re-provision's
// first contact).
type rotatePanelKeyResponse struct {
	NodeID        string `json:"node_id"`
	PublicKeyLine string `json:"public_key_line"`
	Fingerprint   string `json:"fingerprint"`
}

// HandleRotatePanelKey returns the HTTP handler
// for the rotate-panel-key endpoint. The
// function is called by the nodes router and is
// mounted as `POST /{id}/rotate-panel-key` (the
// parent subrouter already validated the {id} as
// a UUID via chi.URLParam).
//
// # Wire format
//
//	POST /api/v1/nodes/{id}/rotate-panel-key
//	{
//	  "ssh_private_key": "-----BEGIN OPENSSH PRIVATE KEY-----\n...",
//	  "ssh_port": 22,        // optional, default = service-wide
//	  "ssh_user": "root"     // optional, default = service-wide
//	}
//
//	200 OK
//	{
//	  "node_id":         "...",
//	  "public_key_line": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... aegis-panel@node-<name>",
//	  "fingerprint":     "SHA256:..."
//	}
//
// # Auth
//
// The parent nodes router already enforces
// auth.RequireScope(ScopeNodes); no extra
// scope is required (any operator who can
// install a node can also rotate the panel's
// persistent SSH key on it — they are the
// same trust boundary).
//
// # Error mapping
//
//   - 400: malformed UUID, missing
//     ssh_private_key, malformed JSON.
//   - 404: node row not found.
//   - 500: envelope not configured (a panel
//     booted without
//     AEGIS_WEBHOOKS_SECRET_AGE_*). Treated
//     as 500 (server config) rather than 503
//     (transient); the operator must fix the
//     panel's env and retry.
//   - 502: SSH-side failure (Connect / Upload
//     / Run / SetSSHPrivateKeyCiphertext).
//     The upstream SSH server is the source
//     of the problem.
//
// The signature is `func(...) http.HandlerFunc`
// rather than `func(...) http.Handler` so the
// caller can keep its existing
// `r.Post("/{id}/rotate-panel-key", svc.HandleRotatePanelKey())`
// style — same seam as HandleProvision.
func (s *Service) HandleRotatePanelKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rawID := chi.URLParam(r, "id")
		nodeID, err := uuid.Parse(rawID)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "invalid node id: "+err.Error())
			return
		}
		var req rotatePanelKeyRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpjson.WriteError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		if req.SSHPrivateKey == "" {
			httpjson.WriteError(w, http.StatusBadRequest, "ssh_private_key is required")
			return
		}
		// Fail fast if the panel was booted
		// without an envelope. The 500 (not
		// 503) is intentional: a missing
		// envelope is a server-config error
		// the operator must fix in the
		// panel's env, not a transient
		// upstream problem.
		if s.envelope == nil {
			httpjson.WriteError(w, http.StatusInternalServerError, "bootstrap: envelope is not configured on the panel (set AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS and AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE)")
			return
		}
		// Resolve the node row. The handler
		// does NOT enforce the state machine
		// (a rotate-panel-key is legal from
		// any state per the CLI's
		// rationale in admin_node.go), but
		// the row must exist.
		row, err := s.nodes.GetByID(r.Context(), nodeID)
		if err != nil {
			httpjson.WriteError(w, http.StatusNotFound, "node not found: "+err.Error())
			return
		}
		// Build the SSH client. The handler
		// uses the same SSH defaults as the
		// provisioner (s.sshUser / s.sshPort
		// / s.knownHosts) so the operator
		// does not have to repeat them on
		// every call. The per-request
		// ssh_user / ssh_port overrides
		// match the provision request
		// shape.
		sshUser := req.SSHUser
		if sshUser == "" {
			sshUser = s.sshUser
		}
		sshPort := req.SSHPort
		if sshPort == 0 {
			sshPort = s.sshPort
		}
		// ClientConfig.Address is the
		// canonical "host:port" string (the
		// SSH client appends ":22" only when
		// the address has no colon at all).
		address := row.Address
		if !strings.Contains(address, ":") {
			address = fmt.Sprintf("%s:%d", address, sshPort)
		}
		// A bounded context: a stuck SSH
		// handshake should not block the
		// handler indefinitely. 30s matches
		// the CLI's timeout.
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		sshClient, err := newSSHClientForRotate(ClientConfig{
			Address:    address,
			User:       sshUser,
			PrivateKey: []byte(req.SSHPrivateKey),
			KnownHosts: s.knownHosts,
		})
		if err != nil {
			httpjson.WriteError(w, http.StatusBadGateway, "ssh client: "+err.Error())
			return
		}
		defer func() {
			if err := sshClient.Close(); err != nil {
				log.Warn().Err(err).Str("node_id", nodeID.String()).Msg("bootstrap: rotate-panel-key ssh close")
			}
		}()
		if err := sshClient.Connect(ctx); err != nil {
			httpjson.WriteError(w, http.StatusBadGateway, "ssh connect: "+err.Error())
			return
		}
		// Rotate. The function generates
		// the new ed25519 keypair, encrypts
		// the private half, pushes the
		// public half via SFTP + constant
		// shell command, and persists the
		// ciphertext via
		// SetSSHPrivateKeyCiphertext.
		result, err := s.RotatePanelKey(ctx, nodeID, row.Name, sshClient)
		if err != nil {
			httpjson.WriteError(w, http.StatusBadGateway, "rotation failed: "+err.Error())
			return
		}
		// Audit. After-commit ordering:
		// the row's ciphertext was already
		// persisted by the RotatePanelKey
		// call above; the audit entry
		// records what happened for the
		// operator's records.
		if s.audits != nil {
			audits.RecordFromRequest(s.audits, r, audits.Entry{
				Action:       "node.rotate-panel-key",
				ResourceType: "node",
				ResourceID:   nodeID.String(),
				After: map[string]any{
					"node_name":   row.Name,
					"address":     row.Address,
					"fingerprint": result.Fingerprint,
				},
			})
		}
		httpjson.WriteJSON(w, http.StatusOK, rotatePanelKeyResponse{
			NodeID:        nodeID.String(),
			PublicKeyLine: result.PublicKeyLine,
			Fingerprint:   result.Fingerprint,
		})
	}
}

// _ = strconv keeps the strconv import in
// use while the handler is growing. A future
// PR may add query-string filters (e.g.
// ?include=state-only for a status endpoint).
// (the placeholder itself has been removed: with the
// D3 migration, the import went unused and the placeholder
// was no longer needed.)
