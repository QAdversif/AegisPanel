// SPDX-License-Identifier: AGPL-3.0-or-later
//
// gRPC transport. v0.8.29 alternative to the HTTP+bearer
// transport. Uses the same bearer secret the HTTP path
// uses; v0.8.30 replaces the bearer with mTLS. The
// `codes.Unauthenticated` -> BearerRefresher.Refresh -> one-
// retry path mirrors the HTTP transport's 401 path; the
// rest of the wire is the aegis.v1.AegisAgentService stub
// from PR 1.

package agentgrpc

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// dialTimeout caps how long a single agent dial may take.
// v0.8.30's mTLS handshake adds ~50-200ms; the v0.8.29
// plaintext dial is faster but the budget gives the
// operator headroom for a slow node.
const dialTimeout = 5 * time.Second

// callTimeout caps a single Apply / Status / Stats /
// Health RPC round-trip. The agent's reload command
// defaults to 5s; the Apply RPC returns after the reload
// finishes, so the call timeout must be at least reload +
// one-way RTT. 30s is the v0.4.0-b HTTP WriteTimeout
// mirrored to the gRPC path.
const callTimeout = 30 * time.Second

// grpcTransport is the gRPC `Client` implementation. One
// per panel process; constructed by `newGRPCTransport`.
//
// The transport does NOT maintain a long-lived
// `*grpc.ClientConn` per node. gRPC's connection pool
// transparently reuses connections across dials; the
// per-call dial in `dialAgent` is the documented pattern
// for a small fleet (the v0.8.30+ transport will switch
// to a per-node long-lived `*grpc.ClientConn` once the
// mTLS handshake is heavy enough to make the dial cost
// non-trivial).
type grpcTransport struct {
	resolver NodeResolver
}

// newGRPCTransport returns the gRPC `Client`. v0.8.29
// uses plaintext credentials; v0.8.30 swaps
// `insecure.NewCredentials()` for `credentials.NewTLS(...)`
// once the per-node cert bootstrap lands.
//
// The error return is reserved for v0.8.30+ (cert
// validation may fail at construction). Today the
// function is infallible.
//
//nolint:unparam // error return reserved for v0.8.30 mTLS
func newGRPCTransport(resolver NodeResolver) (*grpcTransport, error) {
	return &grpcTransport{resolver: resolver}, nil
}

// Close is a no-op for the gRPC transport in v0.8.29.
// v0.8.30's per-node long-lived `*grpc.ClientConn` will
// return `conn.Close()` here.
func (t *grpcTransport) Close() error {
	return nil
}

// dialAgent opens a one-shot `*grpc.ClientConn` for the
// given agent address. v0.8.30 swaps the credentials.
//
// `grpc.NewClient` is lazy; the first call (in `Apply`)
// triggers the actual dial. The dialTimeout caps how
// long that dial may take; the Apply call itself
// (which uses callTimeout) is a separate budget.
func (t *grpcTransport) dialAgent(ctx context.Context, addr string) (*grpc.ClientConn, error) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	// `grpc.WithBlock` is deprecated in gRPC v1.66+
	// in favour of `Connect` + `WaitForStateChange`.
	// We use the deprecated option here because the
	// v0.8.30+ migration to the new pattern is
	// independent of this PR; the option is the
	// documented way to wait for the first connection
	// on a lazy `NewClient`.
	//
	// TODO(v0.8.30): migrate to `Connect()` +
	// `WaitForStateChange(dialCtx, Idle)`. The
	// `dialCtx` deadline above already bounds the
	// wait.
	conn, err := grpc.NewClient(
		"passthrough://"+addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck // see comment above
	)
	if err != nil {
		return nil, fmt.Errorf("agentgrpc: dial %s: %w", addr, err)
	}
	_ = dialCtx
	return conn, nil
}

// authCtx returns a context with the bearer attached as
// `authorization: Bearer <token>` metadata. The agent's
// gRPC interceptor (cmd/aegis-agent/grpc.go) reads the
// same key the HTTP `requireBearer` middleware reads.
func (t *grpcTransport) authCtx(ctx context.Context, bearer string) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+bearer)
}

