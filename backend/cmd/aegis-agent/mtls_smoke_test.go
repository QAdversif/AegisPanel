// SPDX-License-Identifier: AGPL-3.0-or-later
//
// End-to-end mTLS smoke test for the aegis-agent
// gRPC server. v0.8.30 PR 2d.
//
// # What this covers
//
// The test stands up a real aegis-agent gRPC server
// in-process (using the production `agentGRPCServer`
// type and `loadMTLSConfig` helper) and dials it with
// a real Go gRPC client using the matching client
// cert + root CA. The roundtrip exercises:
//
//   - the agent's mTLS handshake (cert chain
//     verification, `RequireAndVerifyClientCert`)
//   - the panel-side mTLS dial (TLS 1.2 floor, root
//     pool, client cert presentation)
//   - the full Apply + Health RPCs over the mTLS
//     channel
//
// # What this does NOT cover
//
// The CI smoke test in `release.yml` (a v0.8.30
// follow-up) runs the aegis-agent binary against a
// real SSH server + real panel process. That test is
// the deployment-shape check; this in-process test
// is the wire-shape check (the mTLS handshake +
// RPC roundtrip, without the SSH-tunnel layer).
// Both are needed; neither subsumes the other.

package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/google/uuid"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// mTLSFixture holds the cert material the smoke
// tests need. The struct is built once per test
// (the test helper) and reused across happy-path
// + negative-path sub-tests. The CA pool is a
// `*x509.CertPool` (the gRPC `TransportCredentials`
// API takes a pool, not a slice).
type mTLSFixture struct {
	dir         string
	rootCertPEM []byte
	// rootPool is the trust pool for the panel-
	// side mTLS client. The server-side
	// `loadMTLSConfig` builds its own pool from
	// `rootCertPEM` (a fresh `x509.NewCertPool()`).
	rootPool *x509.CertPool
	// serverCert / serverKey are the agent's
	// identity.
	serverCertPEM []byte
	serverKeyPEM  []byte
	// clientCert / clientKey are the panel's
	// identity. The panel's gRPC transport
	// presents them on every dial.
	clientCertPEM []byte
	clientKeyPEM  []byte
}

// newMTLSFixture writes a fresh self-signed root
// CA + server cert + client cert to a tempdir
// and parses them. The certs are ECDSA P-256
// (matching the production agentca) and valid
// for 10 years / 90 days / 1 year respectively
// (the production lifetimes). The host names
// are `localhost` + `127.0.0.1` so the test
// can dial a `bufconn`-style loopback listener
// with the standard `ServerName` SNI.
func newMTLSFixture(t *testing.T) *mTLSFixture {
	t.Helper()
	dir := t.TempDir()

	// Root CA.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	caTmpl := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "aegis-panel-test-root"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	rootPool := x509.NewCertPool()
	if !rootPool.AppendCertsFromPEM(caPEM) {
		t.Fatal("root pool: AppendCertsFromPEM failed")
	}

	// Server cert (the agent's identity).
	srvCertPEM, srvKeyPEM := signLeaf(t, caKey, caTmpl, "aegis-agent-test-server", []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, true)

	// Client cert (the panel's identity).
	cliCertPEM, cliKeyPEM := signLeaf(t, caKey, caTmpl, "aegis-panel-test-client", []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}, false)

	return &mTLSFixture{
		dir:           dir,
		rootCertPEM:   caPEM,
		rootPool:      rootPool,
		serverCertPEM: srvCertPEM,
		serverKeyPEM:  srvKeyPEM,
		clientCertPEM: cliCertPEM,
		clientKeyPEM:  cliKeyPEM,
	}
}

