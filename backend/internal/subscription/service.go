// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer for the
// subscription package. It owns:
//
//   - the public /sub/{token} handler's user-lookup
//     entry point: sub_token is the public-facing
//     identifier; the operator hands it to the end
//     user and they paste it into a VPN client;
//   - the "which hosts should this user see" resolver
//     that walks users.plan_id -> plan_pool -> host_pools
//     -> host_pool_members and resolves each member back
//     to a *hosts.Host through the hosts service;
//   - the per-endpoint expansion that the renderer
//     needs: each endpoint of each entitled host, with
//     the node and inbound already resolved.
//
// Phase 0 intentionally does NOT implement:
//   - per-host `status_filter` (ARCHITECTURE.md §10.1.3):
//     we ignore the filter and return every host the
//     user is entitled to. The list of entitled hosts
//     is already filtered by pool membership;
//   - per-user `hosts_allowlist` / `hosts_blocklist`:
//     the slices are stored on User but not consulted
//     yet. The Service returns every entitled host;
//   - non-`all` pool strategies: round_robin /
//     least_loaded / geo_aware land with the Phase 1
//     strategy work;
//   - antiaffinity: not modelled in Phase 0.
//
// Each "future" item is a method-local filter pass that
// we add behind a test once the feature lands, so
// existing call sites do not need to change.
//
// # d-refactor.2 split
//
// As of d-refactor.2 the user-CRUD surface has moved
// to `internal/users` (the d.1 + d-refactor.1 work
// aligned the wire format so the two Go types are
// interchangeable via the `type User = users.User`
// alias). The Service here keeps a small set of
// thin-wrapper methods (GetUserBySubToken /
// RotateSubToken / CreateUser / ListUsers) that
// delegate to the users package and translate
// `users.ErrNotFound` / `users.ErrDuplicate` /
// `users.ValidationError` into the subscription
// package's own sentinels, so the existing render
// handler and admin handler do not have to learn a
// parallel error space yet. d-refactor.3 will move
// admin_handler.go to `users/admin_handler.go` and
// drop the four wrappers from this Service.

package subscription

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/users"
)

// ResolvedEndpoint is the renderer's view of a single
// endpoint inside a subscription: the host it belongs to
// (for display name and pool context), the endpoint
// itself (for the URL parameters), and the node +
// inbound it points at (for the address / protocol /
// params).
type ResolvedEndpoint struct {
	Host     *hosts.Host
	Endpoint hosts.Endpoint
	Node     *nodes.Node
	Inbound  *inbounds.Inbound
	// Download is the XHTTP download-farm endpoint,
	// populated when Endpoint.DownloadHostID is set.
	// The Service picks a random endpoint of the
	// referenced host at resolve time; the sing-box
	// renderer emits a `download_settings` block on
	// the outbound with this endpoint's address and
	// port. Nil = no download_settings block.
	Download *ResolvedDownload
}

// ResolvedDownload is the (address, port) pair for the
// XHTTP download farm endpoint. We keep the bare
// minimum rather than a full ResolvedEndpoint because
// the renderer only needs the connection target; the
// download host's protocol, params, and the rest of
// the inbound stack are irrelevant.
type ResolvedDownload struct {
	Address string
	Port    int
}

// Service is the subscription render-orchestration layer.
// It owns the public /sub/{token} handler's logic:
// given a sub_token, look up the user (via the users
// package), resolve the user's hosts and endpoints, and
// render the wire format (base64 / sing-box / Clash /
// HTML).
//
// The Service also exposes four thin user-CRUD wrappers
// (GetUserBySubToken / RotateSubToken / CreateUser /
// ListUsers) so the existing render handler and admin
// handler do not have to learn the users package's
// types. d-refactor.3 will move admin_handler.go into
// `internal/users` and drop these four wrappers; the
// render handler's GetUserBySubToken usage will then
// be replaced with a direct call to users.Service.
type Service struct {
	store    Store
	users    users.Store
	usersSvc *users.Service
	hosts    *hosts.Service
	nodes    *nodes.Service
	inbounds *inbounds.Service
	now      func() time.Time
}

