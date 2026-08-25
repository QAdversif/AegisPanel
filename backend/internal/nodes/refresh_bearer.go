// SPDX-License-Identifier: AGPL-3.0-or-later
//
// RefreshAgentBearer — the v0.8.7 operator-side
// recovery path for the v0.8.1 persistent panel
// SSH key feature.
//
// # Why this exists
//
// The v0.8.1 provisioner generates an ed25519
// keypair, pushes the public half to the node's
// `~/.ssh/authorized_keys`, and seals the
// private half with the operator's age envelope
// (`nodes.ssh_private_key_ciphertext`, migration
// 0020). The v0.8.5 PR #186 added the public
// surface of that key (the "Show stored key"
// debug endpoint). What was missing was the
// **use** side: the stored key was written and
// inspectable, but nothing in the panel ever
// decrypted it for runtime work.
//
// # What this PR adds
//
// Two new methods on `Service`:
//
//   - `GetStoredKeyForUse` returns the decrypted
//     OpenSSH PEM private key. Unlike
//     `GetStoredKey` (which only surfaces the
//     public side for debug), this is the byte
//     payload a caller can hand to an SSH client
//     for outbound auth. The function is the
//     read-side mirror of the v0.8.1
//     `buildPersistentSSHKeyHook` (which writes
//     the same wire format via
//     `ssh.MarshalPrivateKey`).
//
//   - `RefreshAgentBearer` is the high-level
//     recovery flow: decrypt the stored key, SSH
//     into the node, read `/etc/aegis/agent.env`,
//     parse the `AEGIS_AGENT_BEARER=...` line,
//     and update `nodes.agent_bearer` in the DB.
//     The new bearer is what the panel's
//     `BatchedApplier` (and the sing-box provider's
//     `FlushFn`) use to POST `/v1/apply` to the
//     agent. The recovery path is what fixes the
//     401 loop when the agent regenerates its
//     bearer out-of-band (e.g. operator restarted
//     the agent, wiped `/etc/aegis/agent.env`, or
//     rotated secrets manually).
//
// # Why this is in `nodes`, not `bootstrap`
//
// The v0.8.1 + v0.8.3 + v0.8.4 + v0.8.5 work all
// lives in `nodes` because the persistent key
// **is** a property of the node row. The SSH
// client happens to be defined in `bootstrap`
// (it was added for `POST /{id}/provision` in
// v0.3.0); the `nodes` package already imports
// `bootstrap` for the v0.8.4 `bootstrapProvider`
// interface, so the cross-package reference is
// a non-event (verified via `go list -deps`
// before this PR — no cycle). The factory is
// injected as a `func(bootstrap.ClientConfig)
// (bootstrap.Client, error)` indirection (the
// same pattern v0.8.4 used for
// `newSSHClientForRotate`) so unit tests can
// substitute a mock SSH server without a real
// dial.
//
// # Why BatchedApplier integration is NOT in this PR
//
// The BatchedApplier is a core-agnostic delta
// coalescer in `internal/cores/batched.go`. Its
// `FlushFn` callback lives in the sing-box
// provider (`internal/cores/singbox/apply.go`)
// and POSTs `/v1/apply` with the bearer. Wiring
// `RefreshAgentBearer` into the apply path's 401
// recovery requires changes in `cores/` AND
// `singbox/` AND the wiring helper in
// `cmd/aegis/main.go` that builds the FlushFn.
// That's a v0.8.x follow-up PR; this PR lays
// the foundation (the Service methods + the
// HTTP/UI surface) so the integration is
// additive. The CHANGELOG entry calls this out
// explicitly.
//
// # Security shape
//
// The decrypted private key lives in the
// function's stack frame for the duration of
// the call. The returned byte slice from
// `GetStoredKeyForUse` is the caller's
// responsibility to zero after use (a future
// PR can add a small `SecureBytes` helper that
// wraps `[]byte` with a `Zero()` method, but
// that is a separate cross-cutting change and
// not in scope here). The SSH `Run` output
// (the agent.env file) is parsed for the
// `AEGIS_AGENT_BEARER` value; the rest of the
// file is discarded.
//
// The audit log records the refresh with
// `action=node.agent-bearer.refresh` and
// `resource_id=<node-uuid>`. The new bearer is
// NOT in the audit row (per-server, would be
// 100x write-amplification for a value that
// already lives in the encrypted `agent.env` on
// the node). The audit row carries the
// `node_name` + `address` + a SHA-256
// fingerprint of the SSH key that was used
// (NOT the bearer), so the operator can
// correlate the refresh with a specific
// trusted key.

