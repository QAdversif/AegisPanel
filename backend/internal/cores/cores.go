// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package cores defines the CoreProvider interface — the
// abstraction that lets the panel talk to any VPN core
// (sing-box today, Xray / Hysteria-2 in the future) without the
// rest of the codebase having to know which one is wired in.
//
// The interface is intentionally small: 8 methods, all of which
// correspond to a step in the agent's "give me a config, apply it,
// check on it" lifecycle. The package also carries the
// `Capabilities` enum (which protocols and features a given
// provider supports) and a minimal `CoreConfig` DTO that every
// provider must be able to render and validate.
//
// # Why a separate package
//
// Putting CoreProvider in its own package keeps the rest of the
// runtime (auth, nodes, hosts, subscription) free of any
// `sing-box` or `xray` types. Implementations live under
// `internal/cores/<name>/` and self-register via the registry
// on import. The panel binary pulls in the providers it wants
// by importing the corresponding subpackages — a build with
// only the sing-box provider has no Xray types in the binary.
package cores

import (
	"context"
	"time"
)

// Capability is a single feature flag exposed by a CoreProvider.
// The string values mirror the names in ARCHITECTURE.md §7 so
// the JSON wire format is human-readable in logs and
// `GET /api/v1/cores` responses.
//
// New capabilities should be added here, not in a per-provider
// file, so the registry can give a stable cross-provider
// capability matrix. The Capabilities.Supports method is the
// single source of truth for "does this provider do X".
type Capability string

// Phase 1 capabilities. Mirrors ARCHITECTURE.md §7.
const (
	// CapProtocol family — at least one inbound protocol is
	// required. A provider that supports none of these is
	// useless and the registry should refuse to expose it.
	CapVLESS           Capability = "VLESS"
	CapVMESS           Capability = "VMESS"
	CapTrojan          Capability = "TROJAN"
	CapShadowsocks     Capability = "SHADOWSOCKS"
	CapVLESSReality    Capability = "VLESS_REALITY"
	CapVLESSXTLSVision Capability = "VLESS_XTLS_VISION"
	CapHY2             Capability = "HY2"
	CapTUIC            Capability = "TUIC"

	// CapProtocol-feature family — orthogonal to the protocol
	// flags above.
	CapBalancer       Capability = "BALANCER"
	CapACL            Capability = "ACL"
	CapCascade        Capability = "CASCADE"
	CapDynamicUsers   Capability = "DYNAMIC_USERS"
	CapWildcardRandom Capability = "WILDCARD_RANDOM"
	CapMultiPort      Capability = "MULTI_PORT"
	CapXHTTPDownload  Capability = "XHTTP_DOWNLOAD"
)

// Capabilities is the immutable capability matrix of a single
// provider. The provider hands one of these to the panel at
// registration time; the panel reads it from then on.
type Capabilities struct {
	// Name is the canonical provider name, e.g. "sing-box".
	// Must be unique across all registered providers.
	Name string
	// Version is the provider's own self-reported version
	// (e.g. "1.8.0"). Updated by the provider when it knows
	// its own version; the registry does not validate it.
	Version string
	// Flags is the set of supported capabilities. Lookups go
	// through Supports so we can add derived capabilities
	// later (e.g. "any VLESS variant") without rewriting
	// every caller.
	Flags []Capability
}

// Supports reports whether the provider has the given
// capability. A nil receiver returns false.
func (c *Capabilities) Supports(want Capability) bool {
	if c == nil {
		return false
	}
	for _, f := range c.Flags {
		if f == want {
			return true
		}
	}
	return false
}

// MarshalJSON renders the capability list as a sorted
// []string so the wire format is deterministic. The panel UI
// relies on this for diffing capability matrices.
func (c *Capabilities) MarshalJSON() ([]byte, error) {
	if c == nil {
		return []byte("null"), nil
	}
	seen := make(map[Capability]struct{}, len(c.Flags))
	out := make([]string, 0, len(c.Flags))
	for _, f := range c.Flags {
		if _, dup := seen[f]; dup {
			continue
		}
		seen[f] = struct{}{}
		out = append(out, string(f))
	}
	// jsonMarshalOrdered sorts the slice internally before
	// serialising; we just hand it the deduped list.
	return jsonMarshalOrdered(out, c.Name, c.Version)
}

