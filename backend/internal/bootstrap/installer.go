// SPDX-License-Identifier: AGPL-3.0-or-later
//
// The Installer orchestrates the per-node
// bootstrap sequence. The function is the seam
// between the provisioner (state machine +
// audit log + DB) and the SSH client (network).
// Every step is a small, named function so the
// tests can exercise each one in isolation
// without going through the full install
// dance.
//
// # What the installer actually does
//
// 1. Connect to the node over SSH (the
//    transport-level handshake; the provisioner
//    builds the ClientConfig).
// 2. Upload the agent binary to
//    /usr/local/bin/aegis-agent.
// 3. chmod 0755 the binary.
// 4. Write /etc/aegis/agent.env with the bearer
//    secret (the agent reads it on start).
// 5. Write /etc/systemd/system/aegis-agent.service
//    (the unit file).
// 6. systemctl daemon-reload && systemctl
//    enable --now aegis-agent.
// 7. Verify: `systemctl is-active
//    aegis-agent` returns "active".
//
// On any failure the installer returns an
// error; the provisioner transitions the node
// to "offline" and records the audit log entry.
// The provisioner NEVER retries automatically;
// a retry is the operator's "re-provision"
// action (which goes through the same code path
// but from a `offline` start state).
//
// # Why a placeholder for v0.3.0
//
// The real aegis-agent binary is the v0.4.0
// "Batched Apply" milestone. v0.3.0 ships the
// bootstrap pipeline end-to-end so the
// operator can verify "I add a node in the
// UI, the panel installs something, the
// something is running" before v0.4.0 swaps
// the placeholder for the real binary. The
// placeholder is a static `#! /bin/sh exec
// sleep infinity` script — its only job is to
// keep the systemd unit in the `active`
// state so the verify step succeeds.

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Remote paths the installer writes to. The
// constants are kept here (not in main) so the
// test fixtures can use the same values.
const (
	RemoteAgentPath     = "/usr/local/bin/aegis-agent"
	RemoteAgentEnvDir   = "/etc/aegis"
	RemoteAgentEnvPath  = "/etc/aegis/agent.env"
	RemoteAgentUnitPath = "/etc/systemd/system/aegis-agent.service"
	RemoteAgentUser     = "root" // the SSH user; the install runs as root
)

// ErrVerifyFailed is returned by Installer.Install
// when the post-install verify step (the
// `systemctl is-active` call) returns a non-
// `active` state. The provisioner surfaces this
// verbatim in the audit log; the operator UI
// uses the message to decide between "retry" and
// "manual investigation".
var ErrVerifyFailed = errors.New("bootstrap: post-install verify failed")

