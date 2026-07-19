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

	// Aegis Phase 1 — pre-declared runtime dependencies. These are pulled in
	// as blank imports so that `go mod tidy` keeps the corresponding
	// requirements in go.mod. They will be wired into real modules in
	// upcoming phases (auth/users → pgx, jwt, crypto, uuid; events → nats;
	// cache → redis; validation → validator; openapi → swag).
	_ "github.com/go-playground/validator/v10" // Phase 1 — input validation
	_ "github.com/golang-jwt/jwt/v5"           // Phase 1 — JWT (access + refresh tokens)
	_ "github.com/google/uuid"                 // Phase 1 — UUIDv4 generation
	_ "github.com/nats-io/nats.go"             // Phase 1 — event bus / JetStream
	_ "github.com/redis/go-redis/v9"           // Phase 1 — Redis client
	_ "github.com/swaggo/swag"                 // Phase 1 — OpenAPI generator

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/noop"
	_ "github.com/QAdversif/AegisPanel/internal/cores/singbox" // Phase 1 — real core provider (init() self-registers)
	"github.com/QAdversif/AegisPanel/internal/db"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/migrations"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/obs"
	"github.com/QAdversif/AegisPanel/internal/panelcfg"
	"github.com/QAdversif/AegisPanel/internal/ratelimit"
	"github.com/QAdversif/AegisPanel/internal/router"
	"github.com/QAdversif/AegisPanel/internal/subscription"
)

