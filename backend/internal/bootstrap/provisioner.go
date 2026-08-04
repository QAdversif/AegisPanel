// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Provisioner is the Service that ties the
// state machine, the installer, the audit log,
// and the nodes store together. The HTTP
// handler (handler.go) is the only caller.
//
// # Why a Service, not free functions
//
// The state machine + the installer + the audit
// log + the node store each have their own
// lifecycle. A Service struct holds references
// to all four and exposes the two operations
// the rest of the panel cares about:
//   - Provision(nodeID): kick off the install
//     workflow, return when the install is
//     done (the function blocks). State
//     transitions are recorded in the audit
//     log.
//   - Retry(nodeID): same as Provision, but
//     only legal from the `offline` state.
//     Convenience for the operator's "re-
//     provision" button.
//
// The Service is intentionally synchronous
// (v0.3.0). The install is fast enough
// (sub-second on a healthy network, sub-5s
// on the verify deadline) that a goroutine
// pool is premature optimisation. v0.5.0
// adds an async "kick off and poll" mode for
// large fleets.
//
// # State type
//
// The provisioner returns `bootstrap.State`
// (a `string` defined in state.go) rather
// than `nodes.State` to break the import
// cycle (`nodes` imports `bootstrap` for the
// provision handler; `bootstrap` cannot
// import `nodes` without cycling). The two
// types are wire-compatible (string copies);
// the call site in the nodes router does the
// conversion.
//
// # Audit log entries
//
// Every transition (new -> online, new ->
// offline, offline -> new) writes one row
// to the audit_log table with:
//   - action: "node.provision", "node.fail",
//     "node.retry"
//   - resource_type: "node"
//   - resource_id: the node UUID
//   - before: the previous state
//   - after: the new state
//   - actor_username: the operator's username
//     (from the JWT claims)
//   - ip / user_agent: from the request
//
// The v0.2.0 audits package (PR-M) is the
// writer. The provisioner is the v0.3.0 first
// in-handler caller; the v0.4.0 work extends
// the call-sites to nodes / hosts / inbounds.

package bootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"
	"golang.org/x/crypto/ssh"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// NodeProvider is the subset of nodes.Service
// the provisioner depends on. The interface is
// the seam for tests: the integration tests
// substitute a stub that returns canned rows
// from a hard-coded set; the production path
// delegates to the real nodes.Service.
//
// The State field on the returned *Node is
// read + written as `string` (not as
// `nodes.State`) so the bootstrap package
// stays free of the nodes import. The
// conversion happens in the caller; the
// provisioner does not care.
type NodeProvider interface {
	GetByID(ctx context.Context, id uuid.UUID) (NodeRow, error)
	Update(ctx context.Context, n NodeRow) error
	// SetAgentBearer persists the freshly-minted
	// bearer for the node so the panel can ship
	// POST /v1/apply later. v0.4.0-mvp-batched
	// added this; the v0.3.0 provisioner did not
	// need it (one-shot bootstrap, no ongoing
	// panel->agent traffic).
	SetAgentBearer(ctx context.Context, id uuid.UUID, bearer string) error
	// SetSSHPrivateKeyCiphertext persists the
	// panel's persistent SSH private key for
	// the node, sealed with the operator's age
	// envelope (see internal/crypto/envelope,
	// PR #177). v0.8.x added this; the
	// provisioner's post-install hook calls
	// it on the first password-based install
	// and on every password-based re-provision
	// where the stored key has been wiped.
	SetSSHPrivateKeyCiphertext(ctx context.Context, id uuid.UUID, ciphertext []byte) error
}