// NewService wires a Service. Every dependency is
// required:
//
//   - store: the subscription plan / pool Store (the
//     render-side join table; the user CRUD store is
//     NOT here anymore).
//   - usersStore: the user-CRUD store the
//     GetUserBySubToken thin wrapper consults.
//   - usersSvc: the user-CRUD service the
//     RotateSubToken / CreateUser / ListUsers thin
//     wrappers consult.
//   - hosts / nodes / inbounds: every ResolvedEndpoint
//     returned to a caller resolves its host, node, and
//     inbound through them, so a missing Service here
//     would surface as a nil-deref at the first render.
func NewService(
	store Store,
	usersStore users.Store,
	usersSvc *users.Service,
	hostsSvc *hosts.Service,
	nodesSvc *nodes.Service,
	inboundsSvc *inbounds.Service,
) *Service {
	return &Service{
		store:    store,
		users:    usersStore,
		usersSvc: usersSvc,
		hosts:    hostsSvc,
		nodes:    nodesSvc,
		inbounds: inboundsSvc,
		now:      time.Now,
	}
}

// SetClock swaps the time source. Intended for tests.
// Propagates to the subscription Store (so plan / pool
// fixtures use a fixed clock), the users MemoryStore
// (so the user-CRUD fixtures use the same fixed clock),
// and the users Service (so future Service-level tests
// of the user-CRUD-during-render path have a consistent
// clock).
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
	if s.users != nil {
		if ms, ok := s.users.(*users.MemoryStore); ok {
			ms.SetClock(now)
		}
	}
	if s.usersSvc != nil {
		s.usersSvc.SetClock(now)
	}
}

// --- user-CRUD thin wrappers ------------------------------------------
//
// The four methods below delegate to the users package.
// The mapping in each method:
//
//   - `users.ErrNotFound` -> `*NotFoundError` (the
//     subscription-side error type the handlers expect)
//   - `users.ErrDuplicate` -> `ErrDuplicate`
//   - `users.ValidationError` (and the wrapped form) ->
//     `*ValidationError` (with the same Field / Message
//     shape, since the users validator produces the same
//     structure)
//
// The wrappers keep the existing render and admin
// handler code working with no error-space changes;
// d-refactor.3 will remove them.

// GetUserBySubToken looks up a user by sub_token. The
// lookup chain is "primary sub_token, then
// sub_token_prev within its grace window" — the
// underlying users.MemoryStore handles both in one
// call (usePrev=true). The Service is a thin wrapper
// that maps the users package's ErrNotFound into the
// subscription-side *NotFoundError so the existing
// render handler does not have to learn a new error
// space yet.
func (s *Service) GetUserBySubToken(ctx context.Context, token string) (out *User, err error) {
	if token == "" {
		return nil, &ValidationError{Field: "sub_token", Message: "must not be empty"}
	}
	if s.users == nil {
		return nil, &ValidationError{Field: "users", Message: "users store is not wired"}
	}
	u, err := s.users.GetBySubToken(ctx, token, true)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, &NotFoundError{What: "user", Key: "sub_token"}
		}
		return nil, fmt.Errorf("get user by sub_token: %w", err)
	}
	return u, nil
}

// DefaultSubTokenRotationGrace is the grace window the
// Service applies when RotateSubToken is called
// without an explicit grace. 24h matches the 3X-UI
// convention: the end user has 24h to re-import the
// new URL on every device before the old one stops
// working. The users Service applies this constant
// when grace is non-positive (see
// `users.Service.RotateSubToken`).
const DefaultSubTokenRotationGrace = 24 * time.Hour

// RotateSubToken generates a fresh sub_token for the
// user, parks the current one as `sub_token_prev`
// with a grace window, and bumps `SubTokenRotatedAt`.
// The grace is honoured by the GetUserBySubToken
// lookup chain. The Service is a thin wrapper over
// `users.Service.RotateSubToken`; the only translation
// is `users.ValidationError` → `*ValidationError` so
// the admin handler can keep using the existing
// `writeUserError` shape.
func (s *Service) RotateSubToken(ctx context.Context, userID uuid.UUID, grace time.Duration) (out *User, err error) {
	if s.usersSvc == nil {
		return nil, &ValidationError{Field: "users", Message: "users service is not wired"}
	}
	u, err := s.usersSvc.RotateSubToken(ctx, userID, grace)
	if err != nil {
		if errors.Is(err, users.ErrNotFound) {
			return nil, &NotFoundError{What: "user", Key: "id"}
		}
		var verr *users.ValidationError
		if errors.As(err, &verr) {
			return nil, &ValidationError{Field: verr.Field, Message: verr.Message}
		}
		return nil, fmt.Errorf("rotate sub_token: %w", err)
	}
	return u, nil
}

