// SPDX-License-Identifier: AGPL-3.0-or-later
//
// sing-box CoreProvider. v0.4.0-mvp-batched adds the
// `Apply` side-effect: the rendered sing-box config is
// POSTed to the node's aegis-agent, which writes it to
// disk and reloads sing-box.
//
// v0.8.29 (this file): the HTTP+bearer transport moves
// out into `internal/agentgrpc` (see
// `internal/agentgrpc/http_transport.go` and
// `internal/agentgrpc/grpc_transport.go`). The
// `Provider` now holds an `agentgrpc.Client` and
// `Apply` is a one-liner that delegates. The
// 401 -> BearerRefresher -> one-retry contract is
// enforced inside the transport; the Provider is
// transport-agnostic.
//
// # v0.8.30+
//
// The bearer is replaced by mTLS (see
// docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md).
// The Provider does not need to change.

package singbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/agentgrpc"
)

// providerImpl is the concrete Provider type. The alias
// `Provider = providerImpl` in singbox.go keeps the public
// name stable so existing imports (`singbox.New()`) work
// after the v0.8.29 refactor. Splitting the type alias from
// the field declaration lets apply.go own the `client`
// field without an import cycle on `singbox` (the alias
// makes `Provider` and `providerImpl` the same type at
// compile time, so external code never sees a difference).
type providerImpl struct {
	// client is the transport the Provider uses to
	// ship rendered configs to the agent. Set by
	// Configure at boot. The zero value is safe
	// for the Capabilities / Name / Version /
	// RenderConfig / ValidateConfig / Diff /
	// ParseStatus / ParseStats methods, but
	// Apply() returns an error until Configure
	// has been called.
	client agentgrpc.Client
}

// ErrApplyNotConfigured is the sentinel returned by
// `Apply` when `Configure` has not been called. Kept
// exported so callers that explicitly want the "not
// wired" path can still detect it via `errors.Is`.
var ErrApplyNotConfigured = errors.New("singbox: provider not configured (call Configure first)")

// Configure wires the provider's transport dependency. Must
// be called before any Apply. The first call wins;
// subsequent calls are no-ops so a re-run of a configuration
// test does not swap the client out from under an in-flight
// apply.
//
// In production, main() calls this once at boot. Tests call
// it per-test in a fresh Provider with a `agentgrpc.Client`
// backed by a `httptest.Server` (see `apply_test.go`).
func (p *providerImpl) Configure(client agentgrpc.Client) {
	if p.client != nil {
		return
	}
	p.client = client
}

// Apply implements cores.CoreProvider. The v0.4.0
// implementation POSTs the rendered config to the
// node's aegis-agent `/v1/apply` endpoint (HTTP path)
// or invokes the `AegisAgent.Apply` RPC (gRPC path);
// the choice is hidden behind the `agentgrpc.Client`
// interface.
//
// The 401 -> BearerRefresher -> one-retry contract lives
// inside the transport. v0.8.7 (PR #188) introduced the
// retry; v0.8.28.7 (PR #292) closed the agent-FD leak
// during the retry path; v0.8.29 (this file) moved the
// plumbing into `internal/agentgrpc`. The Provider sees
// only the final `error` return value: `nil` for any
// 2xx (including a successful retry), a wrapped error
// for any non-2xx or transport failure.
func (p *providerImpl) Apply(ctx context.Context, nodeID string, cfg []byte) error {
	if p.client == nil {
		return fmt.Errorf("singbox apply: provider not configured (call singbox.Configure first): %w", ErrApplyNotConfigured)
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return fmt.Errorf("singbox apply: node %q: not a UUID: %w", nodeID, err)
	}
	if err := p.client.Apply(ctx, id, cfg); err != nil {
		return fmt.Errorf("singbox apply: %w", err)
	}
	return nil
}