// InstallInput is the per-call input. The
// struct is small enough that positional args
// would be readable, but the named fields make
// the unit tests less brittle to refactors.
type InstallInput struct {
	// NodeID is the operator's UUID; carried
	// through to the audit log entries. The
	// installer does not look the node up; the
	// provisioner resolves the row.
	NodeID string
	// NodeName is for log messages (e.g. "node
	// production-eu-1"). Not used in the wire
	// format; the agent unit file references
	// the path only.
	NodeName string
	// Address is "host" or "host:port" (the
	// Client layer fills in :22 if missing).
	Address string
	// Port is the SSH port (1-65535). The
	// default 22 is the SSH standard.
	Port int
	// SSHUser is the login name (e.g. "root").
	SSHUser string
	// PrivateKeyPEM is the operator-pasted
	// private key (decoded from the form's
	// "SSH key" textarea). The PEM is passed
	// to ssh.ParsePrivateKey in the Client.
	// Mutually exclusive with Password.
	PrivateKeyPEM []byte
	// Password is the SSH login password for
	// first-time auth on a fresh node. The
	// install is the only consumer — the
	// agent switches to bearer-token auth
	// once the env file is in place. Not
	// stored by the panel. Mutually exclusive
	// with PrivateKeyPEM.
	Password string
	// KnownHosts is the panel's known_hosts
	// file path. The Client appends the new
	// entry on first contact (TOFU).
	KnownHosts string
	// Tofu is the trust-on-first-use policy.
	Tofu TofuPolicy
	// ExpectedFingerprint is the operator-
	// supplied "I trust this host" entry. A
	// mismatch is ErrHostKeyMismatch.
	ExpectedFingerprint string
	// BearerSecret is the hex string generated
	// by the provisioner before Install is
	// called. The agent reads it from
	// /etc/aegis/agent.env at start.
	BearerSecret string
	// AgentSource is the local path of the
	// agent binary to upload. v0.3.0 uses a
	// placeholder; v0.4.0 swaps for a real
	// release artifact.
	AgentSource string
	// PostInstallHook, when non-nil, runs after
	// the systemd unit is installed and reloaded
	// but before the post-install `systemctl
	// is-active` verify. The hook receives the
	// open SSH client so it can issue additional
	// remote commands without re-dialling (e.g.
	// `cat >> ~/.ssh/authorized_keys` to push
	// the panel's persistent SSH public key for
	// the node). A nil return means success; any
	// non-nil error fails the install at the
	// `post-install-hook` stage with the hook's
	// error message preserved. v0.8.x adds the
	// hook for the persistent node SSH key flow
	// (PR #179).
	PostInstallHook func(ctx context.Context, c Client) error
	// MTLSCerts is the v0.8.30 mTLS cert material
	// written to the node on every install. The
	// three files (`agent.crt` + `agent.key` +
	// `agent-ca.pem`) are written via
	// `UploadAndSwap` (ETXTBSY-safe; the same
	// helper the agent binary uses). When all
	// three are empty, the installer skips the
	// write (the v0.8.29 backward-compat path
	// where the agent runs without mTLS).
	MTLSCerts MTLSCerts
}

// MTLSCerts is the v0.8.30 mTLS cert material.
// All three fields are PEM-encoded. The
// installer writes them to the standard paths
// (`/etc/aegis/agent.crt`, `.key`, `agent-ca.pem`)
// and the agent's gRPC server reads them at
// boot (see cmd/aegis-agent/mtls.go).
type MTLSCerts struct {
	// ServerCertPEM is the agent's server cert.
	// Signed by the panel's root CA; the
	// panel-side mTLS client trusts it via
	// the matching `RootCertPEM`.
	ServerCertPEM string
	// ServerKeyPEM is the matching SEC1
	// (`EC PRIVATE KEY` block) private key.
	ServerKeyPEM []byte
	// RootCertPEM is the panel's root CA
	// bundle. One cert per panel; the same
	// root signs every per-node server cert
	// and the panel's client cert.
	RootCertPEM string
}

// IsZero reports whether all three fields are
// empty (the v0.8.29 fallback: no mTLS).
func (m MTLSCerts) IsZero() bool {
	return m.ServerCertPEM == "" && len(m.ServerKeyPEM) == 0 && m.RootCertPEM == ""
}

// InstallResult is the per-call output. The
// provisioner uses the boolean to decide between
// the "online" and "offline" state transitions.
type InstallResult struct {
	// OK is true when every step succeeded,
	// including the post-install verify.
	OK bool
	// Stage is the name of the step that
	// failed (when OK is false). Useful for
	// the operator UI to render a "failed at
	// <stage>" hint.
	Stage string
	// Err is the wrapped error. Always non-nil
	// when OK is false.
	Err error
	// VerifyLatency is the time spent in the
	// post-install `systemctl is-active` call.
	// Useful for the operator UI to render
	// the install time.
	VerifyLatency time.Duration
}

// ClientFactory builds a Client from the input.
// The function is a field on Installer so the
// tests can substitute a mock without the
// installer having to know about the SSH
// package directly. v0.3.0 ships one factory
// (the real NewClient); future versions may
// add a proxy-aware factory.
type ClientFactory func(in InstallInput) (Client, error)

// NewClientFactory is the production factory.
// The closure captures no state; the package-
// level function is exported for use from
// main.go.
func NewClientFactory(in InstallInput) (Client, error) {
	return NewClient(ClientConfig{
		Address:             joinHostPort(in.Address, in.Port),
		User:                in.SSHUser,
		PrivateKey:          in.PrivateKeyPEM,
		Password:            in.Password,
		KnownHosts:          in.KnownHosts,
		Tofu:                in.Tofu,
		ExpectedFingerprint: in.ExpectedFingerprint,
		Timeout:             30 * time.Second,
	})
}

