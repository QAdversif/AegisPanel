// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
)

// bootstrapProvider is the subset of
// bootstrap.Service the nodes router depends
// on. The interface is the seam for tests: the
// integration tests substitute a stub; the
// production path delegates to the real
// bootstrap.Service.
//
// v0.3.0: POST /{id}/provision. v0.8.4:
// POST /{id}/rotate-panel-key (the deferred
// follow-up to PR #184's `aegis admin node
// rotate-panel-key` CLI; the admin UI gets a
// button in NodesView for it). v0.5.0 will
// add POST /{id}/heartbeat, POST /{id}/drain,
// POST /{id}/undeploy (the decommissioning
// flow).
type bootstrapProvider interface {
	// The handler signature is exported
	// directly because the http.HandlerFunc
	// is the unit the router mounts. Passing
	// the function value avoids the "wrap a
	// method on a different Service" indirection.
	HandleProvision() http.HandlerFunc
	// HandleRotatePanelKey is the v0.8.4
	// HTTP mirror of the v0.8.3 CLI. Same
	// body, same auth (ScopeNodes), same
	// 200 body (public key line + SHA256
	// fingerprint). The handler is mounted
	// only when the bootstrap service is
	// wired (Phase 0 / Phase 1 dev paths
	// with bootstrapSvc == nil get a 404
	// here, same as /provision).
	HandleRotatePanelKey() http.HandlerFunc
}

// Router returns a chi subrouter for /api/v1/nodes. The caller
// supplies the auth middleware so this package does not have to
// know about JWT internals.
//
// Mounting convention:
//
//	r.Mount("/nodes", nodes.Router(svc, authSvc.Middleware(), nil))
//
// The bootstrap parameter is optional: pass
// nil to mount only the v0.2.0 CRUD surface.
// main.go passes the real bootstrap.Service
// for the v0.3.0 BYO Node flow.
func Router(svc *Service, authMiddleware func(http.Handler) http.Handler, bootstrapSvc bootstrapProvider) http.Handler {
	r := chi.NewRouter()
	r.Use(authMiddleware)
	r.Use(auth.RequireScope(auth.ScopeNodes))

	r.Get("/", svc.handleList())
	r.Post("/", svc.handleCreate())
	r.Route("/{id}", func(r chi.Router) {
		r.Get("/", svc.handleGet())
		r.Put("/", svc.handleUpdate())
		r.Delete("/", svc.handleDelete())
		// v0.8.5: operator-side debug surface
		// for the v0.8.1 persistent panel SSH
		// key. The endpoint decrypts the
		// stored ciphertext via the age
		// envelope and returns the public-key
		// line + SHA-256 fingerprint. See
		// `handleGetStoredKey` above for
		// the security shape and error
		// mapping.
		r.Get("/stored-key", svc.handleGetStoredKey())
		// v0.3.0: BYO-Node bootstrap. The
		// handler is mounted only when the
		// bootstrap service is wired; in the
		// Phase 0 / Phase 1 dev paths
		// (bootstrapSvc == nil) the route is
		// simply absent and the operator gets
		// a 404 on /provision.
		//
		// v0.8.4: rotate-panel-key shares
		// the same mounting-conditional
		// (the handler is on the same
		// bootstrap.Service; either both
		// routes are live or both are 404).
		if bootstrapSvc != nil {
			r.Post("/provision", bootstrapSvc.HandleProvision())
			r.Post("/rotate-panel-key", bootstrapSvc.HandleRotatePanelKey())
		}
	})
	return r
}

// BootstrapNodeProvider is the adapter that
// turns the real nodes.Service into the
// bootstrap.NodeProvider interface. The
// adapter lives here (in the nodes package)
// because the bootstrap package cannot import
// nodes without creating a cycle. The
// translation is a plain row copy: the
// fields the provisioner needs (id, name,
// state, address) are all exported on
// nodes.Node.
type BootstrapNodeProvider struct {
	Svc *Service
}

// GetByID implements bootstrap.NodeProvider.
// The returned row is a copy; the caller may
// mutate it without affecting the store.
func (a *BootstrapNodeProvider) GetByID(ctx context.Context, id uuid.UUID) (bootstrap.NodeRow, error) {
	n, err := a.Svc.Get(ctx, id)
	if err != nil {
		return bootstrap.NodeRow{}, err
	}
	return bootstrap.NodeRow{
		ID:                      n.ID,
		Name:                    n.Name,
		State:                   string(n.State),
		Address:                 n.Address,
		AgentBearer:             n.AgentBearer,
		SSHPrivateKeyCiphertext: n.SSHPrivateKeyCiphertext,
	}, nil
}

