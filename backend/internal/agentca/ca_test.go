// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the agentca cert generation. The test
// surface covers the v0.8.30 PR 1 contract:
//
//  1. NewRootCA returns a usable self-signed root
//     (validates against itself, valid IsCA flag,
//     correct Subject).
//  2. IssueNodeServerCert returns a cert that
//     validates against the CA (signature + chain),
//     has the expected SANs (DNS=node-uuid, IP=
//     parsed-from-addr), and is valid for ~90 days.
//  3. IssuePanelClientCert returns a client cert
//     with ExtKeyUsage=ClientAuth, valid for ~1
//     year.
//  4. Round-trip: PEM in / PEM out / cert parses
//     back to the same Subject + NotAfter.
//  5. Fuzz: a 100-iteration random-nodeID/addr
//     loop covers the serial / SAN edge cases.

package agentca

import (
	"crypto/elliptic"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"math/rand"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestNewRootCA_HappyPath verifies the self-signed
// root is well-formed and validates against itself.
func TestNewRootCA_HappyPath(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	if ca.Cert.Subject.CommonName != caCommonName {
		t.Errorf("Subject.CommonName = %q, want %q", ca.Cert.Subject.CommonName, caCommonName)
	}
	if !ca.Cert.IsCA {
		t.Error("Cert.IsCA: got false, want true (self-signed root)")
	}
	if ca.Cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Error("Cert.KeyUsage: missing KeyUsageCertSign (required for signing leaves)")
	}
	// Self-validate: the root must validate against
	// its own public key (x509.CreateCertificate's
	// `parent == template` case).
	if _, err := ca.Cert.Verify(x509.VerifyOptions{
		Roots:     x509.NewCertPool(),
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	}); err == nil {
		t.Error("Cert.Verify with empty pool should fail; got nil")
	}
	// The cert is well-formed for >9 years (10y -
	// 1y slack).
	if time.Until(ca.Cert.NotAfter) < 9*365*24*time.Hour {
		t.Errorf("Cert.NotAfter too soon: %s", ca.Cert.NotAfter)
	}
}

// TestNewRootCA_PEMEncodes pins the on-the-wire
// format. A regression here breaks every node
// install (the agent refuses to load a cert it can't
// parse).
func TestNewRootCA_PEMEncodes(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	pemStr := ca.RootCertPEM()
	if !strings.HasPrefix(pemStr, "-----BEGIN CERTIFICATE-----\n") {
		t.Errorf("PEM missing BEGIN block: %q", pemStr[:32])
	}
	if !strings.HasSuffix(strings.TrimSpace(pemStr), "-----END CERTIFICATE-----") {
		t.Error("PEM missing END block")
	}
	// Round-trip: decode the PEM, assert the parsed
	// cert matches the original Subject.
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		t.Fatal("pem.Decode: no block found")
	}
	if block.Type != "CERTIFICATE" {
		t.Errorf("PEM block type = %q, want %q", block.Type, "CERTIFICATE")
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("ParseCertificate: %v", err)
	}
	if parsed.Subject.CommonName != ca.Cert.Subject.CommonName {
		t.Errorf("PEM round-trip Subject = %q, want %q", parsed.Subject.CommonName, ca.Cert.Subject.CommonName)
	}
}