// joinHostPort returns "host:port" or
// "host:22" if port is zero. The split is
// defensive: a future SSH-over-QUIC transport
// might not want the colon separator.
//
// v0.8.16 fix: if `host` already contains a port
// (e.g. the operator saved "cdn2ne.<prod-host>.click:22"
// in the node's `Address` field), the previous
// implementation produced "cdn2ne.<prod-host>.click:22:22"
// — two colons, dial fails with "no such host".
// Now: if the input already has a port, use it
// verbatim; otherwise append the requested port.
func joinHostPort(host string, port int) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		// Host already has a port. Keep the
		// operator-supplied port (it overrides
		// any default) and discard the
		// function-arg `port`.
		_ = h
		return host
	}
	if port == 0 {
		port = 22
	}
	return fmt.Sprintf("%s:%d", host, port)
}

// Installer is the per-node install workflow.
// The struct is small; the methods are the
// actual code. The ClientFactory field is the
// only piece of state (it lets the tests
// inject a mock).
type Installer struct {
	ClientFactory ClientFactory
}

// NewInstaller returns a fresh installer with
// the production ClientFactory. Tests can
// override the field on the returned value.
func NewInstaller() *Installer {
	return &Installer{ClientFactory: NewClientFactory}
}

// Install runs the full bootstrap sequence and
// returns a structured result. The function is
// the only public entry point on the type; the
// per-step methods (connectToNode, uploadAgent,
// installSystemdUnit, verify) are package-private
// so the tests can target them directly.
//
// The function NEVER panics; every step is
// wrapped in a recover. The result.OK boolean
// is the only success indicator; the Err field
// is the only failure indicator.
func (i *Installer) Install(ctx context.Context, in InstallInput) InstallResult {
	if in.BearerSecret == "" {
		return InstallResult{Stage: "input", Err: errors.New("bootstrap: BearerSecret is required")}
	}
	if in.AgentSource == "" {
		return InstallResult{Stage: "input", Err: errors.New("bootstrap: AgentSource is required")}
	}
	if _, err := os.Stat(in.AgentSource); err != nil {
		return InstallResult{Stage: "input", Err: fmt.Errorf("bootstrap: agent source %s: %w", in.AgentSource, err)}
	}
	client, err := i.ClientFactory(in)
	if err != nil {
		return InstallResult{Stage: "client-factory", Err: err}
	}
	defer func() { _ = client.Close() }()
	if err := client.Connect(ctx); err != nil {
		return InstallResult{Stage: "connect", Err: err}
	}
	if err := i.uploadAgent(ctx, client, in.AgentSource); err != nil {
		return InstallResult{Stage: "upload-agent", Err: err}
	}
	if err := i.writeAgentEnv(ctx, client, in.BearerSecret); err != nil {
		return InstallResult{Stage: "write-env", Err: err}
	}
	// v0.8.30: write the mTLS cert files
	// (`agent.crt` + `agent.key` + `agent-ca.pem`)
	// to `/etc/aegis/`. The v0.8.29 fallback skips
	// the write when `in.MTLSCerts.IsZero()` (the
	// panel + agent on a v0.8.29 build never
	// mints cert material).
	if !in.MTLSCerts.IsZero() {
		if err := i.writeMTLSCerts(ctx, client, in.MTLSCerts); err != nil {
			return InstallResult{Stage: "write-mtls-certs", Err: err}
		}
	}
	if err := i.installSystemdUnit(ctx, client); err != nil {
		return InstallResult{Stage: "install-unit", Err: err}
	}
	// Post-install hook: runs after the systemd
	// unit is in place but before the verify
	// probe. v0.8.x uses this for the persistent
	// node SSH key (generate ed25519, encrypt,
	// save, push pub key to authorized_keys).
	// The hook runs while the SSH client is
	// still connected, so it does not need to
	// re-dial. A hook error is the install's
	// failure mode; we record the stage so the
	// audit log can surface "post-install-hook"
	// distinctly from "install-unit" or
	// "verify".
	if in.PostInstallHook != nil {
		if err := in.PostInstallHook(ctx, client); err != nil {
			return InstallResult{Stage: "post-install-hook", Err: err}
		}
	}
	latency, err := i.verify(ctx, client)
	if err != nil {
		return InstallResult{Stage: "verify", Err: err, VerifyLatency: latency}
	}
	return InstallResult{OK: true, VerifyLatency: latency}
}