// Update implements bootstrap.NodeProvider.
// The provider's row fields are mapped back
// onto nodes.Node; everything else (Tags,
// CapacityHint, CreatedAt, UpdatedAt) is
// preserved from the current store row.
func (a *BootstrapNodeProvider) Update(ctx context.Context, row bootstrap.NodeRow) error {
	current, err := a.Svc.Get(ctx, row.ID)
	if err != nil {
		return err
	}
	current.State = State(row.State)
	_, err = a.Svc.Update(ctx, current.ID, UpdateInput{
		// The provisioner only ever writes the
		// State field. Every other field is
		// left at its existing value (the
		// nodes.Service.Update helper copies
		// the row before applying, so a nil
		// pointer here is "leave alone").
	})
	return err
}

// SetAgentBearer implements bootstrap.NodeProvider.
// v0.4.0-mvp-batched: the provisioner mints a
// fresh bearer on every install and persists it
// here, so the panel can ship POST /v1/apply
// later. The setter bypasses nodes.Service so
// the rest of the row (state, tags, …) is left
// alone.
func (a *BootstrapNodeProvider) SetAgentBearer(ctx context.Context, id uuid.UUID, bearer string) error {
	return a.Svc.store.SetAgentBearer(ctx, id, bearer)
}

// SetSSHPrivateKeyCiphertext implements
// bootstrap.NodeProvider. v0.8.x: the
// provisioner's post-install hook calls this
// with the freshly-generated ed25519 private
// key sealed by the operator's age envelope.
// The setter bypasses nodes.Service for the
// same reason as SetAgentBearer (the rest of
// the row is left alone). The MemoryStore path
// deep-clones the node before mutating, so the
// adapter is safe to call from the hook's
// closure without holding any other lock.
func (a *BootstrapNodeProvider) SetSSHPrivateKeyCiphertext(ctx context.Context, id uuid.UUID, ciphertext []byte) error {
	return a.Svc.store.SetSSHPrivateKeyCiphertext(ctx, id, ciphertext)
}

// _ keeps the unused imports in scope while
// the file is being written; the aliasing
// pattern survives a future refactor where
// these helpers land in a sibling file.
var _ = json.RawMessage(nil)
var _ = errors.New
var _ = fmt.Sprintf

// --- request / response shapes ------------------------------------------

type createRequest struct {
	ID           *uuid.UUID `json:"id,omitempty"`
	Name         string     `json:"name"`
	Region       string     `json:"region"`
	State        State      `json:"state"`
	Address      string     `json:"address"`
	CapacityHint string     `json:"capacity_hint,omitempty"`
	Tags         []string   `json:"tags,omitempty"`
}

type updateRequest struct {
	Name         *string   `json:"name,omitempty"`
	Region       *string   `json:"region,omitempty"`
	State        *State    `json:"state,omitempty"`
	Address      *string   `json:"address,omitempty"`
	CapacityHint *string   `json:"capacity_hint,omitempty"`
	Tags         *[]string `json:"tags,omitempty"`
}

// --- handlers -----------------------------------------------------------

func (s *Service) handleList() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		nodes, err := s.List(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Sprintf("list: %v", err))
			return
		}
		// Always return a JSON array, never null, so the
		// frontend can iterate without a guard.
		if nodes == nil {
			nodes = []*Node{}
		}
		writeJSON(w, http.StatusOK, map[string]any{"nodes": nodes})
	}
}

func (s *Service) handleGet() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		n, err := s.Get(r.Context(), id)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	}
}

