// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis entry point.
//
// Aegis is a self-hosted VPN control panel that orchestrates a fleet of
// BYO nodes (running sing-box / Xray / Hysteria 2) via SSH, exposes a
// REST API for the admin UI, and renders multi-format subscription
// configurations for end-user VPN clients.
//
// Architecture is documented in ../ARCHITECTURE.md. The OpenAPI
// spec for the HTTP API lives at docs/openapi.yaml.

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	// Aegis Phase 1 — pre-declared runtime dependencies. These are pulled in
	// as blank imports so that `go mod tidy` keeps the corresponding
	// requirements in go.mod. They will be wired into real modules in
	// upcoming phases (auth/users → pgx, jwt, crypto, uuid; events → nats;
	// cache → redis; validation → validator; openapi → swag).
	_ "github.com/go-playground/validator/v10" // Phase 1 — input validation
	_ "github.com/golang-jwt/jwt/v5"           // Phase 1 — JWT (access + refresh tokens)
	"github.com/google/uuid"                   // v0.4.0: needed for singboxNodeResolver.Resolve signature
	_ "github.com/google/uuid"                 // Phase 1 — UUIDv4 generation
	_ "github.com/nats-io/nats.go"             // Phase 1 — event bus / JetStream
	_ "github.com/redis/go-redis/v9"           // Phase 1 — Redis client
	_ "github.com/swaggo/swag"                 // Phase 1 — OpenAPI generator

	"github.com/rs/zerolog/log"

	// Composition root + the v0.7.2 audit fix: `internal/app`
	// owns every service wiring (stores, migrations, retry
	// worker, v0.7.x outbound event surface, router, server).
	// main.go keeps only the cmd-level concerns: logger
	// setup, subcommand dispatch (migrate / admin), singbox
	// per-node BatchedApplier wiring, signal handling, and
	// graceful shutdown.
	"github.com/QAdversif/AegisPanel/internal/app"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/builder"
	"github.com/QAdversif/AegisPanel/internal/cores/singbox"   // v0.4.0: needed for *Provider type assertion + Configure
	_ "github.com/QAdversif/AegisPanel/internal/cores/singbox" // Phase 1 — real core provider (init() self-registers)
	"github.com/QAdversif/AegisPanel/internal/db"
	"github.com/QAdversif/AegisPanel/internal/migrations"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/obs"
)