// CreateUser is the thin wrapper over users.Service.Create.
// The CreateUserInput is the alias for users.CreateInput
// (see subscription/subscription.go) so the admin
// handler can keep using the `subscription.CreateUserInput`
// spelling until d-refactor.3 moves it to users.
func (s *Service) CreateUser(ctx context.Context, in CreateUserInput) (*User, error) {
	if s.usersSvc == nil {
		return nil, &ValidationError{Field: "users", Message: "users service is not wired"}
	}
	u, err := s.usersSvc.Create(ctx, in)
	if err != nil {
		if errors.Is(err, users.ErrDuplicate) {
			return nil, fmt.Errorf("%w: %w", ErrDuplicate, err)
		}
		var verr *users.ValidationError
		if errors.As(err, &verr) {
			return nil, &ValidationError{Field: verr.Field, Message: verr.Message}
		}
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

// ListUsers returns every user in the system,
// sorted by created_at ascending. Thin wrapper over
// `users.Store.List` — the Service does not own the
// user-CRUD side. The admin handler calls this
// directly; the render handler does not.
func (s *Service) ListUsers(ctx context.Context) ([]*User, error) {
	if s.users == nil {
		return nil, &ValidationError{Field: "users", Message: "users store is not wired"}
	}
	return s.users.List(ctx)
}

// --- render-side resolvers (unchanged) -------------------------------

// ResolveHostsForUser returns every host the user is
// entitled to see, deduplicated and sorted by host
// priority ascending (lower = higher in the list, per
// ARCHITECTURE.md §10.1). The slice is freshly allocated.
//
// The walk is:
//
//  1. ListPoolsForUser -> every pool the user's plan
//     is attached to (Phase 0 ignores per-user pool
//     overrides);
//  2. ListPoolMembers(pool.ID) -> host ids + weights;
//  3. hosts.Service.Get(host_id) -> *hosts.Host for
//     each id. Missing hosts are skipped silently (a
//     pool member that no longer resolves to a host is
//     treated as "drift", not as an error).
//
// Phase 0 does not honour per-host status_filter,
// per-user allow/block lists, or non-`all` pool
// strategies. All of those are method-local additions
// once the relevant tests exist.
func (s *Service) ResolveHostsForUser(ctx context.Context, u *User) (out []*hosts.Host, err error) {
	if u == nil {
		return nil, &ValidationError{Field: "user", Message: "must not be nil"}
	}
	if !u.Status.IsLive() {
		return nil, &UserNotLiveError{Status: u.Status}
	}

	pools, err := s.store.ListPoolsForUser(ctx, u)
	if err != nil {
		return nil, fmt.Errorf("list pools for user: %w", err)
	}

	// First-pass: collect candidate host ids with the
	// host's pool weight summed (a host in two pools
	// is still one host, but the weight is the sum of
	// the pool-member weights so the renderer can
	// order by combined weight if it wants to).
	type candidate struct {
		id     uuid.UUID
		weight int
	}
	seen := make(map[uuid.UUID]*candidate)
	for _, p := range pools {
		members, err := s.store.ListPoolMembers(ctx, p.ID)
		if err != nil {
			return nil, fmt.Errorf("list pool members: %w", err)
		}
		for _, m := range members {
			if c, ok := seen[m.HostID]; ok {
				c.weight += m.Weight
				continue
			}
			seen[m.HostID] = &candidate{id: m.HostID, weight: m.Weight}
		}
	}

	// Second-pass: resolve to *hosts.Host, drop
	// missing / disabled, sort by priority.
	out = make([]*hosts.Host, 0, len(seen))
	for _, c := range seen {
		h, err := s.hosts.Get(ctx, c.id)
		if err != nil {
			if errors.Is(err, hosts.ErrNotFound) {
				// Pool member points at a host the
				// store no longer has. Skip; do not
				// fail the whole subscription.
				continue
			}
			return nil, fmt.Errorf("resolve host %s: %w", c.id, err)
		}
		if !h.Enabled {
			continue
		}
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Priority != out[j].Priority {
			return out[i].Priority < out[j].Priority
		}
		// Tie-break by CreatedAt (older first), then
		// by ID for full determinism — see
		// internal/hosts Store.List contract.
		if !out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].CreatedAt.Before(out[j].CreatedAt)
		}
		return out[i].ID.String() < out[j].ID.String()
	})
	return out, nil
}

