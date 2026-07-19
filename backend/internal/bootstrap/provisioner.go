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
}

// Service is the bootstrap entry point. main.go
// builds one Service and hands it to the
// HTTP handler.
type Service struct {
	nodes     NodeProvider
	installer *Installer
	sm        *StateMachine
	audits    *audits.Service
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
		agentBinary: cfg.AgentBinary,
		knownHosts:  cfg.KnownHosts,
		sshUser:     cfg.SSHUser,
		sshPort:     cfg.SSHPort,
	}
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
	// 2. Run the installer. The result is
	// always populated, even on failure.
	in := InstallInput{
		NodeID:              row.ID.String(),
		NodeName:            row.Name,
		Address:             row.Address,
		Port:                r.SSHPort,
		SSHUser:             r.SSHUser,
		PrivateKeyPEM:       []byte(r.SSHPrivateKey),
		KnownHosts:          s.knownHosts,
		Tofu:                r.Tofu,
		ExpectedFingerprint: r.ExpectedFingerprint,
		BearerSecret:        plain,
		AgentSource:         s.agentBinary,
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
	// 3. Transition the state. The target
	// state is `online` on success, `offline`
	// on failure. The state machine accepts
	// both from the start state.
	target := StateOffline
	if result.OK {
		target = StateOnline
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
			ActorUsername: usernameFromClaims(claims),
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
	SSHPrivateKey string
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

// claimsFromClaims / usernameFromClaims are
// tiny adapters so we don't have to import the
// auth package types into every call-site. The
// JWT subject is the user UUID; the username
// lives in the admins table and would require a
// second round-trip. v0.3.0 sets actor_id but
// leaves actor_username empty; v0.5.0 adds the
// lookup.
func claimsFromClaims(c *auth.Claims) string {
	if c == nil {
		return ""
	}
	return c.Subject
}

func usernameFromClaims(c *auth.Claims) string {
	if c == nil {
		return ""
	}
	// v0.3.0: the username is not on the
	// claims. The audits entry will show
	// actor_id without actor_username; the
	// "who" of a self-service action is
	// implicitly the operator (no admin-
	// to-admin distinction yet). v0.5.0
	// adds a LookupUser round-trip.
	return ""
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