// NodeRow is the minimal projection of
// nodes.Node that the provisioner needs.
// Defined here (not imported from nodes) to
// keep the import cycle out. The nodes
// router builds the NodeRow on read +
// applies the State update on write.
type NodeRow struct {
	ID      uuid.UUID
	Name    string
	State   string
	Address string
	// AgentBearer is the bearer the panel uses to
	// authenticate to the agent's POST /v1/apply
	// endpoint. v0.3.0 did not need it (one-shot
	// bootstrap); v0.4.0 added it to the row
	// projection so the provisioner can persist
	// the freshly-minted secret without a full
	// Update cycle on the rest of the row.
	AgentBearer string
	// SSHPrivateKeyCiphertext is the panel's
	// persistent SSH private key for this node,
	// sealed with the operator's age envelope.
	// Empty bytes mean "no key yet" — the
	// provisioner treats empty as the signal to
	// fall through to the operator-supplied
	// password / key on the install path, and
	// the signal to gen-and-save a new panel
	// key on the post-install hook. v0.8.x
	// added this field (migration 0020).
	SSHPrivateKeyCiphertext []byte
}

// Service is the bootstrap entry point. main.go
// builds one Service and hands it to the
// HTTP handler.
type Service struct {
	nodes     NodeProvider
	installer *Installer
	sm        *StateMachine
	audits    *audits.Service
	// envelope is the age cipher the provisioner
	// uses to seal / open the panel's persistent
	// node SSH private key. v0.8.x added this;
	// the field is nil-safe (a Service without
	// an envelope skips the post-install key
	// generation and the on-the-fly decrypt
	// path, falling back to the operator-
	// supplied auth on every call). Production
	// wiring (internal/app/app.go) installs the
	// same envelope the webhooks PgStore uses.
	envelope envelope.SecretCipher
	// agentBinary is the absolute path of the
	// placeholder agent binary the installer
	// uploads. The path is set by main.go from
	// cfg.AgentBinaryPath (the operator-
	// configurable release-artifact location).
	// Empty means "not configured" and the
	// provisioner refuses to run.
	agentBinary string
	// knownHosts is the absolute path of the
	// panel's known_hosts file. The installer
	// uses it for TOFU.
	knownHosts string
	// sshUser / sshPort are defaults when the
	// per-node row does not carry its own
	// (the v0.3.0 schema does not; the operator
	// is expected to use root + 22).
	sshUser string
	sshPort int
}

// ServiceConfig groups the constructor inputs.
type ServiceConfig struct {
	Nodes       NodeProvider
	Audits      *audits.Service
	Envelope    envelope.SecretCipher // optional; see Service.envelope
	AgentBinary string
	KnownHosts  string
	SSHUser     string
	SSHPort     int
}

// NewService wires a Service from cfg. The
// caller (main.go) is responsible for picking
// the AgentBinary path: a placeholder script
// for dev, a release-artifact path for prod.
func NewService(cfg ServiceConfig) *Service {
	if cfg.SSHUser == "" {
		cfg.SSHUser = "root"
	}
	if cfg.SSHPort == 0 {
		cfg.SSHPort = 22
	}
	return &Service{
		nodes:       cfg.Nodes,
		installer:   NewInstaller(),
		sm:          NewStateMachine(),
		audits:      cfg.Audits,
		envelope:    cfg.Envelope,
		agentBinary: cfg.AgentBinary,
		knownHosts:  cfg.KnownHosts,
		sshUser:     cfg.SSHUser,
		sshPort:     cfg.SSHPort,
	}
}

// WithEnvelope replaces the service's age
// cipher. The setter is nil-safe (a nil
// cipher disables the persistent-key path),
// matching the `WithAudits` / `WithWebhooks`
// pattern in other Service types. Tests use
// the setter to inject an in-memory
// `envelope.NoopSecretCipher` without going
// through the production `cfg.Envelope` field.
func (s *Service) WithEnvelope(cipher envelope.SecretCipher) *Service {
	s.envelope = cipher
	return s
}