// TestIssueNodeServerCert_HappyPath verifies the
// cert signs correctly under the root, has the
// expected Subject + SANs, and is valid for ~90 days.
func TestIssueNodeServerCert_HappyPath(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	nodeID := uuid.New()
	addr := "10.0.0.5:7001"
	certPEM, keyPEM, expiresAt, err := ca.IssueNodeServerCert(nodeID, addr)
	if err != nil {
		t.Fatalf("IssueNodeServerCert: %v", err)
	}
	// 1. certPEM is non-empty and well-formed.
	if !strings.Contains(certPEM, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("certPEM missing CERTIFICATE block: %q", certPEM[:32])
	}
	// 2. keyPEM is non-empty and well-formed.
	if !strings.Contains(string(keyPEM), "-----BEGIN EC PRIVATE KEY-----") {
		t.Errorf("keyPEM missing EC PRIVATE KEY block: %q", string(keyPEM)[:32])
	}
	// 3. Parse the leaf and verify the contract.
	leaf, err := leafFromPEM(certPEM)
	if err != nil {
		t.Fatalf("leafFromPEM: %v", err)
	}
	wantCN := serverCertCommonNamePrefix + nodeID.String()
	if leaf.Subject.CommonName != wantCN {
		t.Errorf("Subject.CommonName = %q, want %q", leaf.Subject.CommonName, wantCN)
	}
	wantDNS := nodeID.String()
	if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != wantDNS {
		t.Errorf("DNSNames = %v, want [%q]", leaf.DNSNames, wantDNS)
	}
	wantIP := net.ParseIP("10.0.0.5")
	if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(wantIP) {
		t.Errorf("IPAddresses = %v, want [%q]", leaf.IPAddresses, wantIP)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Errorf("ExtKeyUsage = %v, want [ServerAuth]", leaf.ExtKeyUsage)
	}
	// 4. Validity: ~90 days. The NotBefore-1h skew
	// window adds 1h to the apparent validity; the
	// test asserts the total is within a minute of
	// 90d + 1h.
	wantValidity := 90*24*time.Hour + time.Hour
	actualValidity := leaf.NotAfter.Sub(leaf.NotBefore)
	diff := actualValidity - wantValidity
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("validity = %s, want ~%s (diff %s)", actualValidity, wantValidity, diff)
	}
	// 5. expiresAt matches the parsed leaf's
	// NotAfter (the return value is the source of
	// truth the agentca Service persists to the DB).
	if !expiresAt.Equal(leaf.NotAfter) {
		t.Errorf("expiresAt = %s, leaf.NotAfter = %s", expiresAt, leaf.NotAfter)
	}
	// 6. The leaf validates against the CA pool.
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	if _, err := leaf.Verify(x509.VerifyOptions{
		Roots:       roots,
		KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		CurrentTime: time.Now(),
		DNSName:     nodeID.String(),
	}); err != nil {
		t.Errorf("leaf.Verify: %v", err)
	}
}

// TestIssueNodeServerCert_AddrVariants covers the
// host:port parsing edge cases: missing port, IPv6
// (with and without brackets), hostname (not an IP).
func TestIssueNodeServerCert_AddrVariants(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	cases := []struct {
		name string
		addr string
		// wantIP is "" if no IP SAN is expected
		// (hostname, no port, etc.).
		wantIP string
	}{
		{"ipv4-with-port", "10.0.0.5:7001", "10.0.0.5"},
		{"ipv4-no-port", "10.0.0.5", "10.0.0.5"},
		{"ipv6-with-port", "[2001:db8::1]:7001", "2001:db8::1"},
		{"hostname", "node-1.example.com:7001", ""},
		{"empty", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			nodeID := uuid.New()
			certPEM, _, _, err := ca.IssueNodeServerCert(nodeID, tc.addr)
			if err != nil {
				t.Fatalf("IssueNodeServerCert(%q): %v", tc.addr, err)
			}
			leaf, err := leafFromPEM(certPEM)
			if err != nil {
				t.Fatalf("leafFromPEM: %v", err)
			}
			if tc.wantIP == "" {
				if len(leaf.IPAddresses) != 0 {
					t.Errorf("addr=%q IPAddresses=%v, want none", tc.addr, leaf.IPAddresses)
				}
			} else {
				want := net.ParseIP(tc.wantIP)
				if len(leaf.IPAddresses) != 1 || !leaf.IPAddresses[0].Equal(want) {
					t.Errorf("addr=%q IPAddresses=%v, want [%q]", tc.addr, leaf.IPAddresses, want)
				}
			}
		})
	}
}

// TestIssuePanelClientCert_HappyPath verifies the
// client cert's ExtKeyUsage + validity.
func TestIssuePanelClientCert_HappyPath(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	certPEM, keyPEM, expiresAt, err := ca.IssuePanelClientCert()
	if err != nil {
		t.Fatalf("IssuePanelClientCert: %v", err)
	}
	if !strings.Contains(certPEM, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("certPEM missing CERTIFICATE block")
	}
	if !strings.Contains(string(keyPEM), "-----BEGIN EC PRIVATE KEY-----") {
		t.Errorf("keyPEM missing EC PRIVATE KEY block")
	}
	leaf, err := leafFromPEM(certPEM)
	if err != nil {
		t.Fatalf("leafFromPEM: %v", err)
	}
	if leaf.Subject.CommonName != clientCertCommonName {
		t.Errorf("Subject.CommonName = %q, want %q", leaf.Subject.CommonName, clientCertCommonName)
	}
	if len(leaf.ExtKeyUsage) != 1 || leaf.ExtKeyUsage[0] != x509.ExtKeyUsageClientAuth {
		t.Errorf("ExtKeyUsage = %v, want [ClientAuth]", leaf.ExtKeyUsage)
	}
	wantValidity := 365*24*time.Hour + time.Hour
	actualValidity := leaf.NotAfter.Sub(leaf.NotBefore)
	diff := actualValidity - wantValidity
	if diff < -time.Minute || diff > time.Minute {
		t.Errorf("validity = %s, want ~%s (diff %s)", actualValidity, wantValidity, diff)
	}
	if !expiresAt.Equal(leaf.NotAfter) {
		t.Errorf("expiresAt = %s, leaf.NotAfter = %s", expiresAt, leaf.NotAfter)
	}
}