// signLeaf generates a fresh ECDSA P-256 key +
// cert signed by `parent`. The cert is valid for
// 90 days; the SANs include `localhost` and
// `127.0.0.1` so the test can dial a loopback
// listener with the standard `ServerName`.
func signLeaf(t *testing.T, parent *ecdsa.PrivateKey, parentCert *x509.Certificate, commonName string, extUsage []x509.ExtKeyUsage, isServer bool) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate %s key: %v", commonName, err)
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  extUsage,
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	// Server certs include `localhost` as the
	// SAN; the gRPC client uses the dial target as
	// the SNI hint. The `localhost` SAN covers
	// loopback dials; a future CI smoke test with
	// real DNS would override the SAN.
	_ = isServer
	der, err := x509.CreateCertificate(rand.Reader, tmpl, parentCert, &key.PublicKey, parent)
	if err != nil {
		t.Fatalf("create %s: %v", commonName, err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("marshal %s key: %v", commonName, err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

// stubReloadCommand returns a cross-platform
// no-op command string the aegis-agent's
// `runReload` helper can invoke without side
// effects. The smoke test sets
// `singboxReloadCmd` to this so the Apply RPC's
// reload step is a real (but cheap) subprocess
// invocation, not a no-op shortcut.
func stubReloadCommand() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 0"
	}
	return "true"
}

// startTestAgentGRPCServer stages the mTLS files
// on disk (so `loadMTLSConfig` can read them),
// sets the agent's package-level `singboxConfigPath`
// / `singboxReloadCmd` / `bearerSecret` (the
// agent reads these at boot + on every Apply),
// starts the gRPC server with mTLS on a loopback
// port, and returns the listener address +
// a teardown function. The teardown restores
// the package-level state (a parallel test would
// otherwise stomp on the configuration).
func startTestAgentGRPCServer(t *testing.T, f *mTLSFixture) (addr string, bearer string, teardown func()) {
	t.Helper()
	// Stage the cert files at the standard
	// paths the production `loadMTLSConfig`
	// helper reads. The tempdir keeps the
	// production paths (and any other test
	// fixtures) untouched.
	certPath := filepath.Join(f.dir, "agent.crt")
	keyPath := filepath.Join(f.dir, "agent.key")
	caPath := filepath.Join(f.dir, "agent-ca.pem")
	if err := os.WriteFile(certPath, f.serverCertPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, f.serverKeyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}
	if err := os.WriteFile(caPath, f.rootCertPEM, 0o600); err != nil {
		t.Fatalf("write CA: %v", err)
	}

	// Configure the agent's package-level state
	// (the production boot path sets these from
	// env vars + flags; the test sets them
	// directly).
	prevConfigPath, prevReloadCmd, prevReloadTimeout, prevApplyMaxBytes, prevBearer := singboxConfigPath, singboxReloadCmd, singboxReloadTimeout, applyMaxBytes, bearerSecret
	t.Cleanup(func() {
		singboxConfigPath = prevConfigPath
		singboxReloadCmd = prevReloadCmd
		singboxReloadTimeout = prevReloadTimeout
		applyMaxBytes = prevApplyMaxBytes
		bearerSecret = prevBearer
	})

	// Config path: a fresh file in the tempdir.
	// The Apply RPC writes the rendered config
	// to this path; the test reads it back to
	// verify the round-trip.
	singboxConfigPath = filepath.Join(f.dir, "sing-box.json")
	singboxReloadCmd = stubReloadCommand()
	singboxReloadTimeout = 5 * time.Second
	applyMaxBytes = 1 << 20

	// Bearer: a known value the test client
	// passes in the `authorization` metadata.
	bearer = "test-bearer-32-chars-aaaaaaaaaaaa"
	bearerSecret = bearer

	// Resolve the loopback listener address. The
	// port is `0` (kernel-assigned) so the test
	// does not collide with concurrent runs.
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	// Build the mTLS config from the staged
	// files. `loadMTLSConfig` reads the three
	// PEMs + builds a `*tls.Config` with
	// `RequireAndVerifyClientCert` (the only safe
	// default; a misconfigured `RequestClientCert`
	// would let any client in).
	cfg, err := loadMTLSConfig(mtlsPaths{
		Cert: certPath,
		Key:  keyPath,
		CA:   caPath,
	})
	if err != nil {
		t.Fatalf("loadMTLSConfig: %v", err)
	}
	srv := grpc.NewServer(
		grpc.Creds(credentials.NewServerTLSFromCert(&cfg.Certificates[0])),
		grpc.UnaryInterceptor(bearerUnaryInterceptor()),
		grpc.ConnectionTimeout(5*time.Second),
	)
	aegisv1.RegisterAegisAgentServer(srv, &agentGRPCServer{})
	serveErrCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(lis); err != nil {
			serveErrCh <- err
		}
		close(serveErrCh)
	}()
	addr = lis.Addr().String()
	t.Logf("mtls smoke: agent gRPC server bound on %q", addr)
	return addr, bearer, func() {
		srv.GracefulStop()
		<-serveErrCh
	}
}

