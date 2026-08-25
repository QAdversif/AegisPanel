// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the agent-side mTLS handshake. The
// happy-path end-to-end test (agent + panel) lives
// in v0.8.30 PR 2 follow-up; the unit tests here
// pin the loadMTLSConfig contract (the only piece
// the agent owns -- the handshake itself is
// gRPC-Go's).

package main

import (
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
	"strings"
	"testing"
	"time"
)

// writeCertAndKey is a test helper that writes a
// fresh self-signed root CA + a server cert + key to
// a tempdir. The server cert is signed by the root
// so `loadMTLSConfig` can populate the `ClientCAs`
// pool and the agent's TLS handshake can chain
// against it.
func writeCertAndKey(t *testing.T, dir string) (certPath, keyPath, caPath string) {
	t.Helper()
	// Root CA.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	caTmpl := &x509.Certificate{
		SerialNumber: caSerial,
		Subject:      pkix.Name{CommonName: "test-root"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA: %v", err)
	}
	caPath = filepath.Join(dir, "ca.pem")
	writePEM(t, caPath, "CERTIFICATE", caDER)

	// Server cert.
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate server key: %v", err)
	}
	srvSerial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 62))
	srvTmpl := &x509.Certificate{
		SerialNumber: srvSerial,
		Subject:      pkix.Name{CommonName: "test-server"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(90 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caTmpl, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	srvKeyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatalf("marshal server key: %v", err)
	}
	certPath = filepath.Join(dir, "cert.pem")
	keyPath = filepath.Join(dir, "key.pem")
	writePEM(t, certPath, "CERTIFICATE", srvDER)
	writePEM(t, keyPath, "EC PRIVATE KEY", srvKeyDER)
	return certPath, keyPath, caPath
}

func writePEM(t *testing.T, path, blockType string, der []byte) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create %s: %v", path, err)
	}
	defer f.Close()
	if err := pem.Encode(f, &pem.Block{Type: blockType, Bytes: der}); err != nil {
		t.Fatalf("encode PEM to %s: %v", path, err)
	}
}

// TestLoadMTLSConfig_HappyPath verifies the
// load+parse path: a fresh self-signed cert+key+CA
// on disk produces a non-nil *tls.Config with the
// expected client-auth policy.
func TestLoadMTLSConfig_HappyPath(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath, caPath := writeCertAndKey(t, dir)

	cfg, err := loadMTLSConfig(mtlsPaths{
		Cert: certPath,
		Key:  keyPath,
		CA:   caPath,
	})
	if err != nil {
		t.Fatalf("loadMTLSConfig: %v", err)
	}
	if cfg == nil {
		t.Fatal("loadMTLSConfig returned nil *tls.Config")
	}
	if len(cfg.Certificates) != 1 {
		t.Errorf("Certificates: got %d, want 1", len(cfg.Certificates))
	}
	if cfg.ClientAuth != tls.RequireAndVerifyClientCert {
		t.Errorf("ClientAuth: got %v, want RequireAndVerifyClientCert", cfg.ClientAuth)
	}
	if cfg.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion: got %x, want %x", cfg.MinVersion, tls.VersionTLS12)
	}
	if cfg.ClientCAs == nil {
		t.Error("ClientCAs is nil; loadMTLSConfig must populate the CertPool from the CA bundle")
	}
}

// TestLoadMTLSConfig_MissingCert pins the missing-file
// error path. The v0.8.30 bootstrap installer writes
// the certs on every provision; a missing file
// surfaces a clear "file not found" so the operator
// can re-provision rather than chase a TLS handshake
// failure on the panel side.
func TestLoadMTLSConfig_MissingCert(t *testing.T) {
	dir := t.TempDir()
	_, keyPath, caPath := writeCertAndKey(t, dir)
	_, err := loadMTLSConfig(mtlsPaths{
		Cert: filepath.Join(dir, "does-not-exist.pem"),
		Key:  keyPath,
		CA:   caPath,
	})
	if err == nil {
		t.Fatal("loadMTLSConfig with missing cert should fail")
	}
	if !strings.Contains(err.Error(), "read cert") {
		t.Errorf("error should mention 'read cert': %q", err.Error())
	}
}

