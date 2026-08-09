// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/mail"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/webhooks"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// MinUsernameLen / MaxUsernameLen are the inclusive
// bounds on Username. The DB has no CHECK on
// username length; the Service is the authoritative
// gate. The lower bound matches the sing-box
// provider's minimum per-user identifier length
// (3 chars); the upper bound matches the typical
// VPS / Linux username limit (32 chars) so the
// sing-box render layer can reuse Username in
// paths / log lines without truncation.
const (
	MinUsernameLen = 3
	MaxUsernameLen = 32
)

// MinSubTokenLen / MaxSubTokenLen are the inclusive
// bounds on the random sub_token hex string. 32 hex
// chars = 16 bytes = 128 bits, matching the
// subscription package's other secrets (see
// `internal/bootstrap/secrets.go`).
const (
	MinSubTokenLen = 32
	MaxSubTokenLen = 64
)

// Service is the business-logic layer on top of
// Store. It owns:
//
//   - input validation (username format, status
//     enum, traffic limit non-negative, telegram ID
//     range, email format, hosts allow/block list
//     shape);
//   - ID / timestamp / sub_token generation on
//     Create (the operator does not pre-assign any
//     of these);
//   - sub_token rotation (the rotateSubToken
//     helper implements migration 0011's
//     "previous token keeps working for a grace
//     window" semantics);
//   - v0.5.0 BatchedApplier fan-out: every mutating
//     method (Create/Update/Delete/RotateSubToken)
//     enqueues a cores.Delta into every applier
//     registered via WithBatchApplier. The appliers
//     coalesce by user ID and call back into the
//     FlushFn (wired in cmd/aegis/main.go) which
//     re-renders the sing-box config and POSTs it
//     to the agent.
//
// Handlers call Service rather than Store directly
// so the rules stay in one place and the pgx
// migration in Phase 1.1 can swap the Store without
// touching validation.
type Service struct {
	store      Store
	now        func() time.Time
	idGen      func() uuid.UUID
	tokenBytes int               // bytes of random for sub_token (default 32 → 64 hex)
	webhooks   *webhooks.Service // v0.7.x: outbound event surface. May be nil (see WithWebhooks).
	// audits is the v0.7.x deferred call-site:
	// every mutating method records an audit_log
	// row after the mutation is committed. nil =
	// the field is unset (unit tests), and the
	// RecordFromContext helper is nil-safe (it
	// short-circuits when svc is nil). The setter
	// follows the same pattern as WithWebhooks so
	// the existing 167+ test fixtures stay
	// untouched.
	audits *audits.Service
	// batchedAppliers is the v0.5.0 outbound
	// render+apply fan-out. nil = feature disabled
	// (no auto-apply); the field is intentionally
	// kept separate from the webhooks field so a
	// future "webhooks but not batched" deployment
	// (e.g. an operator who only wants the event
	// stream) is one flag away. See
	// AEGIS_BATCHED_APPLIER_ENABLED.
	batchedAppliers map[uuid.UUID]*cores.BatchedApplier
	// hosts is the v0.8.x host→node lookup that
	// powers `enqueueUserDelta`'s per-user
	// host-allow/block filter. Declared as a
	// narrow interface so the existing unit-test
	// fixtures can stub the lookup without
	// instantiating a real *hosts.Service. nil =
	// the field is unset (unit tests + pre-v0.8.x
	// behaviour); the filter falls through to the
	// v0.7.x default-allow path.
	hosts HostNodesLookup
}

// HostNodesLookup is the slice of the hosts
// service the v0.8.x user fan-out needs. Declared
// here (in the consumers' package) to keep the
// `internal/users` -> `internal/hosts` import
// optional in the future; the real implementer
// is `*hosts.Service` and the field is set via
// `WithHosts` in main wiring.
type HostNodesLookup interface {
	NodesForHost(ctx context.Context, hostID uuid.UUID) ([]uuid.UUID, error)
}

// NewService wires a Service around the given store.
// The now function stamps CreatedAt / UpdatedAt on
// writes; tests inject a fixed clock.
func NewService(store Store) *Service {
	return &Service{
		store:      store,
		now:        time.Now,
		idGen:      uuid.New,
		tokenBytes: 32,
	}
}

