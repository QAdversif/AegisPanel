// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis entry point.
//
// Aegis is a self-hosted VPN control panel that orchestrates a fleet of
// BYO nodes (running sing-box / Xray / Hysteria 2) via SSH, exposes a
// REST API for the admin UI, and renders multi-format subscription
// configurations for end-user VPN clients.
//
// Architecture is documented in ../ARCHITECTURE.md.

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
	// cache → redis; validation → validator; migrations → goose; openapi → swag).
	_ "github.com/go-playground/validator/v10" // Phase 1 — input validation
	_ "github.com/golang-jwt/jwt/v5"           // Phase 1 — JWT (access + refresh tokens)
	_ "github.com/google/uuid"                  // Phase 1 — UUIDv4 generation
	_ "github.com/jackc/pgx/v5/stdlib"          // Phase 1.1 — sql driver for goose
	_ "github.com/nats-io/nats.go"              // Phase 1 — event bus / JetStream
	_ "github.com/pressly/goose/v3"             // Phase 1.1 — SQL migrations
	_ "github.com/redis/go-redis/v9"            // Phase 1 — Redis client
	_ "github.com/swaggo/swag"                  // Phase 1 — OpenAPI generator
	"golang.org/x/crypto/bcrypt"                // Phase 1 — password hashing (auth seeds)

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"database/sql"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pressly/goose/v3"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/obs"
	"github.com/QAdversif/AegisPanel/internal/router"
)

func main() {
	// Pretty console output in dev, structured JSON in prod.
	if os.Getenv("AEGIS_ENV") != "production" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}).
			With().Timestamp().Logger()
	}

	// Top-level context for boot-time operations. Cancelled when
	// the process receives SIGINT / SIGTERM (see signal.NotifyContext
	// below).
	ctx, cancelBoot := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelBoot()

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
		// Goose wants *sql.DB, so we open a sibling connection
		// through the pgx stdlib adapter. The pool above is the
		// runtime handle for the auth store; the sql.DB is just
		// for migrations and is closed as soon as Up returns.
		migDB, err := sql.Open("pgx", cfg.PostgresDSN)
		if err != nil {
			log.Fatal().Err(err).Msg("sql.Open: failed to open migration connection")
		}
		if err := runMigrations(ctx, migDB, "migrations"); err != nil {
			migDB.Close()
			log.Fatal().Err(err).Msg("migrations: failed to apply")
		}
		migDB.Close()
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
	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           obs.Middleware(router.Build(cfg, authSvc)),
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

// mustHash is a tiny panic-on-error wrapper around bcrypt for the
// Phase 0 dev seed. Phase 1+ reads hashes from the database.
func mustHash(plaintext string) []byte {
	h, err := bcrypt.GenerateFromPassword([]byte(plaintext), bcrypt.DefaultCost)
	if err != nil {
		panic(fmt.Errorf("seed hash: %w", err))
	}
	return h
}

// runMigrations applies all "*.sql" files under dir in lexical
// order using goose. db is the *sql.DB destination; goose borrows
// it for the duration of the migration run.
func runMigrations(ctx context.Context, db *sql.DB, dir string) error {
	goose.SetBaseFS(os.DirFS("."))
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, db, dir); err != nil {
		return fmt.Errorf("goose up: %w", err)
	}
	return nil
}