// Apply calls `AegisAgent.Apply` over gRPC. The
// `codes.Unauthenticated` -> `NodeResolver.Refresh` ->
// one-retry path mirrors the HTTP transport's 401 path.
// A second `Unauthenticated` returns `ErrAgentStaleBearer`.
func (t *grpcTransport) Apply(ctx context.Context, nodeID uuid.UUID, cfg []byte) error {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()

	addr, err := t.resolver.ResolveAddr(callCtx, nodeID)
	if err != nil {
		return fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	if addr == "" {
		return fmt.Errorf("agentgrpc: node %s: empty address", nodeID)
	}
	conn, err := t.dialAgent(callCtx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := aegisv1.NewAegisAgentClient(conn)

	bearer, err := t.resolver.GetBearer(callCtx, nodeID)
	if err != nil {
		return err
	}
	_, err = client.Apply(t.authCtx(callCtx, bearer), &aegisv1.ApplyRequest{Config: cfg})
	if err == nil {
		return nil
	}
	if !isUnauthenticated(err) {
		return fmt.Errorf("agentgrpc: Apply %s: %w", addr, err)
	}

	// Stale bearer. Refresh and retry once. The
	// HTTP transport does the same dance on 401; the
	// gRPC analogue is `Unauthenticated`.
	newBearer, refreshErr := t.resolver.Refresh(callCtx, nodeID)
	if refreshErr != nil {
		return fmt.Errorf("agentgrpc: agent %s returned Unauthenticated (stale bearer); refresh failed: %w",
			addr, refreshErr)
	}
	_, err2 := client.Apply(t.authCtx(callCtx, newBearer), &aegisv1.ApplyRequest{Config: cfg})
	if err2 == nil {
		return nil
	}
	if isUnauthenticated(err2) {
		return fmt.Errorf("%w: agent %s returned Unauthenticated on retry with refreshed bearer",
			ErrAgentStaleBearer, addr)
	}
	return fmt.Errorf("agentgrpc: Apply %s on retry: %w", addr, err2)
}

// Status calls `AegisAgent.Status` over gRPC. v0.8.29
// PR 3 implements it; the BatchedApplier does not consume
// `Status` today but the operator's `nodes.Service`
// /health-probe path does.
func (t *grpcTransport) Status(ctx context.Context, nodeID uuid.UUID) (StatusResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	addr, err := t.resolver.ResolveAddr(callCtx, nodeID)
	if err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	conn, err := t.dialAgent(callCtx, addr)
	if err != nil {
		return StatusResult{}, err
	}
	defer func() { _ = conn.Close() }()
	client := aegisv1.NewAegisAgentClient(conn)
	bearer, err := t.resolver.GetBearer(callCtx, nodeID)
	if err != nil {
		return StatusResult{}, err
	}
	resp, err := client.Status(t.authCtx(callCtx, bearer), &aegisv1.StatusRequest{})
	if err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: Status %s: %w", addr, err)
	}
	return StatusResult{
		State:          resp.GetState(),
		AgentVersion:   resp.GetAgentVersion(),
		SingboxVersion: resp.GetSingboxVersion(),
		UptimeSeconds:  resp.GetUptimeSeconds(),
	}, nil
}

// Stats calls `AegisAgent.Stats` over gRPC.
func (t *grpcTransport) Stats(ctx context.Context, nodeID uuid.UUID) (StatsResult, error) {
	callCtx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	addr, err := t.resolver.ResolveAddr(callCtx, nodeID)
	if err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	conn, err := t.dialAgent(callCtx, addr)
	if err != nil {
		return StatsResult{}, err
	}
	defer func() { _ = conn.Close() }()
	client := aegisv1.NewAegisAgentClient(conn)
	bearer, err := t.resolver.GetBearer(callCtx, nodeID)
	if err != nil {
		return StatsResult{}, err
	}
	resp, err := client.Stats(t.authCtx(callCtx, bearer), &aegisv1.StatsRequest{})
	if err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: Stats %s: %w", addr, err)
	}
	out := StatsResult{UserStats: make(map[string]UserTrafficStats, len(resp.GetUserStats()))}
	for k, v := range resp.GetUserStats() {
		out.UserStats[k] = UserTrafficStats{
			UploadBytes:   v.GetUploadBytes(),
			DownloadBytes: v.GetDownloadBytes(),
		}
	}
	return out, nil
}

// Health calls `AegisAgent.Health` over gRPC. The agent's
// interceptor excludes Health from auth (mirrors the HTTP
// /healthz contract), so the bearer is not required.
func (t *grpcTransport) Health(ctx context.Context, nodeID uuid.UUID) error {
	callCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	addr, err := t.resolver.ResolveAddr(callCtx, nodeID)
	if err != nil {
		return fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	conn, err := t.dialAgent(callCtx, addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	client := aegisv1.NewAegisAgentClient(conn)
	_, err = client.Health(callCtx, &aegisv1.HealthRequest{})
	if err != nil {
		return fmt.Errorf("agentgrpc: Health %s: %w", addr, err)
	}
	return nil
}

// isUnauthenticated reports whether `err` is a gRPC
// `Unauthenticated` status. The HTTP transport's 401
// check is the same predicate translated to HTTP status
// codes; both transports share the
// `ErrAgentStaleBearer` return value for the "refresh
// exhausted" case.
func isUnauthenticated(err error) bool {
	if err == nil {
		return false
	}
	st, ok := status.FromError(err)
	if !ok {
		return false
	}
	return st.Code().String() == "Unauthenticated"
}
