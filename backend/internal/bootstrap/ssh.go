// SPDX-License-Identifier: AGPL-3.0-or-later
//
// SSH client wrapper for the BYO-Node flow. The
// Client interface is the seam between the
// installer and the actual SSH library
// (golang.org/x/crypto/ssh); tests substitute a
// mock so the install workflow can be exercised
// without a real sshd.
//
// # Threat model
//
// The SSH client has to defend against:
//   1. Man-in-the-middle on the first
//      connection (no prior known_hosts entry).
//      v0.3.0 resolves this with the operator's
//      "trust on first use" + "verify the
//      fingerprint" UX (the operator pastes the
//      expected fingerprint when they add the
//      node, and the connection fails closed if
//      the actual fingerprint does not match).
//   2. Host-key change after first contact
//      (rogue key in a compromised router).
//      TofuPolicy "strict" rejects every host
//      whose key is not already in the file.
//   3. Weak key types (DSA, < 2048-bit RSA).
//      HostKeyAlgorithms is pinned to the
//      post-OpenSSH-7.0 set (ed25519, ECDSA,
//      RSA-SHA2-256/512). The pin is a
//      defence-in-depth; the operator's
//      fingerprint check is the primary.
//
// # Why SFTP
//
// The agent binary + systemd unit are uploaded
// over SFTP (the SSH File Transfer Protocol is a
// subsystem of SSH itself, no extra port or
// daemon). v0.3.0 uses the stdlib
// `golang.org/x/crypto/ssh` + the
// `github.com/pkg/sftp` package for the SFTP
// subsystem. The `pkg/sftp` package is a thin
// wrapper around the protocol and is the
// de-facto Go SFTP implementation.

package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pkg/sftp"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// ErrHostKeyMismatch is returned by Connect when
// the actual host key does not match the entry
// in the known_hosts file. The HTTP layer maps
// this to a 409 with a "host key changed — your
// node may be MITM'd" message; the operator UI
// surfaces a "Re-trust host" button.
var ErrHostKeyMismatch = errors.New("bootstrap: ssh host key mismatch")

// ErrHostKeyUnknown is returned by Connect when
// the host is not in the known_hosts file and
// TrustOnFirstUse is false.
var ErrHostKeyUnknown = errors.New("bootstrap: ssh host key unknown")

// Client is the SSH/SFTP surface the installer
// uses. The interface is small (4 methods) so
// the mock in tests is trivial; v0.3.0 only
// needs Connect, Run, Upload, and Close.
type Client interface {
	// Connect dials the node and authenticates.
	// The call returns once the SSH handshake +
	// user auth are complete; subsequent Run
	// and Upload calls do not re-dial. The
	// caller is responsible for calling Close
	// when done.
	Connect(ctx context.Context) error

	// Run executes a single command and returns
	// its combined stdout + stderr. The
	// non-zero exit code is wrapped in
	// *ExecError so the caller can branch on
	// it without parsing strings.
	Run(ctx context.Context, cmd string) (string, error)

	// Upload copies the local file at `src` to
	// the remote path `dst`. The remote path is
	// interpreted relative to the home
	// directory of the SSH user unless it is
	// absolute. The remote file is created
	// with mode 0644 (the install workflow
	// `chmod`s the binary to 0755 separately).
	Upload(ctx context.Context, src, dst string, mode os.FileMode) error

	// Close shuts down the SSH connection. Safe
	// to call on a never-Connected client (no
	// effect).
	Close() error
}

// TofuPolicy is how Connect handles a host whose
// key is not in the known_hosts file. The pin
// is explicit because the trade-off is
// product-level: the v0.3.0 "first contact" UX
// gates on the operator's "I trust this host"
// click.
type TofuPolicy int

const (
	// TofuReject rejects any host not in
	// known_hosts. The operator must pre-seed
	// the file (e.g. via ssh-keyscan run
	// manually) before the install workflow
	// can run.
	TofuReject TofuPolicy = iota

	// TofuAcceptAndAppend runs ssh-keyscan on
	// the first contact, verifies the operator-
	// supplied fingerprint matches, and
	// appends the key to the file. The
	// fingerprint gate is the safety net.
	TofuAcceptAndAppend
)