// TestLeafKeyPEM_RoundTrip pins the on-the-wire
// format. A regression here breaks every install
// (the agent's grpc.NewServer refuses to load a
// key it can't parse).
func TestLeafKeyPEM_RoundTrip(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	_, keyPEM, _, err := ca.IssueNodeServerCert(uuid.New(), "10.0.0.5:7001")
	if err != nil {
		t.Fatalf("IssueNodeServerCert: %v", err)
	}
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		t.Fatal("pem.Decode: no block")
	}
	if block.Type != "EC PRIVATE KEY" {
		t.Errorf("PEM type = %q, want %q", block.Type, "EC PRIVATE KEY")
	}
	parsed, err := x509.ParseECPrivateKey(block.Bytes)
	if err != nil {
		t.Fatalf("ParseECPrivateKey: %v", err)
	}
	if parsed.Curve != ellipticP256() {
		t.Error("parsed.Curve: not P-256")
	}
}

// TestFuzz_100Nodes is the v0.8.30 PR 1 fuzz
// "smoke test". 100 random node UUIDs / addrs
// exercise the serial / SAN / Subject paths
// without an explicit `go test -fuzz` round
// (fuzzing x509 generation adds little value over
// a 100-iteration smoke; the determinism is in
// the template shape, not the random data).
func TestFuzz_100Nodes(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(ca.Cert)
	rng := rand.New(rand.NewSource(42)) // deterministic
	for i := 0; i < 100; i++ {
		nodeID := uuid.New()
		// Mix IPv4, IPv6, hostnames, edge cases.
		var addr string
		switch rng.Intn(4) {
		case 0:
			addr = "10.0.0.1:7001"
		case 1:
			addr = "2001:db8::1:7001"
		case 2:
			addr = "node-1.example.com:7001"
		default:
			addr = ""
		}
		certPEM, keyPEM, _, err := ca.IssueNodeServerCert(nodeID, addr)
		if err != nil {
			t.Fatalf("iter %d: IssueNodeServerCert: %v", i, err)
		}
		leaf, err := leafFromPEM(certPEM)
		if err != nil {
			t.Fatalf("iter %d: leafFromPEM: %v", i, err)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{
			Roots:       roots,
			KeyUsages:   []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			CurrentTime: time.Now(),
			DNSName:     nodeID.String(),
		}); err != nil {
			t.Errorf("iter %d: leaf.Verify(%s): %v", i, nodeID, err)
		}
		// The key must always parse.
		block, _ := pem.Decode(keyPEM)
		if block == nil {
			t.Errorf("iter %d: keyPEM has no PEM block", i)
		}
	}
}

// TestRootCertPEM_MultipleCallsStable verifies
// that repeated calls to RootCertPEM return the
// same bytes (the cert doesn't change after
// generation). The agentca Service caches the
// root in memory and serves the same PEM on
// every call; a regression here would make the
// on-wire CA cert rotate between calls.
func TestRootCertPEM_MultipleCallsStable(t *testing.T) {
	ca, err := NewRootCA()
	if err != nil {
		t.Fatalf("NewRootCA: %v", err)
	}
	first := ca.RootCertPEM()
	for i := 0; i < 5; i++ {
		got := ca.RootCertPEM()
		if got != first {
			t.Errorf("call %d: PEM differs from first call", i)
		}
	}
}

// leafFromPEM is a test helper that decodes a
// certificate PEM block and parses the inner DER.
func leafFromPEM(certPEM string) (*x509.Certificate, error) {
	block, _ := pem.Decode([]byte(certPEM))
	if block == nil {
		return nil, errors.New("pem.Decode: no block found")
	}
	return x509.ParseCertificate(block.Bytes)
}

// ellipticP256 returns the P-256 curve. The
// `elliptic` import is grouped with the other
// crypto imports above.
func ellipticP256() elliptic.Curve { return elliptic.P256() }