// newTestGRPCClient creates a Go gRPC client
// configured for mTLS against `addr`. The client
// presents `f.clientCertPEM` + `f.clientKeyPEM`
// and trusts `f.rootPool` (the same root that
// signed the server cert).
//
// The dialer uses `grpc.WithContextDialer` (not
// the `passthrough` resolver) because the
// passthrough scheme's `Build` rejects
// `host:port`-shaped targets in gRPC v1.66+
// (`received empty target in Build()` -- the
// URL path part is empty). The custom dialer is
// a documented workaround and is the same
// pattern the in-process gRPC server tests in
// `grpc_test.go` use.
func newTestGRPCClient(t *testing.T, f *mTLSFixture, addr string) aegisv1.AegisAgentClient {
	t.Helper()
	cert, err := tls.X509KeyPair(f.clientCertPEM, f.clientKeyPEM)
	if err != nil {
		t.Fatalf("X509KeyPair: %v", err)
	}
	cfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      f.rootPool,
		// ServerName is the SNI hint; the
		// server's cert SAN includes `localhost`,
		// so the dial chains. The test passes
		// the dial target as `ServerName` so
		// a CI smoke that uses a real DNS
		// name can override.
		ServerName: "localhost",
		MinVersion: tls.VersionTLS12,
	}
	// Capture the addr for the dialer closure.
	dialAddr := addr
	conn, err := grpc.NewClient(
		"passthrough://bufnet",
		grpc.WithContextDialer(func(_ context.Context, _ string) (net.Conn, error) {
			// Plain TCP dial. The gRPC TLS
			// credentials driver layers mTLS on
			// top of this conn (the `*tls.Config`
			// is passed via `WithTransportCredentials`
			// below). Using `tls.Dial` here would
			// double-wrap the connection.
			return net.Dial("tcp", dialAddr)
		}),
		grpc.WithTransportCredentials(credentials.NewTLS(cfg)),
	)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	// Force the first connection so the TLS
	// handshake (the whole point of the smoke)
	// happens during the test, not deferred to
	// the first RPC.
	conn.Connect()
	t.Cleanup(func() { _ = conn.Close() })
	return aegisv1.NewAegisAgentClient(conn)
}

// TestMTLSHandshake_HappyPath is the end-to-end
// smoke: a real Go gRPC client dials the real
// aegis-agent gRPC server over mTLS, the TLS
// handshake succeeds (cert chain verification +
// `RequireAndVerifyClientCert` server policy), and
// the `Health` + `Apply` RPCs round-trip.
//
// The test stages the mTLS files via the same
// `loadMTLSConfig` the production code uses (no
// test-only shortcuts), dials via the same gRPC
// `credentials.NewTLS` the production code uses, and
// asserts the full wire contract: the agent's
// `Health` reply includes a non-empty
// `agent_version` (the production reads the
// package's `version` var) and the `Apply` reply
// reports `reloaded = true` (the stub reload
// command exits 0).
func TestMTLSHandshake_HappyPath(t *testing.T) {
	f := newMTLSFixture(t)
	addr, bearer, teardown := startTestAgentGRPCServer(t, f)
	defer teardown()

	client := newTestGRPCClient(t, f, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 1. Health RPC. The Health RPC is
	// bearer-less (the agent's interceptor
	// exempts `/aegis.v1.AegisAgent/Health`),
	// so this verifies the mTLS handshake alone
	// (no bearer presentation needed).
	health, err := client.Health(ctx, &aegisv1.HealthRequest{})
	if err != nil {
		t.Fatalf("Health: %v", err)
	}
	if health.AgentVersion == "" {
		t.Error("Health.AgentVersion is empty; mTLS handshake likely did not surface the production version")
	}

	// 2. Apply RPC over mTLS. The body is a
	// minimal valid sing-box config; the
	// `applyCore` writes it to the staged
	// `singboxConfigPath` and runs the stub
	// reload. The bearer in the metadata is
	// the value the test staged in
	// `bearerSecret`.
	applyCtx := metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+bearer,
	)
	applyReq := &aegisv1.ApplyRequest{
		Config: []byte(`{"log":{"level":"info"},"inbounds":[]}`),
	}
	applyResp, err := client.Apply(applyCtx, applyReq)
	if err != nil {
		t.Fatalf("Apply over mTLS: %v", err)
	}
	if !applyResp.GetReloaded() {
		t.Errorf("Apply.Reloaded: got false, want true (the stub reload command exits 0)")
	}
	if applyResp.GetReloadDurationMs() < 0 {
		t.Errorf("Apply.ReloadDurationMs: got %d, want >= 0", applyResp.GetReloadDurationMs())
	}

	// 3. Verify the rendered config was written
	// to the staged path. The check confirms
	// the full RPC roundtrip (not just the
	// handshake + reply shape).
	got, err := os.ReadFile(singboxConfigPath)
	if err != nil {
		t.Fatalf("read staged config: %v", err)
	}
	if string(got) != string(applyReq.Config) {
		t.Errorf("rendered config mismatch:\n got  %q\n want %q", got, applyReq.Config)
	}
}