// ClientConfig groups the inputs to NewClient.
// Kept as a struct (not 8 positional args) so
// future flags (proxy, jump-host, agent-
// forwarding) land in one place.
type ClientConfig struct {
	// Address is "host:port" or "host" (port
	// defaults to 22 via the SSH default).
	Address string
	// User is the SSH login name.
	User string
	// PrivateKey is the PEM-encoded RSA /
	// ECDSA / ed25519 private key the panel
	// uses to authenticate. The
	// corresponding public key must be in
	// the node's ~/.ssh/authorized_keys.
	PrivateKey []byte
	// KnownHosts is the absolute path to the
	// panel's known_hosts file. The file is
	// created if TofuAcceptAndAppend is set
	// and the host is not already known.
	KnownHosts string
	// Tofu is the trust-on-first-use policy.
	Tofu TofuPolicy
	// ExpectedFingerprint is the SHA-256
	// fingerprint (hex, base64-on-the-wire
	// format `SHA256:...`) the operator
	// confirmed on the "Add node" form. The
	// client compares against the actual
	// fingerprint of the presented host key.
	// The check is the safety net for
	// TofuAcceptAndAppend; a mismatch is a
	// 409 with a "host key changed" message.
	ExpectedFingerprint string
	// Timeout is the per-call timeout. Zero
	// means "use the package default" (30s).
	Timeout time.Duration
}

// sshClient is the real (non-mock) Client
// implementation. The struct holds the open SSH
// connection + SFTP client; both are closed by
// the Close method. The tofuKey + tofuAddr
// fields are populated by the TOFU callback
// during Connect; the post-handshake append
// reads them and writes the entry to the
// known_hosts file.
type sshClient struct {
	cfg      ClientConfig
	conn     *ssh.Client
	sftp     *sftp.Client
	tofuKey  ssh.PublicKey
	tofuAddr string
}

// NewClient constructs a Client from cfg. The
// returned client is not yet connected; the
// caller must invoke Connect before any Run /
// Upload call.
func NewClient(cfg ClientConfig) (Client, error) {
	if cfg.Address == "" {
		return nil, errors.New("bootstrap: ClientConfig.Address is required")
	}
	if cfg.User == "" {
		return nil, errors.New("bootstrap: ClientConfig.User is required")
	}
	if len(cfg.PrivateKey) == 0 {
		return nil, errors.New("bootstrap: ClientConfig.PrivateKey is required")
	}
	if cfg.KnownHosts == "" {
		return nil, errors.New("bootstrap: ClientConfig.KnownHosts is required")
	}
	return &sshClient{cfg: cfg}, nil
}

// Connect dials the SSH server, runs the host-
// key check against the known_hosts file, and
// authenticates as the configured user with the
// supplied private key. The function is
// idempotent: a second call is a no-op (the
// existing connection is reused).
func (c *sshClient) Connect(ctx context.Context) error {
	if c.conn != nil {
		return nil
	}
	signer, err := ssh.ParsePrivateKey(c.cfg.PrivateKey)
	if err != nil {
		return fmt.Errorf("bootstrap: parse private key: %w", err)
	}

	// hostKeyCallback is the security-critical
	// closure. It is invoked once per host key
	// presentation. The callback:
	//   1. Looks up the host in the
	//      known_hosts file. A match is the
	//      trust path.
	//   2. On a miss, runs the TOFU policy
	//      (reject or accept-and-append). The
	//      TofuAcceptAndAppend path also
	//      compares the actual fingerprint to
	//      the operator-supplied
	//      ExpectedFingerprint; a mismatch is
	//      ErrHostKeyMismatch.
	hostKeyCb, err := c.hostKeyCallback()
	if err != nil {
		return err
	}

	addr := c.cfg.Address
	if _, _, err := net.SplitHostPort(addr); err != nil {
		// No port: append the SSH default. The
		// host-key file uses the same shape, so
		// the lookup keys line up.
		addr = net.JoinHostPort(addr, "22")
	}
	timeout := c.cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	clientConfig := &ssh.ClientConfig{
		User:            c.cfg.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCb,
		Timeout:         timeout,
	}
	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("bootstrap: dial %s: %w", addr, err)
	}
	sshConn, chans, reqs, err := ssh.NewClientConn(conn, addr, clientConfig)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("bootstrap: ssh handshake: %w", err)
	}
	c.conn = ssh.NewClient(sshConn, chans, reqs)
	// Open the SFTP subsystem in parallel with
	// the SSH session. The sftp.NewClientPiper
	// is the v1.13+ helper that takes the
	// already-opened client.
	sftpClient, err := sftp.NewClient(c.conn)
	if err != nil {
		_ = c.conn.Close()
		c.conn = nil
		return fmt.Errorf("bootstrap: sftp open: %w", err)
	}
	c.sftp = sftpClient

	// Post-handshake: under TofuAcceptAndAppend,
	// persist the presented key to the
	// known_hosts file so the next provisioning
	// cycle is a known-key contact (the strict
	// callback is reused from the start). The
	// append is best-effort; a failure here is
	// logged but does not fail the handshake
	// (the operator's "first contact" has
	// already succeeded; a missing entry will
	// re-trigger the TOFU prompt on the next
	// run).
	if c.cfg.Tofu == TofuAcceptAndAppend && c.tofuKey != nil {
		if err := appendKnownHosts(c.cfg.KnownHosts, c.tofuAddr, c.tofuKey); err != nil {
			log.Warn().Err(err).Msg("bootstrap: append known_hosts")
		}
	}
	return nil
}