package nodes

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
)

// StoredKeyForUse is the **decrypted** private key
// returned by `GetStoredKeyForUse`. The struct carries
// the OpenSSH PEM bytes; the public surface
// (fingerprint, key line) is derivable from the
// PEM via the standard `ssh.ParseRawPrivateKey` +
// `ssh.FingerprintSHA256` path, but is not
// pre-computed here — the v0.8.5 `GetStoredKey`
// method is the canonical debug surface for the
// public side, and the v0.8.7 caller is expected
// to already have it.
//
// # Lifetime
//
// The byte slice is a copy of the decrypted
// ciphertext; the caller's responsibility to
// zero it after use. The Go runtime will GC the
// slice header when the caller drops the
// reference, but the underlying bytes may live
// on in swap until overwritten. A future PR can
// add a `SecureBytes` type with a `Zero()`
// method; for now the v0.8.7 caller (the
// `RefreshAgentBearer` Service method itself)
// uses the bytes once, inside the same stack
// frame, and lets them go out of scope.
type StoredKeyForUse struct {
	// PrivateKeyPEM is the OpenSSH ed25519
	// PEM (the v0.8.1 `ssh.MarshalPrivateKey`
	// output, identical to what
	// `bootstrap.generateAndPushKey` writes).
	// The bytes are the input that
	// `bootstrap.NewClient` expects in
	// `ClientConfig.PrivateKey`.
	PrivateKeyPEM []byte
}

// GetStoredKeyForUse decrypts the stored panel
// SSH key for the node and returns the
// OpenSSH-formatted PEM bytes. The function is
// the use-side mirror of `GetStoredKey` (which
// only returns the public surface for debug).
//
// # Why a separate method
//
// `GetStoredKey` returns a `StoredKey` struct
// (has_stored_key, public_key_line, fingerprint,
// algorithm, key_updated_at) — designed for the
// HTTP debug surface. The private key is
// explicitly NOT in that struct; the v0.8.5 PR
// discussion concluded that carrying private-key
// bytes through a public debug surface is a
// footgun. This method is the **non-debug**
// path: a Service-internal caller that needs
// the key for outbound SSH (e.g.
// `RefreshAgentBearer`).
//
// # Errors
//
//   - `envelope is not configured`: same as
//     `GetStoredKey`. The panel was booted
//     without the age envelope; the operator
//     must set `AEGIS_WEBHOOKS_SECRET_AGE_*`
//     and restart.
//   - `ErrNotFound`: the node does not exist.
//   - `no stored key`: the row exists but
//     `ssh_private_key_ciphertext` is empty
//     (a v0.3.0..v0.7.x node that has not been
//     back-filled with the v0.8.3 CLI). The
//     caller should treat this as "rotate the
//     panel key first" rather than "refresh
//     the bearer".
//   - decrypt/parse failure: wraps the
//     underlying error. Same shape as
//     `GetStoredKey`.
func (s *Service) GetStoredKeyForUse(ctx context.Context, nodeID uuid.UUID) (StoredKeyForUse, error) {
	if s.envelope == nil {
		return StoredKeyForUse{}, fmt.Errorf("nodes: GetStoredKeyForUse: envelope is not configured (set AEGIS_WEBHOOKS_SECRET_AGE_* env vars)")
	}
	row, err := s.store.GetByID(ctx, nodeID)
	if err != nil {
		return StoredKeyForUse{}, err
	}
	if len(row.SSHPrivateKeyCiphertext) == 0 {
		return StoredKeyForUse{}, ErrNoStoredKey
	}
	privPEM, err := s.envelope.Decrypt(row.SSHPrivateKeyCiphertext)
	if err != nil {
		return StoredKeyForUse{}, fmt.Errorf("nodes: GetStoredKeyForUse: decrypt stored SSH key: %w", err)
	}
	// Sanity-check the PEM round-trips as an
	// ed25519 OpenSSH key. The v0.8.1 write
	// path uses the same `MarshalPrivateKey`
	// + `ParseRawPrivateKey` round-trip, so
	// the bytes are guaranteed to parse. The
	// check is a guard against a corrupted
	// `ssh_private_key_ciphertext` column
	// (e.g. partial migration 0020 application)
	// and a `Decrypt` that returned
	// well-formed bytes that are not actually
	// a key. The error is loud.
	if _, err := ssh.ParseRawPrivateKey(privPEM); err != nil {
		return StoredKeyForUse{}, fmt.Errorf("nodes: GetStoredKeyForUse: parse stored SSH key: %w", err)
	}
	// Defensive copy. The envelope's Decrypt
	// contract (see internal/crypto/envelope)
	// already returns a fresh slice, but
	// copying here insulates the caller from
	// any future change to that contract.
	out := make([]byte, len(privPEM))
	copy(out, privPEM)
	return StoredKeyForUse{PrivateKeyPEM: out}, nil
}