func main() {
	// Pretty console output in dev, structured JSON in prod.
	if os.Getenv("AEGIS_ENV") != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	}

	// `aegis migrate …` is a maintenance subcommand that
	// runs before the rest of the boot sequence. It does not
	// touch the rest of config (env, observability, …) on
	// purpose: a migrations command should not require a
	// fully-initialised runtime to run.
	if len(os.Args) >= 2 && os.Args[1] == "migrate" {
		runMigrate(os.Args[2:])
		return
	}

	// `aegis admin …` is a second maintenance subcommand
	// for managing the panel principals. It needs the
	// auth.Store (so it can hash + persist) but does not
	// need the HTTP server or observability. Same
	// rationale as `migrate`: a maintenance command
	// should not require a fully-booted panel.
	if len(os.Args) >= 2 && os.Args[1] == "admin" {
		runAdmin(os.Args[2:])
		return
	}

	// Top-level context for boot-time operations. Cancelled when
	// the process receives SIGINT / SIGTERM (see signal.NotifyContext
	// below). The cancel is registered as a defer *after* the early
	// log.Fatal() call sites so that exitAfterDefer (gocritic) does
	// not flag the boot sequence — log.Fatal calls os.Exit, which
	// skips defers anyway, so it is safe to register later.
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

	// All boot-time resources are now live — safe to register the
	// signal-context cancel so graceful shutdown actually runs.
	defer cancelBoot()

	// In dev builds, register the noop core provider so the
	// UI can talk to /api/v1/cores before any real provider
	// (sing-box, xray, …) has been wired in. In production we
	// expect a real provider to have self-registered via its
	// own init() — adding noop there would shadow it.
	if cfg.Env != "production" {
		if err := cores.Register(noop.New("noop", "0.0.0-dev")); err != nil {
			// Duplicate registration (e.g. a test that already
			// inserted noop) is benign — log and move on.
			log.Debug().Err(err).Msg("cores: noop already registered")
		} else {
			log.Info().Msg("cores: registered noop provider (dev mode)")
		}
	}

	log.Info().
		Str("version", "0.0.0-dev").
		Str("commit", cfg.GitCommit).
		Str("env", cfg.Env).
		Msg("aegis panel starting")

	// 3. Open the PostgreSQL pool. Every service that uses
	//    AEGIS_*_BACKEND=pg shares the same pool; the
	//    MemoryStore backends never touch it. Opening is
	//    lazy: if no service is configured for pg, we skip
	//    the connection entirely. A misconfigured DSN (or
	//    an unreachable server) fails the boot here, not on
	//    the first query, thanks to the Ping inside db.Open.
	var (
		pool    *pgxpool.Pool
		needsPg = cfg.AuthBackend == "pg" ||
			cfg.HostsBackend == "pg" ||
			cfg.NodesBackend == "pg" ||
			cfg.InboundsBackend == "pg" ||
			cfg.SubscriptionBackend == "pg" ||
			cfg.PanelcfgBackend == "pg" ||
			cfg.AuditsBackend == "pg"
	)
	if needsPg {
		p, err := db.Open(ctx, cfg.PostgresDSN)
		if err != nil {
			log.Fatal().Err(err).Msg("db: failed to open postgres connection pool")
		}
		pool = p
		defer pool.Close()
		// Apply migrations on the same pool the runtime
		// uses. We deliberately do NOT open a sibling
		// *sql.DB through the pgx stdlib adapter: that
		// adapter does not honour multi-statement
		// transactions, and Aegis migrations rely on
		// BEGIN; ... COMMIT; in each file.
		if err := migrations.Up(ctx, pool, "migrations"); err != nil {
			log.Fatal().Err(err).Msg("migrations: failed to apply")
		}
	}

	// 4. Build the auth service. The backing store is
	//    selected at startup:
	//      AEGIS_AUTH_BACKEND=memory  -> MemoryStore (Phase 0 default)
	//      AEGIS_AUTH_BACKEND=pg      -> PgStore backed by the shared pool
	authSigner := auth.NewSigner(cfg.JWTSecret)
	var authStore auth.Store
	switch cfg.AuthBackend {
	case "pg":
		authStore = auth.NewPgStore(pool)
		log.Info().Msg("auth: using pgx-backed store (PgStore)")
	default:
		// Dev seed admin. Phase 0 only — production
		// uses the pg backend with a real operator
		// minted via `aegis admin add <username>`.
		// Refuse to boot in production with the dev
		// password: a real install must not run with
		// a known-public credential.
		if cfg.Env == "production" {
			log.Fatal().Msg("auth: cannot start in production with the dev-only MemoryStore; set AEGIS_AUTH_BACKEND=pg and run `aegis admin add` to seed the first operator")
		}
		authStore = auth.NewMemoryStore().WithUser(&auth.User{
			ID:           "u-bootstrap",
			Username:     "admin",
			Email:        "admin@localhost",
			PasswordHash: mustHash("aegis-dev-password"),
			Role:         "super-admin",
			Enabled:      true,
			Scopes:       auth.Scopes{auth.ScopeAdmin, auth.ScopeRead, auth.ScopeWrite, auth.ScopeNodes, auth.ScopeUsers, auth.ScopeSubscriptions, auth.ScopeHosts, auth.ScopeAudits},
			CreatedAt:    time.Now().UTC(),
		})
		log.Warn().Msg("auth: using in-memory store with the dev seed (username: admin, password: aegis-dev-password). DO NOT use in production.")
	}
	authSvc := auth.NewService(authSigner, authStore)

	// 5. Nodes service persistence layer:
	//    AEGIS_NODES_BACKEND=memory (default) uses the
	//    Phase 0 MemoryStore; =pg uses PgStore backed by
	//    the shared pool and the `nodes` / `node_tags`
	//    tables (migrations 0001 + 0005).
	var nodesStore nodes.Store
	switch cfg.NodesBackend {
	case "pg":
		nodesStore = nodes.NewPgStore(pool)
		log.Info().Msg("nodes: using pgx-backed store (PgStore)")
	default:
		nodesStore = nodes.NewMemoryStore()
		log.Info().Msg("nodes: using in-memory store (MemoryStore, dev only)")
	}
	nodesSvc := nodes.NewService(nodesStore)

	// 6. Inbounds service also references nodes (every
	//    inbound belongs to a node). The backend is
	//    selected the same way as the nodes / hosts
	//    services: AEGIS_INBOUNDS_BACKEND=memory (default)
	//    uses the Phase 0 MemoryStore; =pg uses PgStore
	//    backed by the shared pool and the `inbounds`
	//    table (migration 0003).
	var inboundsStore inbounds.Store
	switch cfg.InboundsBackend {
	case "pg":
		inboundsStore = inbounds.NewPgStore(pool)
		log.Info().Msg("inbounds: using pgx-backed store (PgStore)")
	default:
		inboundsStore = inbounds.NewMemoryStore()
		log.Info().Msg("inbounds: using in-memory store (MemoryStore, dev only)")
	}
	inboundsSvc := inbounds.NewService(inboundsStore, nodesSvc)

	// 7. Hosts service references nodes AND inbounds (every
	//    endpoint is a (Node, Inbound) pair), so it is
	//    constructed after both. AEGIS_HOSTS_BACKEND=pg
	//    uses PgStore backed by the shared pool and the
	//    `hosts` / `host_endpoints` tables (migration
	//    0004).
	var hostsStore hosts.Store
	switch cfg.HostsBackend {
	case "pg":
		hostsStore = hosts.NewPgStore(pool)
		log.Info().Msg("hosts: using pgx-backed store (PgStore)")
	default:
		hostsStore = hosts.NewMemoryStore()
		log.Info().Msg("hosts: using in-memory store (MemoryStore, dev only)")
	}
	hostsSvc := hosts.NewService(hostsStore, nodesSvc, inboundsSvc)

	// 8. Subscription service. The Phase 0 package
	//    ships a MemoryStore and a base64 renderer; the
	//    HTTP handler lands with the next PR. The
	//    service is constructed here so the boot path
	//    validates the wiring (e.g. the cross-service
	//    pointer dance does not nil-deref) and the
	//    subsequent PRs can call into it without a
	//    main.go change.
	//
	//    Backend is selected at startup:
	//      AEGIS_SUBSCRIPTION_BACKEND=memory (default) uses
	//      the Phase 0 MemoryStore; =pg uses PgStore backed
	//      by the shared pool and the `users`, `plans`,
	//      `plan_pool`, `host_pools`, `host_pool_members`
	//      tables (migrations 0001 + 0011).
	var subscriptionStore subscription.Store
	switch cfg.SubscriptionBackend {
	case "pg":
		subscriptionStore = subscription.NewPgStore(pool)
		log.Info().Msg("subscription: using pgx-backed store (PgStore)")
	default:
		subscriptionStore = subscription.NewMemoryStore()
		log.Info().Msg("subscription: using in-memory store (MemoryStore, dev only)")
	}
	subscriptionSvc := subscription.NewService(subscriptionStore, hostsSvc, nodesSvc, inboundsSvc)

	// Panel-wide config (the rotating URL prefix).
	// Backend is selected at startup:
	//   AEGIS_PANELCFG_BACKEND=memory (default) uses the
	//   Phase 0 MemoryStore; =pg uses PgStore backed by
	//   the shared pool and the `panel_path_config`
	//   table (migration 0010).
	var panelCfgStore panelcfg.Store
	switch cfg.PanelcfgBackend {
	case "pg":
		panelCfgStore = panelcfg.NewPgStore(pool)
		log.Info().Msg("panelcfg: using pgx-backed store (PgStore)")
	default:
		panelCfgStore = panelcfg.NewMemoryStore()
		log.Info().Msg("panelcfg: using in-memory store (MemoryStore, dev only)")
	}
	panelCfgSvc := panelcfg.NewService(panelCfgStore)

	// Audit log service. The v0.2.0 surface is
	// read-only (the GET /api/v1/audits and
	// GET /api/v1/audits/{id} endpoints); the
	// write path (Service.Record) is exported for
	// the v0.3+ wiring that will be added to the
	// nodes / hosts / inbounds / users / panelcfg
	// mutating handlers.
	//
	// Backend is selected at startup:
	//   AEGIS_AUDITS_BACKEND=memory (default) uses
	//   the Phase 0 MemoryStore; =pg uses PgStore
	//   backed by the existing `audit_log` table
	//   from migration 0001.
	var auditsStore audits.Store
	switch cfg.AuditsBackend {
	case "pg":
		auditsStore = audits.NewPgStore(pool)
		log.Info().Msg("audits: using pgx-backed store (PgStore)")
	default:
		auditsStore = audits.NewMemoryStore()
		log.Info().Msg("audits: using in-memory store (MemoryStore, dev only)")
	}
	auditsSvc := audits.NewService(auditsStore)

	// Subscription endpoint rate limiter. One
	// instance shared across the default and the
	// rotated sub_path mount - a stolen sub_token
	// is therefore rate-limited regardless of which
	// URL the caller uses. v0.3 swaps the in-memory
	// map for Redis + TTL (the panel is small enough
	// today that the in-memory cap of 50k keys is
	// not a real concern). The limiter is created
	// before the HTTP server so the first request
	// already has a budget allocated.
	subLimiter := newSubscriptionRateLimiter(cfg)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           obs.Middleware(router.Build(cfg, authSvc, nodesSvc, hostsSvc, inboundsSvc, subscriptionSvc, panelCfgSvc, auditsSvc, nil /* bootstrapSvc — wired in v0.3.0 PR */, subLimiter)),
	}

	// 8. Run the server in a goroutine so we can listen for signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// 9. Wait for SIGINT / SIGTERM or a fatal server error.
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

	// 10. Graceful shutdown with a hard deadline.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Error().Err(err).Msg("graceful shutdown failed")
	}

	log.Info().Msg("aegis panel stopped")
}

