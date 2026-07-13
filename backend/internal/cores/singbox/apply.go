// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"errors"
	"fmt"
)

// ErrApplyNotImplemented is returned by Apply. The sing-box
// provider is the canonical example of "the panel can
// render a config but cannot yet ship it to a node" — the
// agent gRPC transport lands in a later PR.
//
// Surfacing this as a typed error (not a panic) lets the
// panel UI render the failure with a useful message ("agent
// transport not yet wired up") instead of crashing the
// whole request. Callers should check with errors.Is.
var ErrApplyNotImplemented = errors.New("singbox: apply: agent transport not implemented (Phase 1 stub)")

// Apply implements cores.CoreProvider. The current
// implementation is a stub that always returns
// ErrApplyNotImplemented. The real implementation will:
//
//  1. Open the gRPC channel to the named node's agent
//     (the channel set up during the mTLS handshake).
//  2. Stream the rendered config as a single
//     `ApplyConfig` RPC.
//  3. Wait for the agent to acknowledge the apply
//     (which in turn waits for sing-box's "config
//     loaded" event).
//  4. Return nil on success, or a wrapped error on
//     transport / apply / timeout failure.
//
// Until that lands, the panel's "render" path is fully
// exercised by tests and the dev UI; the "apply" path
// surfaces this error so it is obvious to a developer
// running the panel locally that they have hit the
// end of the Phase 1 implementation.
func (p *Provider) Apply(_ context.Context, nodeID string, _ []byte) error {
	return fmt.Errorf("node %q: %w", nodeID, ErrApplyNotImplemented)
}
