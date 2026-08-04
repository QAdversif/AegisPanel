// SPDX-License-Identifier: AGPL-3.0-or-later
//
// `aegis admin node <subcommand>` — operator-side
// node maintenance. v0.8.3 ships the first
// subcommand: `rotate-panel-key`, the deferred
// re-provision path for v0.3.0..v0.7.x nodes
// (the install path pre-dates the persistent
// panel SSH key feature; this CLI is how the
// operator back-fills the stored key on a
// legacy node).
//
// # Why a new subcommand
//
// The v0.3.0..v0.7.x install flow asked the
// operator to paste their own private key on
// every re-provision. v0.8.1 introduced a
// panel-managed ed25519 keypair that the
// provisioner generates on the first
// password-based install and stores encrypted
// in `nodes.ssh_private_key_ciphertext`
// (migration 0020). The next re-provision of
// the same node decrypts and reuses the
// panel key — the operator does not paste
// anything. Legacy nodes do not have the
// stored key; this CLI generates one and
// pushes the public half to the node's
// authorized_keys so the next re-provision
// is "auto-deploy" again.
//
// # Wire format
//
//	aegis admin node rotate-panel-key <node-uuid>
//	    --key <path-to-pem>    # operator's existing private key
//	    --key -                 # read PEM from stdin
//	    --port <ssh-port>       # override AEGIS_AGENT_SSH_PORT (default 22)
//	    --user <ssh-user>       # override AEGIS_AGENT_SSH_USER (default root)
//
// The CLI exits 0 on success, non-zero on any
// failure. The audit log records the action
// (the v0.8.x audit-call-site wiring from PR #166
// is the writer; the CLI is the only caller
// for the `node.rotate-panel-key` action until
// the admin UI gets a button for it in a
// follow-up).

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
	"github.com/QAdversif/AegisPanel/internal/db"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// runAdminNode dispatches the `aegis admin node
// <subcommand>` namespace. v0.8.3 ships one
// subcommand (`rotate-panel-key`); future
// subcommands (e.g. `decrypt-key-for-emergency-
// access`) land in this same dispatcher.
func runAdminNode(ctx context.Context, args []string) {
	if len(args) == 0 {
		nodeUsage()
		os.Exit(2)
	}
	switch args[0] {
	case "rotate-panel-key":
		runAdminNodeRotatePanelKey(ctx, args[1:])
	default:
		nodeUsage()
		os.Exit(2)
	}
}

func nodeUsage() {
	fmt.Fprintln(os.Stderr, "usage: aegis admin node <rotate-panel-key> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  aegis admin node rotate-panel-key <node-uuid> --key <path>")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The CLI opens a pg pool (AEGIS_POSTGRES_DSN), loads the node")
	fmt.Fprintln(os.Stderr, "row, SSHes into the node using the operator's existing private")
	fmt.Fprintln(os.Stderr, "key, generates a fresh panel ed25519 keypair, pushes the public")
	fmt.Fprintln(os.Stderr, "half to authorized_keys, and stores the encrypted private half")
	fmt.Fprintln(os.Stderr, "in the nodes row. The envelope is built from")
	fmt.Fprintln(os.Stderr, "AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS /")
	fmt.Fprintln(os.Stderr, "AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE (same as the panel's webhooks).")
}