// Provision runs the full bootstrap sequence
// for a node. The function is synchronous: it
// returns when the install is done (success or
// failure). The state transition is recorded
// in the audit log.
//
// Pre-conditions:
//   - The node row exists in the store.
//   - The node row is in state "new" or
//     "offline" (the state machine rejects
//     any other start state).
//   - The HTTP handler is expected to enforce
//     scope (auth.ScopeNodes) before calling.
//
// Returns the new state (online on success,
// offline on failure). On a pre-condition
// violation (e.g. the node is already online),
// the function returns the unchanged state
// and a non-nil error.
func (s *Service) Provision(
	ctx context.Context,
	nodeID uuid.UUID,
	claims *auth.Claims,
	r ProvisionRequest,
) (State, error) {
	row, err := s.nodes.GetByID(ctx, nodeID)
	if err != nil {
		return "", fmt.Errorf("bootstrap: get node: %w", err)
	}
	// Pre-condition: the start state must be
	// `new` (first-time install) or `offline`
	// (re-provision after a failure). The set
	// is hard-coded here (rather than derived
	// from the state machine's "reachable
	// from online" set) because the policy
	// question is "what is provisionable",
	// not "what can we reach from here" — a
	// node that is already `online` is not
	// provisionable (it is already installed),
	// even though the state machine would let
	// the install "transition" online ->
	// online as a no-op. The HTTP layer maps
	// the errInvalidStartState sentinel to a
	// 409.
	prev := State(row.State)
	if !isProvisionable(prev) {
		return prev, fmt.Errorf("%w: cannot provision from state %q", errInvalidStartState, prev)
	}
	// 1. Mint the bearer secret. The plain
	// text is installed on the node; the hash
	// is the placeholder for the v0.5.0
	// challenge-response verification (v0.3.0
	// does not yet store the hash; the secret
	// is one-shot).
	plain, _, err := GenerateBearerSecret()
	if err != nil {
		return prev, fmt.Errorf("bootstrap: mint secret: %w", err)
	}
	// 1.5. Decide the SSH auth method.
	//
	// Three options, in priority order:
	//
	//   a. Stored panel key (v0.8.x, migration
	//      0020). If the row already carries a
	//      non-empty ssh_private_key_ciphertext,
	//      decrypt it via the envelope and use
	//      that PEM as the auth material. The
	//      operator's request (key OR password)
	//      is ignored on this path — the stored
	//      key wins. The post-install hook is
	//      NOT registered because the key is
	//      already in place on the node from
	//      the first install.
	//
	//   b. Operator-supplied password (first-
	//      time install or fresh re-provision
	//      after the stored key was wiped). Use
	//      the password; register the post-
	//      install hook to generate a fresh
	//      ed25519 keypair, encrypt the private
	//      half via the envelope, persist the
	//      ciphertext to the node row, and
	//      append the public half to the node's
	//      authorized_keys.
	//
	//   c. Operator-supplied private key. Use
	//      the operator's key as-is; the
	//      operator's key is already the
	//      persistent credential so no panel-
	//      side rotation is needed. The
	//      post-install hook is NOT registered.
	//
	// Option (a) is what makes the v0.8.x
	// "auto-deploy" actually auto: the operator
	// does not need to paste a key on every
	// re-provision. Option (b) is the first-
	// time-install path; the operator pastes
	// the VPS root password once and the panel
	// takes over from there.
	//
	// The HTTP layer still enforces the XOR on
	// the request (the operator must supply at
	// least one of key or password); the
	// provisioner is authoritative on the
	// ACTUAL auth method, which is the right
	// place for "stored key overrides request"
	// because the request does not know what
	// is in the DB.
	var postInstallHook func(ctx context.Context, c Client) error
	switch {
	case len(row.SSHPrivateKeyCiphertext) > 0:
		if s.envelope == nil {
			// v0.8.x failure mode: the column
			// has a stored key but the panel
			// was booted without an envelope.
			// The two are designed to be
			// installed together (the app
			// wiring installs both in the same
			// block); a missing envelope
			// means a config bug. Fail
			// closed.
			return prev, errors.New("bootstrap: node has stored SSH key but envelope is not configured")
		}
		privPEM, err := s.envelope.Decrypt(row.SSHPrivateKeyCiphertext)
		if err != nil {
			return prev, fmt.Errorf("bootstrap: decrypt stored SSH key: %w", err)
		}
		r.SSHPassword = ""
		r.SSHPrivateKey = string(privPEM)
	case r.SSHPassword != "":
		// Password-first install. Register the
		// post-install hook ONLY if the
		// envelope is configured (without
		// the envelope we cannot seal the
		// private key for later re-use, so
		// the rotation step is a no-op;
		// the password remains one-shot).
		if s.envelope != nil {
			postInstallHook = s.buildPersistentSSHKeyHook(ctx, row.ID, row.Name)
		}
	}
	// 2. Run the installer. The result is
	// always populated, even on failure.
	in := InstallInput{
		NodeID:              row.ID.String(),
		NodeName:            row.Name,
		Address:             row.Address,
		Port:                r.SSHPort,
		SSHUser:             r.SSHUser,
		PrivateKeyPEM:       []byte(r.SSHPrivateKey),
		Password:            r.SSHPassword,
		KnownHosts:          s.knownHosts,
		Tofu:                r.Tofu,
		ExpectedFingerprint: r.ExpectedFingerprint,
		BearerSecret:        plain,
		AgentSource:         s.agentBinary,
		PostInstallHook:     postInstallHook,
	}
	// Default the SSH port / user to the
	// service-wide values when the request does
	// not override them.
	if in.Port == 0 {
		in.Port = s.sshPort
	}
	if in.SSHUser == "" {
		in.SSHUser = s.sshUser
	}
	result := s.installer.Install(ctx, in)
	// 2.5. Persist the bearer to the panel side.
	// v0.4.0-mvp-batched: the panel needs the
	// bearer at every Apply (POST /v1/apply), not
	// just at install time. We mint it in step 1
	// (above) and only write it to the node here
	// once the install has succeeded — if the
	// install failed, there is no live agent to
	// pair the bearer with, and a stale DB value
	// would be a footgun. The Update on row.State
	// below preserves the bearer (it is not in
	// NodeRow; only SetAgentBearer touches it).
	// 3. Transition the state. The target
	// state is `online` on success, `offline`
	// on failure. The state machine accepts
	// both from the start state. Declared
	// before the SetAgentBearer block below
	// so the bail-out on persistence failure
	// can flip target back to offline.
	target := StateOffline
	if result.OK {
		target = StateOnline
	}
	// 2.5. Persist the bearer to the panel side.
	// v0.4.0-mvp-batched: the panel needs the
	// bearer at every Apply (POST /v1/apply), not
	// just at install time. We mint it in step 1
	// (above) and only write it to the node here
	// once the install has succeeded — if the
	// install failed, there is no live agent to
	// pair the bearer with, and a stale DB value
	// would be a footgun. The Update on row.State
	// below preserves the bearer (it is not in
	// NodeRow; only SetAgentBearer touches it).
	if result.OK {
		if err := s.nodes.SetAgentBearer(ctx, row.ID, plain); err != nil {
			log.Error().Err(err).Str("node", row.Name).
				Msg("bootstrap: persist agent bearer failed; panel cannot Apply until re-provisioned")
			// Treat the row as offline so the
			// operator knows the panel cannot talk
			// to the agent. The audit log records
			// the install success; the DB row
			// shows the inconsistent state.
			result.OK = false
			target = StateOffline
		}
	}
	// The transition is best-effort: a DB
	// error here is logged and the original
	// install error is returned. The operator
	// can re-provision to retry.
	if _, err := s.sm.Transition(prev, target); err != nil {
		log.Warn().Err(err).Msg("bootstrap: invalid transition (should not happen)")
	}
	row.State = string(target)
	if err := s.nodes.Update(ctx, row); err != nil {
		log.Error().Err(err).Msg("bootstrap: persist new state")
		return target, err
	}
	// 4. Record the audit log entry. The
	// RecordFromRequest helper pulls the IP +
	// user-agent + actor from the request
	// context; we only have a `*auth.Claims`
	// here, so the actor ID is set explicitly
	// and the username is left blank (the
	// audits package fills it in from the
	// caller-supplied Entry).
	if s.audits != nil {
		action := "node.provision"
		if !result.OK {
			action = "node.fail"
		}
		_, _ = s.audits.Record(ctx, audits.Entry{
			Action:        action,
			ResourceType:  "node",
			ResourceID:    row.ID.String(),
			Before:        map[string]any{"state": string(prev)},
			After:         map[string]any{"state": string(target), "stage": result.Stage, "err": errString(result.Err)},
			ActorID:       claimsFromClaims(claims),
			ActorUsername: "", // v0.5.0: resolve via auth.Service.LookupByID(claims.Subject)
		})
	}
	if !result.OK {
		return target, fmt.Errorf("bootstrap: install failed at stage %q: %w", result.Stage, result.Err)
	}
	return target, nil
}

