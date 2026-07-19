// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package inbounds implements the panel-side CRUD for the
// `inbounds` table (migration 0003_node_inbounds.sql).
//
// # Model
//
// An Inbound is a single protocol listener on a specific
// node (VLESS-Reality, Hysteria 2, Shadowsocks, …). It
// carries the per-listener parameters (port, TLS, Reality
// keys, …) that the sing-box provider's RenderConfig
// already understands; the panel passes them through
// unchanged.
//
// # Relationship to the v3 Host model (PR #33)
//
// The v3 Host model (ARCHITECTURE.md §10) calls for a
// Host.Endpoint to reference an Inbound by `inbound_id`.
// PR #33 implemented the model with a temporary
// `Endpoint.Protocol` string + Service-side allow-list;
// the next PR (#35) will replace the string with
// `InboundID uuid.UUID` and add a migration that
// re-points existing endpoints at the matching inbounds.
//
// Until that lands, the inbounds CRUD is fully usable
// from the admin UI / API — the operator registers
// inbounds on nodes, and the host manager can already
// reference them by ID for the future migration. The
// Service here enforces the same protocol allow-list the
// host manager enforces, so the eventual join is a
// straight rename + a FK addition.
//
// # Relationship to inbound_sets (0001 migration)
//
// `inbound_sets` from the 0001 migration is a *reusable
// template* concept (named bundle of inbounds that
// several nodes can subscribe to). The `inbounds` table
// added here is a *concrete listener* on a specific
// node. They serve different purposes and the package
// deliberately does not model inbound_sets — that lives
// (if at all) in a future PR that adds the
// "render-set-for-node" path.
package inbounds

import (
	"time"

	"github.com/google/uuid"
)

// Protocol is the protocol family of an Inbound. The
// string values match the sing-box provider's
// per-protocol renderers (internal/cores/singbox/) and
// are pinned by the DB CHECK constraint in
// migration 0003_node_inbounds.sql.
type Protocol string

// Protocol values. The set is closed (see
// allowedProtocols in service.go) — any value outside
// this list is rejected at the Service boundary.
const (
	ProtocolVLESS       Protocol = "vless"
	ProtocolHysteria2   Protocol = "hysteria2"
	ProtocolShadowsocks Protocol = "shadowsocks"
	ProtocolTrojan      Protocol = "trojan"
)

// defaultListen is the default bind address. sing-box
// accepts "::" (IPv6 wildcard) and "0.0.0.0" (IPv4
// wildcard); "::" works on every modern OS for both
// stacks. The Go side keeps it as a const so the
// Service layer can normalise empty inputs.
const defaultListen = "::"

// Inbound is the panel-side view of a single protocol
// listener on a node. The fields mirror the `inbounds`
// table one-to-one; we keep them as a Go struct rather
// than `map[string]any` so handlers can rely on the
// type at compile time.
type Inbound struct {
	ID uuid.UUID `json:"id"`
	// NodeID is the FK to the `nodes` table. The
	// Service layer rejects Inbounds whose NodeID
	// does not resolve, mirroring the host manager's
	// cross-entity validation.
	NodeID uuid.UUID `json:"nodeId"`
	// Name is the operator's human-readable label,
	// unique per node. The DB enforces this through
	// the UNIQUE (node_id, name) constraint; the
	// Service layer surfaces the violation as a
	// 409 Conflict.
	Name string `json:"name"`
	// Protocol is the protocol family. Closed set;
	// see the Protocol constants above.
	Protocol Protocol `json:"protocol"`
	// Listen is the bind address. Defaults to "::"
	// (IPv6 wildcard, dual-stack on every modern
	// OS).
	Listen string `json:"listen"`
	// ListenPort is the primary TCP/UDP port. The DB
	// UNIQUE (node_id, listen_port) constraint
	// enforces one-inbound-per-port-per-node at the
	// storage layer. The agent binds this port plus
	// every entry in ListenPorts.
	ListenPort int `json:"listenPort"`
	// ListenPorts is the optional list of additional
	// ports the same inbound also listens on. The
	// subscription renderer picks one at random per
	// fetch (defeating per-port DPI correlation);
	// the agent binds every entry with the same
	// protocol / params as the primary port. An
	// entry that collides with another inbound's
	// ListenPort on the same node is the operator's
	// responsibility to avoid (the DB enforces
	// ListenPort uniqueness, not the union of
	// ListenPort and ListenPorts). Nil / empty =
	// single-port mode (the historical default).
	ListenPorts []int `json:"listenPorts,omitempty"`
	// Enabled is a soft-disable. Disabled inbounds
	// are kept in the database so the operator can
	// re-enable them without re-entering the
	// configuration.
	Enabled bool `json:"enabled"`
	// Tags are operator-supplied free-form labels.
	// The admin UI uses them to group inbounds in
	// lists; the agent reads them during apply to
	// decide which inbounds to render.
	Tags []string `json:"tags,omitempty"`
	// Params is the protocol-specific configuration
	// blob. The Go side stores it as map[string]any
	// because the per-protocol schema is owned by
	// the sing-box provider and would otherwise
	// duplicate the renderer's parameters. The
	// service validator does not enforce a shape
	// here; the sing-box provider's RenderConfig is
	// the authoritative schema check.
	Params map[string]any `json:"params,omitempty"`

	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// IsValid reports whether the inbound carries the
// minimum data the Service layer requires to accept it.
// It is intentionally permissive — heavy validation
// (cross-entity, length, format) lives in
// `Service.Create` so it can return rich per-field
// errors. IsValid is the cheap pre-flight check used
// by the store to reject obviously broken inserts.
func (i *Inbound) IsValid() bool {
	if i == nil || i.Name == "" {
		return false
	}
	if i.NodeID == uuid.Nil {
		return false
	}
	if i.Protocol == "" {
		return false
	}
	if i.ListenPort < 1 || i.ListenPort > 65535 {
		return false
	}
	if i.Listen == "" {
		return false
	}
	return true
}
