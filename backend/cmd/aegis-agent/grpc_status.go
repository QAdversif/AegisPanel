// SPDX-License-Identifier: AGPL-3.0-or-later
//
// gRPC handlers for the Status / Stats / Health RPCs. Apply lives
// in `grpc_apply.go` because its side-effect surface (the
// `applyCore` shared with the HTTP path) is the load-bearing part
// of the migration; the three handlers here are read-only and
// trivial — they exist so the panel-side transport switch in
// v0.8.29 PR 3 has a 1:1 mapping of HTTP routes to gRPC methods.

package main

import (
	"context"
	"sync"
	"time"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// agentGRPCServer implements `aegisv1.AegisAgentServer`. The four
// methods are intentionally side-effect-free except `Apply` (which
// delegates to the shared `applyCore`); the read-only methods
// (`Status`, `Stats`, `Health`) read package-level state and are
// safe to call concurrently. The `startedAt` is a process-global
// defined in main.go (RFC3339Nano string); both the HTTP
// `/healthz` handler and the gRPC `Health` handler read it via
// `uptimeSeconds` below.
type agentGRPCServer struct {
	aegisv1.UnimplementedAegisAgentServer
}

// agentStateMu serialises reads of `lastApplyISO` (a string,
// written by the apply path, read by Status). The HTTP
// /v1/status handler is the only other reader today; the gRPC
// Status handler is the second. v0.8.30 widens the lock's scope
// to cover the sing-box-version probe + any new shared state.
var agentStateMu sync.RWMutex

// Status returns the node's running state. v0.8.29 returns the
// same shape the HTTP /v1/status surface returns today:
// `state` is the local view (today: "online" because the
// service is up; the panel is the source of truth for the
// node state machine and writes the value back via the regular
// node-update path), `agent_version` and `singbox_version` are
// best-effort, and `uptime_seconds` is the wall-clock delta.
//
// `last_apply_iso` is not yet a field on the proto (lands in
// v0.8.30). Today the gRPC reader of `Status` falls back to the
// HTTP /v1/status endpoint for that one field. The handler takes
// the read-lock on `agentStateMu` to keep the variable's
// documentation close to the only place that uses it.
func (s *agentGRPCServer) Status(_ context.Context, _ *aegisv1.StatusRequest) (*aegisv1.StatusResponse, error) {
	agentStateMu.RLock()
	// `lastApplyISO` is read so the lock covers the
	// shared state. The value is not exposed in the
	// proto today (lands in v0.8.30); the HTTP
	// /v1/status handler is the consumer.
	_ = lastApplyISO
	agentStateMu.RUnlock()
	return &aegisv1.StatusResponse{
		State:          "online",
		AgentVersion:   version,
		SingboxVersion: singboxVersion(),
		UptimeSeconds:  uptimeSeconds(),
	}, nil
}

// Stats returns per-user traffic counters. v0.4.0-c wires this
// to the sing-box clash-api listener; until then, the map is
// empty (matches the v0.4.0-b HTTP /v1/stats surface which also
// returns the empty shape). The gRPC shape uses a `map<string,
// UserTrafficStats>` because the panel's v0.8.29 transport
// switch can pipe the response directly into the next apply
// round without re-decoding a JSON array.
func (s *agentGRPCServer) Stats(_ context.Context, _ *aegisv1.StatsRequest) (*aegisv1.StatsResponse, error) {
	return &aegisv1.StatsResponse{
		UserStats: map[string]*aegisv1.UserTrafficStats{},
	}, nil
}

// Health is the liveness probe. Mirrors the v0.4.0-b HTTP
// /healthz contract: always reachable, even when the bearer is
// unset (the docker-compose smoke relies on this). The
// `bearerUnaryInterceptor` excludes the Health RPC from auth so
// the smoke works without exporting `AEGIS_AGENT_BEARER`.
func (s *agentGRPCServer) Health(_ context.Context, _ *aegisv1.HealthRequest) (*aegisv1.HealthResponse, error) {
	return &aegisv1.HealthResponse{
		UptimeSeconds: uptimeSeconds(),
		AgentVersion:  version,
	}, nil
}

// singboxVersion returns the on-disk sing-box version string,
// or "" if sing-box is not on PATH. v0.8.29 returns "" (the
// v0.4.0-b HTTP path also returns ""; the version probe lands
// in v0.8.30 alongside the mTLS cert bootstrap). The function
// exists today so the Status handler is structurally complete and
// v0.8.30 can land the probe as a one-line change here.
func singboxVersion() string {
	return ""
}

// uptimeSeconds derives the agent's process uptime from the
// `startedAt` package var (defined in main.go as an RFC3339Nano
// string for the HTTP /healthz surface). The gRPC handlers
// re-parse it back to a `time.Time`. A parse failure (only
// possible if a future PR changes the format literal in main.go
// without updating this file) returns 0; the panel-side reader
// falls back to the HTTP /healthz in that case. v0.8.30 moves
// `startedAt` to a `time.Time` and drops the parse step.
func uptimeSeconds() int64 {
	t, err := time.Parse(time.RFC3339Nano, startedAt)
	if err != nil {
		return 0
	}
	return int64(time.Since(t).Seconds())
}
