// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package agentca is the panel-side Certificate Authority
// for the mTLS+gRPC control plane. v0.8.30 introduces it
// alongside the v0.8.29 dual-stack transport; v0.8.32
// removes the HTTP+bearer path entirely, at which point
// the per-node certs generated here are the only auth
// the agent has.
//
// # Cert model
//
// The panel owns a single self-signed root CA, stored
// in the `agentca` table with the private key sealed
// with the operator's age envelope (the same pattern
// as `nodes.ssh_private_key_ciphertext` per PR #179).
// On every `nodes.Service.Provision` the panel:
//
//  1. Reads (or creates) the root CA.
//  2. Issues a server cert for the node (CN=
//     `<node-uuid>`, SAN=DNS:<node-uuid>, IP:<node-ip>,
//     validity 90 days).
//  3. Issues a client cert for the panel (CN=
//     `aegis-panel`, validity 1 year — the panel
//     rotates far less often than the nodes).
//  4. Encrypts both private keys with the age envelope
//     and persists them in the `nodes` table.
//  5. Pushes the cert+key files to the node via the
//     bootstrap installer (the same SFTP channel the
//     agent binary uses).
//
// The agent reads the cert+key files from
// `/etc/aegis/agent.env` and serves the gRPC listener
// with `creds.NewServerTLSFromCert(...)`. The panel
// reads the same column on every `Apply` and dials
// the agent with `credentials.NewTLS(...)` using the
// client cert + the shared root CA.
//
// # Why ECDSA P-256 (not RSA)
//
// ECDSA P-256 keeps the wire size small (a server
// handshake is ~1.5 KiB vs ~3 KiB for RSA-2048), the
// keygen is fast (~10ms vs ~500ms for RSA-2048), and
// Go's `crypto/ecdsa` + `crypto/elliptic` are
// well-audited. The CA + per-node certs are valid for
// years; the operator can opt into RSA later if a
// specific compliance regime requires it (one
// `KeyGenerator` interface change).
//
// # Why 90-day server certs
//
// 90 days is the Apple / Google / Mozilla convention
// for short-lived leaf certs and the floor for most
// compliance regimes. A rotation cadence of 90 days
// means a leaked cert expires before it can be
// weaponised in a meaningful attack window. The v0.8.31
// migration CLI rotates the certs on operator demand;
// v0.8.30 only adds the bootstrap path. Per-cert
// rotation is the next PR after this one.

package agentca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"time"

	"github.com/google/uuid"
)

// certValidity is the duration the per-node server
// certs are valid for. 90 days mirrors the public-CA
// convention and is the floor for most compliance
// regimes. See the package comment for the rotation
// cadence.
const certValidity = 90 * 24 * time.Hour

// clientCertValidity is the duration the per-panel
// client cert is valid for. The panel rotates far
// less often than the nodes (a panel re-deploy is a
// rare event) so a longer lifetime is appropriate.
const clientCertValidity = 365 * 24 * time.Hour

// caCertValidity is the duration the self-signed root
// CA is valid for. 10 years is the common default for
// a private CA; the operator can rotate by re-running
// `agentca.EnsureRoot` (a v0.8.30+ follow-up).
const caCertValidity = 10 * 365 * 24 * time.Hour

// caCommonName is the subject CN for the self-signed
// root. The string is fixed so the operator can grep
// the keystore / OS trust list for it.
const caCommonName = "Aegis Panel Root CA"

// serverCertCommonNamePrefix is the per-node server
// cert CN. The full CN is `Aegis Agent <node-uuid>`.
// The format is a deliberate pattern so the operator
// can grep the node's filesystem / trust list.
const serverCertCommonNamePrefix = "Aegis Agent "

// clientCertCommonName is the panel client cert CN.
// One cert for the whole panel (the panel process is
// the only mTLS client).
const clientCertCommonName = "Aegis Panel Client"

// CA is the panel's self-signed root. The fields are
// exported so the `agentca` tests can inspect the
// serial + validity; production code consumes
// `RootCertPEM` + `SignLeaf` only.
type CA struct {
	// PrivateKey is the CA's signing key. Never
	// logged; the panel process holds it in memory
	// for the duration of the boot. Persistence is
	// the responsibility of the Store (see
	// store.go).
	PrivateKey *ecdsa.PrivateKey
	// Cert is the parsed x509 certificate. The
	// PEM-encoded form is `CertPEM`.
	Cert *x509.Certificate
	// Serial is the CA's serial number (used as
	// the prefix for per-node leaf cert serials).
	Serial int64
}

// RootCertPEM returns the CA certificate as a PEM
// block. The string is what the agent writes to
// `/etc/aegis/agent-ca.pem` and what the panel
// embeds in the gRPC client's `RootCAs` pool.
func (c *CA) RootCertPEM() string {
	return EncodeCertPEM(c.Cert)
}

// SignLeaf produces a leaf certificate (server or
// client) signed by this CA. The leaf's `Subject`
// and `SANs` come from the `template` argument; the
// caller fills in the per-node / per-panel fields and
// passes a ready-to-sign certificate. The function
// returns the signed `*x509.Certificate` and the
// matching PEM-encoded private key.
//
// The template's `SerialNumber` MUST be unique per
// leaf (the convention is `<ca.Serial> * 1e9 +
// <node-serial>` — the agentca Service computes the
// node serial and sets it before calling `SignLeaf`).
// The CA's `IsCA` and `KeyUsage` are not on the leaf;
// the leaf carries `ExtKeyUsage: ServerAuth` or
// `ExtKeyUsage: ClientAuth` as appropriate.
func (c *CA) SignLeaf(template *x509.Certificate) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	leafKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("agentca: generate leaf key: %w", err)
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.Cert, &leafKey.PublicKey, c.PrivateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("agentca: create leaf certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, nil, fmt.Errorf("agentca: parse leaf certificate: %w", err)
	}
	return leaf, leafKey, nil
}