func main() {
	// Pretty console output in dev, structured JSON in prod.
	// ConfigureLogger reads AEGIS_ENV directly so the format
	// is ready before config.Load() — see obs.ConfigureLogger
	// for the rationale.
	obs.ConfigureLogger()

	// `aegis migrate …` and `aegis admin …` are maintenance
	// subcommands that run before the rest of the boot
	// sequence. They do not touch the rest of config (env,
	// observability, …) on purpose: a maintenance command
	// should not require a fully-initialised runtime to
	// run. The dispatch lives in main.go (not in app)
	// because each subcommand only needs the pg pool, not
	// the full service graph that app.Build returns.
	if len(os.Args) >= 2 {
		switch os.Args[1] {
		case "migrate":
			runMigrate(os.Args[2:])
			return
		case "admin":
			runAdmin(os.Args[2:])
			return
		}
	}

	// Top-level context for boot-time operations. Cancelled
	// when the process receives SIGINT / SIGTERM. The cancel
	// is registered as a defer *after* the early log.Fatal
	// call sites so that exitAfterDefer (gocritic) does not
	// flag the boot sequence — log.Fatal calls os.Exit,
	// which skips defers anyway.
	ctx, cancelBoot := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)

	// 1. Load configuration from environment + .env file.
	cfg, err := config.Load()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load configuration")
	}

	// 2. Wire up observability (tracing, metrics, logging).
	cleanup, err := obs.Init(cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to initialise observability")
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := cleanup(shutdownCtx); err != nil {
			log.Error().Err(err).Msg("observability shutdown failed")
		}
	}()

	// All boot-time resources are now live — safe to
	// register the signal-context cancel so graceful
	// shutdown actually runs.
	defer cancelBoot()

	log.Info().
		Str("version", "0.0.0-dev").
		Str("commit", cfg.GitCommit).
		Str("env", cfg.Env).
		Msg("aegis panel starting")

	// 3. Composition root. Build() opens the pg pool
	//    when one is needed, applies migrations,
	//    builds every store and every service, wires
	//    the v0.7.x outbound event surface, starts
	//    the webhook retry worker, and returns a
	//    fully wired *App. The cmd/ binary owns the
	//    http.Server lifecycle and the SIGINT handling.
	a, err := app.Build(ctx, cfg)
	if err != nil {
		log.Fatal().Err(err).Msg("app: composition root failed")
	}
	defer a.Close()

	// 4. v0.5.0: wire the sing-box provider's HTTP
	//    transport (panel -> agent) and the per-node
	//    BatchedApplier queue. The wiring populates
	//    `a.BatchedAppliers` and registers cancel
	//    funcs for `a.Close()` to stop every
	//    applier goroutine uniformly.
	if sbp, sbErr := cores.Get("sing-box"); sbErr != nil {
		log.Warn().Err(sbErr).Msg("cores: sing-box provider not registered; Apply will return ErrApplyNotConfigured")
	} else if sbProvider, ok := sbp.(*singbox.Provider); ok {
		if err := singboxWiring(ctx, sbProvider, a); err != nil {
			log.Fatal().Err(err).Msg("v0.5.0: singbox wiring failed")
		}
	} else {
		log.Warn().Msg("cores: registered sing-box provider is not *singbox.Provider — Apply transport disabled")
	}

	// 5. Run the HTTP server in a goroutine so we
	//    can listen for signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP server listening")
		if err := a.Server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// 6. Wait for SIGINT / SIGTERM or a fatal server
	//    error.
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	select {
	case sig := <-stop:
		log.Info().Str("signal", sig.String()).Msg("shutdown signal received")
	case err := <-serverErr:
		if err != nil {
			log.Error().Err(err).Msg("HTTP server failed")
		}
	}

	// 7. Graceful shutdown with a hard deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := a.Server.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}

	log.Info().Msg("aegis panel stopped")
}

