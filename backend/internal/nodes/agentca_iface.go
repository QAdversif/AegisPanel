// SPDX-License-Identifier: AGPL-3.0-or-later
//
// AgentCertIssuer is the v0.8.30 mTLS cert issuer
// interface the `nodes.Service` consumes. The real
// implementation is `*agentca.Service` (adapted via
// the `app/agentca_adapter.go` bridge).
//
// # Why an interface (not a concrete *agentca.Service)
//
// The `nodes` package must not import the `agentca`
// package (cycle: `agentca` is wired by `app.Build`
// and the `agentca` package is a peer of `nodes`).
// Defining the interface here lets the consumer
// (`nodes`) and the producer (the adapter in
// `internal/app/agentca_adapter.go`) meet at a
// boundary the compiler can verify.
//
// # v0.8.30 PR 1c
//
// This PR wires the interface into `nodes.Service`
// (via `WithAgentCA`) but does not yet call
// `EnsureNodeCerts` from any production code path.
// The `bootstrap` package's `Provision` is the
// consumer; the integration lands in v0.8.30 PR 2
// alongside the mTLS handshake (the existing
// `bootstrap <-> nodes` import cycle blocks the
// `bootstrap` package from importing the
// `nodes.AgentCertIssuer` interface without a
// refactor; v0.8.31 lifts the cycle).

package nodes

import (
	"context"

	"github.com/google/uuid"
)

// IssuedNodeCerts is the result of
// AgentCertIssuer.EnsureNodeCerts. The shape mirrors
// `agentca.IssuedNodeCerts` (the `nodes` package
// cannot import `agentca` to avoid a cycle; the
// duplication is the cost of the interface
// boundary).
type IssuedNodeCerts struct {
	ServerCertPEM   string
	ServerKeyPEM    []byte
	ClientCertPEM   string
	ServerExpiresAt string // RFC 3339; surfaced for the audit log
}

// AgentCertIssuer is the contract the `nodes.Service`
// consumes. The real implementation is
// `*agentca.Service` (adapted via
// `internal/app/agentca_adapter.go`).
type AgentCertIssuer interface {
	EnsureNodeCerts(ctx context.Context, nodeID uuid.UUID, addr string) (*IssuedNodeCerts, error)
	// RootCertPEM returns the panel's root CA cert as
	// PEM. The v0.8.30 PR 2b mTLS client uses this
	// to verify the agent's server cert. Returns
	// `agentca.ErrNotFound` when no root has been
	// generated yet (the panel that has not
	// provisioned any nodes).
	RootCertPEM() (string, error)
}
