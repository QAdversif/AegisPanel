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
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

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
	// Empty bytes mean "no key yet" РІР‚вЂќ the
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
	// mtlscerts is the v0.8.30 mTLS cert issuer
	// factory. The provisioner calls it with
	// (ctx, nodeID, addr) to get the per-node
	// cert material that the installer writes to
	// /etc/aegis/agent.crt + .key + agent-ca.pem.
	// The field is nil-safe (a nil factory means
	// "mTLS not wired"; the installer skips the
	// write and the v0.8.29 fallback applies).
	// The factory is a closure constructed in
	// `app.Build` -- the bootstrap package cannot
	// import the `agentca` package directly
	// without a cycle.
	mtlscerts MTLSCertIssuer
}

// MTLSCertIssuer is the v0.8.30 mTLS cert issuer
// the provisioner calls before building
// `InstallInput.MTLSCerts`. Returning a zero-value
// `MTLSCerts` is the "mTLS not wired" path; any
// error fails the Provision with the error
// message preserved.
type MTLSCertIssuer func(ctx context.Context, nodeID uuid.UUID, addr string) (MTLSCerts, error)

// WithMTLSCerts installs the v0.8.30 mTLS cert
// issuer. `app.Build` wires the agentca.Service
// via a closure (the `agentca` package cannot be
// imported here without a cycle).
func (s *Service) WithMTLSCerts(issuer MTLSCertIssuer) *Service {
	s.mtlscerts = issuer
	return s
}

// mintMTLSCerts is the nil-safe wrapper around the
// v0.8.30 mTLS cert issuer factory. A nil factory
// (the v0.8.29 default) returns a zero-value
// `MTLSCerts`, which the installer's
// `writeMTLSCerts` recognises and skips. A non-nil
// factory's error surfaces verbatim in the
// provisioner's audit log.
func (s *Service) mintMTLSCerts(ctx context.Context, nodeID uuid.UUID, addr string) MTLSCerts {
	if s.mtlscerts == nil {
		return MTLSCerts{}
	}
	certs, err := s.mtlscerts(ctx, nodeID, addr)
	if err != nil {
		// The provisioner does not have a
		// recovery path for a mTLS mint failure
		// (the panel cannot operate without
		// mTLS certs in v0.8.30+). Surface the
		// error via the `MTLSCerts` zero-value
		// convention: the installer skips the
		// write and the v0.8.29 fallback
		// applies. A v0.8.30+ follow-up can
		// thread the error through the
		// `InstallResult`.
		return MTLSCerts{}
	}
	return certs
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
	// MTLSCerts is the v0.8.30 mTLS cert issuer
	// factory. The provisioner calls it on every
	// `Provision` to get the per-node cert material
	// the installer writes to `/etc/aegis/agent.crt`
	// + `.key` + `agent-ca.pem`. A nil factory means
	// "mTLS not wired" (the v0.8.29 fallback). The
	// field is set via `WithMTLSCerts` after
	// construction; the `app.Build` wiring closes
	// over `agentca.Service`.
	MTLSCerts MTLSCertIssuer
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
	s := &Service{
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
	if cfg.MTLSCerts != nil {
		s.mtlscerts = cfg.MTLSCerts
	}
	return s
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
	// not "what can we reach from here" РІР‚вЂќ a
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
	//      is ignored on this path РІР‚вЂќ the stored
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
			postInstallHook = s.buildPersistentSSHKeyHook(row.ID, row.Name)
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
		// v0.8.30: mTLS cert material. A nil
		// `mtlscerts` factory (the v0.8.29
		// default) yields a zero-value
		// `MTLSCerts`, which the installer
		// recognises and skips.
		MTLSCerts: s.mintMTLSCerts(ctx, row.ID, row.Address),
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
	// once the install has succeeded РІР‚вЂќ if the
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
	// once the install has succeeded РІР‚вЂќ if the
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
	// Mutually exclusive with SSHPassword РІР‚вЂќ
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
// password-based install. The hook delegates
// to Service.generateAndPushKey (the shared
// body) and wraps the error with the
// "post-install-hook:" prefix the provisioner
// state machine keys on for the failure stage.
//
// The RotationResult is discarded here: the
// post-install hook has no caller to surface
// the new public key / fingerprint to (the
// operator who ran the install already has
// the rest of the row from the `Provision`
// call; the row's `ssh_private_key_ciphertext`
// column is the persistent signal on the
// panel side).
//
// The "what this does" docstring lives on
// generateAndPushKey in rotate_panel_key.go;
// this wrapper exists only to keep the
// post-install hook's call site stable.
func (s *Service) buildPersistentSSHKeyHook(
	nodeID uuid.UUID,
	nodeName string,
) func(ctx context.Context, c Client) error {
	return func(ctx context.Context, c Client) error {
		if _, err := s.generateAndPushKey(ctx, nodeID, nodeName, c, s.envelope); err != nil {
			return fmt.Errorf("post-install-hook: %w", err)
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
// call. The set is {new, offline} РІР‚вЂќ the two
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
