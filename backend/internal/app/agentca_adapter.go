// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Adapter that bridges the `*agentca.Service` to the
// `nodes.AgentCertIssuer` interface consumed by
// `nodes.WithAgentCA`.
//
// # Why in `app` (not in `agentca`)
//
// The interface is in the `nodes` package (the
// consumer); the implementation is in the `agentca`
// package (the producer). A naive adapter in
// `agentca` would have to import `nodes` to
// reference the interface, which closes an import
// cycle. Putting the adapter in `app` (which can
// import both `agentca` and `nodes`) breaks the
// cycle.

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/agentca"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// agentCAAdapter wraps `*agentca.Service` so it
// satisfies the `nodes.AgentCertIssuer` interface.
// The conversion is a no-op for PEM strings
// (immutable) and a `time.Time.String()` call for
// the expiry.
type agentCAAdapter struct {
	svc *agentca.Service
}

// EnsureNodeCerts is the interface method. The
// adapter is the only place the field-by-field
// conversion lives; a future refactor that adds
// fields to either side surfaces here first.
func (a agentCAAdapter) EnsureNodeCerts(ctx context.Context, nodeID uuid.UUID, addr string) (*nodes.IssuedNodeCerts, error) {
	issued, err := a.svc.EnsureNodeCerts(ctx, nodeID, addr)
	if err != nil {
		return nil, fmt.Errorf("app: agentCAAdapter.EnsureNodeCerts: %w", err)
	}
	return &nodes.IssuedNodeCerts{
		ServerCertPEM:   issued.ServerCertPEM,
		ServerKeyPEM:    issued.ServerKeyPEM,
		ClientCertPEM:   issued.ClientCertPEM,
		ServerExpiresAt: issued.ServerExpiresAt.UTC().Format(time.RFC3339),
	}, nil
}

// RootCertPEM forwards to the underlying
// `*agentca.Service.RootCertPEM`. The adapter is
// the only place the error is translated to the
// `nodes.AgentCertIssuer` contract (no translation
// today; the agentca error type is exported as
// `agentca.ErrNotFound` and the call site compares
// it).
func (a agentCAAdapter) RootCertPEM() (string, error) {
	return a.svc.RootCertPEM()
}

// Compile-time check that the adapter implements
// the interface. A future refactor that breaks the
// contract surfaces here at the package level.
var _ nodes.AgentCertIssuer = (*agentCAAdapter)(nil)