// ProvisionRequest is the operator-supplied
// per-call input. The struct is separate from
// InstallInput so the HTTP layer can validate
// + sanitize it (e.g. trim the private-key
// whitespace) before the provisioner sees it.
type ProvisionRequest struct {
	// SSHPort is the per-call override. Zero
	// means "use the service-wide default".
	SSHPort int
	// SSHUser is the per-call override. Empty
	// means "use the service-wide default".
	SSHUser string
	// SSHPrivateKey is the operator's pasted
	// private key (PEM). The provisioner
	// passes it to the installer as-is.
	// Mutually exclusive with SSHPassword —
	// the HTTP layer enforces the XOR.
	SSHPrivateKey string
	// SSHPassword is the operator's SSH login
	// password for first-time auth on a fresh
	// node. The provisioner passes it to the
	// installer as-is; the installer is the
	// only consumer. Mutually exclusive with
	// SSHPrivateKey.
	SSHPassword string
	// Tofu is the trust-on-first-use policy.
	// The provisioner forwards to the
	// installer; the installer's TofuReject
	// is the safe default.
	Tofu TofuPolicy
	// ExpectedFingerprint is the operator-
	// supplied SHA256 fingerprint for first
	// contact. Required when Tofu is
	// TofuAcceptAndAppend.
	ExpectedFingerprint string
}