// LeafKeyPEM returns the leaf's private key as a
// PEM block. The encoding uses `EC PRIVATE KEY`
// (SEC1) — the format every TLS library consumes.
// (PKCS#8 is more modern but every Go tls.Config +
// every gRPC + every OpenSSL version handles SEC1.)
func LeafKeyPEM(key *ecdsa.PrivateKey) ([]byte, error) {
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("agentca: marshal EC private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: der,
	}), nil
}

// EncodeCertPEM returns the certificate as a PEM
// block (`CERTIFICATE` type).
func EncodeCertPEM(cert *x509.Certificate) string {
	return string(pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: cert.Raw,
	}))
}

// NewRootCA generates a fresh self-signed root. The
// returned `*CA` is in-memory only; persistence is the
// Store's job. The serial is read from `crypto/rand`
// (63 bits, fits comfortably in int64 with a positive
// sign; the per-node leaf serials are derived from it
// in `nodeServerCertTemplate`).
func NewRootCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("agentca: generate CA key: %w", err)
	}
	// 63 bits of randomness is enough for a 10-year
	// CA + 90d/365d leaves. The `1<<62` upper bound
	// guarantees the int64 conversion is non-negative.
	serial, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		return nil, fmt.Errorf("agentca: generate serial: %w", err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   caCommonName,
			Organization: []string{"Aegis Panel"},
		},
		NotBefore:             now.Add(-1 * time.Hour), // clock skew window
		NotAfter:              now.Add(caCertValidity),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("agentca: self-sign root: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("agentca: parse root: %w", err)
	}
	return &CA{
		PrivateKey: key,
		Cert:       cert,
		Serial:     serial.Int64(),
	}, nil
}

// IssueNodeServerCert builds a server-cert template
// for `nodeID` and signs it with `ca`. The SAN set
// covers the standard `nodeID` UUID (the agent's
// loopback listener is bound to `127.0.0.1`, but the
// `nodeID` SAN is the canonical name the panel uses
// to dial) plus the agent's listen IP (so a future
// direct-IP dial works without a DNS lookup). The
// validity is `certValidity` (90 days).
//
// `nodeIP` is the host:port string the panel uses to
// dial the agent. Only the IP portion is extracted;
// the port is irrelevant for a cert (the SAN is the
// address, not the port).
func (ca *CA) IssueNodeServerCert(nodeID uuid.UUID, nodeIP string) (certPEM string, keyPEM []byte, expiresAt time.Time, err error) {
	tmpl, serial, err := nodeServerCertTemplate(nodeID, nodeIP, time.Now().UTC())
	if err != nil {
		return "", nil, time.Time{}, err
	}
	leaf, leafKey, err := ca.SignLeaf(tmpl)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	_ = serial
	keyPEM, err = LeafKeyPEM(leafKey)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return EncodeCertPEM(leaf), keyPEM, leaf.NotAfter, nil
}

// IssuePanelClientCert builds a client-cert template
// for the panel process and signs it. The cert is
// reused across every node the panel talks to (the
// panel is the only mTLS client). Validity is
// `clientCertValidity` (1 year).
func (ca *CA) IssuePanelClientCert() (certPEM string, keyPEM []byte, expiresAt time.Time, err error) {
	tmpl, err := panelClientCertTemplate(time.Now().UTC())
	if err != nil {
		return "", nil, time.Time{}, err
	}
	leaf, leafKey, err := ca.SignLeaf(tmpl)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	keyPEM, err = LeafKeyPEM(leafKey)
	if err != nil {
		return "", nil, time.Time{}, err
	}
	return EncodeCertPEM(leaf), keyPEM, leaf.NotAfter, nil
}

// nodeServerCertTemplate builds the x509 template
// for a node's server cert. Extracted so the
// `serial` derivation is testable in isolation.
func nodeServerCertTemplate(nodeID uuid.UUID, nodeIP string, now time.Time) (*x509.Certificate, int64, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, 0, fmt.Errorf("agentca: generate node serial: %w", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   serverCertCommonNamePrefix + nodeID.String(),
			Organization: []string{"Aegis Panel"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	// SANs: DNS = <node-uuid>, IP = <node-ip-if-parseable>.
	// The `nodeID` SAN is the canonical name; the IP
	// SAN is opportunistic (a future dialer can use
	// either). `nodeIP` may be `host:port`,
	// `host` (no port), or a bare hostname; the
	// `SplitHostPort` first then `ParseIP` fallback
	// covers all three.
	tmpl.DNSNames = []string{nodeID.String()}
	if host, _, err := net.SplitHostPort(nodeIP); err == nil {
		if ip := net.ParseIP(host); ip != nil {
			tmpl.IPAddresses = []net.IP{ip}
		}
	} else if ip := net.ParseIP(nodeIP); ip != nil {
		// No port in the address; try the bare string
		// as an IP. (A hostname falls through both
		// branches and gets no IP SAN — that is the
		// intended behaviour; the DNS SAN is enough
		// for the panel's dialer.)
		tmpl.IPAddresses = []net.IP{ip}
	}
	return tmpl, serial.Int64(), nil
}

// panelClientCertTemplate builds the x509 template
// for the panel's client cert.
func panelClientCertTemplate(now time.Time) (*x509.Certificate, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 64))
	if err != nil {
		return nil, fmt.Errorf("agentca: generate client serial: %w", err)
	}
	return &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   clientCertCommonName,
			Organization: []string{"Aegis Panel"},
		},
		NotBefore:             now.Add(-1 * time.Hour),
		NotAfter:              now.Add(clientCertValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}, nil
}