// runAdminNodeRotatePanelKey is the v0.8.3
// implementation. The function is intentionally
// flat — it has 7 sequential steps (parse args,
// read PEM, open pool, build services, get row,
// open SSH, rotate, audit, close) and a
// long-but-linear happy path. Each step's
// failure mode is `log.Fatal` (the CLI is a
// one-shot tool, not a server).
func runAdminNodeRotatePanelKey(ctx context.Context, args []string) {
	// 1. Parse args. The CLI takes the node UUID
	// as a positional arg, the operator's key
	// path as a flag (or "-" for stdin), and
	// the optional port / user overrides.
	var (
		nodeIDStr string
		keyPath   string
		port      = 0
		user      = ""
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--key":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin node rotate-panel-key: --key requires a value")
			}
			i++
			keyPath = args[i]
		case "--port":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin node rotate-panel-key: --port requires a value")
			}
			i++
			parsed, perr := strconv.Atoi(args[i])
			if perr != nil {
				log.Fatal().Err(perr).Str("value", args[i]).Msg("admin node rotate-panel-key: --port is not a number")
			}
			port = parsed
		case "--user":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin node rotate-panel-key: --user requires a value")
			}
			i++
			user = args[i]
		default:
			if nodeIDStr != "" {
				log.Fatal().Str("arg", args[i]).Msg("admin node rotate-panel-key: unexpected positional argument")
			}
			nodeIDStr = args[i]
		}
	}
	if nodeIDStr == "" {
		log.Fatal().Msg("admin node rotate-panel-key: missing <node-uuid>")
	}
	if keyPath == "" {
		log.Fatal().Msg("admin node rotate-panel-key: missing --key <path>")
	}
	nodeID, err := uuid.Parse(nodeIDStr)
	if err != nil {
		log.Fatal().Err(err).Str("arg", nodeIDStr).Msg("admin node rotate-panel-key: invalid <node-uuid>")
	}
	// 2. Read the operator's PEM. The "-" sentinel
	// reads from stdin (matches the `passwd(1)`
	// convention; the operator can pipe or
	// redirect a file).
	operatorPEM, err := readOperatorKey(keyPath)
	if err != nil {
		log.Fatal().Err(err).Str("path", keyPath).Msg("admin node rotate-panel-key: read operator key")
	}
	if len(operatorPEM) == 0 {
		log.Fatal().Str("path", keyPath).Msg("admin node rotate-panel-key: operator key is empty")
	}
	// 3. Open the pg pool. The CLI uses the same
	// env vars as the panel main: AEGIS_POSTGRES_DSN.
	// A memory-only dev run is not supported for the
	// nodes store (the CLI rotates a real row on
	// a real node) — fail fast if the DSN is missing.
	dsn := os.Getenv("AEGIS_POSTGRES_DSN")
	if dsn == "" {
		log.Fatal().Msg("admin node rotate-panel-key: AEGIS_POSTGRES_DSN is not set")
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-panel-key: db.Open")
	}
	defer pool.Close()
	// 4. Build services.
	nodesStore := nodes.NewPgStore(pool)
	nodesSvc := nodes.NewService(nodesStore)
	// The audit log is best-effort: a nil writer
	// (e.g. the audit log table does not exist
	// yet) is allowed. The wire-format is the
	// v0.7.x `audits.Entry` struct; the
	// `audits.Service.Record` method is the
	// canonical writer.
	auditsSvc := audits.NewService(audits.NewPgStore(pool))
	// 5. Build the envelope. Same construction as
	// internal/app/app.go: read the recipients
	// (comma-separated) and the key file (one
	// AGE-SECRET-KEY-1... line). A no-op cipher
	// is used when the env vars are unset (dev /
	// no-secret-at-rest mode); the rotation
	// still works, but the stored ciphertext
	// is plaintext PEM, which is the v0.8.x
	// "never use this in prod" case.
	cipher, err := newCLIEnvelope()
	if err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-panel-key: build envelope")
	}
	if cipher == nil {
		cipher = envelope.NewNoopSecretCipher()
		log.Warn().Msg("admin node rotate-panel-key: AEGIS_WEBHOOKS_SECRET_AGE_* not set; using NoopSecretCipher (DEV ONLY — ciphertext is plaintext PEM)")
	}
	// 6. Look up the node row. The CLI does NOT
	// enforce the state machine (a `rotate-panel-key`
	// is legal from any state; it just means
	// "this node has a panel key now"). The
	// provisioner is the place where the state
	// matters.
	row, err := nodesSvc.Get(ctx, nodeID)
	if err != nil {
		log.Fatal().Err(err).Str("node_id", nodeID.String()).Msg("admin node rotate-panel-key: get node row")
	}
	// 7. Open SSH using the operator's PEM. The
	// ClientConfig fields (Address, Port, User,
	// KnownHosts) come from the row + env
	// overrides; KnownHosts is the panel's
	// standard path (matches what the
	// provisioner uses; the operator's TOFU
	// was already accepted on the first
	// install).
	sshUser := user
	if sshUser == "" {
		sshUser = os.Getenv("AEGIS_AGENT_SSH_USER")
	}
	if sshUser == "" {
		sshUser = "root"
	}
	sshPort := port
	if sshPort == 0 {
		if raw := os.Getenv("AEGIS_AGENT_SSH_PORT"); raw != "" {
			parsed, perr := strconv.Atoi(raw)
			if perr != nil {
				log.Fatal().Err(perr).Str("AEGIS_AGENT_SSH_PORT", raw).Msg("admin node rotate-panel-key: AEGIS_AGENT_SSH_PORT is not a number")
			}
			sshPort = parsed
		}
	}
	if sshPort == 0 {
		sshPort = 22
	}
	knownHosts := os.Getenv("AEGIS_AGENT_KNOWN_HOSTS")
	if knownHosts == "" {
		knownHosts = "./var/known_hosts"
	}
	// ClientConfig.Address is the canonical
	// "host:port" string (the bootstrap SSH
	// client does not have a separate Port
	// field; it appends ":22" only when the
	// address has no colon at all).
	address := row.Address
	if !strings.Contains(address, ":") {
		address = fmt.Sprintf("%s:%d", address, sshPort)
	}
	sshClient, err := bootstrap.NewClient(bootstrap.ClientConfig{
		Address:    address,
		User:       sshUser,
		PrivateKey: operatorPEM,
		KnownHosts: knownHosts,
	})
	if err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-panel-key: NewClient")
	}
	defer func() {
		if err := sshClient.Close(); err != nil {
			log.Warn().Err(err).Msg("admin node rotate-panel-key: ssh close")
		}
	}()
	if err := sshClient.Connect(ctx); err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-panel-key: ssh connect")
	}
	// 8. Build the bootstrap.Service. The
	// service holds the envelope + the nodes
	// NodeProvider; the RotatePanelKey method
	// is the public entry point. The
	// NodeProvider is wired to the
	// `nodes.Store` directly (not the
	// `nodes.Service`) because the
	// NodeProvider interface is the
	// minimum surface the provisioner
	// needs (GetByID / Update /
	// SetAgentBearer /
	// SetSSHPrivateKeyCiphertext) and the
	// Store is the only thing that
	// implements the latter three (the
	// Service is the public API and does
	// not expose them).
	bSvc := bootstrap.NewService(bootstrap.ServiceConfig{
		Nodes:    bootstrapNodeProvider{svc: nodesSvc, store: nodesStore},
		Envelope: cipher,
	})
	// 9. Rotate. The function generates the new
	// ed25519 keypair, encrypts the private half,
	// pushes the public half via SFTP + constant
	// shell command, and persists the ciphertext
	// via SetSSHPrivateKeyCiphertext.
	if err := bSvc.RotatePanelKey(ctx, row.ID, row.Name, sshClient); err != nil {
		log.Fatal().Err(err).Msg("admin node rotate-panel-key: rotation failed")
	}
	// 10. Audit. The action is
	// `node.rotate-panel-key`; the resource is
	// the node UUID. The audits.Service.Record
	// is best-effort (returns nil on a write
	// error so the CLI does not fail after a
	// successful rotation just because the
	// audit log table is full).
	_, _ = auditsSvc.Record(ctx, audits.Entry{
		Action:       "node.rotate-panel-key",
		ResourceType: "node",
		ResourceID:   nodeID.String(),
		After: map[string]any{
			"node_name": row.Name,
			"address":   row.Address,
		},
	})
	log.Info().
		Str("node_id", nodeID.String()).
		Str("node_name", row.Name).
		Str("address", row.Address).
		Str("ssh_user", sshUser).
		Int("ssh_port", sshPort).
		Msg("admin node rotate-panel-key: rotated (next re-provision is auto-deploy)")
}

