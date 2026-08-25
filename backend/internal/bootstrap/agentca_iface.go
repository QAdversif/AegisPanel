// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This file is intentionally empty. The
// `AgentCertIssuer` interface was originally here but
// was moved back to `internal/nodes/agentca_iface.go`
// in v0.8.30 PR 1c to avoid a pre-existing import
// cycle between `bootstrap` and `nodes`.
//
// # Why the move
//
// The `bootstrap` package already imports `nodes`
// (for the `NodeRow` projection + the `NodeProvider`
// interface); `nodes` imports `bootstrap` (for
// `bootstrap.NewService` from the handler). The
// cycle is broken at the production level because
// the `bootstrap.Service.nodes` field is a
// `bootstrap.NodeProvider` (interface), not a
// concrete `*nodes.Service`. Adding another
// interface to `bootstrap` would force the
// import to become cyclic when `nodes` consumed
// the interface (the `nodes -> bootstrap` edge
// already exists).
//
// The interface lives in `nodes` for v0.8.30;
// v0.8.31 can revisit this when the `nodes ->
// bootstrap` import is removed (the handler
// `nodes/handler.go` does not need the
// `bootstrap.NewService` constructor; it could
// accept a `bootstrap.NodeProvider` factory
// instead).

package bootstrap
