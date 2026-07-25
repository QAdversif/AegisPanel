// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package singbox is the sing-box CoreProvider implementation.
//
// sing-box (https://sing-box.sagernet.org) is the first concrete
// core Aegis ships against. The provider implements every method
// of cores.CoreProvider:
//
//   - RenderConfig builds a sing-box JSON configuration from the
//     panel's normalised CoreConfig DTO. Protocol-specific knobs
//     (port, TLS, Reality keys, …) flow in through
//     CoreConfig.Experimental["inbound_params"], keyed by inbound
//     tag. See render.go for the per-protocol mapping.
//
//   - ValidateConfig is a structural check — it does not validate
//     the schema, only that the payload is non-empty valid JSON
//     with an "inbounds" field. Schema-level validation belongs
//     to the agent, which actually feeds the config to the core.
//
//   - Diff returns a unified diff using go-difflib. The agent
//     uses it in its activity log so an operator can see what
//     changed across a render.
//
//   - Apply is currently a stub. The agent gRPC transport lands
//     in a later PR; once it does, Apply will round-trip the
//     rendered config to the agent and wait for ack.
//
//   - ParseStatus / ParseStats are also stubs. sing-box exposes
//     a JSON API and per-user stats over its gRPC extension;
//     the parsing side lands together with the agent gRPC.
//
// The package self-registers via init() so importing
// `internal/cores/singbox` is enough to wire the provider into
// the process-global registry. The cmd/aegis binary pulls in
// the package with a blank import.
package singbox

import (
	"github.com/QAdversif/AegisPanel/internal/cores"
)

// ProviderVersion is the sing-box version this provider is
// built against. The version string is what shows up in
// `GET /api/v1/cores` and in agent heartbeats, so it must be
// the *protocol* version (what the rendered config is valid
// for), not the Go module version.
//
// v0.4.0-mvp-batched pinned this to 1.14.0-beta.2 to match
// the binary version the install_singbox Ansible role
// deploys. 1.14.0 is a beta at the time of writing (the
// release has been in beta since 2026-07 with 50 alphas
// before that — sing-box ships 1.14.0-GA only after the
// feature set freezes). If a regression in 1.14.0-beta.x
// breaks the rendered config, bump this to the latest
// 1.13.x GA release; the Go provider's render output is
// backward-compatible across the 1.8.x → 1.13.x range.
//
// The v0.3.0 placeholder was "1.8.0" — kept as the minimum
// supported protocol version in the comment above the
// Capabilities() implementation.
const ProviderVersion = "1.14.0-beta.2"

// ProviderName is the canonical name the provider registers
// under. Kept as a constant so tests can assert against it.
const ProviderName = "sing-box"

// Provider is the sing-box CoreProvider. v0.4.0-mvp-batched
// adds two fields — nodes and client — that the panel
// wires in via Configure() at boot. The zero value is
// still safe for the Capabilities / Name / Version /
// RenderConfig / ValidateConfig / Diff / ParseStatus /
// ParseStats methods, but Apply() returns an error until
// Configure has been called.
type Provider struct {
	// nodes resolves a node UUID to the panel-side
	// (address, bearer) pair the agent /v1/apply endpoint
	// expects. Set by Configure at boot.
	nodes NodeResolver
	// client is the HTTP client used for the agent POST.
	// Defaults to a *http.Client with a 30-second timeout
	// when Configure is not called; tests inject a fake
	// (httptest.Server wrapped in *http.Client).
	client httpClient
}

// New returns a new sing-box provider. The package's init()
// already registers one in the global registry, so main code
// rarely needs to call this; tests do.
func New() *Provider { return &Provider{} }

// Name implements cores.CoreProvider.
func (p *Provider) Name() string { return ProviderName }

// Version implements cores.CoreProvider.
func (p *Provider) Version() string { return ProviderVersion }

// Capabilities implements cores.CoreProvider. The flag set is
// the Phase 1 MVP: every protocol sing-box 1.8+ supports at
// the inbound layer that the panel models in CoreConfig, plus
// the ortho features (balancer, ACL, dynamic users, multi-port)
// that the panel UI surfaces as a capability checkbox.
//
// sing-box supports every Phase 1 capability from
// ARCHITECTURE.md §7 except WIREGUARD, which is Phase 4+.
// The list is sorted in Capabilities.MarshalJSON so the wire
// format is deterministic; do not rely on its order here.
func (p *Provider) Capabilities() cores.Capabilities {
	return cores.Capabilities{
		Name:    ProviderName,
		Version: ProviderVersion,
		Flags: []cores.Capability{
			cores.CapVLESS,
			cores.CapVLESSReality,
			cores.CapVLESSXTLSVision,
			cores.CapHY2,
			cores.CapShadowsocks,
			cores.CapTrojan,
			cores.CapBalancer,
			cores.CapACL,
			cores.CapDynamicUsers,
			cores.CapMultiPort,
			cores.CapWildcardRandom,
			cores.CapXHTTPDownload,
		},
	}
}

// init registers a sing-box provider in the process-global
// registry. The cmd/aegis binary pulls in this package via a
// blank import (`_ "…/internal/cores/singbox"`), which is
// what fires this init. Tests can additionally call
// cores.Register(singbox.New()) to put a second instance in
// the registry under a different name (ErrDuplicateName guards
// against accidental double-registration).
func init() {
	if err := cores.Register(New()); err != nil {
		// Register at init can only fail on a duplicate name,
		// which means the binary imported this package twice
		// (directly and through a test) or the global registry
		// was pre-populated with a sing-box provider in a test
		// before main ran. Both are programmer errors — panic
		// with a clear message rather than starting the panel
		// without a real core.
		panic("cores/singbox: init register failed: " + err.Error())
	}
}