// WithHosts installs the host→node lookup. Used by
// the v0.8.x `enqueueUserDelta` to expand
// `User.HostsAllowlist` (host IDs, per the
// architecture) into the node IDs the BatchedApplier
// fan-out matches against. Same setter pattern as
// WithWebhooks / WithAudits so the existing 167+
// test fixtures stay untouched (nil is the
// pre-v0.8.x default-allow behaviour).
func (s *Service) WithHosts(h HostNodesLookup) *Service {
	s.hosts = h
	return s
}

// WithWebhooks installs the outbound event service.
// See plans.Service.WithWebhooks for the rationale
// (existing test fixtures stay untouched; the
// webhooks field is nil for unit tests and
// MustDispatch silently no-ops).
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// WithAudits installs the audit-log writer. The
// setter mirrors WithWebhooks: nil-safe at the
// call-site (RecordFromContext short-circuits when
// svc is nil), the field stays nil for unit
// tests, and only the new dispatch tests wire a
// real `*audits.Service` (via the MemoryStore +
// spy pattern from PR #148).
func (s *Service) WithAudits(svc *audits.Service) *Service {
	s.audits = svc
	return s
}

// WithBatchApplier installs the per-node
// BatchedApplier map. The map is owned by the
// caller (typically `*app.App.BatchedAppliers`);
// this setter only saves the reference so the
// mutating methods can fan out a Delta to every
// node on every change. A nil or empty map is a
// no-op (the feature is effectively off); the
// mutating methods test for that before
// dereferencing.
func (s *Service) WithBatchApplier(aps map[uuid.UUID]*cores.BatchedApplier) *Service {
	s.batchedAppliers = aps
	return s
}

// enqueueUserDelta fans out a single Delta to every
// registered BatchedApplier that the user is
// eligible for. Phase 1 / v0.5.0 did not narrow
// the fan-out (the sing-box renderer was single-
// user, the FlushFn re-rendered the full config
// anyway). Phase 2 / v0.7.x narrows by the user's
// `HostsAllowlist` and `HostsBlocklist`:
//
//   - If both are empty: fan-out to every node
//     (default allow). This is the v0.5.0 behaviour
//     and the safe migration path — a panel that
//     has not yet populated the allowlist keeps
//     its existing fan-out.
//   - If `HostsAllowlist` is non-empty: only fan
//     out to appliers whose node ID is in the
//     host-expanded allowlist (v0.8.x). The field
//     stores host IDs (per the architecture); the
//     fan-out expands each host to its node IDs
//     via `s.hosts.NodesForHost` and unions them.
//     This is the "user is allowed on this set of
//     hosts" semantic — the canonical place for
//     the per-user server filter per
//     `docs/comparison/remnave.md:118-119`.
//   - If `HostsBlocklist` is non-empty: skip any
//     applier whose node ID is in the
//     host-expanded blocklist (same expansion).
//     The blocklist wins over the allowlist (an
//     empty allowlist + non-empty blocklist means
//     "all nodes EXCEPT the hosts in the
//     blocklist").
//
// The `nodeID` argument to Enqueue is the
// BatchedApplier's per-node key. The user model
// stores `HostsAllowlist` / `Blocklist` as
// `[]uuid.UUID`; in v0.8.x the UUIDs are host IDs
// (not node IDs — the previous v0.7.x code treated
// them as node IDs, which is what the architecture
// calls a misimplementation; the v0.8.x work
// here fixes the semantic to match the field
// name). The BatchedApplier fan-out still keys on
// node IDs; the expansion via `NodesForHost` is
// the bridge.
//
// v0.8.x fail-closed: if the user has a non-empty
// allow/block list but the `s.hosts` lookup is
// nil (main forgot to call `WithHosts`), the
// fan-out is empty (no applier matches) and a
// warning is logged. The alternative — fan-out
// to every node when the lookup is missing — is
// fail-open and would silently grant access on
// a misconfigured v0.8.x install. The fail-closed
// behaviour is what the architecture intends.
//
// The helper swallows no errors — Enqueue is a
// blocking channel write and a slow consumer
// (the FlushFn + agent round-trip) is the only
// failure mode. The 1000-deep queue (set in
// cmd/aegis/main.go) gives a 50-deltas-per-second
// user-management rate ~20s of buffer; sustained
// over that the channel blocks the mutating
// method, which is the desired backpressure
// signal (the operator will see latency on the
// HTTP write and either scale the agent or
// disable the feature).
func (s *Service) enqueueUserDelta(ctx context.Context, d cores.Delta, user *User) {
	if len(s.batchedAppliers) == 0 {
		return
	}
	// Fast path: nil user (shouldn't happen at the
	// call sites, but be defensive) OR no allow/block
	// list at all → fan out to every node (v0.5.0
	// default-allow; the safe migration path).
	if user == nil || (len(user.HostsAllowlist) == 0 && len(user.HostsBlocklist) == 0) {
		for _, ap := range s.batchedAppliers {
			if ap == nil {
				continue
			}
			ap.Enqueue(d)
		}
		return
	}
	// v0.8.x: the user has a non-empty allow/block
	// list. Expand the host IDs to node IDs via
	// `s.hosts.NodesForHost`. A nil lookup is a
	// fail-closed empty fan-out (logged); a real
	// lookup that errors on a missing host is
	// treated as "no nodes for that host" (a
	// deleted host in the allowlist should not
	// silently grant access to all nodes).
	//
	// The `hasAllowFilter` flag is keyed off the
	// *user's* field (not the expanded set) so
	// the fan-out is fail-closed: a non-empty
	// field whose expansion is empty (missing
	// lookup, missing host) yields an empty
	// fan-out rather than falling back to the
	// v0.5.0 default-allow. The "user did not set
	// anything" path is the early return above;
	// here we are explicitly in the "user set
	// something" path and must honour the field
	// even if the expansion is empty.
	hasAllowFilter := len(user.HostsAllowlist) > 0
	allow := s.expandHostsToNodes(ctx, user.HostsAllowlist, "allowlist")
	block := s.expandHostsToNodes(ctx, user.HostsBlocklist, "blocklist")
	for nodeID, ap := range s.batchedAppliers {
		if ap == nil {
			continue
		}
		if _, blocked := block[nodeID]; blocked {
			continue
		}
		if hasAllowFilter {
			if _, allowed := allow[nodeID]; !allowed {
				continue
			}
		}
		ap.Enqueue(d)
	}
}

