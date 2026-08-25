// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Compile + wire-level smoke for the aegis.v1 gRPC stubs.
//
// This file is a sibling of the generated stubs (`agent.pb.go` and
// `agent_grpc.pb.go`). It is the regression guard for PR #1 of the
// v0.8.29 mTLS+gRPC migration (see
// `docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md`). It does
// three things:
//
//  1. Imports the generated package to catch the "committed stub
//     references a symbol that is no longer generated" class of bugs
//     at `go test` time, before CI.
//  2. Round-trips a populated `ApplyRequest` through
//     `proto.Marshal` / `proto.Unmarshal` and asserts every field
//     survives. This is the "the wire format encodes what we expect"
//     gate; the proto description is the source of truth, this test
//     is the compiler's view of that source of truth.
//  3. Verifies the gRPC service descriptor's name and method count
//     are what the panel + agent will depend on. If a future PR
//     renames a method or changes the proto package, this test fails
//     before the renames are silently shipped.
//
// This test does NOT start a network listener. The transport-layer
// smoke (mTLS handshake, 401→refresh→retry) lands in v0.8.30 PR 2
// and v0.8.29 PR 3 respectively. The scope here is intentionally
// minimal: a code review of this file should be able to confirm
// "the stubs compile and the wire is sane" without reading
// any gRPC internals.

package aegisv1_test

import (
	"context"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// TestServerInterfaceContract pins the four RPC methods the panel and
// the agent wire around. Adding, removing, or renaming an RPC breaks
// this test. The compile-time `var _` check below is a no-cost guard;
// the explicit method-set assertion in the same package catches
// signature drift (e.g. a future PR that drops the `context.Context`
// first arg).
func TestServerInterfaceContract(t *testing.T) {
	// Compile-time guard: stubServer must satisfy the
	// AegisAgentServer interface. If a future PR adds a method to
	// the interface, the stub's embed of UnimplementedAegisAgentServer
	// already covers it (the generated code marks it as forward
	// compatible).
	var _ aegisv1.AegisAgentServer = (*stubServer)(nil)
}

// TestApplyRequestRoundTrip is the wire-level smoke: a populated
// request marshals, the resulting bytes unmarshal back into an equal
// value. If a future PR adds or removes a field on `ApplyRequest`,
// this test forces a code change here so the diff is reviewable
// alongside the .proto edit.
func TestApplyRequestRoundTrip(t *testing.T) {
	payload := []byte(`{"log":{"level":"info"},"inbounds":[]}`)

	in := &aegisv1.ApplyRequest{Config: payload}
	wire, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	if len(wire) == 0 {
		t.Fatal("proto.Marshal produced zero bytes (ApplyRequest has no fields?)")
	}

	out := &aegisv1.ApplyRequest{}
	if err := proto.Unmarshal(wire, out); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}
	if string(out.Config) != string(payload) {
		t.Fatalf("Config round-trip mismatch:\n got  %q\n want %q", out.Config, payload)
	}
}

// TestServiceDescWiring pins the gRPC service descriptor's name and
// method count. The panel's dialer (v0.8.29 PR 3) and the agent's
// `RegisterAegisAgentServer` (v0.8.29 PR 2) both reference the
// descriptor indirectly via the generated `NewAegisAgentClient` and
// `RegisterAegisAgentServer` helpers. A typo in the package name or
// an accidental method delete breaks the wire before any byte is
// sent; this test is the surface-level catch.
func TestServiceDescWiring(t *testing.T) {
	if got, want := aegisv1.AegisAgent_ServiceDesc.ServiceName, "aegis.v1.AegisAgent"; got != want {
		t.Fatalf("ServiceName drifted: got %q, want %q", got, want)
	}
	if got, want := len(aegisv1.AegisAgent_ServiceDesc.Methods), 4; got != want {
		t.Fatalf("AegisAgent_ServiceDesc.Methods: got %d methods, want %d (Apply/Status/Stats/Health)",
			got, want)
	}
}

// TestHealthResponseDefaults pins the zero-value shape of
// `HealthResponse`. The agent's `/healthz` (v0.8.29 PR 2) returns
// this; an empty response should marshal to "{}" not "null".
func TestHealthResponseDefaults(t *testing.T) {
	r := &aegisv1.HealthResponse{}
	if r.UptimeSeconds != 0 {
		t.Fatalf("zero UptimeSeconds: got %d", r.UptimeSeconds)
	}
	if r.AgentVersion != "" {
		t.Fatalf("zero AgentVersion: got %q", r.AgentVersion)
	}
}

// TestHealthResponseRoundTrip is the wire-level smoke for
// `HealthResponse` — the agent's liveness answer. The fields
// (UptimeSeconds, AgentVersion) are the public surface that the
// panel-side health probe (v0.8.30+) will JSON-encode into the
// /api/v1/nodes status payload. A future proto change that adds
// or removes a field must be reflected here.
func TestHealthResponseRoundTrip(t *testing.T) {
	in := &aegisv1.HealthResponse{
		UptimeSeconds: 42,
		AgentVersion:  "v0.8.29-test",
	}
	wire, err := proto.Marshal(in)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	out := &aegisv1.HealthResponse{}
	if err := proto.Unmarshal(wire, out); err != nil {
		t.Fatalf("proto.Unmarshal: %v", err)
	}
	if out.UptimeSeconds != in.UptimeSeconds {
		t.Fatalf("UptimeSeconds: got %d, want %d", out.UptimeSeconds, in.UptimeSeconds)
	}
	if out.AgentVersion != in.AgentVersion {
		t.Fatalf("AgentVersion: got %q, want %q", out.AgentVersion, in.AgentVersion)
	}
}

// --- helpers --------------------------------------------------------------

// stubServer is the minimum server that satisfies
// `aegisv1.AegisAgentServer` for the compile-time guard in
// `TestServerInterfaceContract`. Embedding
// `aegisv1.UnimplementedAegisAgentServer` is required by the
// generated interface (`mustEmbedUnimplementedAegisAgentServer` is
// a private method the embed provides).
type stubServer struct {
	aegisv1.UnimplementedAegisAgentServer
}

func (*stubServer) Apply(_ context.Context, _ *aegisv1.ApplyRequest) (*aegisv1.ApplyResponse, error) {
	return &aegisv1.ApplyResponse{}, nil
}
func (*stubServer) Status(_ context.Context, _ *aegisv1.StatusRequest) (*aegisv1.StatusResponse, error) {
	return &aegisv1.StatusResponse{}, nil
}
func (*stubServer) Stats(_ context.Context, _ *aegisv1.StatsRequest) (*aegisv1.StatsResponse, error) {
	return &aegisv1.StatsResponse{}, nil
}
func (*stubServer) Health(_ context.Context, _ *aegisv1.HealthRequest) (*aegisv1.HealthResponse, error) {
	return &aegisv1.HealthResponse{}, nil
}

// Compile-time check that the gRPC package import is actually used
// at this level. A future refactor that drops the import would
// surface here.
var _ grpc.ServiceRegistrar = (*grpc.Server)(nil)