// claimsFromClaims is a tiny adapter so we
// don't have to import the auth package types
// into every call-site. The JWT subject is the
// user UUID; the username lives in the admins
// table and would require a second round-trip.
// v0.3.0 only sets actor_id; the actor_username
// is left empty (v0.5.0 adds the lookup).
func claimsFromClaims(c *auth.Claims) string {
	if c == nil {
		return ""
	}
	return c.Subject
}

// buildPersistentSSHKeyHook returns a
// post-install hook (the function shape
// expected by InstallInput.PostInstallHook)
// that runs at the end of a successful
// password-based install. The hook:
//
//  1. Generates a fresh ed25519 keypair on
//     the panel side.
//  2. Marshals the private key to OpenSSH
//     PEM and seals it with the operator's
//     age envelope (see
//     internal/crypto/envelope, PR #177).
//  3. Persists the ciphertext via
//     SetSSHPrivateKeyCiphertext so the
//     next re-provision can decrypt and
//     reuse the same key without the
//     operator re-pasting a password.
//  4. Uploads the public key to a temp
//     path on the node via SFTP and then
//     runs a constant shell command (no
//     string interpolation) that copies
//     the key into
//     $HOME/.ssh/authorized_keys with the
//     correct mode. The constant command
//     keeps the gosec G204 (sub-shell
//     injection) check happy: the command
//     is built at compile time and never
//     touches operator-controlled bytes.
//
// The hook runs while the SSH client is
// still connected, so no second dial is
// needed. A failure at any step fails the
// install at the `post-install-hook` stage
// (recorded in the audit log by the
// provisioner) and the agent is left
// un-initialised — the operator's "retry"
// button restarts the install with the same
// password and a new keypair is generated.
//
// nodeName is folded into the SSH key
// comment ("aegis-panel@node-<name>") so
// the operator's `ssh-add -L` output is
// self-documenting in a multi-node fleet.
func (s *Service) buildPersistentSSHKeyHook(
	_ context.Context,
	nodeID uuid.UUID,
	nodeName string,
) func(ctx context.Context, c Client) error {
	return func(ctx context.Context, c Client) error {
		// 1. Generate ed25519 keypair.
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return fmt.Errorf("post-install-hook: ed25519.GenerateKey: %w", err)
		}
		// 2. Marshal private to OpenSSH PEM.
		// The comment is informational only
		// (the SSH client never sends it back
		// to the server); it shows up in
		// `ssh-add -L` and in the agent's
		// debug logs.
		privPEMBlock, err := ssh.MarshalPrivateKey(priv, "aegis-panel@node-"+nodeName)
		if err != nil {
			return fmt.Errorf("post-install-hook: ssh.MarshalPrivateKey: %w", err)
		}
		privPEM := pem.EncodeToMemory(privPEMBlock)
		// 3. Marshal public to authorized_keys
		// line. ssh.MarshalAuthorizedKey returns
		// "<key-type> <base64> <comment>\n" —
		// the trailing newline is trimmed so
		// the per-line `grep -qxF` idempotency
		// check on the remote side matches the
		// line shape we upload.
		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			return fmt.Errorf("post-install-hook: ssh.NewPublicKey: %w", err)
		}
		pubLine := bytes.TrimSpace(ssh.MarshalAuthorizedKey(sshPub))
		// 4. Encrypt private key.
		cipher, err := s.envelope.Encrypt(privPEM)
		if err != nil {
			return fmt.Errorf("post-install-hook: envelope encrypt: %w", err)
		}
		// 5. Persist ciphertext. The hook runs
		// BEFORE the post-install verify, so a
		// SetSSHPrivateKeyCiphertext error
		// here does not leave the agent in a
		// half-installed state — the verify
		// probe is the next step and would
		// fail anyway.
		if err := s.nodes.SetSSHPrivateKeyCiphertext(ctx, nodeID, cipher); err != nil {
			return fmt.Errorf("post-install-hook: persist ciphertext: %w", err)
		}
		// 6. Push public key. We upload the
		// single line to a fixed temp path
		// on the node via SFTP (the bytes
		// are written verbatim, so there
		// is no shell-quoting concern), then
		// run a CONSTANT shell command (no
		// string interpolation) that ensures
		// $HOME/.ssh exists with mode 0700,
		// creates authorized_keys with mode
		// 0600 if it does not exist,
		// idempotently appends the uploaded
		// line, and removes the temp file.
		// The `grep -qxF` check makes the
		// append idempotent on retry: a key
		// that is already in the file is
		// left untouched.
		//
		// The fixed path is safe because the
		// provisioner state machine forbids
		// concurrent provisions of the same
		// node (a node is `new` -> `online`
		// or `offline`; the state machine
		// rejects a re-provision from
		// `online`). On retry from `offline`
		// the previous temp file is left
		// behind but the `rm -f` at the end
		// of the new run cleans it up.
		//
		// The remote command is a constant
		// string — the public key is in a
		// file, not interpolated. gosec G204
		// (sub-shell injection) is satisfied
		// because the only `Run` argument
		// here is a compile-time string
		// literal.
		const remotePubKeyPath = "/tmp/.aegis-pubkey"
		// Upload the public key to a local
		// temp file first, then SFTP it onto
		// the node. The local file is the
		// panel-side source-of-truth for the
		// bytes; we clean it up below.
		localTmp, err := os.CreateTemp("", "aegis-pubkey-*.pub")
		if err != nil {
			return fmt.Errorf("post-install-hook: local temp: %w", err)
		}
		localPath := localTmp.Name()
		defer func() { _ = os.Remove(localPath) }()
		if _, err := localTmp.Write(pubLine); err != nil {
			_ = localTmp.Close()
			return fmt.Errorf("post-install-hook: local write: %w", err)
		}
		if err := localTmp.Close(); err != nil {
			return fmt.Errorf("post-install-hook: local close: %w", err)
		}
		if err := c.Upload(ctx, localPath, remotePubKeyPath, 0o600); err != nil {
			return fmt.Errorf("post-install-hook: sftp upload: %w", err)
		}
		// The remote command is a constant
		// string — the public key is in a
		// file, not interpolated. gosec G204
		// (sub-shell injection) is satisfied
		// because the only `Run` argument
		// here is a compile-time string
		// literal.
		const cmd = "set -e\n" +
			"install -d -m 0700 \"$HOME/.ssh\"\n" +
			"touch \"$HOME/.ssh/authorized_keys\"\n" +
			"chmod 0600 \"$HOME/.ssh/authorized_keys\"\n" +
			"PUBKEY_FILE=\"/tmp/.aegis-pubkey\"\n" +
			"if [ -f \"$PUBKEY_FILE\" ]; then\n" +
			"  if ! grep -qxF \"$(cat \"$PUBKEY_FILE\")\" \"$HOME/.ssh/authorized_keys\" 2>/dev/null; then\n" +
			"    cat \"$PUBKEY_FILE\" >> \"$HOME/.ssh/authorized_keys\"\n" +
			"  fi\n" +
			"  rm -f \"$PUBKEY_FILE\"\n" +
			"fi\n"
		if _, err := c.Run(ctx, cmd); err != nil {
			return fmt.Errorf("post-install-hook: append authorized_keys: %w", err)
		}
		return nil
	}
}