// expandHostsToNodes turns a list of host IDs (from
// `User.HostsAllowlist` / `HostsBlocklist`) into a
// set of node IDs the BatchedApplier fan-out can
// match against. A nil `s.hosts` returns an empty
// set (fail-closed; logged once at the call site).
// A real lookup that errors on a missing host is
// treated as "no nodes for that host" (fail-closed;
// the host was deleted). `label` is the field name
// for the warning log ("allowlist" / "blocklist").
// The `ctx` argument is propagated from the caller
// (the mutating method) so the lookup inherits the
// request deadline / cancellation.
func (s *Service) expandHostsToNodes(ctx context.Context, hosts []uuid.UUID, label string) map[uuid.UUID]struct{} {
	out := make(map[uuid.UUID]struct{})
	if len(hosts) == 0 {
		return out
	}
	if s.hosts == nil {
		log.Warn().
			Str("field", "hosts_"+label).
			Int("hosts", len(hosts)).
			Msg("users: host→node lookup is nil; user fan-out is empty (fail-closed). Wire WithHosts in main wiring.")
		return out
	}
	for _, hostID := range hosts {
		if hostID == uuid.Nil {
			continue
		}
		nodeIDs, err := s.hosts.NodesForHost(ctx, hostID)
		if err != nil {
			log.Warn().Err(err).
				Str("field", "hosts_"+label).
				Str("host", hostID.String()).
				Msg("users: host→node lookup failed; treating host as empty")
			continue
		}
		for _, n := range nodeIDs {
			out[n] = struct{}{}
		}
	}
	return out
}

// SetClock swaps the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// Store returns the underlying Store. Intended for
// tests that need direct in-memory mutation (e.g. to
// pre-seed rows the public Service would reject, or
// to bypass the IsValid() check on the Create path).
// Production code does not need this; it would
// suggest the caller is reaching past the Service
// boundary for something the Service should expose as
// a method.
func (s *Service) Store() Store {
	return s.store
}