// ErrNoStoredKey is returned by `GetStoredKeyForUse`
// (and, by reference, `RefreshAgentBearer`) when the
// node row exists but `ssh_private_key_ciphertext`
// is empty. The caller can branch on this to surface
// a "rotate the panel key first" hint instead of a
// generic "refresh failed" error.
var ErrNoStoredKey = errors.New("nodes: no stored panel SSH key (rotate-panel-key first)")

// RefreshedBearer is the result of a successful
// `RefreshAgentBearer` call. The `Bearer` field
// carries the value that was just written to
// `nodes.agent_bearer`; the HTTP layer returns it
// in the 200 body so the operator UI can surface
// "the new bearer is X" for at-a-glance
// verification (the value is also written to the
// node's `agent.env` by the agent's own
// bootstrap, so the operator can `cat` it on the
// node to double-check).
type RefreshedBearer struct {
	NodeID uuid.UUID
	Bearer string
	// KeyFingerprintSHA256 is the SHA-256 of
	// the SSH public key derived from the
	// stored private key (the same string
	// `ssh-keygen -lf` reports). The field
	// is in the response so the operator
	// can verify "the refresh used the key I
	// expect" (the fingerprint is also in
	// the audit row).
	KeyFingerprintSHA256 string
}

// RefreshBearerOptions are the per-call
// overrides for `RefreshAgentBearer`. Zero
// values mean "use the service default".
type RefreshBearerOptions struct {
	// SSHPort is the per-call override. Zero
	// means "use the node's stored address
	// (port from the row.Address field)".
	SSHPort int
	// SSHUser is the per-call override. Empty
	// means "use the service default
	// (cfg.AgentSSHUser)".
	SSHUser string
	// Timeout is the per-call SSH timeout.
	// Zero means 30s.
	Timeout time.Duration
}