// runMigrate implements the `aegis migrate` subcommand. The
// caller has already verified that os.Args[1] == "migrate";
// args is the rest of the command line.
//
// Usage:
//
//	aegis migrate up    [DIR]    — apply every .sql file in DIR
//	                              (default "migrations").
//	aegis migrate down  FILE    — roll back a single migration
//	                              file (filename only, no path).
//
// The DSN is read from AEGIS_POSTGRES_DSN directly so the
// subcommand does not require the rest of the configuration
// to be valid (env, observability, …) — useful when a
// migrations run is the only thing that can recover a broken
// install.
func runMigrate(args []string) {
	if len(args) == 0 {
		migrateUsage()
		os.Exit(2)
	}
	dsn := os.Getenv("AEGIS_POSTGRES_DSN")
	if dsn == "" {
		log.Fatal().Msg("migrate: AEGIS_POSTGRES_DSN is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	pool, err := db.Open(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("migrate: db.Open")
	}
	defer pool.Close()

	switch args[0] {
	case "up":
		dir := "migrations"
		if len(args) >= 2 {
			dir = args[1]
		}
		if err := migrations.Up(ctx, pool, dir); err != nil {
			log.Fatal().Err(err).Msg("migrate up: failed")
		}
		log.Info().Str("dir", dir).Msg("migrate up: applied")
	case "down":
		if len(args) < 2 {
			log.Fatal().Msg("migrate down: usage: aegis migrate down <file>")
		}
		target := args[1]
		dir := "migrations"
		if len(args) >= 3 {
			dir = args[2]
		}
		if err := migrations.Down(ctx, pool, dir, target); err != nil {
			log.Fatal().Err(err).Str("file", target).Msg("migrate down: failed")
		}
		log.Info().Str("file", target).Msg("migrate down: applied")
	default:
		migrateUsage()
		os.Exit(2)
	}
}

func migrateUsage() {
	fmt.Fprintln(os.Stderr, "usage: aegis migrate <up|down> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  aegis migrate up    [DIR]    apply every .sql in DIR (default migrations)")
	fmt.Fprintln(os.Stderr, "  aegis migrate down  FILE    roll back FILE inside migrations/ (or DIR)")
}

// runAdmin implements the `aegis admin …` subcommand.
// Like migrate, it runs without the rest of the boot
// sequence (HTTP server, observability, …). The Store
// is selected at runtime from AEGIS_AUTH_BACKEND; the
// DSN is read from AEGIS_POSTGRES_DSN for the pg path.
//
// Usage:
//
//	aegis admin add <username> --email <email> [--role <role>]
//	aegis admin passwd <username>
//	aegis admin list
//
// `add` and `passwd` prompt for the password on the
// terminal; stdin is read with the standard readline
// semantics. The plaintext never leaves the process —
// it is hashed with argon2id before the Store sees it.
func runAdmin(args []string) {
	if len(args) == 0 {
		adminUsage()
		os.Exit(2)
	}
	authBackend := os.Getenv("AEGIS_AUTH_BACKEND")
	if authBackend == "" {
		authBackend = "memory"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var authStore auth.Store
	switch authBackend {
	case "pg":
		dsn := os.Getenv("AEGIS_POSTGRES_DSN")
		if dsn == "" {
			log.Fatal().Msg("admin: AEGIS_POSTGRES_DSN is not set (required by AEGIS_AUTH_BACKEND=pg)")
		}
		pool, err := db.Open(ctx, dsn)
		if err != nil {
			log.Fatal().Err(err).Msg("admin: db.Open")
		}
		defer pool.Close()
		authStore = auth.NewPgStore(pool)
	default:
		// Memory store — useful for the dev / CI flow
		// but the seeded admin is not persisted across
		// restarts. The CLI prints a warning so the
		// operator does not mistake it for a real
		// install.
		log.Warn().Msg("admin: AEGIS_AUTH_BACKEND not set, using in-memory store (changes will not persist)")
		authStore = auth.NewMemoryStore()
	}
	svc := auth.NewService(auth.NewSigner("cli-tool-not-a-jwt-signer"), authStore)

	switch args[0] {
	case "add":
		runAdminAdd(ctx, svc, args[1:])
	case "passwd":
		runAdminPasswd(ctx, svc, args[1:])
	case "list":
		runAdminList(ctx, svc)
	default:
		adminUsage()
		os.Exit(2)
	}
}

// runAdminAdd parses the add-subcommand flags, prompts
// for a password, and persists the new admin. Flags:
//
//	--email   <email>   (required)
//	--role    <role>    ('super-admin' | 'operator' | 'viewer', default 'operator')
//
// The password is read from stdin (the CLI is meant to
// be invoked from a shell where the operator can pipe
// the password in or type it directly). v0.3 adds
// /dev/tty echo suppression for a true `passwd(1)`-like
// experience.
func runAdminAdd(ctx context.Context, svc *auth.Service, args []string) {
	var (
		username string
		email    string
		role     string
	)
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--email":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin add: --email requires a value")
			}
			email = args[i+1]
			i++
		case "--role":
			if i+1 >= len(args) {
				log.Fatal().Msg("admin add: --role requires a value")
			}
			role = args[i+1]
			i++
		default:
			if username == "" {
				username = args[i]
			} else {
				log.Fatal().Str("arg", args[i]).Msg("admin add: unexpected positional argument")
			}
		}
	}
	if username == "" {
		log.Fatal().Msg("admin add: missing username")
	}
	if email == "" {
		log.Fatal().Msg("admin add: missing --email")
	}
	if role == "" {
		role = "operator"
	}
	plain, err := promptPassword("New password: ")
	if err != nil {
		log.Fatal().Err(err).Msg("admin add: read password")
	}
	confirm, err := promptPassword("Confirm:     ")
	if err != nil {
		log.Fatal().Err(err).Msg("admin add: read password")
	}
	if plain != confirm {
		log.Fatal().Msg("admin add: passwords do not match")
	}
	if len(plain) < 8 {
		log.Fatal().Msg("admin add: password is too short (min 8 chars)")
	}
	u, err := svc.CreateAdmin(ctx, auth.CreateAdminInput{
		Username:  username,
		Email:     email,
		Plaintext: plain,
		Role:      role,
	})
	if err != nil {
		if errors.Is(err, auth.ErrConflict) {
			log.Fatal().Err(err).Msg("admin add: conflict (username or email already exists)")
		}
		log.Fatal().Err(err).Msg("admin add: failed")
	}
	log.Info().
		Str("id", u.ID).
		Str("username", u.Username).
		Str("email", u.Email).
		Str("role", u.Role).
		Msg("admin add: created")
}