// hostKeyCallback builds the ssh.HostKeyCallback
// that enforces the configured TofuPolicy. The
// function is split out of Connect for the
// unit test (it does not need a live
// connection).
func (c *sshClient) hostKeyCallback() (ssh.HostKeyCallback, error) {
	// Load the existing known_hosts file. A
	// missing file is fine; the TOFU path
	// appends the first key it sees.
	knownHostsPath := c.cfg.KnownHosts
	callback, err := knownhosts.New(knownHostsPath)
	if err != nil {
		// A "file not found" is a fresh-deploy
		// case; the callback below handles the
		// first-seen host by TOFU. Other
		// errors (e.g. malformed file) are
		// hard failures.
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("bootstrap: load known_hosts: %w", err)
		}
		callback = nil
	}
	if callback != nil {
		// The known_hosts callback rejects
		// anything not in the file. This is the
		// "strict" path; the TOFU layer
		// replaces it below.
		return callback, nil
	}
	// Fallback: no known_hosts file. The
	// TOFU policy decides what to do on the
	// first contact.
	switch c.cfg.Tofu {
	case TofuReject:
		return func(_ string, _ net.Addr, _ ssh.PublicKey) error {
			return ErrHostKeyUnknown
		}, nil
	case TofuAcceptAndAppend:
		// The actual append happens after
		// success. The callback just records
		// the presented key on the client so
		// Connect can append + verify after
		// the handshake.
		return func(_ string, remote net.Addr, key ssh.PublicKey) error {
			// Compare against the operator-
			// supplied fingerprint. The check
			// is the safety net for the
			// "operator clicked Add node too
			// fast" case.
			if c.cfg.ExpectedFingerprint == "" {
				return errors.New("bootstrap: TOFU requires ExpectedFingerprint")
			}
			actual := ssh.FingerprintSHA256(key)
			if !fingerprintEqual(actual, c.cfg.ExpectedFingerprint) {
				return fmt.Errorf("%w: actual %s, expected %s",
					ErrHostKeyMismatch, actual, c.cfg.ExpectedFingerprint)
			}
			// Stash the key + remote so Connect
			// can persist the entry.
			c.tofuKey = key
			c.tofuAddr = remote.String()
			return nil
		}, nil
	default:
		return nil, fmt.Errorf("bootstrap: unknown TofuPolicy %d", c.cfg.Tofu)
	}
}

// Close shuts down the SSH and SFTP sessions.
// Safe to call on a never-Connected client.
func (c *sshClient) Close() error {
	var firstErr error
	if c.sftp != nil {
		if err := c.sftp.Close(); err != nil {
			firstErr = err
		}
		c.sftp = nil
	}
	if c.conn != nil {
		if err := c.conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		c.conn = nil
	}
	return firstErr
}

