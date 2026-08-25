// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the aegis-agent gRPC server. The HTTP test surface
// (main_test.go + apply_test.go) is unchanged; the gRPC tests
// mirror the HTTP happy/sad paths so a future regression in the
// shared `applyCore` (apply_core.go) surfaces in BOTH test
// suites.
//
// The tests start an in-process gRPC server on a random
// `bufconn`-backed listener (no real socket) so the suite is
// hermetic and does not collide with the `httptest.Server` the
// HTTP tests use. The bearer secret is set per-test; an empty
// bearer exercises the "insecure mode" path that the bearer
// interceptor enforces.

package main

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

const testBearer = "test-bearer-secret-aaaaaaaaaaaaaaaaaaaaaa" // test fixture, not a real secret

// startTestGRPCServer starts the agent's gRPC server on a
// bufconn-backed listener. Returns a connected client and a
// teardown function. The agent's package-level state (sing-box
// config path, reload command, etc.) is set per-test by the
// caller via `withApplyConfig`.
func startTestGRPCServer(t *testing.T) (aegisv1.AegisAgentClient, func()) {
	t.Helper()

	// 1 MiB cap matches the package default; tests that
	// want to exercise the cap set their own
	// `applyMaxBytes` via `withApplyConfig`.
	applyMaxBytes = 1 << 20

	lis := bufconn.Listen(1024 * 1024)
	srv := grpc.NewServer(
		grpc.Creds(insecure.NewCredentials()),
		grpc.UnaryInterceptor(bearerUnaryInterceptor()),
	)
	aegisv1.RegisterAegisAgentServer(srv, &agentGRPCServer{})

	serveErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(lis); err != nil {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()

	dialCtx, dialCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer dialCancel()
	// grpc.NewClient is lazy; `Connect()` kicks the
	// state machine out of IDLE. The `passthrough`
	// resolver is the only one that does not look up a
	// real address, so the `WithContextDialer` (which
	// returns a bufconn net.Conn) is what actually
	// opens the connection.
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			return lis.Dial()
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		t.Fatalf("bufconn client: %v", err)
	}
	conn.Connect()
	for {
		state := conn.GetState()
		if state == connectivity.Ready {
			break
		}
		if !conn.WaitForStateChange(dialCtx, state) {
			srv.Stop()
			_ = conn.Close()
			t.Fatalf("bufconn client did not reach READY in time (state=%s)", state)
		}
	}

	teardown := func() {
		_ = conn.Close()
		srv.GracefulStop()
		// Drain the serve goroutine so the test does
		// not race with the bufconn listener close.
		<-serveErrCh
	}
	return aegisv1.NewAegisAgentClient(conn), teardown
}

// authCtxWith returns a context with the test bearer attached as
// `authorization: Bearer <token>` metadata. The gRPC interceptor
// reads the same key the HTTP `requireBearer` middleware reads
// (`Authorization`); the gRPC metadata helper canonicalises to
// lowercase, so the canonical form is fine. The base `ctx` lets
// the test set a per-call timeout before the auth metadata is
// appended.
func authCtxWith(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+testBearer)
}

// writeStubReloadScript writes a cross-platform shell script
// that touches the sentinel file and exits 0. The reload
// command itself (passed to `withApplyConfig`) is the absolute
// path of the script. `runReload` splits on whitespace and
// runs the first field as the executable, so the script
// approach avoids the `>` redirect being interpreted as a
// literal arg.
func writeStubReloadScript(t *testing.T, sentinel string) string {
	t.Helper()
	dir := t.TempDir()
	var script, content string
	if runtime.GOOS == "windows" {
		script = filepath.Join(dir, "reload.cmd")
		// .cmd files can be invoked directly via
		// `cmd /c` only; the agent's `runReload`
		// uses `exec.Command` which finds the
		// interpreter via PATHEXT.
		content = "@echo off\r\ntype nul > \"" + sentinel + "\"\r\nexit /b 0\r\n"
	} else {
		script = filepath.Join(dir, "reload.sh")
		content = "#!/bin/sh\ntouch '" + sentinel + "'\nexit 0\n"
	}
	if err := os.WriteFile(script, []byte(content), 0o755); err != nil {
		t.Fatalf("write reload script: %v", err)
	}
	return script
}