// runAdminPasswd prompts for a new password and rotates
// the existing admin's hash. The username must already
// exist in the Store. There is no "current password"
// check — the CLI is for the operator who already has
// shell access; the on-disk hash is the source of truth.
func runAdminPasswd(ctx context.Context, svc *auth.Service, args []string) {
	if len(args) == 0 {
		log.Fatal().Msg("admin passwd: missing username")
	}
	if len(args) > 1 {
		log.Fatal().Msg("admin passwd: too many arguments")
	}
	username := args[0]
	plain, err := promptPassword("New password: ")
	if err != nil {
		log.Fatal().Err(err).Msg("admin passwd: read password")
	}
	confirm, err := promptPassword("Confirm:     ")
	if err != nil {
		log.Fatal().Err(err).Msg("admin passwd: read password")
	}
	if plain != confirm {
		log.Fatal().Msg("admin passwd: passwords do not match")
	}
	if len(plain) < 8 {
		log.Fatal().Msg("admin passwd: password is too short (min 8 chars)")
	}
	u, err := svc.LookupByUsername(ctx, username)
	if err != nil {
		log.Fatal().Err(err).Str("username", username).Msg("admin passwd: user not found")
	}
	if err := svc.ChangePassword(ctx, u.ID, plain); err != nil {
		log.Fatal().Err(err).Msg("admin passwd: failed")
	}
	log.Info().
		Str("username", u.Username).
		Msg("admin passwd: rotated")
}

// runAdminList dumps every user the Store knows about.
// The output is human-readable, not machine-parseable;
// this is a maintenance command, not a daily-driver
// UI.
func runAdminList(ctx context.Context, svc *auth.Service) {
	rows, err := svc.ListUsers(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("admin list: failed")
	}
	if len(rows) == 0 {
		log.Info().Msg("admin list: no users")
		return
	}
	for _, u := range rows {
		log.Info().
			Str("id", u.ID).
			Str("username", u.Username).
			Str("email", u.Email).
			Str("role", u.Role).
			Bool("enabled", u.Enabled).
			Msg("admin")
	}
}