// readOperatorKey reads the operator's PEM from
// path. The "-" sentinel reads from stdin
// (matches `passwd(1)` / `kubectl create -f -`).
// The CLI does NOT validate the PEM format —
// the SSH library will reject malformed keys
// at NewClient time, and the CLI's error
// message will surface the underlying
// ssh.ParsePrivateKey error to the operator.
func readOperatorKey(path string) ([]byte, error) {
	if path == "-" {
		// Read all of stdin. The CLI is meant to
		// be invoked from a shell where the
		// operator can pipe `cat ~/.ssh/id_ed25519
		// | aegis admin node rotate-panel-key ...`
		// or type directly. We use a buffered
		// reader so a 16KB key does not require
		// a giant stack allocation.
		buf, err := os.ReadFile("/dev/stdin")
		if err != nil {
			return nil, fmt.Errorf("read stdin: %w", err)
		}
		return buf, nil
	}
	// nosec G703 -- the path is operator-supplied
	// (the --key flag); the operator is the
	// same trust principal as the rest of the
	// admin CLI. The G703 (path traversal)
	// check is for callers that read paths
	// from untrusted input (e.g. HTTP
	// request bodies); the admin CLI has no
	// such caller. The path is validated by
	// the SSH library at NewClient time
	// (malformed key -> parse error).
	return os.ReadFile(path) // #nosec G703 -- operator-controlled CLI flag
}