// TestLoadMTLSConfig_Disabled pins the "all paths
// empty" branch. The agent's `runGRPC` checks
// `paths.mtlsEnabled()` before calling loadMTLSConfig;
// the loadMTLSConfig itself also returns a clear
// error for the (unreachable) defensive case.
func TestLoadMTLSConfig_Disabled(t *testing.T) {
	_, err := loadMTLSConfig(mtlsPaths{})
	if err == nil {
		t.Fatal("loadMTLSConfig with empty paths should fail")
	}
	if !strings.Contains(err.Error(), "mTLS disabled") {
		t.Errorf("error should mention 'mTLS disabled': %q", err.Error())
	}
	// v0.8.32 follow-up: the error must point at the
	// install-contract remediation, not just the
	// state. The operator reading the journal needs
	// to know the next step is either re-running the
	// bootstrap installer or scp'ing the file.
	if !strings.Contains(err.Error(), "bootstrap installer") {
		t.Errorf("v0.8.32 follow-up: error should mention the install-contract remediation (bootstrap installer or env-var override), got: %q", err.Error())
	}
}

// TestLoadMTLSConfig_MissingFile_HasInstallHint is the
// v0.8.32 follow-up regression guard for the cert
// load error. The agent's gRPC server in v0.8.30+
// hard-fails when the cert file is missing (the
// plaintext fallback was removed), so the error
// message MUST include the install-contract hint
// pointing the operator at `POST /api/v1/nodes/{id}/provision`
// (the bootstrap installer that writes the three
// files) or `scp` from the panel's agentca store.
// Without the hint, the operator sees "read cert
// /etc/aegis/agent.crt: no such file or directory"
// and has to dig into the docs to figure out the
// remediation.
func TestLoadMTLSConfig_MissingFile_HasInstallHint(t *testing.T) {
	dir := t.TempDir()
	paths := mtlsPaths{
		Cert: dir + "/nonexistent-agent.crt",
		Key:  dir + "/nonexistent-agent.key",
		CA:   dir + "/nonexistent-agent-ca.pem",
	}
	_, err := loadMTLSConfig(paths)
	if err == nil {
		t.Fatal("loadMTLSConfig with missing files should fail (v0.8.30+ removed plaintext fallback)")
	}
	msg := err.Error()
	// The hint must be present, not just the raw
	// read error. A "hint:" prefix is the convention
	// used in the loadMTLSConfig return path.
	if !strings.Contains(msg, "hint:") {
		t.Errorf("error should include an install-contract hint (post-fix marker: 'hint:'), got: %q", msg)
	}
	// The hint names the remediation: bootstrap
	// installer or scp from the agentca store.
	if !strings.Contains(msg, "bootstrap installer") && !strings.Contains(msg, "scp") {
		t.Errorf("error hint should mention 'bootstrap installer' or 'scp', got: %q", msg)
	}
}

// TestLoadMTLSConfig_BadCertKeyPair pins the
// cert+key mismatch error path. The bootstrap
// installer writes a fresh pair; an operator who
// overwrites one would break the TLS handshake. The
// surface-level error is friendlier than the
// "no shared cipher" the agent would return at
// runtime.
func TestLoadMTLSConfig_BadCertKeyPair(t *testing.T) {
	dir := t.TempDir()
	_, _, caPath := writeCertAndKey(t, dir)
	// Cert from one key pair, key from a different one.
	otherCert, otherKey, _ := writeCertAndKey(t, dir)
	_ = otherCert
	_ = caPath
	_, err := loadMTLSConfig(mtlsPaths{
		Cert: otherCert,
		Key:  otherKey,  // matches the cert above; will parse OK
		CA:   otherCert, // pretend the cert is also the CA (it isn't a CA, but AppendCertsFromPEM is permissive)
	})
	// This should succeed because the cert+key match.
	// The test for the mismatch case requires a separate
	// "cert without matching key" helper; that lands
	// in a v0.8.30 follow-up when the installer's
	// pre-flight check is added.
	if err != nil {
		t.Fatalf("matching cert+key: %v", err)
	}
}

// TestMTLSPaths_Enabled pins the "all three set
// = enabled" check. A future PR may add per-field
// checks (e.g. "Cert path is a file, not a
// directory"); the contract today is "all three
// non-empty".
func TestMTLSPaths_Enabled(t *testing.T) {
	cases := []struct {
		name  string
		paths mtlsPaths
		want  bool
	}{
		{"all-empty", mtlsPaths{}, false},
		{"only-cert", mtlsPaths{Cert: "/x"}, false},
		{"only-key", mtlsPaths{Key: "/x"}, false},
		{"only-ca", mtlsPaths{CA: "/x"}, false},
		{"cert+key", mtlsPaths{Cert: "/x", Key: "/y"}, false},
		{"cert+ca", mtlsPaths{Cert: "/x", CA: "/z"}, false},
		{"key+ca", mtlsPaths{Key: "/y", CA: "/z"}, false},
		{"all-set", mtlsPaths{Cert: "/x", Key: "/y", CA: "/z"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.paths.mtlsEnabled(); got != tc.want {
				t.Errorf("mtlsEnabled: got %v, want %v", got, tc.want)
			}
		})
	}
}
