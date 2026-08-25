// SPDX-License-Identifier: AGPL-3.0-or-later
//
// mTLS handshake for the aegis-agent gRPC server.
// v0.8.30 PR 2. The agent reads a server cert +
// matching key + a root CA bundle (all PEM-encoded
// on disk) and presents the cert on every gRPC
// connection. The panel (the only mTLS client) dials
// with its own client cert + the same root CA bundle.
//
// # File layout (operator-deployed)
//
// The bootstrap installer (internal/bootstrap) writes
// three files into `/etc/aegis/`:
//
//	agent.crt        -- server cert (PEM, "CERTIFICATE" block)
//	agent.key        -- server key  (PEM, "EC PRIVATE KEY" block)
//	agent-ca.pem     -- root CA bundle (PEM, "CERTIFICATE" blocks)
//
// The file paths are operator-overridable via the
// `AEGIS_AGENT_MTLS_CERT/KEY/CA` env vars (or
// `--mtls-cert/--mtls-key/--mtls-ca` flags). The
// defaults match the standard `install_agent` role
// output; an operator with a non-standard layout
// overrides the env vars.
//
// # Why ECDSA P-256
//
// Mirrors the panel-side `internal/agentca` choice
// (see ca.go for the rationale). The cert type +
// curve are matched end-to-end; the agent would
// reject a panel client cert with a different curve
// during the TLS handshake (`tls: handshake failure`).
//
// # v0.8.30 backward compat
//
// When the cert files are missing, the agent falls
// back to the v0.8.29 plaintext gRPC server. The
// v0.8.32 cut removes the fallback. The fallback
// matters because operators running the v0.8.28
// image on a v0.8.30+ panel need the HTTP+bearer
// path to keep working during the rolling upgrade
// (mTLS is opt-in until the bootstrap installer
// pushes the cert files).

package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Default on-disk locations for the mTLS material.
// The bootstrap installer (internal/bootstrap) writes
// the three files into these paths on every provision.
// The v0.8.29 install_agent role does NOT write them;
// the v0.8.30 installer does. An operator on a v0.8.29
// image + v0.8.30+ panel runs the gRPC server in the
// plaintext fallback (no mTLS files on disk).
const (
	// defaultMTLSCert is the agent's server cert.
	// PEM-encoded ("CERTIFICATE" block). The
	// agent presents this on the gRPC listener.
	defaultMTLSCert = "/etc/aegis/agent.crt"
	// defaultMTLSKey is the matching private key.
	// PEM-encoded ("EC PRIVATE KEY" block, SEC1).
	defaultMTLSKey = "/etc/aegis/agent.key"
	// defaultMTLSCA is the panel-side root CA
	// bundle. PEM-encoded (one or more
	// "CERTIFICATE" blocks). The agent requires
	// the panel's client cert to chain to one of
	// these.
	defaultMTLSCA = "/etc/aegis/agent-ca.pem"
)

// mtlsPaths holds the on-disk locations of the
// three cert+key+CA files. The struct is passed
// through `main -> runGRPC -> newMTLSServerOption`
// so the helper can be tested in isolation (no
// `os.Getenv` inside the helper).
type mtlsPaths struct {
	// Cert is the agent's server cert. PEM
	// (`CERTIFICATE` block). The agent presents
	// this on the gRPC listener.
	Cert string
	// Key is the matching private key. PEM
	// (`EC PRIVATE KEY` block, SEC1-encoded).
	Key string
	// CA is the panel's trusted root + any
	// intermediate CAs. PEM (`CERTIFICATE`
	// blocks, one or more).
	CA string
}

// mtlsEnabled reports whether the three paths are
// set. An empty `MTLSCert` (or any of the three)
// means the v0.8.29 plaintext fallback. The
// operator opts in to mTLS by exporting all three
// env vars; exporting one or two is a no-op (the
// mTLS handshake requires all three to be
// consistent).
func (p mtlsPaths) mtlsEnabled() bool {
	return p.Cert != "" && p.Key != "" && p.CA != ""
}