// Get returns a single user by id. ErrNotFound
// bubbles up from the store unchanged so the handler
// can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// GetByUsername returns a single user by username.
// ErrNotFound bubbles up.
func (s *Service) GetByUsername(ctx context.Context, username string) (*User, error) {
	if err := validateUsername(username); err != nil {
		return nil, err
	}
	return s.store.GetByUsername(ctx, username)
}

// GetBySubToken returns a user matching the sub_token
// (or its prev-token, when usePrev is true and the
// prev token is still in its grace window). Used by
// the subscription package's resolution path.
func (s *Service) GetBySubToken(ctx context.Context, token string, usePrev bool) (*User, error) {
	if token == "" {
		return nil, &ValidationError{Field: "sub_token", Message: "must be non-empty"}
	}
	return s.store.GetBySubToken(ctx, token, usePrev)
}

// List returns every user, sorted by CreatedAt asc.
func (s *Service) List(ctx context.Context) ([]*User, error) {
	return s.store.List(ctx)
}

// ListByStatus returns every user with the given
// status, sorted by CreatedAt asc.
func (s *Service) ListByStatus(ctx context.Context, status Status) ([]*User, error) {
	if !status.IsValid() {
		return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(status)}
	}
	return s.store.ListByStatus(ctx, status)
}

// CreateInput is the payload the HTTP handler passes
// in (d.2 will add the JSON DTO). The Service
// assigns the ID, CreatedAt, and SubToken — the
// operator does not pre-assign any of these.
//
// Fields with a "*" default to the DB default if
// left zero; the rest are required.
type CreateInput struct {
	Username          string
	ExternalID        string
	Status            Status // defaults to StatusActive
	PlanID            *uuid.UUID
	ExpireAt          *time.Time
	TrafficLimitBytes int64 // defaults to 0 (no cap)
	TrafficUsedBytes  int64 // defaults to 0
	LastResetAt       *time.Time
	DeviceLimit       int // defaults to 0 (unlimited)
	HostsAllowlist    []uuid.UUID
	HostsBlocklist    []uuid.UUID
	TelegramID        *int64
	Email             string
}