// mustHash is a tiny panic-on-error wrapper around argon2id for the
// Phase 0 dev seed. Phase 1+ reads hashes from the database.
func mustHash(plaintext string) string {
	h, err := auth.HashPassword(plaintext)
	if err != nil {
		panic(fmt.Errorf("seed hash: %w", err))
	}
	return h
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
	users, err := svc.ListUsers(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("admin list: failed")
	}
	if len(users) == 0 {
		log.Info().Msg("admin list: no users")
		return
	}
	for _, u := range users {
		log.Info().
			Str("id", u.ID).
			Str("username", u.Username).
			Str("email", u.Email).
			Str("role", u.Role).
			Bool("enabled", u.Enabled).
			Msg("admin")
	}
}

// promptPassword reads a single line from stdin. v0.3
// will replace this with a /dev/tty-echo-suppressed
// reader that hides the password on the terminal (the
// `passwd(1)` UX). For v0.2 the CLI is good enough for
// scripted operators and the dev seed.
func promptPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}

// newSubscriptionRateLimiter builds the per-sub_token
// rate limiter the HTTP layer hands to
// subscription.RouterWithLimiter. The settings are
// taken from cfg; a non-positive RPS disables
// throttling (the v0.1.0 behaviour).
//
// Defaults (1 rps, 5 burst, 50k keys) are tuned for a
// single-user-with-multiple-devices usage model: a
// phone + laptop + tablet + desktop can all wake up
// at once after a 24h client poll cycle and still fit
// inside the burst budget.
func newSubscriptionRateLimiter(cfg *config.Config) *ratelimit.Limiter {
	if cfg.SubscriptionRateLimitRPS <= 0 {
		log.Info().Msg("subscription rate limiter disabled (AEGIS_SUBSCRIPTION_RATELIMIT_RPS <= 0)")
		return nil
	}
	l := ratelimit.New(
		cfg.SubscriptionRateLimitRPS,
		cfg.SubscriptionRateLimitBurst,
		10*time.Minute, // idle: a stale token gets a fresh burst on first re-use
	)
	if cfg.SubscriptionRateLimitMaxKeys > 0 {
		l.SetMaxKeys(cfg.SubscriptionRateLimitMaxKeys)
	}
	log.Info().
		Float64("rps", cfg.SubscriptionRateLimitRPS).
		Float64("burst", cfg.SubscriptionRateLimitBurst).
		Int("max_keys", cfg.SubscriptionRateLimitMaxKeys).
		Msg("subscription rate limiter enabled")
	return l
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