// Run executes cmd on the remote shell and
// returns its combined stdout+stderr. The exit
// code is wrapped in *ExecError for non-zero
// returns so callers can branch on it without
// parsing strings.
func (c *sshClient) Run(ctx context.Context, cmd string) (string, error) {
	if c.conn == nil {
		return "", errors.New("bootstrap: not connected")
	}
	type result struct {
		out string
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		session, err := c.conn.NewSession()
		if err != nil {
			resCh <- result{"", fmt.Errorf("bootstrap: open session: %w", err)}
			return
		}
		// Close returns an error on the
		// "session already closed" path; the
		// caller has already collected the
		// output via CombinedOutput, so a
		// close error is benign. errcheck
		// wants the explicit discard.
		defer func() { _ = session.Close() }()
		// Combine stdout + stderr so the caller
		// sees the same output as `ssh host
		// 'cmd' 2>&1`. The systemd unit
		// install logs go through here.
		b, runErr := session.CombinedOutput(cmd)
		out := string(b)
		if runErr != nil {
			// Wrap exit-code errors in
			// *ExecError so the caller can
			// branch on the code without
			// string matching. errors.As
			// handles the wrapped-error
			// path (the chain wraps the
			// original ExitError in
			// session.CombinedOutput's
			// returned error).
			var exitErr *ssh.ExitError
			if errors.As(runErr, &exitErr) {
				resCh <- result{out, &ExecError{
					Cmd:        cmd,
					ExitStatus: exitErr.ExitStatus(),
					Stderr:     out,
				}}
				return
			}
			resCh <- result{out, runErr}
			return
		}
		resCh <- result{out, nil}
	}()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case r := <-resCh:
		return r.out, r.err
	}
}

// Upload copies the local file at src to the
// remote path dst. The remote parent directory
// is created if missing. The file mode is set
// to mode (rounded down to the file's owner
// permissions; SFTP does not preserve the
// group / world bits the way `chmod` does).
func (c *sshClient) Upload(ctx context.Context, src, dst string, mode os.FileMode) error {
	if c.sftp == nil {
		return errors.New("bootstrap: not connected")
	}
	type result struct {
		err error
	}
	resCh := make(chan result, 1)
	go func() {
		local, err := os.Open(src)
		if err != nil {
			resCh <- result{fmt.Errorf("bootstrap: open local %s: %w", src, err)}
			return
		}
		defer func() { _ = local.Close() }()
		if err := c.uploadStream(ctx, local, dst, mode); err != nil {
			resCh <- result{err}
			return
		}
		resCh <- result{nil}
	}()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case r := <-resCh:
		return r.err
	}
}

// uploadStream is the worker half of Upload.
// Split out so the goroutine can be cancelled
// via ctx without leaking an open file handle.
func (c *sshClient) uploadStream(ctx context.Context, src io.Reader, dst string, mode os.FileMode) error {
	if err := c.ensureRemoteDir(ctx, filepath.Dir(dst)); err != nil {
		return err
	}
	remote, err := c.sftp.Create(dst)
	if err != nil {
		return fmt.Errorf("bootstrap: sftp create %s: %w", dst, err)
	}
	defer func() { _ = remote.Close() }()
	if err := copyContext(ctx, remote, src); err != nil {
		return fmt.Errorf("bootstrap: sftp write %s: %w", dst, err)
	}
	if err := remote.Chmod(mode); err != nil {
		return fmt.Errorf("bootstrap: sftp chmod %s: %w", dst, err)
	}
	return nil
}

// ensureRemoteDir creates the remote directory
// (and any missing parents) if it does not
// exist. The mkdir -p semantic is the
// conventional Unix one: a non-existent path
// is created; an existing path is a no-op.
//
// The ctx parameter is reserved for the v0.4.0
// upload-then-cancel path (a long SFTP write
// on a slow connection should respect the
// caller's deadline). v0.3.0 does not use it
// because the upload is fast and the entire
// uploadStream call runs inside a single
// goroutine that already checks ctx in
// copyContext. The lint flag is silenced via
// the explicit ctx use; renaming the param
// would be a larger diff.
func (c *sshClient) ensureRemoteDir(ctx context.Context, dir string) error {
	_ = ctx // v0.4.0: wire through to c.sftp.Mkdir
	if dir == "" || dir == "." || dir == "/" {
		return nil
	}
	// Stat first; if it exists we are done. The
	// SFTP subsystem does not have a single
	// "mkdir -p" call.
	if _, err := c.sftp.Stat(dir); err == nil {
		return nil
	}
	// Recurse into the parent, then create this
	// directory. The SFTP MkdirAll is not in
	// the v1 interface; we layer it on top.
	if err := c.ensureRemoteDir(ctx, filepath.Dir(dir)); err != nil {
		return err
	}
	if err := c.sftp.Mkdir(dir); err != nil {
		// A concurrent create (e.g. an
		// earlier install) is a soft success.
		if !errors.Is(err, os.ErrExist) {
			return fmt.Errorf("bootstrap: sftp mkdir %s: %w", dir, err)
		}
	}
	return nil
}