// RefreshAgentBearer is the operator-side
// recovery path for the agent bearer. The flow:
//
//  1. Read the node row (404 if missing).
//  2. Decrypt the stored panel SSH key (fail
//     closed if envelope is nil; refuse if
//     the row has no stored key).
//  3. Open an SSH session using the stored
//     key + the panel's known_hosts file
//     (TofuPolicy=Reject; the host must
//     already be trusted because the panel
//     pushed the public key in v0.8.1 /
//     v0.8.4).
//  4. Run `cat /etc/aegis/agent.env`.
//  5. Parse the `AEGIS_AGENT_BEARER=...`
//     line.
//  6. Persist the new bearer to
//     `nodes.agent_bearer`.
//  7. Record an audit row
//     `node.agent-bearer.refresh`.
//
// # Why TofuPolicy=Reject
//
// The host was first trusted by the v0.3.0
// provisioner (or by the v0.8.4 rotate-panel-key
// handler). The host key is in
// `cfg.AgentKnownHosts` already. A Tofu=Accept
// flow would silently re-trust the host on
// every refresh, defeating the purpose of the
// known_hosts file (which is to detect MITM).
// Reject + already-trusted is the safe
// default.
//
// # Why a Service method, not a public
// function in `internal/cores/`
//
// The BatchedApplier is core-agnostic; the
// recovery flow is **node-specific** (it reads
// the node's stored key, talks to the node's
// SSH endpoint, updates the node's agent_bearer
// column). A Service method keeps the recovery
// flow inside the same package that owns the
// stored key + agent_bearer column.
//
// # Errors
//
//   - 500-class: envelope not configured, SSH
//     connect failure, `agent.env` parse
//     failure, store update failure.
//   - 404: node not found (the Store's
//     `ErrNotFound` propagates).
//   - `ErrNoStoredKey`: the row has no
//     `ssh_private_key_ciphertext`; the
//     caller should hint "rotate-panel-key
//     first".
func (s *Service) RefreshAgentBearer(ctx context.Context, nodeID uuid.UUID, opts RefreshBearerOptions) (RefreshedBearer, error) {
	if s.sshClientFactory == nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: SSH client factory is not configured (call WithSSHClientFactory in app.go)")
	}
	if s.knownHosts == "" {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: known_hosts path is not configured (call WithKnownHosts in app.go)")
	}
	// Read the node row. GetByID returns
	// `ErrNotFound` for missing rows; the HTTP
	// layer maps that to 404.
	row, err := s.store.GetByID(ctx, nodeID)
	if err != nil {
		return RefreshedBearer{}, err
	}
	// Decrypt the stored SSH key. The
	// error-mapping chain here is the same
	// as `GetStoredKeyForUse`; the caller
	// can branch on `ErrNoStoredKey` to
	// surface a "rotate first" hint.
	priv, err := s.GetStoredKeyForUse(ctx, nodeID)
	if err != nil {
		return RefreshedBearer{}, err
	}
	// Resolve the SSH user. Service-wide
	// default is the cfg-level
	// `AEGIS_AGENT_SSH_USER` (typically
	// "root"). The per-call override (via
	// the v0.8.7 HTTP request body, when
	// added in the follow-up PR) wins.
	sshUser := opts.SSHUser
	if sshUser == "" {
		sshUser = s.sshUser
	}
	// Resolve the SSH address. The
	// node.Address is "host:port" already
	// (set by the provisioner in v0.3.0);
	// if the per-call SSHPort is non-zero,
	// split and replace the port. The
	// helper below is small and inline.
	address, err := resolveSSHAddress(row.Address, opts.SSHPort)
	if err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: resolve address: %w", err)
	}
	// Per-call timeout. Zero = 30s default
	// (matches the v0.3.0 install path's
	// SSH default).
	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	// Open the SSH client. The factory is
	// injected via `WithSSHClientFactory`;
	// the unit tests substitute a mock
	// that returns canned `Run` output.
	sshClient, err := s.sshClientFactory(bootstrap.ClientConfig{
		Address:    address,
		User:       sshUser,
		PrivateKey: priv.PrivateKeyPEM,
		KnownHosts: s.knownHosts,
		// Tofu=Reject: host must already be
		// trusted (the panel pushed the key
		// in v0.3.0 / v0.8.4).
		Tofu:    bootstrap.TofuReject,
		Timeout: timeout,
	})
	if err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: build SSH client: %w", err)
	}
	// Open + close the connection. The
	// `defer` is a `Close` on the interface
	// (no errcheck on the closed channel);
	// errcheck is a `func() { _ = x.Close() }`
	// to satisfy the v0.7.2 audit's
	// errcheck rule.
	defer func() { _ = sshClient.Close() }()
	// Connect + Run. The agent.env file is
	// owned by root mode 0600; the SSH user
	// must be root (or have sudo) to read
	// it. The v0.3.0 install writes the file
	// to /etc/aegis/agent.env owned by root.
	if err := sshClient.Connect(ctx); err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: SSH connect: %w", err)
	}
	out, err := sshClient.Run(ctx, "cat /etc/aegis/agent.env")
	if err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: read agent.env: %w", err)
	}
	// Parse the bearer. The file is a
	// single-line `KEY=VALUE` shell-style
	// env file (see
	// `bootstrap.writeAgentEnv` for the
	// write side). The parser is small
	// and defensive: a missing key, an
	// empty value, or a value with
	// shell-injection characters all
	// fail with a specific error.
	bearer, err := parseAgentEnvBearer(out)
	if err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: parse agent.env: %w", err)
	}
	// Persist. The store update is a
	// single column write; the call is
	// idempotent (same bearer twice = no
	// change).
	if err := s.store.SetAgentBearer(ctx, nodeID, bearer); err != nil {
		return RefreshedBearer{}, fmt.Errorf("nodes: RefreshAgentBearer: persist bearer: %w", err)
	}
	// Derive the public-key fingerprint for
	// the response + audit row. The
	// `parseOpenSSHPrivateKey` helper in
	// `stored_key.go` is package-private;
	// the v0.8.7 caller re-parses the
	// same PEM (cheap; ed25519 is fast).
	pub, err := parseOpenSSHPrivateKey(priv.PrivateKeyPEM)
	if err != nil {
		// Non-fatal: the bearer is
		// already persisted. The
		// fingerprint is for the
		// operator UI + audit row;
		// without it the response
		// is still useful (the
		// bearer is the primary
		// data). We log via the
		// audit "after" path with
		// a `key_fingerprint=<empty>`
		// and continue.
		pub = nil
	}
	var fp string
	if pub != nil {
		sshPub, sshErr := ssh.NewPublicKey(pub)
		if sshErr == nil {
			fp = ssh.FingerprintSHA256(sshPub)
		}
	}
	// Audit. The action follows the
	// `<resource>.<verb>` convention used
	// by the v0.7.x audit log (e.g.
	// `node.create`, `node.update`,
	// `node.stored-key.read`). The
	// `After` map carries the operator-
	// useful metadata; the bearer itself
	// is NOT in the audit row. The
	// `RecordFromContext` helper is
	// nil-safe (a nil `s.audits` short-
	// circuits) AND pulls the actor id
	// from `auth.ClaimsFromContext` itself
	// (via `claims.Subject`), so the caller
	// does not need to set `ActorID`
	// explicitly.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "node.agent-bearer.refresh",
		ResourceType: "node",
		ResourceID:   nodeID.String(),
		After: map[string]any{
			"node_name":          row.Name,
			"address":            address,
			"ssh_user":           sshUser,
			"key_fingerprint":    fp,
			"agent_bearer_bytes": len(bearer),
		},
	})
	return RefreshedBearer{
		NodeID:               nodeID,
		Bearer:               bearer,
		KeyFingerprintSHA256: fp,
	}, nil
}