// Create creates a new user with a freshly-minted
// sub_token. The Service generates the ID, the
// timestamps, and the sub_token; the caller supplies
// the operator-visible fields.
func (s *Service) Create(ctx context.Context, in CreateInput) (*User, error) {
	// 1. Validate operator-supplied fields.
	if err := validateUsername(in.Username); err != nil {
		return nil, err
	}
	if in.Status == "" {
		in.Status = StatusActive
	}
	if !in.Status.IsValid() {
		return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(in.Status)}
	}
	if in.TrafficLimitBytes < 0 {
		return nil, &ValidationError{Field: "traffic_limit_bytes", Message: "must be >= 0"}
	}
	if in.TrafficUsedBytes < 0 {
		return nil, &ValidationError{Field: "traffic_used_bytes", Message: "must be >= 0"}
	}
	if in.DeviceLimit < 0 {
		return nil, &ValidationError{Field: "device_limit", Message: "must be >= 0"}
	}
	if in.TelegramID != nil && (*in.TelegramID < 1 || *in.TelegramID > 9_999_999_999) {
		return nil, &ValidationError{Field: "telegram_id", Message: "must be a positive 10-digit number"}
	}
	if in.Email != "" {
		if _, err := mail.ParseAddress(in.Email); err != nil {
			return nil, &ValidationError{Field: "email", Message: "invalid format: " + err.Error()}
		}
	}
	if err := validateUUIDList(in.HostsAllowlist, "hosts_allowlist"); err != nil {
		return nil, err
	}
	if err := validateUUIDList(in.HostsBlocklist, "hosts_blocklist"); err != nil {
		return nil, err
	}
	// 2. Mint the sub_token. Random bytes from
	// crypto/rand; on Linux that is getrandom(2).
	tok, err := s.mintSubToken()
	if err != nil {
		return nil, fmt.Errorf("create: mint sub_token: %w", err)
	}
	// 3. Build the User.
	now := s.now()
	u := &User{
		ID:                s.idGen(),
		ExternalID:        in.ExternalID,
		Username:          in.Username,
		Status:            in.Status,
		PlanID:            in.PlanID,
		ExpireAt:          in.ExpireAt,
		TrafficLimitBytes: in.TrafficLimitBytes,
		TrafficUsedBytes:  in.TrafficUsedBytes,
		LastResetAt:       in.LastResetAt,
		DeviceLimit:       in.DeviceLimit,
		HostsAllowlist:    ensureUUIDList(in.HostsAllowlist),
		HostsBlocklist:    ensureUUIDList(in.HostsBlocklist),
		SubToken:          tok,
		TelegramID:        in.TelegramID,
		Email:             in.Email,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	// 4. Persist. ErrDuplicate bubbles up so the
	// handler can map to 409.
	if err := s.store.Create(ctx, u); err != nil {
		return nil, err
	}
	// 5. Return a defensive copy so the caller
	// cannot mutate the in-memory row.
	out := *u
	// v0.7.x: fan out the lifecycle event AFTER
	// the row is committed. See plans.Service
	// for the after-commit + non-blocking
	// rationale.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventUserCreated, &out)
	// v0.7.x deferred: record the audit row. Same
	// after-commit + best-effort contract as
	// webhooks.MustDispatch. Before is empty (a
	// Create has no pre-state); After is the
	// newly-committed row.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "user.create",
		ResourceType: "user",
		ResourceID:   out.ID.String(),
		After:        out,
	})
	// v0.5.0: enqueue a DeltaAddUser for every
	// registered BatchedApplier. The appliers
	// coalesce by UserID (the cancel/replace
	// logic in cores.BatchedApplier.absorb drops
	// a paired DeltaRemoveUser from the same
	// window). v0.7.x: the fan-out is narrowed by
	// the user's HostsAllowlist / Blocklist (see
	// enqueueUserDelta for the contract).
	s.enqueueUserDelta(ctx, cores.Delta{
		Kind:   cores.DeltaAddUser,
		UserID: out.ID,
	}, &out)
	return &out, nil
}

// UpdateInput is the payload the HTTP handler passes
// in for a PATCH /v1/users/{id}. Every field is a
// pointer so the Service can distinguish "leave
// alone" (nil) from "set to zero" (non-nil & zero).
type UpdateInput struct {
	ExternalID        *string
	Username          *string
	Status            *Status
	PlanID            *uuid.UUID // nil = leave alone; non-nil with uuid.Nil = unlink plan
	ExpireAt          *time.Time
	TrafficLimitBytes *int64
	TrafficUsedBytes  *int64
	LastResetAt       *time.Time
	DeviceLimit       *int
	HostsAllowlist    *[]uuid.UUID
	HostsBlocklist    *[]uuid.UUID
	TelegramID        *int64 // nil = leave alone; non-nil with *TelegramID == 0 = unlink telegram
	Email             *string
}