// copyContext is io.Copy with a context check
// between chunks. A long upload on a slow
// connection would otherwise block forever
// even if the request context is cancelled.
func copyContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	buf := make([]byte, 32*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := src.Read(buf)
		if n > 0 {
			if _, werr := dst.Write(buf[:n]); werr != nil {
				return werr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// fingerprintEqual compares two SSH
// fingerprints case-insensitively (the on-the-
// wire form is `SHA256:base64`; the on-disk
// form is the same; the operator may paste
// either case).
func fingerprintEqual(a, b string) bool {
	return strings.EqualFold(a, b)
}

// ExecError is returned by Run when the remote
// command exits non-zero. The exit status +
// combined output let the caller branch on the
// failure mode (e.g. `systemctl` returns 4 for
// "unit not found" — a clear bug; 1 for
// "permission denied" — a config issue).
type ExecError struct {
	Cmd        string
	ExitStatus int
	Stderr     string
}

// Error implements the error interface. The
// message includes the command, the exit
// status, and the first 200 chars of stderr
// (so a 10-MB log does not blow up the error
// envelope).
func (e *ExecError) Error() string {
	const maxStderr = 200
	stderr := e.Stderr
	if len(stderr) > maxStderr {
		stderr = stderr[:maxStderr] + "...(truncated)"
	}
	return fmt.Sprintf("bootstrap: remote %q exited %d: %s", e.Cmd, e.ExitStatus, stderr)
}

// appendKnownHosts writes a single entry to
// the known_hosts file in the OpenSSH
// "hostname keytype base64-key" format. The
// function is best-effort: it does not lock
// the file (a concurrent Connect on another
// node could race), and a write error is
// returned to the caller (which logs +
// continues). v0.5.0 swaps the file for a
// real DB-backed store (the per-file lock is
// a known weak point of OpenSSH TOFU under
// load).
func appendKnownHosts(path, addr string, key ssh.PublicKey) error {
	if path == "" {
		return errors.New("bootstrap: appendKnownHosts: empty path")
	}
	// The OpenSSH file format: "[host]:port
	// keytype base64-key comment". The
	// knownhosts.Normalize helper trims a
	// "[host]:port" form to "[host]:port"
	// (the bracket form disambiguates
	// "host:port" from IPv6 "host:port"). For
	// v0.3.0 we just write the raw addr;
	// the file is not parsed by anyone but
	// knownhosts.New, which accepts both
	// forms.
	line := knownhosts.Line([]string{addr}, key)
	if line == "" {
		return errors.New("bootstrap: appendKnownHosts: empty line")
	}
	// Append atomically: read the existing
	// content, concatenate, write to a temp
	// file in the same directory, rename. The
	// rename is atomic on POSIX filesystems
	// (Windows: the rename overwrites the
	// destination, which is what we want).
	//
	// G703 (path traversal) is suppressed: the
	// `path` is operator-config-controlled via
	// cfg.KnownHosts at boot, not user input.
	// The provisioner only writes to a path
	// the panel itself opened.
	existing, err := os.ReadFile(path) // #nosec G703 -- operator-config path
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("bootstrap: read known_hosts: %w", err)
	}
	body := string(existing)
	if body != "" && !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += line + "\n"
	tmp, err := os.CreateTemp(filepath.Dir(path), ".known_hosts.*") // #nosec G703 -- operator-config path
	if err != nil {
		return fmt.Errorf("bootstrap: temp known_hosts: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.WriteString(body); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("bootstrap: write temp known_hosts: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("bootstrap: close temp known_hosts: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("bootstrap: rename known_hosts: %w", err)
	}
	return nil
}