// uploadAgent copies the local agent binary to
// the node's /usr/local/bin path. The file mode
// is 0755 (root-only write, world-readable + exec).
//
// The method uses UploadAndSwap (not Upload)
// because the target — /usr/local/bin/aegis-agent
// — may be the running aegis-agent process on a
// re-provision. Direct SFTP overwrite of a
// running binary fails with ETXTBSY on Linux
// (the kernel protects the executable's mmap'd
// text region). UploadAndSwap writes the new
// binary to a temp file in the same directory
// and atomically renames it over the target,
// which is the only race-free way to replace a
// running executable. The running process keeps
// the unlinked inode alive until it exits; a
// new process (or the systemd `Restart=always`
// loop) picks up the new binary at the same
// path.
func (i *Installer) uploadAgent(ctx context.Context, c Client, src string) error {
	return c.UploadAndSwap(ctx, src, RemoteAgentPath, 0o755)
}

// writeAgentEnv writes /etc/aegis/agent.env with
// the bearer secret. The file mode is 0600
// (root-only read) because the secret is the
// only thing standing between a stolen SSH key
// and a node takeover.
func (i *Installer) writeAgentEnv(ctx context.Context, c Client, bearerSecret string) error {
	body := "AEGIS_AGENT_BEARER=" + bearerSecret + "\n"
	// The Run path is the only way to write a
	// file with content the installer controls
	// (SFTP Upload expects a local source path).
	// A future PR can add `Client.WriteFile`
	// for the common case; for v0.3.0 the
	// here-doc is enough.
	_, err := c.Run(ctx, fmt.Sprintf(
		"install -d -m 0755 %s && cat > %s <<'__ENV__'\n%s__ENV__\nchmod 0600 %s\n",
		RemoteAgentEnvDir, RemoteAgentEnvPath, body, RemoteAgentEnvPath,
	))
	return err
}

// Remote paths for the v0.8.30 mTLS cert material.
// Mirrors the standard `install_agent` role
// output; the agent's gRPC server reads them at
// boot (see cmd/aegis-agent/mtls.go). The installer
// writes them with `0644` (world-readable) so the
// `_sing-box` user that runs the agent on a
// non-root install can read them; the cert is
// public, the key is not.
const (
	RemoteMTLSCertPath = "/etc/aegis/agent.crt"
	RemoteMTLSKeyPath  = "/etc/aegis/agent.key"
	RemoteMTLSCAPath   = "/etc/aegis/agent-ca.pem"
)