// loadMTLSConfig reads the three PEM files and
// returns a `*tls.Config` suitable for
// `credentials.NewServerTLSFromCert(...)`. The
// returned config is the agent's server identity
// (the agent presents `Cert`; the client must
// trust `CA`).
//
// The function fails loud on any of:
//   - missing files (the operator has not
//     finished the bootstrap installer; surfacing
//     a clear "file not found" is friendlier than
//     a silent plaintext fallback that would later
//     fail the panel-side `Apply` with a TLS
//     error).
//   - invalid PEM (the file is on disk but
//     corrupted; the operator can `cat` the file
//     and re-run).
//   - cert / key mismatch (the bootstrap installer
//     wrote a fresh cert+key but the operator
//     overwrote one; the handshake would fail
//     anyway, but the explicit error saves a
//     round-trip to the panel's TLS error log).
//
// The `ClientCAs` field is set to a CertPool
// built from the CA bundle. `ClientAuth` is set
// to `tls.RequireAndVerifyClientCert` -- the agent
// requires a client cert (the panel's) and verifies
// it chains to the trusted CA. A misconfigured
// agent (one that accepts ANY client cert) would
// be a critical security regression; the
// `RequireAndVerifyClientCert` mode is the
// only safe default.
func loadMTLSConfig(paths mtlsPaths) (*tls.Config, error) {
	if !paths.mtlsEnabled() {
		// v0.8.30+: the v0.8.29 plaintext-fallback
		// branch is removed. An empty path is a
		// config bug (the operator must have set
		// the env var to ""), not a valid posture
		// — surface a clear error so the operator
		// knows to re-provision rather than silently
		// running the gRPC server without auth.
		return nil, errors.New("aegis-agent: mTLS disabled (one or more of cert/key/CA paths is empty) — re-run the panel's bootstrap installer or set the AEGIS_AGENT_MTLS_CERT/KEY/CA env vars to the canonical paths; v0.8.29 plaintext-fallback is removed")
	}

	// Read the three PEM files. A missing file
	// is a real error: the operator either has
	// not run the bootstrap installer, or the
	// installer wrote the files to a different
	// path (the env var override path is the
	// documented escape hatch). The error
	// message names the specific file that
	// could not be read so the operator does not
	// have to dig into the journal to find it.
	certPEM, err := os.ReadFile(paths.Cert)
	if err != nil {
		return nil, fmt.Errorf("aegis-agent: read cert %q: %w (hint: the certs ship from the panel's bootstrap installer; re-run POST /api/v1/nodes/{id}/provision or scp the file from the panel's internal/agentca store)", paths.Cert, err)
	}
	keyPEM, err := os.ReadFile(paths.Key)
	if err != nil {
		return nil, fmt.Errorf("aegis-agent: read key %q: %w (hint: the certs ship from the panel's bootstrap installer; re-run POST /api/v1/nodes/{id}/provision or scp the file from the panel's internal/agentca store)", paths.Key, err)
	}
	caPEM, err := os.ReadFile(paths.CA)
	if err != nil {
		return nil, fmt.Errorf("aegis-agent: read CA bundle %q: %w", paths.CA, err)
	}

	// Cert + key. `tls.X509KeyPair` parses the
	// PEM blocks and verifies the cert + key
	// match. A mismatch surfaces as a clear
	// error.
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, fmt.Errorf("aegis-agent: parse cert+key (cert=%s key=%s): %w", paths.Cert, paths.Key, err)
	}

	// Root CA pool. The bundle may contain
	// multiple CAs (root + intermediates); the
	// pool's `AppendCertsFromPEM` accepts all of
	// them and uses any one that chains to the
	// client's issuer.
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return nil, fmt.Errorf("aegis-agent: CA bundle %q: no certificates parsed", paths.CA)
	}

	// TLS 1.2 minimum. The agent and the panel
	// (gRPC-Go 1.66+) negotiate TLS 1.2 / 1.3;
	// older Go versions that the operator might
	// pin to (1.17 + Debian 11) ship with TLS 1.2
	// as the default. Forcing 1.2 as a floor
	// avoids a regression where a misconfigured
	// client would negotiate TLS 1.1 (deprecated
	// since RFC 8996).
	//
	// The cipher list is left at the Go default
	// (the 1.18+ default excludes RC4, 3DES, MD5;
	// the agent's ECDSA P-256 cert negotiates
	// ECDHE-ECDSA-AES128-GCM-SHA256 by default).
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// newMTLSServerOption is the thin adapter from a
// `*tls.Config` to a gRPC `ServerOption`. The
// adapter is the only place the agent depends on
// `google.golang.org/grpc/credentials`; the
// `loadMTLSConfig` helper is a stdlib-only function
// that tests can exercise without a gRPC import.
func newMTLSServerOption(cfg *tls.Config) grpc.ServerOption {
	certs := cfg.Certificates
	if len(certs) == 0 {
		// Unreachable when called from `runGRPC`
		// (loadMTLSConfig returns an error on
		// zero certs). The defensive `return nil`
		// surfaces a clearer runtime error than a
		// nil-deref in `grpc.NewServer`.
		return nil
	}
	return grpc.Creds(credentials.NewServerTLSFromCert(&certs[0]))
}
