// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer for the
// subscription package. It owns:
//
//   - the user-lookup entry point (sub_token is the
//     public-facing identifier; the operator hands it to
//     the end user and they paste it into a VPN client);
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

// Service is the subscription business-logic layer.
type Service struct {
	store    Store
	hosts    *hosts.Service
	nodes    *nodes.Service
	inbounds *inbounds.Service
	now      func() time.Time
}

// NewService wires a Service. The hosts, nodes, and
// inbounds services are required: every ResolvedEndpoint
// returned to a caller resolves its host, node, and
// inbound through them, so a missing Service here would
// surface as a nil-deref at the first render.
func NewService(
	store Store,
	hostsSvc *hosts.Service,
	nodesSvc *nodes.Service,
	inboundsSvc *inbounds.Service,
) *Service {
	return &Service{
		store:    store,
		hosts:    hostsSvc,
		nodes:    nodesSvc,
		inbounds: inboundsSvc,
		now:      time.Now,
	}
}

// SetClock swaps the time source. Intended for tests.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// GetUserBySubToken is a passthrough to the Store with a
// friendly error. The token is what the operator hands
// to the end user.
func (s *Service) GetUserBySubToken(ctx context.Context, token string) (out *User, err error) {
	if token == "" {
		return nil, &ValidationError{Field: "sub_token", Message: "must not be empty"}
	}
	u, err := s.store.GetUserBySubToken(ctx, token)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, &NotFoundError{What: "user", Key: "sub_token"}
		}
		return nil, fmt.Errorf("get user by sub_token: %w", err)
	}
	return u, nil
}

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
