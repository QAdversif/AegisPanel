// SPDX-License-Identifier: AGPL-3.0-or-later
//
// gRPC server for the aegis-agent. v0.8.29 introduces this alongside
// the v0.4.0-b HTTP+bearer surface; v0.8.32 cuts the HTTP transport
// entirely. The two transports share the apply core (apply_core.go)
// and the bearer secret (AEGIS_AGENT_BEARER), so an operator who
// runs the v0.8.29 agent can reach any of the four RPCs over gRPC
// while the BatchedApplier keeps using the HTTP path until it
// migrates to gRPC (v0.8.31).
//
// # Why gRPC on a separate port (not HTTP/2 + JSON)
//
// The agent's HTTP surface is `http.DefaultServeMux` semantics (plain
// HTTP/1.1 over `:8080`). gRPC requires HTTP/2; while the standard
// library's `http.Server` supports HTTP/2 with `TLSConfig` set, the
// v0.4.0-b agent listens in plaintext (the panel SSH-tunnels the
// connection, so TLS is overkill at this layer). A separate gRPC
// server on `:7001` keeps the two transports mechanically independent
// — the v0.8.30 mTLS handshake lands on `:7001` first, then the
// v0.8.32 cut is a port-removal instead of a protocol-swap.
//
// # Auth (v0.8.29)
//
// The same `AEGIS_AGENT_BEARER` secret the HTTP path uses, surfaced
// via gRPC metadata `authorization: Bearer <token>`. v0.8.30 replaces
// the bearer interceptor with a TLS-credentials check; v0.8.32 deletes
// this file.

package main

import (
	"context"
	"crypto/subtle"
	"fmt"
	"log"
	"net"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// defaultGRPCListenAddr is the gRPC bind address. Distinct from
// `defaultListenAddr` (the HTTP `:8080`) so the two transports can
// be enabled independently — operators can drop the gRPC listener
// by exporting `AEGIS_AGENT_LISTEN_GRPC=""` without affecting the
// BatchedApplier's HTTP path. v0.8.30 mTLS lands on this port
// first.
const defaultGRPCListenAddr = ":7001"

// bearerUnaryInterceptor validates the `authorization: Bearer <secret>`
// metadata on every unary RPC. The check mirrors the HTTP
// `requireBearer` middleware: when `AEGIS_AGENT_BEARER` is empty,
// only the unauthenticated / healthz equivalent is reachable. Today
// every RPC is auth-gated except the `Health` RPC, which the panel
// uses as a liveness probe (mirrors the v0.4.0-b HTTP /healthz
// contract). Health is excluded here, NOT at registration time, so
// a future operator that wants to lock down Health too only edits
// the `healthzAlwaysOpen` switch.
//
// The constant-time compare prevents timing oracles; the same
// `subtle.ConstantTimeCompare` is used by the HTTP path.
func bearerUnaryInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		// Health is the liveness probe; it stays open
		// when the bearer is unset, mirroring the
		// HTTP /healthz contract.
		if info.FullMethod == "/aegis.v1.AegisAgent/Health" {
			return handler(ctx, req)
		}
		// When the bearer is empty, the agent is in
		// "insecure mode" (the docker-compose smoke).
		// The HTTP path returns 503 for every non-/healthz
		// route; we map that to a gRPC `Unavailable`
		// status so the panel's BatchedApplier can
		// distinguish "agent is not configured" from
		// "agent rejected the request".
		if bearerSecret == "" {
			return nil, status.Error(codes.Unavailable, "agent bearer secret not configured")
		}
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
		}
		vals := md.Get("authorization")
		if len(vals) == 0 {
			return nil, status.Error(codes.Unauthenticated, "missing authorization metadata")
		}
		got := bearerFromMetadataValue(vals[0])
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(bearerSecret)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "invalid bearer")
		}
		return handler(ctx, req)
	}
}

// bearerFromMetadataValue extracts the bearer token from a single
// `authorization` metadata value. Accepts both `Bearer <token>` and
// the raw `<token>` forms; the HTTP `bearerFromRequest` helper does
// the same. Trimmed of surrounding whitespace so a panel-side
// trailing `\n` does not surface as an auth failure.
func bearerFromMetadataValue(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
	}
	return v
}