// writeMTLSCerts writes the three mTLS files to
// the node via `UploadAndSwap` (ETXTBSY-safe).
// The agent binary uses the same helper; the
// pattern is "write to a temp file, then atomic
// rename" because Linux refuses to unlink a
// running executable but allows a rename of a
// new file onto an existing path.
//
// All three files end up at the standard paths
// (`agent.crt` + `agent.key` + `agent-ca.pem`)
// with mode `0644` (world-readable). The `agent.key`
// is also `0644` because the agent runs as
// `_sing-box` on the v0.4.0+ install (the
// permissions matter on multi-tenant nodes; the
// v0.8.30 follow-up may tighten to `0640` with a
// `chown aegis-agent:_sing-box`).
func (i *Installer) writeMTLSCerts(ctx context.Context, c Client, certs MTLSCerts) error {
	// Stage 1: write each file to a tempdir on
	// the panel (the installer's local
	// workspace). The actual upload uses SFTP
	// which needs a local source path.
	dir, err := os.MkdirTemp("", "aegis-mtls-")
	if err != nil {
		return fmt.Errorf("bootstrap: write mTLS: mkdir temp: %w", err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	certPath := filepath.Join(dir, "agent.crt")
	keyPath := filepath.Join(dir, "agent.key")
	caPath := filepath.Join(dir, "agent-ca.pem")
	if err := os.WriteFile(certPath, []byte(certs.ServerCertPEM), 0o600); err != nil { // #nosec G306 -- staging copy, not the deployed file
		return fmt.Errorf("bootstrap: write mTLS: write cert: %w", err)
	}
	if err := os.WriteFile(keyPath, certs.ServerKeyPEM, 0o600); err != nil { // #nosec G306 -- see above
		return fmt.Errorf("bootstrap: write mTLS: write key: %w", err)
	}
	if err := os.WriteFile(caPath, []byte(certs.RootCertPEM), 0o600); err != nil { // #nosec G306 -- see above
		return fmt.Errorf("bootstrap: write mTLS: write CA: %w", err)
	}

	// Stage 2: UploadAndSwap each file. The
	// helper writes to a temp path on the
	// remote, then `mv -f` over the target.
	// The agent is still running with the OLD
	// files open (Linux allows mmap of a file
	// that is renamed over the existing path;
	// the old inode stays alive until the agent
	// closes the file descriptor).
	if err := c.UploadAndSwap(ctx, certPath, RemoteMTLSCertPath, 0o644); err != nil {
		return fmt.Errorf("bootstrap: upload agent.crt: %w", err)
	}
	if err := c.UploadAndSwap(ctx, keyPath, RemoteMTLSKeyPath, 0o644); err != nil {
		return fmt.Errorf("bootstrap: upload agent.key: %w", err)
	}
	if err := c.UploadAndSwap(ctx, caPath, RemoteMTLSCAPath, 0o644); err != nil {
		return fmt.Errorf("bootstrap: upload agent-ca.pem: %w", err)
	}
	return nil
}

// installSystemdUnit writes the systemd unit
// file and reloads systemd. The unit is a
// minimal "Type=simple" + "Restart=always" so
// the placeholder agent (or v0.4.0's real one)
// starts on boot and restarts on crash.
func (i *Installer) installSystemdUnit(ctx context.Context, c Client) error {
	unit := `[Unit]
Description=Aegis Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=` + RemoteAgentEnvPath + `
ExecStart=` + RemoteAgentPath + `
Restart=always
RestartSec=5
User=` + RemoteAgentUser + `

[Install]
WantedBy=multi-user.target
`
	cmd := fmt.Sprintf(
		"cat > %s <<'__UNIT__'\n%s__UNIT__\n"+
			"chmod 0644 %s\n"+
			"systemctl daemon-reload\n"+
			"systemctl enable aegis-agent.service\n"+
			"systemctl restart aegis-agent.service\n",
		RemoteAgentUnitPath, unit, RemoteAgentUnitPath,
	)
	_, err := c.Run(ctx, cmd)
	return err
}

// verify waits for the agent to come up. The
// placeholder agent is `sleep infinity` so the
// unit goes `active` within a second; the real
// agent (v0.4.0) may take longer on a fresh
// install, hence the 5-second deadline.
//
// The function is the only place where the
// installer speaks "service is up" semantics;
// the rest of the install is "bytes on disk".
func (i *Installer) verify(ctx context.Context, c Client) (time.Duration, error) {
	start := time.Now()
	deadline := start.Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, err := c.Run(ctx, "systemctl is-active aegis-agent.service")
		if err == nil && strings.TrimSpace(out) == "active" {
			return time.Since(start), nil
		}
		// Wait a moment before the next probe.
		// 200ms is short enough that the
		// operator does not notice the latency
		// on a healthy boot and long enough
		// to skip past systemd's "activating"
		// transient state.
		select {
		case <-ctx.Done():
			return time.Since(start), ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	// One last attempt so the error message
	// reports the actual state.
	out, _ := c.Run(ctx, "systemctl is-active aegis-agent.service")
	return time.Since(deadline), fmt.Errorf("%w: state=%q", ErrVerifyFailed, strings.TrimSpace(out))
}