// Update modifies an existing user. Only the fields
// the caller marks (non-nil) are touched. The
// sub_token is NOT changed by this method — use
// RotateSubToken for that.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// Fetch the current state.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Apply the patch in-memory first so we can
	// run the same validators Create uses.
	if in.Username != nil {
		if err := validateUsername(*in.Username); err != nil {
			return nil, err
		}
		cur.Username = *in.Username
	}
	if in.ExternalID != nil {
		cur.ExternalID = *in.ExternalID
	}
	if in.Status != nil {
		if !in.Status.IsValid() {
			return nil, &ValidationError{Field: "status", Message: "unknown status: " + string(*in.Status)}
		}
		cur.Status = *in.Status
	}
	if in.PlanID != nil {
		// nil pointer = unlink (uuid.Nil). The d.2
		// handler will pass uuid.Nil to remove the
		// plan; the d.1 caller can pass &uuid.Nil{}
		// to do the same.
		cur.PlanID = in.PlanID
	}
	if in.ExpireAt != nil {
		cur.ExpireAt = in.ExpireAt
	}
	if in.TrafficLimitBytes != nil && *in.TrafficLimitBytes >= 0 {
		cur.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.TrafficUsedBytes != nil && *in.TrafficUsedBytes >= 0 {
		cur.TrafficUsedBytes = *in.TrafficUsedBytes
	}
	if in.LastResetAt != nil {
		cur.LastResetAt = in.LastResetAt
	}
	if in.DeviceLimit != nil && *in.DeviceLimit >= 0 {
		cur.DeviceLimit = *in.DeviceLimit
	}
	if in.HostsAllowlist != nil {
		if err := validateUUIDList(*in.HostsAllowlist, "hosts_allowlist"); err != nil {
			return nil, err
		}
		cur.HostsAllowlist = ensureUUIDList(*in.HostsAllowlist)
	}
	if in.HostsBlocklist != nil {
		if err := validateUUIDList(*in.HostsBlocklist, "hosts_blocklist"); err != nil {
			return nil, err
		}
		cur.HostsBlocklist = ensureUUIDList(*in.HostsBlocklist)
	}
	if in.TelegramID != nil {
		if *in.TelegramID != 0 {
			if *in.TelegramID < 1 || *in.TelegramID > 9_999_999_999 {
				return nil, &ValidationError{Field: "telegram_id", Message: "must be a positive 10-digit number"}
			}
		}
		cur.TelegramID = in.TelegramID
	}
	if in.Email != nil {
		if *in.Email != "" {
			if _, err := mail.ParseAddress(*in.Email); err != nil {
				return nil, &ValidationError{Field: "email", Message: "invalid format: " + err.Error()}
			}
		}
		cur.Email = *in.Email
	}
	cur.UpdatedAt = s.now()
	// Persist.
	if err := s.store.Update(ctx, cur); err != nil {
		return nil, err
	}
	// Return a fresh fetch so the caller sees the
	// post-update state (some stores apply the
	// update by replacing the row, and UpdatedAt is
	// re-stamped by the DB on write).
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see Create. A PATCH that did not
	// change any field still counts as a
	// write (the row's UpdatedAt is bumped) and
	// still fires the event.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventUserUpdated, out)
	// v0.7.x deferred: record the audit row. The
	// pre-patch state is `cur` (fetched at the
	// top of the method); the post-patch state
	// is `out` (the fresh fetch after Update).
	// Both are populated so the operator can
	// see the diff in the audit log without
	// joining against any other table.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "user.update",
		ResourceType: "user",
		ResourceID:   out.ID.String(),
		Before:       cur,
		After:        out,
	})
	// v0.5.0: enqueue a DeltaAddUser so the next
	// FlushFn re-renders the node config. We use
	// DeltaAddUser (not DeltaSetLimit) because
	// the sing-box renderer in Phase 1 is
	// single-user and treats any change as
	// "re-render". A future Phase 2 PR that
	// narrows the renderer to per-user changes
	// will switch this to DeltaSetLimit when
	// in.TrafficLimitBytes is the only changed
	// field, and keep DeltaAddUser otherwise.
	delta := cores.Delta{
		Kind:   cores.DeltaAddUser,
		UserID: out.ID,
	}
	if in.TrafficLimitBytes != nil {
		// TrafficLimitBytes is a per-user quota.
		// In Phase 1 the renderer cannot enforce
		// it natively; the enqueue still fires so
		// the FlushFn re-renders and the agent
		// gets a chance to apply any rate-limit
		// sidecar (e.g. a netfilter nftables
		// rule the agent maintains out-of-band).
		delta.Kind = cores.DeltaSetLimit
		delta.Payload, _ = json.Marshal(struct {
			Bytes int64 `json:"bytes"`
		}{Bytes: out.TrafficLimitBytes})
	}
	s.enqueueUserDelta(ctx, delta, out)
	return out, nil
}