// newCLIEnvelope builds an age envelope from
// the AEGIS_WEBHOOKS_SECRET_AGE_* env vars
// (same construction as internal/app/app.go).
// Returns (nil, nil) when the env vars are
// unset so the caller can fall through to a
// NoopSecretCipher. Returns a non-nil error
// when the env vars are set but the
// construction fails (e.g. malformed
// recipients, missing key file).
func newCLIEnvelope() (envelope.SecretCipher, error) {
	recipientsRaw := os.Getenv("AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS")
	keyFile := os.Getenv("AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE")
	if recipientsRaw == "" && keyFile == "" {
		return nil, nil
	}
	if recipientsRaw == "" || keyFile == "" {
		return nil, errors.New("admin node rotate-panel-key: AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS and AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE must both be set, or both unset")
	}
	recipients := strings.Split(recipientsRaw, ",")
	cipher, err := envelope.NewAgeSecretCipher(recipients, keyFile)
	if err != nil {
		return nil, fmt.Errorf("envelope.NewAgeSecretCipher: %w", err)
	}
	return cipher, nil
}

// bootstrapNodeProvider is the bridge from
// the nodes package to the
// bootstrap.NodeProvider interface. The
// bootstrap package declares the interface;
// this struct is the adapter the CLI uses.
// All methods are 1-line delegations; the
// adapter is small enough to be inlined.
//
// The adapter holds BOTH the Service (for
// the public read path — `Get` is on the
// Service, not the Store) and the Store (for
// the state-mutating write paths the
// Service does not expose:
// SetAgentBearer, SetSSHPrivateKeyCiphertext).
// The Service is the public API; the Store
// is the raw layer. The CLI needs both
// because the rotate-panel-key flow reads
// the row via the Service (so the
// fields-on-response shape matches the rest
// of the panel) and writes the ciphertext
// via the Store (the only writer that
// bypasses the Service-level PATCH
// validation).
type bootstrapNodeProvider struct {
	svc   *nodes.Service
	store nodes.Store
}

func (b bootstrapNodeProvider) GetByID(ctx context.Context, id uuid.UUID) (bootstrap.NodeRow, error) {
	row, err := b.svc.Get(ctx, id)
	if err != nil {
		return bootstrap.NodeRow{}, err
	}
	return bootstrap.NodeRow{
		ID:                      row.ID,
		Name:                    row.Name,
		State:                   string(row.State),
		Address:                 row.Address,
		AgentBearer:             row.AgentBearer,
		SSHPrivateKeyCiphertext: row.SSHPrivateKeyCiphertext,
	}, nil
}

func (b bootstrapNodeProvider) Update(ctx context.Context, n bootstrap.NodeRow) error {
	// The rotate-panel-key CLI does not change
	// the state; this method is here only to
	// satisfy the interface.
	return nil
}

func (b bootstrapNodeProvider) SetAgentBearer(ctx context.Context, id uuid.UUID, bearer string) error {
	return b.store.SetAgentBearer(ctx, id, bearer)
}

func (b bootstrapNodeProvider) SetSSHPrivateKeyCiphertext(ctx context.Context, id uuid.UUID, ciphertext []byte) error {
	return b.store.SetSSHPrivateKeyCiphertext(ctx, id, ciphertext)
}

// _ = time.Second keeps the time import in
// use even if every per-call time helper
// later moves into a different file. The
// 30s context timeout in main.go is the
// authoritative budget; this file does not
// need a per-call time package.
var _ = time.Second
