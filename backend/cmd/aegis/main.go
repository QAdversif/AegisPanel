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
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
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

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/noop"
	"github.com/QAdversif/AegisPanel/internal/migrations"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/obs"
	"github.com/QAdversif/AegisPanel/internal/router"
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

	// 3. Build the auth service. The backing store is selected
	//    at startup:
	//      AEGIS_AUTH_BACKEND=memory  -> MemoryStore (Phase 0 default)
	//      AEGIS_AUTH_BACKEND=pg      -> PgStore backed by pgxpool
	//    The pg backend also runs goose migrations on boot so a
	//    fresh database is ready for /api/v1/auth/{login,refresh}.
	authSigner := auth.NewSigner(cfg.JWTSecret)
	var authStore auth.Store
	switch cfg.AuthBackend {
	case "pg":
		pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
		if err != nil {
			log.Fatal().Err(err).Msg("pgxpool: failed to open postgres connection")
		}
		defer pool.Close()
		// Apply migrations on the same pool the runtime uses. We
		// deliberately do NOT open a sibling *sql.DB through the
		// pgx stdlib adapter: that adapter does not honour
		// multi-statement transactions, and Aegis migrations
		// rely on BEGIN; ... COMMIT; in each file.
		if err := migrations.Up(ctx, pool, "migrations"); err != nil {
			log.Fatal().Err(err).Msg("migrations: failed to apply")
		}
		authStore = auth.NewPgStore(pool)
		log.Info().Msg("auth: using pgx-backed store (PgStore)")
	default:
		authStore = auth.NewMemoryStore().WithUser(&auth.User{
			ID:       "u-bootstrap",
			Username: "admin",
			// Dev seed password — Phase 0 only. Phase 1+ forces a
			// password change on first login and reads from pgx.
			PasswordHash: mustHash("aegis-dev-password"),
			Scopes:       auth.Scopes{auth.ScopeAdmin, auth.ScopeRead, auth.ScopeWrite},
			CreatedAt:    time.Now().UTC(),
		})
		log.Info().Msg("auth: using in-memory store (MemoryStore, dev only)")
	}
	authSvc := auth.NewService(authSigner, authStore)

	// 4. Build the HTTP server with the v1 router.
	//
	// The nodes service uses an in-memory store in Phase 0;
	// the persistence layer is added in Phase 1 when nodes
	// actually start registering themselves.
	nodesSvc := nodes.NewService(nodes.NewMemoryStore())

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           obs.Middleware(router.Build(cfg, authSvc, nodesSvc)),
	}

	// 4. Run the server in a goroutine so we can listen for signals.
	serverErr := make(chan error, 1)
	go func() {
		log.Info().Str("addr", cfg.HTTPAddr).Msg("HTTP server listening")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	// 5. Wait for SIGINT / SIGTERM or a fatal server error.
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

	// 6. Graceful shutdown with a hard deadline.
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

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.Fatal().Err(err).Msg("migrate: pgxpool.New")
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