// promptPassword reads a single line from the controlling
// terminal with echo suppressed, so the operator does not
// leak the password to shoulder-surfers, the shell history,
// or the process table. The flow is:
//
//  1. Print the prompt to stderr (stdout would interleave
//     with the echoed bytes on platforms where the
//     terminal driver reflects the suppressed input).
//  2. Open /dev/tty directly. We cannot read from
//     `os.Stdin` because the kernel's line discipline
//     only suppresses echo on the controlling tty, and a
//     pipe / heredoc is not a tty. `/dev/tty` is the
//     canonical way to reach the controlling terminal
//     even when stdin was redirected (the typical case
//     in a non-interactive deploy script that does
//     `echo pw | aegis admin add`).
//  3. Restore the original terminal state on the way
//     out. `term.ReadPassword` does this internally but
//     defers the cleanup to the function exit; we wrap
//     it so a panic still leaves the terminal sane.
//
// When stdin is not a tty (e.g. CI piping a value),
// `term.IsTerminal(0)` is false and we fall through to
// the plain `bufio.Reader.ReadString` path. This keeps
// the v0.2-vintage "scriptable admin add" workflow
// working — `echo pw | aegis admin add user --email …`
// still completes without a tty.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		// Non-interactive stdin: the operator is
		// supplying the password via a pipe or
		// heredoc, so echo suppression is moot.
		// Read a single line and trim the newline.
		// This matches the v0.2 behaviour exactly so
		// the existing `aegis admin` automation
		// scripts in deploy/ keep working.
		reader := bufio.NewReader(os.Stdin)
		line, err := reader.ReadString('\n')
		if err != nil {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	tty, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	if err != nil {
		// /dev/tty is unavailable (containers
		// without a tty, Windows). Fall through
		// to the same non-interactive read so the
		// command still completes. The operator
		// sees the password in cleartext on the
		// terminal in that case — documented as a
		// known limitation of the windows-pipe
		// fallback.
		reader := bufio.NewReader(os.Stdin)
		line, readErr := reader.ReadString('\n')
		if readErr != nil {
			return "", readErr
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	defer func() { _ = tty.Close() }()
	// ReadPassword writes the prompt to the tty itself,
	// so the cursor sits at the right place on the
	// caller's terminal. We pass the tty as the output
	// fd and let ReadPassword handle the ECHOCTL/ICANON
	// toggling.
	pw, err := term.ReadPassword(int(tty.Fd()))
	if err != nil {
		return "", err
	}
	// term.ReadPassword consumes the trailing newline
	// from the line discipline but the bytes are
	// already trimmed. Print a newline so the next
	// shell prompt sits on its own line.
	fmt.Fprintln(os.Stderr)
	return string(pw), nil
}

func adminUsage() {
	fmt.Fprintln(os.Stderr, "usage: aegis admin <add|passwd|list> [args]")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "  aegis admin add     <username> --email <email> [--role <role>]")
	fmt.Fprintln(os.Stderr, "  aegis admin passwd  <username>")
	fmt.Fprintln(os.Stderr, "  aegis admin list")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The store is selected from AEGIS_AUTH_BACKEND (memory | pg).")
	fmt.Fprintln(os.Stderr, "The pg path requires AEGIS_POSTGRES_DSN.")
}

// singboxWiring is the v0.5.0 glue that connects the
// sing-box CoreProvider (registered in the process-global
// registry by the singbox package's init) to the
// panel's node store + an HTTP client, then creates
// one BatchedApplier per online node, spawns a Run()
// goroutine for each, and registers the appliers on
// the App (so users.Service / inbounds.Service can
// Enqueue deltas via WithBatchApplier).
//
// Each applier's FlushFn is the v0.5.0 real path:
//
//  1. BuildCoreConfigForNode(inbounds, nodeID) —
//     turn the inbounds table into a cores.CoreConfig.
//  2. p.RenderConfig(coreConfig) — render the sing-box
//     JSON from the CoreConfig.
//  3. p.Apply(ctx, nodeID, rendered) — POST the
//     rendered config to the agent's /v1/apply.
//
// The `AEGIS_BATCHED_APPLIER_ENABLED` flag (default
// true) gates the whole wiring. When false, the
// function returns nil after Configure() — no
// appliers, no Enqueue, no goroutines. Services
// that call WithBatchApplier get a nil-safe no-op
// (see users.enqueueUserDelta / inbounds.enqueueForNode).
//
// `ctx` is the boot context (the same one signal.NotifyContext
// returns in main). We derive each BatchedApplier's per-node
// context from it via context.WithCancel so that all the
// applier goroutines die together with the panel — the
// `contextcheck` linter would otherwise reject creating a
// bare `context.Background()` here.
func singboxWiring(
	ctx context.Context,
	p *singbox.Provider,
	a *app.App,
) error {
	// NodeResolver adapter: maps a node UUID to
	// (address, bearer). The address is the
	// host:port the agent listens on (the same
	// string the bootstrap installer wrote to
	// /etc/aegis/agent.env); the bearer is the
	// shared secret from the v0.4.0
	// agent_bearer column.
	resolver := &singboxNodeResolver{svc: a.Nodes}

	// Shared HTTP client. The singbox package's
	// newHTTPClient() sets a 30s per-request
	// timeout; the BatchedApplier's window
	// (default 20s) is the effective budget
	// because the apply request carries the
	// boot ctx (cancelled on shutdown).
	p.Configure(resolver, singbox.NewHTTPClient())

	// Hand the applier map to the services that
	// produce deltas. This must happen BEFORE the
	// loop below starts goroutines — otherwise a
	// Create handler that fires during boot would
	// enqueue into a half-built map.
	a.Users.WithBatchApplier(a.BatchedAppliers)
	a.Inbounds.WithBatchApplier(a.BatchedAppliers)

	// The applier map is always non-nil on App
	// (Build initialises it). When the feature
	// flag is off, we keep the map empty and
	// skip the per-node goroutines; the services
	// above iterate an empty map and Enqueue
	// becomes a no-op.
	if !a.Config.BatchedApplierEnabled {
		log.Warn().Msg("v0.5.0: BatchedApplier disabled (AEGIS_BATCHED_APPLIER_ENABLED=false); Apply will not be called from panel mutations")
		return nil
	}

	// Per-node BatchedApplier map. Built once
	// at boot from the current online node list;
	// the v0.5.0+ provisioning flow will add a
	// callback (node transitioned to online →
	// spawn Run for it) but for v0.5.0 we cover
	// the existing set.
	allNodes, err := a.Nodes.List(ctx)
	if err != nil {
		return fmt.Errorf("v0.5.0: list nodes: %w", err)
	}
	for _, n := range allNodes {
		if n.State != nodes.StateOnline {
			continue
		}
		// Capture loop variable for the closure.
		// The for-range over the slice (not map)
		// would let us drop the shadow, but the
		// linter's `loopclosure` check catches
		// it regardless.
		nodeID := n.ID
		nodeName := n.Name
		// FlushFn: the v0.5.0 real path.
		// Phase 1 / v0.5.0: the sing-box
		// renderer is single-user per inbound,
		// so the per-user deltas are advisory.
		// The flush always re-renders the full
		// config and applies it; the agent's
		// diff (future work) is what determines
		// whether the file on disk actually
		// changes.
		flushFn := builder.NewFlushFn(a.Inbounds, p, nodeID, nodeName)
		a.AddNodeBatchedApplier(ctx, nodeID, nodeName, flushFn)
	}
	return nil
}

// singboxNodeResolver is the adapter from the panel's
// nodes.Service to the singbox.NodeResolver interface.
// Lives in main.go (not in the singbox package) so the
// singbox package can stay free of any nodes import
// (which would create a cycle once the user-management
// layer pulls in both).
type singboxNodeResolver struct {
	svc *nodes.Service
}

// Resolve implements singbox.NodeResolver. The return
// values are named so the gocritic `unnamedResult` check
// is satisfied — the alternative (single-line `return
// n.Address, n.AgentBearer, nil`) is technically fine
// but flags the linter because the next reader cannot
// see at a glance which string is which.
func (r *singboxNodeResolver) Resolve(ctx context.Context, id uuid.UUID) (address, bearer string, err error) {
	n, err := r.svc.Get(ctx, id)
	if err != nil {
		return "", "", err
	}
	return n.Address, n.AgentBearer, nil
}
