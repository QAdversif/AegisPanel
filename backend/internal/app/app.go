// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package app is the composition root for the
// Aegis panel. It owns the wiring that used to
// live in `cmd/aegis/main.go` and exposes a single
// `Build` function that returns a fully wired
// `*App`. The cmd/ binary then does signal
// handling, subcommand dispatch (`aegis migrate`,
// `aegis admin`), and the http.Server lifecycle
// around the `App`.
//
// # Why a struct, not 11 return values
//
// The original wiring had 11 services and a rate
// limiter returned as positional values. A
// struct means callers can name what they need
// (`a.Auth`, `a.Router`, `a.Server`) and tests
// can mock a single field without faking the rest.
//
// # Why a generic `MustBuild[T]`
//
// The nine `switch cfg.XBackend { case "pg": ...
// default: ... }` blocks that lived in main.go
// were the worst offender in the audit. The
// helper lives in `stores.go` and is generic so
// the result is already the right concrete type
// (no `any` + type-assertion). The constructors
// in the per-service packages return concrete
// pointer types (`*nodes.PgStore`, `*nodes.MemoryStore`)
// so the caller wraps them in a closure that
// returns the matching interface — Go generics
// do not allow covariance, only invariance.
//
// # What does not live here
//
//   - The `migrate` and `admin` subcommands stay
//     in `cmd/aegis/main.go`. They are maintenance
//     operations that should not require the full
//     service graph; only the pg pool is needed.
//   - The `singboxWiring` helper (per-node
//     BatchedApplier goroutines tied to the boot
//     ctx) stays in main.go. Lifting it into a
//     method on App would force an `io.Closer`
//     pattern the rest of the services do not need.
package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/backups"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/cores/noop"
	"github.com/QAdversif/AegisPanel/internal/credentials"
	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
	"github.com/QAdversif/AegisPanel/internal/db"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/inboundtemplates"
	"github.com/QAdversif/AegisPanel/internal/migrations"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/obs"
	"github.com/QAdversif/AegisPanel/internal/panelcfg"
	"github.com/QAdversif/AegisPanel/internal/plans"
	"github.com/QAdversif/AegisPanel/internal/ratelimit"
	"github.com/QAdversif/AegisPanel/internal/router"
	"github.com/QAdversif/AegisPanel/internal/subscription"
	"github.com/QAdversif/AegisPanel/internal/users"
	"github.com/QAdversif/AegisPanel/internal/webhooks"

	"github.com/google/uuid"
)

// App is the wired panel. The zero value is not
// usable; obtain one via Build. The struct holds
// the pg pool, every service handle, the
// subscription rate limiter, the http.Server, and
// the per-node BatchedApplier map (one per online
// node; populated only when cfg.BatchedApplierEnabled
// is true, otherwise an empty map). Close releases
// the pool, stops the webhook retry worker, and
// cancels every BatchedApplier goroutine.
type App struct {
	Config *config.Config
	Pool   *pgxpool.Pool

	Auth     *auth.Service
	Nodes    *nodes.Service
	Hosts    *hosts.Service
	Inbounds *inbounds.Service
	// InboundTemplates is the v0.8.x per-tenant
	// `Params` defaults layer — named, reusable
	// protocol configurations that any number of
	// `inbounds` rows can reference via the
	// nullable `inbounds.template_id` FK. The
	// storage backend is shared with Inbounds
	// (no separate AEGIS_INBOUND_TEMPLATES_BACKEND
	// env var); see the StoreBuilder call below.
	InboundTemplates *inboundtemplates.Service
	Users            *users.Service
	Plans            *plans.Service
	Subs             *subscription.Service
	PanelCfg         *panelcfg.Service
	Audits           *audits.Service
	Backups          *backups.Service
	Webhooks         *webhooks.Service
	Bootstrap        *bootstrap.Service

	// Credentials is the Phase 2 multi-user
	// sing-box render data model: the
	// per-(user, inbound) credential join. The
	// service is wired in v0.7.x but the
	// read paths (the renderer, the builder)
	// stay on the Phase 1 single-credential
	// path until the follow-up PRs land. See
	// internal/credentials/credentials.go for
	// the full rationale.
	Credentials *credentials.Service

	SubLimiter *ratelimit.Limiter
	Router     http.Handler
	Server     *http.Server

	// BatchedAppliers is keyed by node UUID. The
	// map is non-nil even when the feature flag is
	// off — services that call WithBatchApplier
	// iterate it on every mutation, and an empty
	// map is a no-op. Mutating handlers
	// (users.Service, inbounds.Service) hold a
	// reference via WithBatchApplier and Enqueue
	// into every value when their state changes.
	BatchedAppliers map[uuid.UUID]*cores.BatchedApplier

	webhooksWorkerCancel  context.CancelFunc
	batchedApplierCancels map[uuid.UUID]context.CancelFunc
}