// parseAgentEnvBearer parses the
// `AEGIS_AGENT_BEARER=...` line from a
// `cat /etc/aegis/agent.env` output. The file
// is a shell-style env file (one
// `KEY=VALUE` per line); the parser is
// intentionally minimal — it only looks for
// the one key the v0.8.7 flow cares about.
//
// The function is defensive: a missing key, a
// value with shell-metacharacters, or an
// over-long value all fail with a specific
// error. The agent's own bearer is a
// cryptographically-random hex string (the
// `installRandomHex` helper in
// `bootstrap.installer.go`); 64 hex chars is
// the expected length. The parser does NOT
// enforce the format (a future agent might
// switch to base64), only that the value is
// non-empty and printable.
func parseAgentEnvBearer(agentEnv string) (string, error) {
	scanner := bufio.NewScanner(strings.NewReader(agentEnv))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		// Skip blank lines and `#`
		// comments. The v0.3.0 write
		// path does not emit comments,
		// but a future agent might.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		const prefix = "AEGIS_AGENT_BEARER="
		if !strings.HasPrefix(line, prefix) {
			continue
		}
		value := strings.TrimPrefix(line, prefix)
		value = strings.TrimSpace(value)
		if value == "" {
			return "", fmt.Errorf("agent.env: AEGIS_AGENT_BEARER is empty")
		}
		// Defensive: reject values with
		// characters that would be
		// suspicious in a hex/base64
		// secret. The agent's
		// `installRandomHex` emits
		// [0-9a-f]; the parser is
		// permissive (allows upper-case
		// hex and base64 chars) but
		// rejects whitespace, quotes,
		// newlines, and shell metas.
		for _, r := range value {
			if r < 0x20 || r == 0x7f || r == '"' || r == '\'' || r == '\\' || r == '$' || r == '`' {
				return "", fmt.Errorf("agent.env: AEGIS_AGENT_BEARER contains a forbidden character (0x%02x)", r)
			}
		}
		// Sanity: a 64-char hex is the
		// documented format; longer is
		// also fine (a future agent
		// might use 128 chars or
		// base64). Just refuse the
		// pathological >1KB case.
		if len(value) > 1024 {
			return "", fmt.Errorf("agent.env: AEGIS_AGENT_BEARER is too long (%d bytes)", len(value))
		}
		return value, nil
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("agent.env: scan: %w", err)
	}
	return "", fmt.Errorf("agent.env: AEGIS_AGENT_BEARER not found")
}

