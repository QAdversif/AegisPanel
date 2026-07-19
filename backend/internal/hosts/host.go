// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package hosts implements the panel-side CRUD for the `hosts`
// and `host_endpoints` tables that the operator-facing UI
// sells to end users.
//
// # v3 model (ARCHITECTURE.md §10)
//
// A Host is a *bundle* of Endpoints exposed as a single
// product. Per the v3 redesign introduced in PR #30:
//
//   - type=direct  → endpoints.length == 1
//   - type=balancer → endpoints.length > 1 + Balancer.Strategy
//   - type=chain   → Phase 4+ (cascade / reverse / forward),
//     explicitly not modelled in Phase 1
//
// Each Endpoint is a (Node, Inbound) pair + an override
// layer that lets the operator tweak the rendered sing-box
// inbound without touching the Node itself (typical case:
// the same node runs VLESS on 443 and HY2 on 8443 — one
// Host, two endpoints, each pointing at a different
// per-node Inbound).
//
// # Inbound reference
//
// The Endpoint's protocol family — VLESS, Hysteria 2,
// Shadowsocks, Trojan — lives on the referenced Inbound
// (see the inbounds package; PR #34 added the `inbounds`
// table). The Endpoint only carries an `InboundID`; the
// protocol is read off the referenced Inbound at render
// time. The Service layer enforces two invariants on
// every Create / Update:
//
//  1. InboundID must resolve to an existing `inbounds`
//     row (FK target).
//  2. The referenced Inbound's NodeID must equal the
//     Endpoint's NodeID. PostgreSQL cannot express this
//     as a CHECK constraint (no subqueries); the
//     application-side guard is canonical.
//
// # Phase 1 scope
//
// The full override chain (Endpoint → Host → Inbound →
// System default), the security / transport /
// format-variable resolution, the wildcard `*` salt
// expansion, and the multi-port selection all live in
// the subscription service that consumes the model.
// We only store what the operator configures; the
// subscription service reads it back at fetch time.
//
// # Persistence
//
// The MemoryStore in this package embeds the
// `Endpoints` array on the Host struct (Phase 0
// default). Migration 0004_hosts_v3.sql defines the
// relational form (`hosts` + `host_endpoints`) for the
// Phase 1.1 PgStore. The cross-entity validation in the
// Service layer is the canonical guard either way.
package hosts

import (
	"time"

	"github.com/google/uuid"
)

// HostType is the topology of a Host. The string values are
// the canonical names that appear in JSON, in the
// subscription URLs, and in the agent protocol — do not change
// them without a coordinated migration.
type HostType string

// HostType values. Chain is intentionally absent — the
// architecture defers it to Phase 4 (cascade topology).
const (
	// HostTypeDirect is a single-endpoint Host. The
	// rendered subscription carries exactly one URL.
	HostTypeDirect HostType = "direct"
	// HostTypeBalancer is a multi-endpoint Host that
	// hands the user N URLs (one per endpoint) and lets
	// the client pick. The Panel does not implement an
	// in-proxy load balancer; the URL list is the
	// balance.
	HostTypeBalancer HostType = "balancer"
)

// BalancerStrategy is the failover / selection strategy for
// type=balancer hosts. Phase 1 stores the strategy but the
// resolution lives in the client (clients pick the first
// reachable URL). Strategies that require an active probe
// (leastPing, urltest) are reserved for the agent-side
// implementation that lands with the host health PR.
type BalancerStrategy string

// BalancerStrategy values. The set is closed — see
// validateStrategy in service.go.
const (
	StrategyRoundRobin  BalancerStrategy = "round_robin"
	StrategyLeastLoaded BalancerStrategy = "least_loaded"
	StrategyRandom      BalancerStrategy = "random"
	// StrategyLeastPing and StrategyURLTest require the
	// agent to actively probe endpoints; they are
	// declared here so the model and the wire format
	// are stable, but the resolution lives in a later
	// PR. Operators can pick them today — the
	// subscription service treats them as "round_robin
	// with a TODO marker" until the probe side lands.
	StrategyLeastPing BalancerStrategy = "least_ping"
	StrategyURLTest   BalancerStrategy = "urltest"
)

// UserStatus is the panel-side status of an end-user VPN
// account. Hosts use the list to filter visibility per
// ARCHITECTURE.md §10.1.3. The string values match the
// `users.status` column convention from migration
// 0001_initial.sql.
type UserStatus string

// UserStatus values. The set is closed; an unknown status
// in a Host's status_filter is a validation error.
const (
	UserStatusActive   UserStatus = "active"
	UserStatusOnHold   UserStatus = "on_hold"
	UserStatusExpired  UserStatus = "expired"
	UserStatusLimited  UserStatus = "limited"
	UserStatusDisabled UserStatus = "disabled"
)

