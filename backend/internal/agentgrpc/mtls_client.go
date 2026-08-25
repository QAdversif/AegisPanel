// SPDX-License-Identifier: AGPL-3.0-or-later
//
// mTLS client for the panel-side gRPC transport.
// v0.8.30 PR 2b. The panel dials the agent with a
// client cert (its own identity) + the agent's root
// CA bundle (to verify the agent's server cert).
// The cert material comes from the `NodeResolver`
// (the v0.8.30 PR 1c `agentca.Service` writes the
// per-node certs to the `nodes` table on every
// `Provision`; the panel reads them on every Apply).
//
// # Why client certs at all
//
// The bearer path (`Authorization: Bearer <token>`)
// authenticates the panel to the agent, but not the
// agent to the panel. mTLS is mutual: both sides
// present a cert, both sides verify the other's
// chain. A network attacker who can read the bearer
// from a memory dump / log leak cannot reuse it
// without also forging a client cert.
//
// # Why a separate file
//
// The mTLS path is the v0.8.30 default; the v0.8.29
// fallback (plaintext + bearer) is in
// `grpc_transport.go`. Splitting the two paths into
// separate files makes the diff between v0.8.29 and
// v0.8.30 easy to read. The two paths are bridged
// by `dialAgent` (in `grpc_transport.go`); the
// transport selection is at the dial site, not per-
// RPC.

package agentgrpc

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"google.golang.org/grpc/credentials"
)

// mtlsMaterial is the cert + key + CA bundle the
// gRPC transport loads from the resolver. All three
// are PEM-encoded; the loader is `loadClientTLS`.
//
// A zero-value (all nil) is the "mTLS not wired"
// sentinel: `dialAgent` falls back to plaintext
// (the v0.8.29 path). A non-nil `Err` is a real
// error (cert files on disk are corrupt or the
// resolver's Store is misconfigured).
type mtlsMaterial struct {
	ClientCert []byte
	ClientKey  []byte
	CA         []byte
	Err        error
}

// loadClientTLS parses the mTLS material into a
// `*tls.Config` suitable for
// `credentials.NewTLS(...)`. The returned config
// demands the server to present a cert chaining to
// the supplied CA (the agent's server cert). The
// client side presents the panel's client cert
// (verified by the agent via the same CA).
//
// `MinVersion: tls.VersionTLS12` matches the agent
// side (cmd/aegis-agent/mtls.go); the cipher list
// is the Go default (ECDSA P-256 + AES-GCM).
func loadClientTLS(m mtlsMaterial) (*tls.Config, error) {
	if m.Err != nil {
		return nil, fmt.Errorf("agentgrpc: loadClientTLS: %w", m.Err)
	}
	if len(m.ClientCert) == 0 || len(m.ClientKey) == 0 || len(m.CA) == 0 {
		return nil, errors.New("agentgrpc: loadClientTLS: missing cert/key/CA (the resolver returned an empty material)")
	}
	cert, err := tls.X509KeyPair(m.ClientCert, m.ClientKey)
	if err != nil {
		return nil, fmt.Errorf("agentgrpc: parse client cert+key: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(m.CA) {
		return nil, errors.New("agentgrpc: loadClientTLS: no certificates parsed from CA bundle")
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		// ServerName is set per-dial (the gRPC
		// dialer uses the node's host as the SNI
		// hint; for SSH-tunnelled nodes the
		// nodeID matches the SAN `DNS` field).
		MinVersion: tls.VersionTLS12,
	}, nil
}

// newClientTLSCreds is the thin adapter from a
// `*tls.Config` to a gRPC `TransportCredentials`. The
// adapter lives in its own function so tests can
// exercise `loadClientTLS` without a gRPC import.
func newClientTLSCreds(cfg *tls.Config) credentials.TransportCredentials {
	return credentials.NewTLS(cfg)
}

// resolveMTLS is the per-call hook: the gRPC
// transport calls this on every Apply to get the
// current mTLS material. The function is non-nil
// in production (the `app` package wires the
// `agentca`-backed `NodeResolver`); the dev-mode
// MemoryStore returns `ErrMTLSNotConfigured` and
// the transport falls back to plaintext.
//
// The `nodeID` is the canonical SNI hint: the
// agent's server cert SANs include
// `DNS=<node-uuid>`, so the gRPC dialer uses
// `<node-uuid>` as the ServerName. The hint is
// passed via the URL scheme (the agent's gRPC
// listener reads the cert from the SAN and
// validates the dialer's SNI matches).
func resolveMTLS(ctx context.Context, r NodeResolver, nodeID uuid.UUID) mtlsMaterial {
	if r == nil {
		return mtlsMaterial{Err: errors.New("agentgrpc: resolveMTLS: nil resolver")}
	}
	cert, key, ca, err := r.LoadMTLS(ctx, nodeID)
	if err != nil {
		return mtlsMaterial{Err: err}
	}
	return mtlsMaterial{ClientCert: cert, ClientKey: key, CA: ca}
}

// ErrMTLSNotConfigured is the sentinel a NodeResolver
// can return from `LoadMTLS` to signal "mTLS is not
// wired for this node". The gRPC transport maps this
// to a plaintext fallback (the v0.8.29 path); any
// other error is a hard error.
var ErrMTLSNotConfigured = errors.New("agentgrpc: mTLS not configured for node")