// CoreConfig is the minimal normalised DTO the panel hands
// to a provider for rendering. Concrete provider
// implementations (sing-box, xray, …) map this DTO into their
// native JSON / YAML. Only the fields Phase 1 actually uses
// are typed here; the rest is held in `Experimental` until we
// need it in Phase 1+.
//
// We deliberately do NOT embed a full schema here — that would
// freeze the internal model of every provider behind a
// `cores` package, which would defeat the abstraction.
type CoreConfig struct {
	Inbounds  []InboundSpec  `json:"inbounds"`
	Outbounds []OutboundSpec `json:"outbounds"`
	// Experimental is a typed-any escape hatch. Providers are
	// free to ignore it; the panel uses it to pass
	// provider-specific options without a cores-package
	// schema change for every one.
	Experimental map[string]any `json:"experimental,omitempty"`
}

// InboundSpec is the placeholder for an inbound. Concrete
// fields land here in the provider's PR; for now it carries
// only the type and a reference back to the panel's Host so
// the provider can resolve overrides and format variables.
type InboundSpec struct {
	// Tag is the inbound's stable identifier inside the
	// rendered config (sing-box "inbounds[*].tag" etc.).
	// Providers MUST keep this stable across renders so a
	// diff can be computed.
	Tag string `json:"tag"`
	// Type is the protocol family — "vless", "hysteria2", …
	// kept as a free-form string so providers can advertise
	// types the panel does not yet model.
	Type string `json:"type"`
	// HostID is the panel-side reference to the Host this
	// inbound belongs to. Providers do not need it for
	// rendering but the panel uses it to fetch overrides
	// (SNI, port, transport) before calling RenderConfig.
	HostID string `json:"host_id"`
}

// OutboundSpec is the placeholder for an outbound. Most
// installations need exactly one ("direct" or "block"); Phase
// 1 leaves the schema open.
type OutboundSpec struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
}

// CoreStatus is what ParseStatus returns. The Status string is
// provider-specific ("running", "degraded", "starting", …) —
// the panel surfaces it verbatim in the UI and in agent
// heartbeat logs.
type CoreStatus struct {
	Status    string
	Version   string
	StartedAt time.Time
	// Uptime is the panel-computed value; providers can fill
	// it in directly or leave it zero (the panel will
	// compute it from StartedAt at presentation time).
	Uptime time.Duration
}

// UserStat is one row of per-user traffic accounting. The
// panel pulls these from the agent on a schedule and rolls
// them up into the per-user "traffic_used_bytes" column.
type UserStat struct {
	UserUUID  string
	BytesUp   int64
	BytesDown int64
}

// CoreProvider is the interface every VPN core must satisfy.
// See ARCHITECTURE.md §7 for the per-method rationale. The
// contract is intentionally narrow: the panel does not need
// to know whether a core uses gRPC, REST, or `os/exec` to
// talk to its agent.
type CoreProvider interface {
	// Name returns the provider's canonical name, e.g.
	// "sing-box". Must match the Name field of the
	// Capabilities this provider registers with.
	Name() string
	// Version returns the provider's own version, e.g.
	// "1.8.0". Used in `GET /api/v1/cores` and in agent
	// heartbeat logs.
	Version() string
	// Capabilities returns the provider's static capability
	// matrix. Returned by value so the registry owns the
	// canonical copy.
	Capabilities() Capabilities
	// RenderConfig converts the panel's normalised CoreConfig
	// into the provider's native format (JSON for sing-box,
	// JSON for xray, …). The returned string is what
	// gets shipped to the agent.
	RenderConfig(ctx context.Context, cfg CoreConfig) (string, error)
	// ValidateConfig parses `raw` and reports whether it is
	// a valid config for this provider. Called before apply
	// to catch typos without round-tripping to the node.
	ValidateConfig(ctx context.Context, raw []byte) error
	// Diff returns a unified diff between two configs.
	// Empty string means "no change". Used in the agent
	// activity log so an operator can see what changed
	// across a render.
	Diff(prev, next []byte) (string, error)
	// Apply ships `cfg` to the named node and waits for
	// the agent's acknowledgement. The provider decides
	// whether that means "agent responded OK" or
	// "core reloaded and reported healthy". The default
	// behaviour in Phase 1 is "agent responded OK".
	Apply(ctx context.Context, nodeID string, cfg []byte) error
	// ParseStatus converts the agent's status payload into
	// the panel's CoreStatus. The payload format is
	// provider-specific.
	ParseStatus(raw []byte) (CoreStatus, error)
	// ParseStats converts the agent's per-user stats
	// payload into []UserStat. The payload format is
	// provider-specific.
	ParseStats(raw []byte) ([]UserStat, error)
}