// TestGRPC_HealthAlwaysOpen mirrors the v0.4.0-b HTTP /healthz
// contract: the RPC is reachable without a bearer (insecure mode
// for the docker-compose smoke). The test does NOT set
// `bearerSecret`; the bearerUnaryInterceptor must let Health
// through anyway because `info.FullMethod` matches the
// "/aegis.v1.AegisAgent/Health" exception.
func TestGRPC_HealthAlwaysOpen(t *testing.T) {
	prev := bearerSecret
	bearerSecret = ""
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Health(ctx, &aegisv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health (insecure mode): %v", err)
	}
	if resp.AgentVersion == "" {
		t.Errorf("Health.AgentVersion is empty (expected %q)", version)
	}
	if resp.UptimeSeconds < 0 {
		t.Errorf("Health.UptimeSeconds negative: %d", resp.UptimeSeconds)
	}
}

// TestGRPC_HealthAuthNotRequired is the explicit "auth does not
// apply to Health" gate. The Health RPC must work whether the
// bearer is set or not.
func TestGRPC_HealthAuthNotRequired(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	// No outgoing auth metadata; the Health RPC must
	// still answer.
	resp, err := client.Health(ctx, &aegisv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health without auth: %v", err)
	}
	if resp == nil {
		t.Fatal("Health returned nil response")
	}
}

// TestGRPC_ApplyRequiresAuth is the "bearer is enforced" gate.
// When the bearer is set and the call has no auth metadata, the
// interceptor must reject with `Unauthenticated`.
func TestGRPC_ApplyRequiresAuth(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Apply(ctx, &aegisv1.ApplyRequest{Config: []byte(`{}`)})
	if err == nil {
		t.Fatal("Apply without auth should fail; got nil error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Apply returned non-status error: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("Apply auth: got code %s, want Unauthenticated", st.Code())
	}
}

// TestGRPC_ApplyRejectsBadBearer is the "wrong bearer is
// rejected" gate. The interceptor uses constant-time compare
// (mirroring the HTTP `subtleCmp`), so a wrong token is
// rejected with the same code as a missing token.
func TestGRPC_ApplyRejectsBadBearer(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ctx = metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer wrong-token-aaaaaaaaaaaaaaaaaaa")
	_, err := client.Apply(ctx, &aegisv1.ApplyRequest{Config: []byte(`{}`)})
	if err == nil {
		t.Fatal("Apply with wrong bearer should fail; got nil error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Apply returned non-status error: %v", err)
	}
	if st.Code() != codes.Unauthenticated {
		t.Errorf("Apply wrong bearer: got code %s, want Unauthenticated", st.Code())
	}
}

// TestGRPC_ApplyWritesAndReloads is the gRPC happy-path mirror
// of `TestApply_RealWritesConfigAndReloads`. It exercises the
// shared `applyCore` (apply_core.go) via the gRPC handler. The
// reload command is a stub script that touches a sentinel file;
// the test asserts both the on-disk config content and the
// sentinel are present.
func TestGRPC_ApplyWritesAndReloads(t *testing.T) {
	prevBearer := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prevBearer })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	sentinel := filepath.Join(dir, "reload.flag")
	reloadScript := writeStubReloadScript(t, sentinel)
	withApplyConfig(t, configPath, reloadScript, 5*time.Second)

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	payload := []byte(`{"log":{"level":"info"},"inbounds":[]}`)
	resp, err := client.Apply(authCtxWith(ctx), &aegisv1.ApplyRequest{Config: payload})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if !resp.GetReloaded() {
		t.Errorf("Apply.Reloaded: got false, want true")
	}
	if resp.GetReloadDurationMs() < 0 {
		t.Errorf("Apply.ReloadDurationMs negative: %d", resp.GetReloadDurationMs())
	}

	// Assert the on-disk config matches the payload.
	got, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("on-disk config mismatch:\n got  %q\n want %q", got, payload)
	}
	// Assert the reload command was actually invoked.
	if _, err := os.Stat(sentinel); err != nil {
		t.Errorf("reload sentinel not created: %v", err)
	}
}

