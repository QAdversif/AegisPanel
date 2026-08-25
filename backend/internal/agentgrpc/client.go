// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package agentgrpc is the panel-side transport package for the
// `aegis.v1` gRPC control plane. v0.8.29 introduces it as the
// dual-stack companion to the v0.4.0-b HTTP+bearer surface
// (singbox/apply.go). The HTTP transport is the default until
// the v0.8.31 migration CLI rotates a node to gRPC; after
// v0.8.32 the HTTP transport is removed and gRPC is the only
// path (see docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md).
//
// # Why a transport package (not a free function)
//
// The Apply side-effect is non-trivial: a 401 from the agent
// means the bearer is stale, the panel must SSH into the node,
// read agent.env, update the DB, and retry once. gRPC surfaces
// the same condition as `codes.Unauthenticated`. Both paths
// need the same retry plumbing; putting the retry inside the
// transport (rather than the singbox/Provider) means the
// singbox/Provider sees a clean `error` return and the
// 401/Unauthenticated -> refresh -> retry contract lives in
// one place. v0.8.30+ replaces the bearer with mTLS and the
// retry path becomes a no-op (mTLS failures are terminal, not
// refresh-able).
//
// # v0.8.29 vs v0.8.30
//
// v0.8.29 uses the same bearer secret on both transports; the
// HTTP transport is the default. v0.8.30 adds the mTLS
// handshake on `:7001`; the bearer path is removed in v0.8.32.
// The transport package survives v0.8.32 with just the gRPC
// transport.
package agentgrpc

import (
	"context"

	"github.com/google/uuid"
)

// Client is the contract the singbox/Provider consumes. The four
// methods mirror the v0.4.0-b HTTP surface 1:1:
//
//   - Apply:  POST /v1/apply  (write config + reload sing-box)
//   - Status: GET  /v1/status
//   - Stats:  GET  /v1/stats
//   - Health: GET  /healthz
//
// Each method takes a `context.Context` (cancellation +
// deadlines) and the node UUID; the transport resolves the
// node's listen address via the `NodeResolver` it was
// constructed with. The `nodeID` is the same field the
// `BearerRefresher` uses for the 401/Unauthenticated retry path.
//
// Errors:
//   - `context.Canceled` / `context.DeadlineExceeded` — the
//     caller's deadline or shutdown signal.
//   - `ErrAgentStaleBearer` — the 401/Unauthenticated refresh
//     path exhausted its retry budget (today: one retry;
//     v0.8.30 mTLS removes the path entirely).
//   - any other error: opaque transport failure.
type Client interface {
	Apply(ctx context.Context, nodeID uuid.UUID, cfg []byte) error
	// Status, Stats, Health are stubs in v0.8.29. The
	// panel-side singbox/Provider does not consume them
	// today (the HTTP /v1/status + /v1/stats + /healthz
	// surface is the operator's debug path; the
	// BatchedApplier only consumes Apply). The methods
	// are declared so the v0.8.30 health probe can land
	// as a one-line method addition.
	Status(ctx context.Context, nodeID uuid.UUID) (StatusResult, error)
	Stats(ctx context.Context, nodeID uuid.UUID) (StatsResult, error)
	Health(ctx context.Context, nodeID uuid.UUID) error

	// Close releases the underlying transport. The
	// gRPC transport closes the connection pool; the
	// HTTP transport is a no-op (the `*http.Client`
	// has no persistent state). v0.8.29 PR 3 calls
	// this from `app.Build` shutdown.
	Close() error
}

// StatusResult is the transport-agnostic outcome of a Status
// call. The HTTP transport maps the v0.4.0-b /v1/status JSON
// response into this struct; the gRPC transport maps the
// aegis.v1.StatusResponse into the same struct so the
// BatchedApplier + operator UI can consume either transport
// without a separate DTO.
type StatusResult struct {
	State          string
	AgentVersion   string
	SingboxVersion string
	UptimeSeconds  int64
}

// StatsResult is the per-user traffic counters map. Empty
// until v0.4.0-c wires the sing-box clash-api; today both
// transports return an empty map.
type StatsResult struct {
	UserStats map[string]UserTrafficStats
}

// UserTrafficStats is a per-user byte counter pair. Same
// shape as the v0.4.0-b HTTP /v1/stats surface (only the
// sing-box clash-api is the source).
type UserTrafficStats struct {
	UploadBytes   int64
	DownloadBytes int64
}

// NodeResolver is the dependency the transports need to
// map a node UUID to (address, bearer). The HTTP transport
// calls `ResolveAddr` for the agent's listen address and
// `GetBearer` for the Authorization header; the gRPC
// transport does the same. The implementation lives in
// `nodes.Service` (an adapter constructed in `app.Build`);
// the interface here exists so the agentgrpc package does
// not import `internal/nodes` (which would create a cycle
// once nodes.Service starts consuming agentgrpc.Client in
// v0.8.31).
//
// `Refresh` is the 401/Unauthenticated recovery path: when
// the agent rejects the panel's stored bearer, the
// transport calls `Refresh` (which SSHes into the node,
// reads agent.env, and updates the DB row) and retries the
// request with the returned bearer.
type NodeResolver interface {
	// ResolveAddr returns the agent's listen address
	// for `nodeID` (host:port, no scheme; the
	// transport adds the right prefix).
	ResolveAddr(ctx context.Context, nodeID uuid.UUID) (string, error)
	// GetBearer returns the panel's currently-stored
	// bearer for `nodeID`. The transport attaches
	// it as `Authorization: Bearer <token>` for the
	// HTTP path, or as gRPC metadata for the gRPC
	// path.
	GetBearer(ctx context.Context, nodeID uuid.UUID) (string, error)
	// Refresh returns the freshest agent bearer for
	// `nodeID`. The transport re-issues the failed
	// request with the returned bearer; if the second
	// attempt also returns 401/Unauthenticated, the
	// transport surfaces `ErrAgentStaleBearer` to the
	// caller and stops retrying.
	Refresh(ctx context.Context, nodeID uuid.UUID) (string, error)
	// LoadMTLS returns the v0.8.30 mTLS material for
	// `nodeID`: the panel's client cert + key + the
	// root CA bundle. The gRPC transport uses these
	// to dial the agent with mutual TLS. The method
	// is OPTIONAL: a resolver that returns
	// `ErrMTLSNotConfigured` (or any error) means
	// "mTLS not wired for this node"; the gRPC
	// transport falls back to the v0.8.29 plaintext
	// path. The HTTP transport ignores this method.
	LoadMTLS(ctx context.Context, nodeID uuid.UUID) (clientCert, clientKey, caBundle []byte, err error)
}