// Build runs the composition root: open the pg
// pool when at least one service is configured
// for it, apply migrations, build every store
// and every service, wire the v0.7.x outbound
// event surface, start the webhook retry worker,
// and return a fully wired App. The cmd/ binary
// owns the http.Server lifecycle and the SIGINT
// handling.
func Build(ctx context.Context, cfg *config.Config) (*App, error) {
	logger := log.Logger

	// 1. Wire the noop core provider in dev. In
	//    production a real provider self-registers
	//    via its own init() and we leave the
	//    registry alone.
	if cfg.Env != "production" {
		if err := cores.Register(noop.New("noop", "0.0.0-dev")); err != nil {
			// Duplicate registration (a test that
			// already inserted noop) is benign.
			logger.Debug().Err(err).Msg("cores: noop already registered")
		} else {
			logger.Info().Msg("cores: registered noop provider (dev mode)")
		}
	}

	// 2. Open the pg pool when at least one
	//    service needs it. If no service is
	//    configured for pg, skip the connection
	//    entirely so a dev run with only memory
	//    stores does not require a live database.
	pool, err := openPgPoolIfNeeded(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if pool != nil {
		// Apply migrations on the same pool the
		// runtime uses. We deliberately do NOT
		// open a sibling *sql.DB through the pgx
		// stdlib adapter: that adapter does not
		// honour multi-statement transactions,
		// and Aegis migrations rely on
		// BEGIN; ... COMMIT; in each file.
		if err := migrations.Up(ctx, pool, "migrations"); err != nil {
			pool.Close()
			return nil, fmt.Errorf("migrations: failed to apply: %w", err)
		}
	} else {
		logger.Info().Msg("db: all stores in memory; skipping pg pool")
	}

	a := &App{
		Config:                cfg,
		Pool:                  pool,
		BatchedAppliers:       make(map[uuid.UUID]*cores.BatchedApplier),
		batchedApplierCancels: make(map[uuid.UUID]context.CancelFunc),
	}

	// 3. Auth. The dev seed is only allowed in
	//    non-production; a production boot with
	//    the memory auth store is refused so a
	//    known-public credential cannot ship.
	authSigner := auth.NewSigner(cfg.JWTSecret)
	authStore := MustBuild(pool, StoreBuilder[auth.Store]{
		Name:    "auth",
		Backend: cfg.AuthBackend,
		PgCtor:  func(p *pgxpool.Pool) auth.Store { return auth.NewPgStore(p) },
		MemCtor: func() auth.Store {
			// The dev seed admin. MustBuild's
			// production check refuses this branch
			// when Env == "production".
			return auth.NewMemoryStore().WithUser(&auth.User{
				ID:           "u-bootstrap",
				Username:     "admin",
				Email:        "admin@localhost",
				PasswordHash: mustHashDevPassword(),
				Role:         "super-admin",
				Enabled:      true,
				Scopes: auth.Scopes{
					auth.ScopeAdmin, auth.ScopeRead, auth.ScopeWrite,
					auth.ScopeNodes, auth.ScopeUsers, auth.ScopeSubscriptions,
					auth.ScopeHosts, auth.ScopeAudits,
				},
				CreatedAt: time.Now().UTC(),
			})
		},
		Env: cfg.Env,
	})
	if cfg.AuthBackend != "pg" {
		logger.Warn().Msg("auth: using in-memory store with the dev seed (username: admin, password: aegis-dev-password). DO NOT use in production.")
	}
	a.Auth = auth.NewService(authSigner, authStore)

	// 4. Nodes.
	nodesStore := MustBuild(pool, StoreBuilder[nodes.Store]{
		Name:    "nodes",
		Backend: cfg.NodesBackend,
		PgCtor:  func(p *pgxpool.Pool) nodes.Store { return nodes.NewPgStore(p) },
		MemCtor: func() nodes.Store { return nodes.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Nodes = nodes.NewService(nodesStore)

	// 5. Inbounds references nodes.
	inboundsStore := MustBuild(pool, StoreBuilder[inbounds.Store]{
		Name:    "inbounds",
		Backend: cfg.InboundsBackend,
		PgCtor:  func(p *pgxpool.Pool) inbounds.Store { return inbounds.NewPgStore(p) },
		MemCtor: func() inbounds.Store { return inbounds.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Inbounds = inbounds.NewService(inboundsStore, a.Nodes)

	// v0.8.x: inbound templates — the per-tenant
	// `Params` defaults layer. Shares the inbounds
	// storage backend (no separate env var) so the
	// feature flips on/off with the same flag the
	// operator already knows.
	inboundTemplatesStore := MustBuild(pool, StoreBuilder[inboundtemplates.Store]{
		Name:    "inbound_templates",
		Backend: cfg.InboundsBackend,
		PgCtor:  func(p *pgxpool.Pool) inboundtemplates.Store { return inboundtemplates.NewPgStore(p) },
		MemCtor: func() inboundtemplates.Store { return inboundtemplates.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.InboundTemplates = inboundtemplates.NewService(inboundTemplatesStore)

	// 6. Hosts references nodes + inbounds.
	hostsStore := MustBuild(pool, StoreBuilder[hosts.Store]{
		Name:    "hosts",
		Backend: cfg.HostsBackend,
		PgCtor:  func(p *pgxpool.Pool) hosts.Store { return hosts.NewPgStore(p) },
		MemCtor: func() hosts.Store { return hosts.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Hosts = hosts.NewService(hostsStore, a.Nodes, a.Inbounds)

	// 7. Users.
	usersStore := MustBuild(pool, StoreBuilder[users.Store]{
		Name:    "users",
		Backend: cfg.UsersBackend,
		PgCtor:  func(p *pgxpool.Pool) users.Store { return users.NewPgStore(p) },
		MemCtor: func() users.Store { return users.NewMemoryStore(nil) },
		Env:     cfg.Env,
	})
	a.Users = users.NewService(usersStore)
	// v0.8.x: wire the host→node lookup so
	// `users.Service.enqueueUserDelta` can expand
	// `User.HostsAllowlist` (host IDs, per the
	// architecture) into node IDs the BatchedApplier
	// fan-out matches against. See
	// `docs/comparison/remnawave.md:118-119` and
	// the v0.8.x builder TODO at
	// `builder.go:32-41` (now resolved).
	a.Users.WithHosts(a.Hosts)

	// 8. Plans.
	plansStore := MustBuild(pool, StoreBuilder[plans.Store]{
		Name:    "plans",
		Backend: cfg.PlansBackend,
		PgCtor:  func(p *pgxpool.Pool) plans.Store { return plans.NewPgStore(p) },
		MemCtor: func() plans.Store { return plans.NewMemoryStore(nil) },
		Env:     cfg.Env,
	})
	a.Plans = plans.NewService(plansStore)

	// 9. Build the age envelope. v0.8.x: the same
	//    cipher is shared between webhooks (for
	//    endpoint secrets) and bootstrap (for the
	//    panel's persistent node SSH key). We
	//    pick the implementation based on the
	//    webhooks backend (the same `pg` /
	//    `memory` switch the panel has used
	//    since v0.7.0) and share the result with
	//    the bootstrap service later in this
	//    function. Memory mode uses the
	//    `envelope.NoopSecretCipher` (dev only,
	//    same plaintext-on-DB caveat as the v0.7.0
	//    webhook memory store).
	var (
		cipher        envelope.SecretCipher
		webhooksStore webhooks.Store
	)
	switch cfg.WebhooksBackend {
	case "pg":
		ageCipher, err := envelope.NewAgeSecretCipher(
			cfg.WebhooksSecretAgeRecipients,
			cfg.WebhooksSecretAgeKeyFile,
		)
		if err != nil {
			return nil, fmt.Errorf("webhooks: failed to build age secret cipher: %w", err)
		}
		cipher = ageCipher
		webhooksStore = webhooks.NewPgStore(pool, cipher)
		logger.Info().
			Int("recipients", len(cfg.WebhooksSecretAgeRecipients)).
			Str("key_file", cfg.WebhooksSecretAgeKeyFile).
			Msg("webhooks: using pgx-backed store (PgStore, age-encrypted secret)")
	default:
		cipher = envelope.NewNoopSecretCipher()
		webhooksStore = webhooks.NewMemoryStore()
		logger.Warn().Msg("webhooks: using in-memory store (MemoryStore, dev only — secret is plaintext)")
	}
	a.Webhooks = webhooks.NewService(webhooksStore)

	// v0.8.5: wire the same age envelope the
	// webhooks Store uses into the nodes
	// Service. The `nodes.stored-key.read`
	// endpoint decrypts
	// `nodes.ssh_private_key_ciphertext` via
	// this envelope; without it, the handler
	// returns 500 ("envelope is not
	// configured"). The memory-mode fallback
	// (`envelope.NewNoopSecretCipher`) is the
	// dev / unencrypted case; the rotate-
	// panel-key CLI uses the same
	// `AEGIS_WEBHOOKS_SECRET_AGE_*` env vars
	// the panel main does, so the production
	// shape is identical on both sides.
	a.Nodes.WithEnvelope(cipher)
	// v0.8.7: wire the v0.8.7
	// refresh-agent-bearer dependencies.
	// The handler refuses to run when
	// the SSH client factory is nil
	// (returns 500 "SSH client
	// factory is not configured"), so
	// the production wiring must call
	// this setter at boot. The same
	// `bootstrap.NewClient` is used by
	// the v0.3.0 install path and the
	// v0.8.4 rotate-panel-key handler;
	// sharing the constructor means a
	// single round of TOFU + known_hosts
	// logic serves every SSH path.
	a.Nodes.WithSSHClientFactory(bootstrap.NewClient)
	a.Nodes.WithKnownHosts(cfg.AgentKnownHosts)
	a.Nodes.WithSSHUser(cfg.AgentSSHUser)

	// 10. Wire the v0.7.x outbound event surface
	//     into every mutating service. The setter
	//     is preferred over a constructor argument
	//     so the existing unit tests stay
	//     untouched. Order does not matter: the
	//     dispatch is invoked AFTER the row is
	//     persisted. `Backups` is wired below, after
	//     the backups New().
	a.Nodes.WithWebhooks(a.Webhooks)
	a.Inbounds.WithWebhooks(a.Webhooks)
	a.InboundTemplates.WithWebhooks(a.Webhooks)
	a.Hosts.WithWebhooks(a.Webhooks)
	a.Users.WithWebhooks(a.Webhooks)
	a.Plans.WithWebhooks(a.Webhooks)

	// 11. Background retry worker.
	if cfg.WebhooksRetryWorkerEnabled {
		workerCtx, cancel := context.WithCancel(ctx)
		w := webhooks.NewWorker(a.Webhooks, cfg.WebhooksRetryWorkerInterval)
		go func() {
			defer cancel()
			if err := w.Run(workerCtx); err != nil {
				log.Error().Err(err).Msg("webhooks: retry worker exited")
			}
		}()
		a.webhooksWorkerCancel = cancel
		logger.Info().
			Dur("interval", cfg.WebhooksRetryWorkerInterval).
			Msg("webhooks: retry worker started")
	} else {
		logger.Warn().Msg("webhooks: retry worker DISABLED (AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED=false); retries must be fired manually")
	}

	// 12. Subscription.
	subscriptionStore := MustBuild(pool, StoreBuilder[subscription.Store]{
		Name:    "subscription",
		Backend: cfg.SubscriptionBackend,
		PgCtor:  func(p *pgxpool.Pool) subscription.Store { return subscription.NewPgStore(p) },
		MemCtor: func() subscription.Store { return subscription.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Subs = subscription.NewService(subscriptionStore, a.Hosts, a.Nodes, a.Inbounds)
	// v0.7.x deferred: Phase 2 multi-user render.
	// The subscription service's per-(user, inbound)
	// credential lookup needs a credentials source;
	// without one the renderer falls back to the
	// v0.7.2 params-based single-credential path
	// (see internal/subscription/service.go
	// WithCreds docstring). Wired AFTER Credentials
	// is built (the order in this function is the
	// same order as the step numbers).
	a.Subs.WithCreds(a.Credentials)

	// 13. Panel-wide config.
	panelCfgStore := MustBuild(pool, StoreBuilder[panelcfg.Store]{
		Name:    "panelcfg",
		Backend: cfg.PanelcfgBackend,
		PgCtor:  func(p *pgxpool.Pool) panelcfg.Store { return panelcfg.NewPgStore(p) },
		MemCtor: func() panelcfg.Store { return panelcfg.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.PanelCfg = panelcfg.NewService(panelCfgStore)

	// 14. Audit log.
	auditsStore := MustBuild(pool, StoreBuilder[audits.Store]{
		Name:    "audits",
		Backend: cfg.AuditsBackend,
		PgCtor:  func(p *pgxpool.Pool) audits.Store { return audits.NewPgStore(p) },
		MemCtor: func() audits.Store { return audits.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Audits = audits.NewService(auditsStore)

	// 14b. v0.7.x deferred call-site: wire the
	//     audit-log writer into every mutating
	//     service so each Create/Update/Delete
	//     records an audit_log row after the row
	//     is committed. Same nil-safe setter
	//     pattern as WithWebhooks: the field
	//     stays nil for unit tests, and the
	//     Service methods always call
	//     RecordFromContext (which short-circuits
	//     when s.audits is nil).
	a.Nodes.WithAudits(a.Audits)
	a.Inbounds.WithAudits(a.Audits)
	a.InboundTemplates.WithAudits(a.Audits)
	a.Hosts.WithAudits(a.Audits)
	a.Users.WithAudits(a.Audits)
	a.Plans.WithAudits(a.Audits)
	// Backups is constructed below; the
	// WithAudits call is in the Backups block
	// (after a.Backups = backups.New(...)).

	// 14c. Credentials (Phase 2 multi-user render
	//      data model). The service is built and
	//      wired into the audit log the same way
	//      as the other v0.7.x services. The table
	//      sits empty in this PR — no HTTP handler,
	//      no read path. The follow-up PRs
	//      (`feat(cores): multi-user sing-box render`
	//      and `feat(builder): narrow fan-out to
	//      per-user nodes`) will start reading it.
	credentialsStore := MustBuild(pool, StoreBuilder[credentials.Store]{
		Name:    "credentials",
		Backend: cfg.CredentialsBackend,
		PgCtor:  func(p *pgxpool.Pool) credentials.Store { return credentials.NewPgStore(p) },
		MemCtor: func() credentials.Store { return credentials.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Credentials = credentials.NewService(credentialsStore)
	a.Credentials.WithAudits(a.Audits)

	// 15. Bootstrap (BYO Node) service. References
	//     nodes + audits. No backend switch: the
	//     bootstrap store is in-process state,
	//     not a separate pg table.
	a.Bootstrap = bootstrap.NewService(bootstrap.ServiceConfig{
		Nodes:       &nodes.BootstrapNodeProvider{Svc: a.Nodes},
		Audits:      a.Audits,
		Envelope:    cipher, // shared with the webhooks Store above; see section 9
		AgentBinary: cfg.AgentBinaryPath,
		KnownHosts:  cfg.AgentKnownHosts,
		SSHUser:     cfg.AgentSSHUser,
		SSHPort:     cfg.AgentSSHPort,
	})

	// 16. Backups. The store is always LocalStore
	//     in v0.5.0 (a JSON index next to the dump
	//     files). The pool may be nil when the
	//     panel runs without pg (dev mode).
	backupsBackend, err := backups.NewOSBackend(cfg.BackupsDir)
	if err != nil {
		return nil, fmt.Errorf("backups: failed to initialise backend: %w (dir=%s)", err, cfg.BackupsDir)
	}
	backupsStore := backups.NewLocalStore(backupsBackend)
	a.Backups = backups.New(
		backups.Config{
			PostgresDSN:    cfg.PostgresDSN,
			BackupsDir:     cfg.BackupsDir,
			AllowUIRestore: cfg.BackupsAllowUIRestore,
			RetentionDays:  cfg.BackupsRetentionDays,
			MaxCount:       cfg.BackupsMaxCount,
		},
		backupsStore, pool,
	)
	a.Backups.WithWebhooks(a.Webhooks)
	a.Backups.WithAudits(a.Audits)
	logger.Info().
		Str("dir", cfg.BackupsDir).
		Int("retention_days", cfg.BackupsRetentionDays).
		Int("max_count", cfg.BackupsMaxCount).
		Bool("allow_ui_restore", cfg.BackupsAllowUIRestore).
		Msg("backups: service initialised")

	// 17. Subscription endpoint rate limiter.
	//     One instance shared across the default
	//     and the rotated sub_path mount.
	a.SubLimiter = newSubscriptionRateLimiter(cfg)

	// 18. Best-effort known_hosts setup. Not
	//     fatal: the installer falls back to a
	//     per-install TempFile.
	if err := bootstrap.EnsureKnownHosts(cfg.AgentKnownHosts); err != nil {
		logger.Warn().Err(err).Str("path", cfg.AgentKnownHosts).
			Msg("bootstrap: known_hosts setup failed; installer will use a per-call TempFile")
	}

	// 19. Router. We do not start the server here
	//     so the cmd/ binary can wrap the handler
	//     in obs.Middleware before ListenAndServe.
	a.Router = router.Build(
		ctx,
		cfg,
		a.Auth, a.Nodes, a.Hosts, a.Inbounds, a.InboundTemplates,
		a.Subs, a.Users, a.PanelCfg, a.Audits,
		a.Plans, a.Bootstrap, a.Backups, a.Webhooks,
		a.Credentials,
		a.SubLimiter,
	)

	// 20. http.Server. The cmd/ binary owns
	//     graceful shutdown.
	a.Server = &http.Server{
		Addr:              cfg.HTTPAddr,
		ReadHeaderTimeout: 10 * time.Second,
		Handler:           obs.Middleware(a.Router),
	}

	return a, nil
}

// openPgPoolIfNeeded opens the pg pool when at
// least one AEGIS_*_BACKEND env var is set to
// "pg". A nil pool is returned (without error)
// when no backend needs pg, so a memory-only dev
// run does not require a live database.
func openPgPoolIfNeeded(ctx context.Context, cfg *config.Config) (*pgxpool.Pool, error) {
	if !needsPg(cfg) {
		return nil, nil
	}
	p, err := db.Open(ctx, cfg.PostgresDSN)
	if err != nil {
		return nil, fmt.Errorf("db: failed to open postgres connection pool: %w", err)
	}
	return p, nil
}

// needsPg reports whether any AEGIS_*_BACKEND
// env var is set to "pg". Hoisted to a helper so
// callers do not have to know which backends
// exist before they ask Build whether the pool
// will exist.
func needsPg(cfg *config.Config) bool {
	return cfg.AuthBackend == "pg" ||
		cfg.HostsBackend == "pg" ||
		cfg.NodesBackend == "pg" ||
		cfg.InboundsBackend == "pg" ||
		cfg.SubscriptionBackend == "pg" ||
		cfg.UsersBackend == "pg" ||
		cfg.PlansBackend == "pg" ||
		cfg.PanelcfgBackend == "pg" ||
		cfg.AuditsBackend == "pg" ||
		cfg.WebhooksBackend == "pg" ||
		cfg.CredentialsBackend == "pg"
}

// mustHashDevPassword is the dev seed admin's
// argon2id hash. It panics on the (unreachable)
// hash failure because the dev seed is exactly
// the path where a hash error is unrecoverable —
// if we cannot produce a valid hash, the dev
// environment is broken and a panic is the
// right outcome. Production boots never reach
// this code (the memory-backend + production
// check in `MustBuild` short-circuits first).
func mustHashDevPassword() string {
	h, err := auth.HashPassword("aegis-dev-password")
	if err != nil {
		panic(fmt.Errorf("seed hash: %w", err))
	}
	return h
}

// AddNodeBatchedApplier registers a per-node
// BatchedApplier on the App. The caller supplies
// the FlushFn (typically a closure that knows
// about the sing-box Provider and calls
// RenderConfig + Apply); this method only handles
// the registration + goroutine lifecycle.
//
// Why this lives on App rather than in
// cmd/aegis/main.go directly: the per-applier
// context cancel funcs are stored on App so
// App.Close() can stop every goroutine uniformly.
// Exposing them via a method keeps the
// unexported field an implementation detail of
// the App (no external code can race the cancel
// map).
//
// The BatchedApplierEnabled flag is NOT consulted
// here — callers that want to gate on it check
// `a.Config.BatchedApplierEnabled` before calling.
// This keeps the gating decision in one place
// (the wiring helper) instead of in every call
// site.
func (a *App) AddNodeBatchedApplier(
	ctx context.Context,
	nodeID uuid.UUID,
	nodeName string,
	flushFn cores.FlushFn,
) *cores.BatchedApplier {
	if a.BatchedAppliers == nil {
		// Defensive: Build initialises the map,
		// so this only fires for a hand-rolled
		// *App{} (e.g. a unit test). Re-init
		// rather than nil-deref.
		a.BatchedAppliers = make(map[uuid.UUID]*cores.BatchedApplier)
	}
	if a.batchedApplierCancels == nil {
		a.batchedApplierCancels = make(map[uuid.UUID]context.CancelFunc)
	}
	applier := cores.NewBatchedApplier(20*time.Second, 1000, flushFn)
	a.BatchedAppliers[nodeID] = applier
	applierCtx, cancel := context.WithCancel(ctx)
	// Register cancel AFTER the map entry so a
	// racing Close() never cancels a goroutine
	// whose applier is not yet visible to
	// services that fan out via WithBatchApplier.
	a.batchedApplierCancels[nodeID] = cancel
	go func() {
		defer cancel()
		if err := applier.Run(applierCtx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error().Err(err).Str("node", nodeName).Msg("app: BatchedApplier.Run exited unexpectedly")
		}
	}()
	log.Info().
		Str("node", nodeName).
		Dur("window", applier.Window()).
		Msg("app: BatchedApplier started")
	return applier
}

// Close stops the webhook retry worker, cancels
// every BatchedApplier goroutine, and releases the
// pg pool. Safe to call on a nil receiver and
// safe to call multiple times (idempotent).
func (a *App) Close() {
	if a == nil {
		return
	}
	if a.webhooksWorkerCancel != nil {
		a.webhooksWorkerCancel()
		a.webhooksWorkerCancel = nil
	}
	// BatchedApplier goroutines: cancel each per-
	// applier context so Run() drains and returns.
	// The BatchedApplier map itself is left for the
	// next process (the test, in particular, may
	// still want to inspect it after Close).
	for id, cancel := range a.batchedApplierCancels {
		if cancel != nil {
			cancel()
		}
		delete(a.batchedApplierCancels, id)
	}
	if a.Pool != nil {
		a.Pool.Close()
		a.Pool = nil
	}
}

// newSubscriptionRateLimiter builds the per-
// sub_token rate limiter the HTTP layer hands
// to subscription.RouterWithLimiter. The
// settings are taken from cfg; a non-positive
// RPS disables throttling (the v0.1.0
// behaviour).
//
// Defaults (1 rps, 5 burst, 50k keys) are
// tuned for a single-user-with-multiple-devices
// usage model: a phone + laptop + tablet +
// desktop can all wake up at once after a 24h
// client poll cycle and still fit inside the
// burst budget.
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