// TestGRPC_ApplyRejectsEmptyConfig pins the
// `errApplyEmptyConfig` -> `InvalidArgument` mapping in
// `applyCoreErrorToGRPC`. The panel-side BatchedApplier
// (v0.8.29 PR 3) relies on `InvalidArgument` to distinguish
// "client sent garbage" from "server is down" вЂ” a regression
// here would either loop forever (if it became `Internal`) or
// crash the panel (if it became `Unknown`).
func TestGRPC_ApplyRejectsEmptyConfig(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	withApplyConfig(t, configPath, stubReloadOK(), 1*time.Second)

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Apply(authCtxWith(ctx), &aegisv1.ApplyRequest{Config: nil})
	if err == nil {
		t.Fatal("Apply with empty config should fail; got nil error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Apply returned non-status error: %v", err)
	}
	if got, want := st.Code(), codes.InvalidArgument; got != want {
		t.Errorf("Apply empty config: got code %s, want %s", got, want)
	}
	if !strings.Contains(st.Message(), "empty config") {
		t.Errorf("Apply empty config: got message %q, want substring %q", st.Message(), "empty config")
	}
}

// TestGRPC_ApplyRejectsNonObjectConfig pins the
// `errApplyNotJSONObject` -> `InvalidArgument` mapping. The
// agent refuses to write a non-object config because sing-box's
// own parser expects a top-level object; a regression here
// would let a buggy panel overwrite sing-box's config with
// garbage.
func TestGRPC_ApplyRejectsNonObjectConfig(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	withApplyConfig(t, configPath, stubReloadOK(), 1*time.Second)

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := client.Apply(authCtxWith(ctx), &aegisv1.ApplyRequest{Config: []byte(`"a string, not an object"`)})
	if err == nil {
		t.Fatal("Apply with non-object config should fail; got nil error")
	}
	st, ok := status.FromError(err)
	if !ok {
		t.Fatalf("Apply returned non-status error: %v", err)
	}
	if got, want := st.Code(), codes.InvalidArgument; got != want {
		t.Errorf("Apply non-object: got code %s, want %s", got, want)
	}
	if !strings.Contains(st.Message(), "JSON object") {
		t.Errorf("Apply non-object: got message %q, want substring %q", st.Message(), "JSON object")
	}
}

// TestGRPC_StatusReturnsExpectedShape pins the Status response
// shape. v0.8.29 returns `state=online`, `agent_version` non-
// empty, `uptime_seconds >= 0`. A regression here breaks the
// panel-side `nodes.Service.Health` consumer.
func TestGRPC_StatusReturnsExpectedShape(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Status(authCtxWith(ctx), &aegisv1.StatusRequest{})
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if resp.GetState() != "online" {
		t.Errorf("Status.State: got %q, want %q", resp.GetState(), "online")
	}
	if resp.GetAgentVersion() == "" {
		t.Error("Status.AgentVersion is empty")
	}
	if resp.GetUptimeSeconds() < 0 {
		t.Errorf("Status.UptimeSeconds negative: %d", resp.GetUptimeSeconds())
	}
}

// TestGRPC_StatsEmptyShape pins the Stats response shape. The
// v0.4.0-b HTTP /v1/stats surface returns the empty shape; the
// gRPC Stats RPC must do the same. v0.4.0-c will populate it
// from the sing-box clash-api; the test does not need to change.
//
// The gRPC wire format for a map field with zero entries
// serialises the same as a nil map; both are valid "no stats to
// report" responses. The test accepts both rather than pin a
// specific marshalling detail that the codegen is free to
// change.
func TestGRPC_StatsEmptyShape(t *testing.T) {
	prev := bearerSecret
	bearerSecret = testBearer
	t.Cleanup(func() { bearerSecret = prev })

	client, teardown := startTestGRPCServer(t)
	defer teardown()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	resp, err := client.Stats(authCtxWith(ctx), &aegisv1.StatsRequest{})
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if got := len(resp.GetUserStats()); got != 0 {
		t.Errorf("Stats.UserStats: got %d entries, want 0", got)
	}
}