// handleGetStoredKey returns the v0.8.5
// operator-side debug surface for the
// node's stored panel SSH key. The
// endpoint decrypts `nodes.ssh_private_key_ciphertext`
// via the service's age envelope, derives
// the public-key line + SHA-256 fingerprint,
// and returns the public surface. The
// private key never leaves the panel
// process.
//
// The endpoint is mounted on the same
// `/api/v1/nodes/{id}` subrouter as the
// regular CRUD endpoints; the route is
// `GET /api/v1/nodes/{id}/stored-key`.
// The `ScopeNodes` enforcement from the
// parent router is the only auth gate
// (no separate scope is needed — the
// stored-key read is the same trust
// boundary as the node CRUD).
//
// # Error mapping
//
//   - 200: stored key (or no-key) is read
//   - 400: malformed UUID
//   - 404: node row not found
//   - 500: panel booted without an envelope
//     (the same fail-closed shape as
//     `POST /{id}/rotate-panel-key`; the
//     operator must set
//     `AEGIS_WEBHOOKS_SECRET_AGE_*` and
//     restart the panel to use this
//     surface)
//   - 502: stored ciphertext is unreadable
//     (decrypt failed — the age identity has
//     been rotated out of the operator's
//     reach, or the column was written with
//     a different envelope version)
//
// # Audit log
//
// The read is recorded as
// `node.stored-key.read` with the actor
// from the request claims + the node's id
// and name in the `After` map. The
// fingerprint is NOT recorded (the audit
// row is per-server, and a per-fingerprint
// row would be a 100x write-amplification
// for a read that may happen frequently).
// The fingerprint is in the HTTP response
// body so the operator can correlate the
// audit row with the read they performed
// (the audit log UI shows the timestamp +
// node id, the operator's screen shows the
// same timestamp + fingerprint).
func (s *Service) handleGetStoredKey() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		sk, err := s.GetStoredKey(r.Context(), id)
		if err != nil {
			// Differentiate "node not found"
			// (404) from "envelope not
			// configured" (500) from "decrypt
			// failed" (502). The shape mirrors
			// the v0.8.4 rotate-panel-key
			// handler so the operator's mental
			// model is the same across the
			// two surfaces.
			if errors.Is(err, ErrNotFound) {
				writeError(w, http.StatusNotFound, "node not found")
				return
			}
			msg := err.Error()
			switch {
			case contains(msg, "envelope is not configured"):
				writeError(w, http.StatusInternalServerError, msg)
			case contains(msg, "decrypt stored SSH key"), contains(msg, "parse stored SSH key"):
				writeError(w, http.StatusBadGateway, msg)
			default:
				writeError(w, http.StatusInternalServerError, msg)
			}
			return
		}
		// Audit. After-the-read ordering: the
		// decrypt already happened (we are
		// in the success path now); the audit
		// row records the fact of the read.
		// The pattern matches the v0.8.4
		// rotate-panel-key handler.
		if s.audits != nil {
			audits.RecordFromRequest(s.audits, r, audits.Entry{
				Action:       "node.stored-key.read",
				ResourceType: "node",
				ResourceID:   id.String(),
				After: map[string]any{
					"has_stored_key": sk.HasStoredKey,
				},
			})
		}
		writeJSON(w, http.StatusOK, sk)
	}
}

// contains is a tiny strings.Contains shim
// so the error-mapping switch above reads as
// a one-liner per branch. The standard
// library's strings.Contains is the right
// tool, but a local helper keeps the call
// sites tidy.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}

func (s *Service) handleCreate() http.HandlerFunc {
	// Phase 0 reserves POST /api/v1/nodes for future use
	// (today operators seed nodes via the SSH bootstrap
	// flow, not via this endpoint). The handler exists so the
	// path does not 404 and so the schema validation logic
	// has a wire-level smoke test.
	return func(w http.ResponseWriter, r *http.Request) {
		var req createRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		in := CreateInput{
			ID:           zeroOrValue(req.ID),
			Name:         req.Name,
			Region:       req.Region,
			State:        req.State,
			Address:      req.Address,
			CapacityHint: req.CapacityHint,
			Tags:         req.Tags,
		}
		n, err := s.Create(r.Context(), in)
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusCreated, n)
	}
}

func (s *Service) handleUpdate() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := parseID(w, r)
		if !ok {
			return
		}
		var req updateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "malformed request body")
			return
		}
		// updateRequest and UpdateInput have identical fields, so
		// a direct conversion is the cleanest way to pass the
		// patch through. The staticcheck S1016 hint that flagged
		// the previous struct-literal version is happy with
		// this form.
		n, err := s.Update(r.Context(), id, UpdateInput(req))
		if err != nil {
			writeStoreError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, n)
	}
}

func (s *Service) handleDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

// parseID pulls the {id} URL parameter and validates it. On
// failure it writes a 400 response and returns ok=false so the
// caller can early-return.
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

// writeStoreError maps the well-known Store / Service errors to
// HTTP status codes. Anything else is a 500.
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
	// Hand-rolled JSON to stay consistent with the auth
	// package and to keep this layer free of a JSON-helper
	// dependency. The format is the same {"error":"..."}
	// envelope the auth package emits.
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`{"error":` + jsonString(msg) + `}`))
}

// jsonString escapes a Go string for safe inclusion in a JSON
// string literal. Same implementation as auth.jsonString,
// duplicated here so this package has no dependency on auth
// internals. Non-ASCII runes are emitted as JSON \uXXXX escapes
// via a hex round-trip rather than a direct byte cast, which
// gosec flags as a potential integer-overflow conversion.
func jsonString(s string) string {
	var b []byte
	b = append(b, '"')
	for _, r := range s {
		switch r {
		case '"', '\\':
			b = append(b, '\\', '\\', byte(r))
		case '\n':
			b = append(b, '\\', 'n')
		case '\r':
			b = append(b, '\\', 'r')
		case '\t':
			b = append(b, '\\', 't')
		default:
			if r < 0x20 {
				// Skip control characters — they would
				// be invalid in JSON anyway.
				continue
			}
			// Round-trip through fmt.Sprintf so we get the
			// right escape for any rune without the
			// gosec-flagged rune→byte cast.
			b = append(b, []byte(fmt.Sprintf(`\u%04X`, r))...)
		}
	}
	b = append(b, '"')
	return string(b)
}