// errString returns the error message or ""
// for nil. The audit log's `after` map cannot
// store a typed error; we record the message
// so the operator can read the failure mode
// in the audits UI.
func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// isProvisionable reports whether the given
// state is a legal start for a Provision
// call. The set is {new, offline} — the two
// states where the install workflow has
// something to do. A node in `online` is
// already installed (nothing to do);
// `draining` and `disabled` are operator-only
// transitions and never trigger an install.
func isProvisionable(s State) bool {
	return s == StateNew || s == StateOffline
}

// DefaultKnownHostsPath returns the conventional
// path for the panel's known_hosts file. The
// caller (main.go) uses it as the default when
// the operator does not override via config.
// The path is `${cfg.DataDir}/known_hosts` for
// v0.3.0; v0.5.0 moves to `${cfg.SecretsDir}`.
func DefaultKnownHostsPath(dataDir string) string {
	if dataDir == "" {
		return "/var/lib/aegis/known_hosts"
	}
	return filepath.Join(dataDir, "known_hosts")
}

// EnsureKnownHosts creates the known_hosts file
// (and parent directory) if it does not exist.
// The function is idempotent: an existing file
// is left untouched. The caller (main.go) runs
// this once at boot.
func EnsureKnownHosts(path string) error {
	if path == "" {
		return errors.New("bootstrap: known_hosts path is empty")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("bootstrap: mkdir %s: %w", dir, err)
	}
	if _, err := os.Stat(path); err == nil {
		return nil // already exists
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("bootstrap: stat %s: %w", path, err)
	}
	// 0o600: the file is sensitive (it is a
	// whitelist of trusted hosts). The
	// installer appends to it; the
	// known_hosts SSH library reads it.
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		return fmt.Errorf("bootstrap: create %s: %w", path, err)
	}
	return nil
}

// _ = time.Second keeps the time import in
// use even if every per-call time helper
// later moves into a different file.
var _ = time.Second