// Delete removes the user. The Store does a hard
// delete (no soft-delete) — operators that need a
// soft-delete semantics should call
// Service.Update(id, UpdateInput{Status: &deleted})
// first and then call Delete on a periodic cleanup
// cron (out of scope for d.1).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// v0.7.x deferred: fetch the row before
	// deleting so the audit entry has a Before.
	// The extra GetByID round-trip is acceptable
	// here because Delete is the slowest CRUD
	// verb in the operator UI (an "are you sure"
	// dialog precedes it 99% of the time). The
	// fetch also gives us a cleaner error
	// (ErrNotFound) when the operator mistypes
	// an id, vs the silent-success the old
	// behaviour produced.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x: the row is gone by the time we
	// dispatch, so the payload carries only
	// the identifier (the receiver may want
	// to fetch the full row from the panel
	// before the deletion was applied, or
	// rely on a local cache).
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventUserDeleted, map[string]string{
		"id": id.String(),
	})
	// v0.7.x deferred: record the audit row.
	// Before is the row we just fetched; After
	// is nil (the row is gone — the v0.2.0
	// audit entry shape renders After as
	// `null` in the JSON envelope, which is
	// the canonical "row was deleted" signal).
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "user.delete",
		ResourceType: "user",
		ResourceID:   id.String(),
		Before:       cur,
	})
	// v0.5.0: enqueue a DeltaRemoveUser so the
	// next FlushFn re-renders the node config
	// without the deleted user. The appliers'
	// cancel/replace logic will drop this delta
	// if a DeltaAddUser for the same UserID is
	// enqueued in the same window (a quick
	// delete-then-recreate). v0.7.x: pass the
	// pre-delete user (`cur`) so the fan-out is
	// narrowed by the same HostsAllowlist /
	// Blocklist the user had when the row was
	// alive.
	s.enqueueUserDelta(ctx, cores.Delta{
		Kind:   cores.DeltaRemoveUser,
		UserID: id,
	}, cur)
	return nil
}

// DefaultSubTokenRotationGrace is the grace window
// the Service applies when RotateSubToken is called
// with grace <= 0 (a "rotate immediately" intent that
// the canonical design maps to the safe default).
// 24h matches the 3X-UI convention: the end user has
// 24h to re-import the new URL on every device
// before the old one stops working.
//
// Re-exported as a package constant so callers
// (admin handler, future cabinet UI, tests) can
// reference the canonical duration without
// duplicating the literal. The literal in
// RotateSubToken was the magic-number site; the
// constant lives here as a public symbol.
const DefaultSubTokenRotationGrace = 24 * time.Hour

// RotateSubToken mints a fresh sub_token, parks the
// old one in sub_token_prev, and sets the prev-token
// grace window. The default grace is 24 hours, per
// migration 0011. The grace parameter is exposed so
// the d.1 tests (and future operator UI) can override.
//
// The grace window is a time.Duration. The new
// sub_token_prev_expires_at = now + grace.
func (s *Service) RotateSubToken(ctx context.Context, id uuid.UUID, grace time.Duration) (*User, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	if grace <= 0 {
		grace = DefaultSubTokenRotationGrace
	}
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Mint a fresh token.
	tok, err := s.mintSubToken()
	if err != nil {
		return nil, fmt.Errorf("rotate: mint sub_token: %w", err)
	}
	now := s.now()
	prev := cur.SubToken
	prevExpires := now.Add(grace)
	cur.SubToken = tok
	cur.SubTokenPrev = prev
	cur.SubTokenPrevExpiresAt = &prevExpires
	cur.SubTokenRotatedAt = &now
	cur.UpdatedAt = now
	if err := s.store.Update(ctx, cur); err != nil {
		return nil, err
	}
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: RotateSubToken is a sub-update of
	// the user (the sub_token column changed).
	// We fire EventUserUpdated with the new
	// row state — receivers that want to
	// detect the rotation specifically can
	// diff the sub_token field. A dedicated
	// EventUserSubTokenRotated would be
	// cleaner but is out of scope for v0.7.x.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventUserUpdated, out)
	// v0.7.x deferred: record the audit row. The
	// action is `user.rotate_token` (not the
	// generic `user.update`) so the operator
	// can filter the audit log to rotation
	// events specifically. The pre-rotation row
	// is the cur snapshot fetched at the top of
	// the method (before the new sub_token was
	// minted); the post-rotation row is `out`.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "user.rotate_token",
		ResourceType: "user",
		ResourceID:   out.ID.String(),
		Before:       cur,
		After:        out,
	})
	// v0.5.0: enqueue a DeltaAddUser so the
	// next FlushFn re-renders the config with
	// the new sub_token. Phase 1's single-user
	// sing-box renderer does not actually
	// consume the token (the operator's
	// credentials are in the inbound's Params),
	// but the enqueue is symmetric with Create
	// and Update so a Phase 2 multi-user
	// renderer picks it up for free. v0.7.x:
	// the fan-out is narrowed by the post-
	// rotate user's HostsAllowlist / Blocklist.
	s.enqueueUserDelta(ctx, cores.Delta{
		Kind:   cores.DeltaAddUser,
		UserID: out.ID,
	}, out)
	return out, nil
}