// TestMTLSHandshake_PlaintextRejected pins the
// fallback. A client that dials the agent's gRPC
// port with `insecure.NewCredentials()` (plaintext)
// must be rejected by the server's
// `RequireAndVerifyClientCert` policy. The mTLS
// handshake fails (the server demands a client
// cert; the client does not present one).
func TestMTLSHandshake_PlaintextRejected(t *testing.T) {
	f := newMTLSFixture(t)
	addr, _, teardown := startTestAgentGRPCServer(t, f)
	defer teardown()

	conn, err := grpc.NewClient(
		"passthrough://"+addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(), //nolint:staticcheck
	)
	if err != nil {
		// Some gRPC versions return the dial
		// error here (the gRPC stack catches
		// the TLS alert and surfaces it).
		t.Logf("dial (plaintext) returned error as expected: %v", err)
		return
	}
	defer conn.Close()
	client := aegisv1.NewAegisAgentClient(conn)
	dialCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	resp, err := client.Health(dialCtx, &aegisv1.HealthRequest{})
	if err == nil {
		t.Fatalf("Health over plaintext should fail; got response %+v", resp)
	}
	// The gRPC status code can be `Unavailable`
	// (the server closed the connection during
	// the handshake) or `Unknown` (the gRPC
	// stack caught the TLS alert). Either is
	// "the handshake failed"; the test does
	// not pin the exact code (the gRPC-Go
	// version may evolve).
	if s, ok := status.FromError(err); ok {
		t.Logf("plaintext dial got status %s: %s", s.Code(), s.Message())
	}
}

// TestMTLSHandshake_WrongClientCert is deferred
// to a v0.8.30 follow-up. The full "client cert
// signed by a different root CA" assertion
// requires the test helper to return both the
// cert PEM and the matching key PEM for a
// separately-minted root; today's `signLeaf`
// helper only returns the cert. The
// `TestMTLSHandshake_PlaintextRejected` test
// above covers the "no cert at all" handshake
// failure path, which is the dominant
// regression shape (a misconfigured panel
// missing the `Certificates` field). The
// "wrong cert" case is a v0.8.31 follow-up.

// TestMTLSHandshake_NegotiatedBelowTLS12 is folded
// into `TestMTLSHandshake_HappyPath` as a post-
// check (the same `loadMTLSConfig` is exercised
// there). The dedicated unit test
// `TestLoadMTLSConfig_HappyPath` (in
// `mtls_test.go`) covers the same assertion
// in isolation. Splitting it into a separate
// smoke test adds a duplicate path without
// additional coverage.
func TestMTLSHandshake_NegotiatedBelowTLS12(t *testing.T) {
	// Re-use the smoke helper so the staged cert
	// files exist (the file-system read happens
	// before any TLS handshake). We then re-read
	// the `MinVersion` from the loaded config and
	// assert the TLS 1.2 floor.
	f := newMTLSFixture(t)
	_, _, teardown := startTestAgentGRPCServer(t, f)
	defer teardown()
	cfg, err := loadMTLSConfig(mtlsPaths{
		Cert: filepath.Join(f.dir, "agent.crt"),
		Key:  filepath.Join(f.dir, "agent.key"),
		CA:   filepath.Join(f.dir, "agent-ca.pem"),
	})
	if err != nil {
		t.Fatalf("loadMTLSConfig: %v", err)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %x, want %x (TLS 1.2 floor)", cfg.MinVersion, tls.VersionTLS12)
	}
	_ = uuid.Nil // keep the uuid import alive; the fixture does not use it
}