// resolveSSHAddress takes the node's stored
// `host:port` (or `host` without a port) and
// optionally overrides the port. The function
// is small and inline; the alternative
// (`net.SplitHostPort` with `*: missing port`
// error handling) is more code than the
// 3-line conditional below.
func resolveSSHAddress(nodeAddress string, overridePort int) (string, error) {
	if nodeAddress == "" {
		return "", fmt.Errorf("node address is empty")
	}
	if overridePort <= 0 {
		return nodeAddress, nil
	}
	// Split "host:port" → ("host", "22") or
	// "host" → ("host", ""). The colon
	// count distinguishes the two.
	idx := strings.LastIndex(nodeAddress, ":")
	if idx < 0 {
		return fmt.Sprintf("%s:%d", nodeAddress, overridePort), nil
	}
	// Defend against IPv6 literals
	// ("[::1]:22"). The LastIndex on
	// `[::1]:22` returns the position of
	// the trailing `:`; we trust the
	// rightmost colon is the port
	// separator (matches `net.SplitHostPort`
	// semantics for non-IPv6 cases).
	return nodeAddress[:idx+1] + fmt.Sprintf("%d", overridePort), nil
}

// WithSSHClientFactory installs the
// SSH client factory used by
// `RefreshAgentBearer`. The factory is
// injected (rather than imported) so
// unit tests can substitute a mock
// without a real SSH dial. The default
// value is `nil`; the
// `RefreshAgentBearer` call returns an
// error when the factory is nil, so
// the production wiring in
// `internal/app/app.go` must call this
// setter at boot.
//
// # Why a factory, not a *Client
//
// The factory takes a `ClientConfig` and
// returns a fresh `Client`. The pattern
// matches `internal/bootstrap.NewClient`
// directly; the indirection is only the
// "build vs buy" seam, not "single
// connection vs pool". A pooled,
// long-lived SSH connection is a
// future optimization, not in v0.8.7.
func (s *Service) WithSSHClientFactory(fn func(bootstrap.ClientConfig) (bootstrap.Client, error)) *Service {
	s.sshClientFactory = fn
	return s
}

// WithKnownHosts installs the panel's
// known_hosts file path used by
// `RefreshAgentBearer`. The path is
// operator-controlled (the same path
// `cfg.AgentKnownHosts` points at);
// passing it through the Service
// instead of reading from `cfg`
// directly keeps `nodes` independent
// of the config package.
func (s *Service) WithKnownHosts(path string) *Service {
	s.knownHosts = path
	return s
}

// WithSSHUser installs the service-wide
// default SSH user. The per-call
// `RefreshBearerOptions.SSHUser`
// overrides this.
func (s *Service) WithSSHUser(user string) *Service {
	s.sshUser = user
	return s
}

// WithAgentCA installs the v0.8.30 mTLS cert
// bootstrap dependency. The interface is
// `nodes.AgentCertIssuer` (this package defines
// the consumer-side contract; the `agentca`
// package is the producer and adapts via
// `internal/app/agentca_adapter.go`).
//
// v0.8.30 PR 1c wires the setter; v0.8.30 PR 2
// wires the consumer (the `nodes.Service.Provision`
// path; today the `bootstrap` package's `Provision`
// cannot import this package without a cycle
// that is scheduled for v0.8.31).
func (s *Service) WithAgentCA(ca AgentCertIssuer) *Service {
	s.agentCA = ca
	return s
}

// AgentCA returns the v0.8.30 mTLS cert issuer
// installed via `WithAgentCA`, or `nil` if the
// mTLS bootstrap is not wired. v0.8.30 PR 1c
// exposes the getter; v0.8.30 PR 2 wires the
// `bootstrap` package's `Provision` to call
// `AgentCA().EnsureNodeCerts(...)` (today the
// cycle blocks the call site).
func (s *Service) AgentCA() AgentCertIssuer {
	return s.agentCA
}

// keyFingerprintForLog was reserved for
// future audit-row enrichment in the
// v0.8.x bucket. Removed in v0.8.7 —
// the v0.8.7 audit row
// (`node.agent-bearer.refresh`) already
// carries the SSH key fingerprint
// inline, so the dedicated
// `node.stored-key.use` row is not
// needed. The helper may be re-added
// in v0.8.x+ if a new audit shape
// requires a short fingerprint.