// runGRPC starts the gRPC server on `addr` and blocks until ctx is
// done. It is the gRPC-side mirror of the v0.4.0-b `run`; both are
// called from `main` and both report their errors to a single
// `errCh` (see `main.go`). On shutdown, `grpc.Server.GracefulStop`
// drains in-flight RPCs with the same 10-second budget the HTTP
// `Shutdown` path uses.
//
// v0.8.30: the gRPC server's transport is selected at boot:
//   - `mtlsPaths.mtlsEnabled() == true` → mTLS (the v0.8.32 default)
//   - `mtlsPaths.mtlsEnabled() == false` → plaintext (v0.8.29 default;
//     the v0.8.30+ panel can dial over HTTP+bearer for nodes that
//     have not been re-provisioned yet)
//
// The fallback is a transitional aid; v0.8.32 removes the
// plaintext branch entirely. The operator opts in to mTLS by
// writing the three cert files to the standard paths (or
// overriding the env vars).
func runGRPC(ctx context.Context, addr string, paths mtlsPaths) error {
	if addr == "" {
		// Operator opted out of gRPC by exporting
		// `AEGIS_AGENT_LISTEN_GRPC=""`. The HTTP path
		// keeps working; the gRPC server is not
		// started. v0.8.30+ removes this branch.
		log.Printf("aegis-agent gRPC disabled (AEGIS_AGENT_LISTEN_GRPC=\"\")")
		return nil
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}

	// Transport selection. The cert+key+CA files
	// are written by the bootstrap installer in
	// v0.8.30; a v0.8.29 (or earlier) installer
	// leaves the paths empty and the gRPC server
	// falls back to plaintext (the v0.8.29
	// bearer-only path).
	var credsOpt grpc.ServerOption
	if paths.mtlsEnabled() {
		cfg, err := loadMTLSConfig(paths)
		if err != nil {
			// Missing / corrupted cert files are
			// a hard error: the operator either
			// has not run the bootstrap installer
			// (which the v0.8.30+ plan does on
			// every provision) or the operator
			// overrode the env var to a wrong
			// path. Silently falling back to
			// plaintext would let the panel dial
			// succeed and the TLS handshake fail
			// (the panel's mTLS client dials with
			// the new root CA; the agent's plaintext
			// gRPC rejects the connection with
			// "unimplemented" or "transport
			// closed"). The hard error is
			// friendlier.
			return fmt.Errorf("aegis-agent: gRPC mTLS required but load failed: %w", err)
		}
		credsOpt = newMTLSServerOption(cfg)
		log.Printf("aegis-agent gRPC mTLS enabled (cert=%s, ca=%s)", paths.Cert, paths.CA)
	} else {
		// v0.8.29 fallback. Removed in v0.8.32.
		credsOpt = grpc.Creds(insecure.NewCredentials())
		log.Printf("aegis-agent gRPC plaintext (mTLS disabled; v0.8.29 backward-compat path)")
	}

	srv := grpc.NewServer(
		credsOpt,
		grpc.UnaryInterceptor(bearerUnaryInterceptor()),
		grpc.ConnectionTimeout(5*time.Second),
	)
	aegisv1.RegisterAegisAgentServer(srv, &agentGRPCServer{})

	log.Printf("aegis-agent gRPC listening on %s (bearer=%t)",
		addr, bearerSecret != "")

	errCh := make(chan error, 1)
	go func() {
		if err := srv.Serve(lis); err != nil {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		log.Printf("gRPC shutdown signal received; draining in-flight RPCs")
		// 10-second drain matches the HTTP `Shutdown`
		// path. `GracefulStop` returns when all
		// in-flight RPCs complete or the deadline
		// fires; the timeout-bound variant avoids
		// hanging on a stuck handler.
		stopped := make(chan struct{})
		go func() {
			srv.GracefulStop()
			close(stopped)
		}()
		select {
		case <-stopped:
			return nil
		case <-time.After(10 * time.Second):
			log.Printf("gRPC graceful stop timed out; forcing close")
			srv.Stop()
			return nil
		}
	case err := <-errCh:
		return err
	}
}