// Host is the bundle of endpoints visible to users as a
// single product. The struct doubles as the JSON wire
// format — every field is tagged so the admin UI and the
// subscription service can round-trip it without a
// separate DTO.
type Host struct {
	ID          uuid.UUID `json:"id"`
	Remark      string    `json:"remark"`
	DisplayName string    `json:"displayName,omitempty"`
	Type        HostType  `json:"type"`
	Enabled     bool      `json:"enabled"`
	// Priority orders hosts in the rendered subscription
	// (lower = higher). Two hosts with the same priority
	// are sorted by CreatedAt. The subscription service
	// applies the final sort; the Store preserves the
	// stored value.
	Priority int `json:"priority"`
	// StatusFilter limits the host to users in the
	// listed statuses. Empty / nil means "all users".
	StatusFilter []UserStatus `json:"statusFilter,omitempty"`
	// Country / City are ISO codes / free-form labels
	// the UI uses for the country flag and the city
	// name in the rendered subscription URL.
	Country string `json:"country,omitempty"`
	City    string `json:"city,omitempty"`
	// Tags are operator-supplied free-form labels. The
	// admin UI uses them to group hosts in lists; the
	// subscription service ignores them.
	Tags []string `json:"tags,omitempty"`
	// Endpoints is the bundle. Validation enforces
	// 1 (direct) or ≥2 (balancer) — see service.go.
	Endpoints []Endpoint `json:"endpoints"`
	// Balancer is required for type=balancer and must
	// be nil for type=direct.
	Balancer *Balancer `json:"balancer,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Endpoint is one (Node, Inbound) pair + a per-endpoint
// override layer. The override is applied at render time
// by the subscription service; the Host model only stores
// it.
//
// # Inbound reference
//
// Endpoint.InboundID is a UUID into the `inbounds` table
// (added in PR #34). The protocol family — VLESS,
// Hysteria 2, Shadowsocks, Trojan — lives on the Inbound
// itself; the Host's Endpoint does not duplicate it.
// The Service layer enforces two invariants:
//
//  1. The InboundID must resolve to an existing
//     `inbounds` row (FK target).
//  2. The referenced Inbound's NodeID must equal the
//     Endpoint's NodeID. PostgreSQL cannot express
//     this as a CHECK constraint (no subqueries); the
//     application-side guard is canonical.
//
// A pre-PR-#35 snapshot of this model used a free-form
// `Protocol` string with a Service-side allow-list.
// That field is gone; new endpoints carry an
// InboundID, and the protocol is read off the
// referenced Inbound at render time.
type Endpoint struct {
	// ID is a server-side generated UUID. Endpoints are
	// addressed by ID in the subscription service
	// (failover_endpoint_ids references these), so the
	// ID has to be stable across re-renders.
	ID uuid.UUID `json:"id"`
	// NodeID is the FK into the `nodes` table. The
	// Service layer rejects Endpoints whose NodeID does
	// not resolve.
	NodeID uuid.UUID `json:"nodeId"`
	// InboundID is the FK into the `inbounds` table
	// (PR #34). The protocol family, the listen port,
	// the TLS / Reality / etc. knobs all live on the
	// referenced Inbound; the Endpoint only carries
	// per-endpoint overrides on top of the Inbound
	// defaults.
	InboundID uuid.UUID `json:"inboundId"`
	// InboundID.
	Protocol string `json:"protocol"`
	// Weight is the per-endpoint load-balancing
	// weight. Default 1; zero or negative is rejected
	// by validation.
	Weight int `json:"weight"`
	// Override fields. Each nil/empty field means "use
	// the Host-level default (or the system default)".
	// The subscription service merges the layers in
	// Endpoint → Host → System order.
	Address []string `json:"address,omitempty"`
	// Port is *int so we can distinguish "absent" from
	// "explicitly zero". A pointer-typed integer is the
	// idiomatic Go way to express "optional" without a
	// custom Optional type.
	Port *int     `json:"port,omitempty"`
	SNI  []string `json:"sni,omitempty"`
	Host []string `json:"host,omitempty"`
	Path string   `json:"path,omitempty"`
	// DownloadHostID, when non-nil, references a
	// separate Host whose endpoints are the
	// "download farm" for the XHTTP transport. The
	// sing-box renderer emits a `download_settings`
	// block on the vless outbound whose
	// `address` / `port` come from a random endpoint
	// of the referenced host. The download host is
	// operator-controlled and is NOT in the user's
	// pool — the Service looks it up by id directly.
	// Nil / zero = no download_settings block.
	DownloadHostID *uuid.UUID `json:"downloadHostId,omitempty"`
}

// Balancer is the per-Host configuration for type=balancer
// hosts. Strategy is required; Healthcheck* are optional —
// when set, the agent will probe Endpoints on a schedule
// and prune the dead ones from the rendered subscription
// URL list. The agent-side probe lands in a later PR; the
// fields exist now so the wire format is stable.
type Balancer struct {
	Strategy BalancerStrategy `json:"strategy"`
	// HealthcheckURL is the URL the agent fetches to
	// decide an endpoint is alive. Per-endpoint, so
	// HealthcheckURL is just a default and the
	// subscription service may build a per-endpoint URL
	// from a template.
	HealthcheckURL         string `json:"healthcheckUrl,omitempty"`
	HealthcheckIntervalSec int    `json:"healthcheckIntervalSec,omitempty"`
	// FailoverEndpointIDs lists endpoints (by Endpoint.ID)
	// that the agent should use as cold-standby targets
	// when all primary endpoints are down. IDs must
	// reference endpoints in the same host.
	FailoverEndpointIDs []uuid.UUID `json:"failoverEndpointIds,omitempty"`
}

// IsValid is the cheap pre-flight check used by the store
// to reject obviously broken inserts. The Service layer
// runs a heavier validation on top of this.
func (h *Host) IsValid() bool {
	if h == nil || h.Remark == "" {
		return false
	}
	if h.Type != HostTypeDirect && h.Type != HostTypeBalancer {
		return false
	}
	if len(h.Endpoints) == 0 {
		return false
	}
	for _, ep := range h.Endpoints {
		if !ep.isValid() {
			return false
		}
	}
	return true
}

// isValid is the Endpoint equivalent of IsValid. The
// Inbound resolution + cross-entity check (Endpoint.NodeID
// must equal Inbound.NodeID) lives in service.go —
// isValid only checks structural rules.
func (e *Endpoint) isValid() bool {
	if e == nil {
		return false
	}
	if e.NodeID == uuid.Nil {
		return false
	}
	if e.InboundID == uuid.Nil {
		return false
	}
	if e.Weight < 0 {
		return false
	}
	return true
}