// mintSubToken generates a random sub_token. The
// default is 32 bytes (64 hex chars), matching the
// subscription package's other secrets.
func (s *Service) mintSubToken() (string, error) {
	buf := make([]byte, s.tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

// --- validation helpers -------------------------------------------------

// validateUsername enforces the format and length
// bounds. The username may contain lowercase ASCII
// letters, digits, dash, underscore, and dot; this
// matches the typical Linux username charset plus
// the dot (for "firstname.lastname" conventions).
// No spaces, no uppercase, no leading/trailing dot.
func validateUsername(u string) error {
	if u == "" {
		return &ValidationError{Field: "username", Message: "must be non-empty"}
	}
	if len(u) < MinUsernameLen {
		return &ValidationError{Field: "username", Message: fmt.Sprintf("must be at least %d characters", MinUsernameLen)}
	}
	if len(u) > MaxUsernameLen {
		return &ValidationError{Field: "username", Message: fmt.Sprintf("must be at most %d characters", MaxUsernameLen)}
	}
	for i, r := range u {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		case r >= 'A' && r <= 'Z':
			// uppercase allowed for legacy
			// Cabinet imports; sing-box's per-user
			// credentials are case-sensitive so we
			// preserve the original case rather than
			// silently lowercasing.
		default:
			return &ValidationError{Field: "username", Message: fmt.Sprintf("invalid character %q at index %d", r, i)}
		}
	}
	if u[0] == '.' || u[len(u)-1] == '.' {
		return &ValidationError{Field: "username", Message: "must not start or end with '.'"}
	}
	return nil
}

// validateUUIDList ensures the list (if non-nil)
// has no zero UUIDs and no duplicates. Empty list
// / nil list are both acceptable (they mean
// "no filter"). The check is intentionally lenient
// on format — a non-zero UUID of any version is
// accepted. The Hosts package is the authoritative
// resolver (it can map a foreign ID to a UUID).
func validateUUIDList(list []uuid.UUID, field string) error {
	if list == nil {
		return nil
	}
	seen := make(map[uuid.UUID]struct{}, len(list))
	for i, u := range list {
		if u == uuid.Nil {
			return &ValidationError{Field: field, Message: fmt.Sprintf("entry %d is zero UUID", i)}
		}
		if _, dup := seen[u]; dup {
			return &ValidationError{Field: field, Message: fmt.Sprintf("duplicate entry %s at index %d", u, i)}
		}
		seen[u] = struct{}{}
	}
	return nil
}

// ensureUUIDList returns a non-nil slice. nil and
// empty both round-trip to a non-nil empty slice so
// the JSON output is always "[]" not "null" (the
// pgx scan path also expects a non-nil empty
// array, not a nil, on round-trip).
func ensureUUIDList(in []uuid.UUID) []uuid.UUID {
	if in == nil {
		return []uuid.UUID{}
	}
	return in
}

// Sentinel: errors.Is(err, ErrInvalid) returns true
// for *ValidationError wrapped via fmt.Errorf.
// Already declared in store.go; this line is just a
// compile-time check that Service uses the same
// sentinel. (No-op; the Go compiler will accept
// either ordering.)
var _ = ErrInvalid

// Sentinel for tests that want to assert the
// Service's Create / Update / Delete / Get call the
// Store. (Compile-time check.)
var _ Store = Store(nil)

// Common errors referenced for `errors.Is` callers.
// Re-exported for clarity at the call site.
var (
	ErrUserNotFound  = ErrNotFound
	ErrUserDuplicate = ErrDuplicate
	ErrUserInvalid   = ErrInvalid
	_                = errors.Is // keep the import in case future helpers wrap
)