// ResolveEndpointsForUser expands ResolveHostsForUser
// into a flat list of endpoints, one ResolvedEndpoint
// per (host, endpoint) pair. Hosts with no resolvable
// endpoints (a missing node or inbound) are skipped;
// individual missing endpoints inside a host are also
// skipped, but the host itself is still kept if any of
// its endpoints resolved.
//
// The result is the input the renderer wants: every
// URL a subscription client should see, with the
// display name, address, and protocol all pre-resolved.
// Endpoints whose `DownloadHostID` is set have their
// `Download` field populated with a random endpoint
// of the referenced host; the XHTTP sing-box renderer
// uses this to emit a `download_settings` block.
func (s *Service) ResolveEndpointsForUser(ctx context.Context, u *User) (out []ResolvedEndpoint, err error) {
	hs, err := s.ResolveHostsForUser(ctx, u)
	if err != nil {
		return nil, err
	}
	for _, h := range hs {
		for _, ep := range h.Endpoints {
			n, err := s.nodes.Get(ctx, ep.NodeID)
			if err != nil {
				if errors.Is(err, nodes.ErrNotFound) {
					continue
				}
				return nil, fmt.Errorf("resolve node %s: %w", ep.NodeID, err)
			}
			inb, err := s.inbounds.Get(ctx, ep.InboundID)
			if err != nil {
				if errors.Is(err, inbounds.ErrNotFound) {
					continue
				}
				return nil, fmt.Errorf("resolve inbound %s: %w", ep.InboundID, err)
			}
			re := ResolvedEndpoint{
				Host:     h,
				Endpoint: ep,
				Node:     n,
				Inbound:  inb,
			}
			// XHTTP download farm: look up the
			// referenced host by id (the host is
			// NOT in the user's pool — operator-
			// controlled CDN) and pick a random
			// endpoint of it. A missing or empty
			// download host is silently skipped
			// (fail-soft), which the sing-box
			// renderer treats as "no
			// download_settings block".
			if ep.DownloadHostID != nil {
				re.Download = s.resolveDownload(ctx, *ep.DownloadHostID)
			}
			out = append(out, re)
		}
	}
	return out, nil
}

// resolveDownload picks a random endpoint of the
// download host. The host is fetched directly by id
// (NOT through the user's pool — the download farm
// is operator-controlled). The endpoints' address
// and port are what the sing-box renderer needs for
// its `download_settings` block; we do not need the
// full ResolvedEndpoint surface (no protocol, no
// params, no display name).
//
// Returns nil when the host is missing, disabled, or
// has no resolvable endpoints — the sing-box renderer
// treats nil as "no download_settings block".
func (s *Service) resolveDownload(ctx context.Context, hostID uuid.UUID) *ResolvedDownload {
	dlHost, err := s.hosts.Get(ctx, hostID)
	if err != nil {
		return nil
	}
	if !dlHost.Enabled {
		return nil
	}
	if len(dlHost.Endpoints) == 0 {
		return nil
	}
	// Pick a random index. The picker is the same
	// package-level one used by pickPort; the tests
	// pin it once and every per-fetch selection
	// uses the same deterministic function.
	randPickerMu.Lock()
	idx := randPicker(len(dlHost.Endpoints))
	randPickerMu.Unlock()
	dep := dlHost.Endpoints[idx]
	dlNode, err := s.nodes.Get(ctx, dep.NodeID)
	if err != nil {
		return nil
	}
	dlInb, err := s.inbounds.Get(ctx, dep.InboundID)
	if err != nil {
		return nil
	}
	addr := dlNode.Address
	if len(dep.Address) > 0 {
		addr = dep.Address[0]
	}
	port := pickPort(dlInb)
	if dep.Port != nil {
		port = *dep.Port
	}
	return &ResolvedDownload{Address: addr, Port: port}
}
