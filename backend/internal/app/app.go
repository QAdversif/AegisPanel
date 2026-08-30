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
	"os"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/agentca"
	"github.com/QAdversif/AegisPanel/internal/agentgrpc"
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
	// AgentCA is the v0.8.30 mTLS cert bootstrap
	// service. Holds the panel's self-signed root
	// CA + the per-node server + client certs.
	// The Service is the only consumer of the
	// Store (the BatchedApplier does not consume
	// it directly; `nodes.Service.Provision`
	// is the integration point).
	AgentCA *agentca.Service

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
	backupsWorkerCancel   context.CancelFunc
	batchedApplierCancels map[uuid.UUID]context.CancelFunc
	batchedApplierWg      sync.WaitGroup
	// agentClient is the per-process agentgrpc.Client
	// (HTTP or gRPC, selected by AEGIS_AGENT_TRANSPORT).
	// The wiring helper that calls `agentgrpc.New` sets
	// this field via SetAgentClient; `App.Close()`
	// releases it last so the BatchedApplier goroutines
	// can drain in-flight Apply requests before the
	// connection pool goes away. Pre-fix the helper's
	// `defer client.Close()` fired when the helper
	// returned — the client was dead before the first
	// Apply.
	agentClient agentgrpc.Client
}

// SetAgentClient records the per-process agentgrpc.Client
// on the App so App.Close() can release it during
// shutdown. The wiring helper (cmd/aegis/main.go)
// calls this once, immediately after `agentgrpc.New`.
// The client is an interface so the test layer can
// inject a fake without depending on the real gRPC or
// HTTP transport.
func (a *App) SetAgentClient(c agentgrpc.Client) {
	a.agentClient = c
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

	// 0. Build the age SecretCipher once, early.
	//    The cipher is the v0.8.x shared age-envelope
	//    boundary for "long-lived at-rest secrets" the
	//    panel stores (see internal/crypto/envelope).
	//    Three call sites share it:
	//
	//      - agentca.Service (v0.8.32.3+, the fix for
	//        issue #326 -- decrypts the v0.8.25
	//        hand-minted prod `agentca.key_ciphertext`
	//        row)
	//      - webhooks.NewPgStore (seals
	//        `webhook_endpoints.secret`)
	//      - nodes.WithEnvelope (decrypts
	//        `nodes.ssh_private_key_ciphertext` for the
	//        stored-key read endpoint)
	//
	//    Constructing it once at the top of Build
	//    means the agentca Service has the envelope
	//    available from its first call (no "build
	//    twice" pattern). Memory/dev mode uses the
	//    NoopSecretCipher (plaintext on disk; same
	//    dev-mode shortcut the v0.7.x webhook memory
	//    store used).
	var cipher envelope.SecretCipher
	switch cfg.WebhooksBackend {
	case "pg":
		c, err := envelope.NewAgeSecretCipher(
			cfg.WebhooksSecretAgeRecipients,
			cfg.WebhooksSecretAgeKeyFile,
		)
		if err != nil {
			return nil, fmt.Errorf("app: build age secret cipher: %w", err)
		}
		cipher = c
	default:
		cipher = envelope.NewNoopSecretCipher()
		logger.Warn().Msg("app: envelope is noop (memory/dev mode; secrets are plaintext on disk)")
	}

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
	// v0.8.13+: the refresh-token cookie's `Secure`
	// attribute is conditional on the deployment env.
	// Production is HTTPS-only; the dev / staging HTTP
	// path needs Secure=false or the browser silently
	// drops the cookie. See
	// `auth.Service.SetCookieSecure` for the rationale.
	if cfg.Env == "production" {
		a.Auth.SetCookieSecure(true)
	}

	// 4. Nodes.
	nodesStore := MustBuild(pool, StoreBuilder[nodes.Store]{
		Name:    "nodes",
		Backend: cfg.NodesBackend,
		PgCtor:  func(p *pgxpool.Pool) nodes.Store { return nodes.NewPgStore(p) },
		MemCtor: func() nodes.Store { return nodes.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.Nodes = nodes.NewService(nodesStore)

	// 4b. AgentCA (v0.8.30 mTLS bootstrap). The
	// Store is the same backend as the rest of
	// the panel (`pg` / `memory`); the Service
	// holds the root CA in memory after the
	// first call to `EnsureRoot`. Nodes service
	// consumes the Service via
	// `nodes.Service.WithAgentCA(a.AgentCA)`
	// (the WithXxx setter pattern matches the
	// rest of the panel's DI; see `nodes.WithAgentCA`).
	agentcaStore := MustBuild(pool, StoreBuilder[agentca.Store]{
		Name:    "agentca",
		Backend: cfg.AgentCABackend,
		PgCtor:  func(p *pgxpool.Pool) agentca.Store { return agentca.NewPgStore(p) },
		MemCtor: func() agentca.Store { return agentca.NewMemoryStore() },
		Env:     cfg.Env,
	})
	a.AgentCA = agentca.NewService(agentcaStore, cipher)
	// v0.8.32.2: Close was previously deferred here so it
	// fired when `Build` returned. That meant
	// `a.AgentCA.RootCertPEM()` after `Build` returned
	// always hit a closed service — the mTLS bootstrap
	// in `bootstrap.ServiceConfig.MTLSCerts` saw
	// `ErrNotFound` (the cache lives on the closed
	// receiver), the provisioner swallowed the error
	// (per the v0.8.30 mintMTLSCerts fail-soft path), and
	// the agent installed without mTLS material. The
	// Close call now lives in `App.Close()` so the
	// service stays open for every consumer that runs
	// after Build (nodes.Service, bootstrap adapter,
	// admin CLI subcommands, etc.).
	//
	// Wire the agentca Service into nodes.Service
	// so `nodes.Service.Provision` (the v0.8.30
	// mTLS bootstrap integration) can call
	// `EnsureNodeCerts` before the SSH dial. The
	// adapter is in `internal/app/agentca_adapter.go`
	// because the `app` package is the only one that
	// can import all three (agentca + bootstrap +
	// nodes) without a cycle. The `nodes` and
	// `bootstrap` packages form a peer pair (cycle);
	// the adapter in `app` is the bridge.
	a.Nodes.WithAgentCA(agentCAAdapter{svc: a.AgentCA})

	// v0.8.31.1 hotfix: lazy-ensure the panel's root
	// CA is provisioned at boot so the mTLS factory
	// in `bootstrap.ServiceConfig.MTLSCerts` does not
	// return "root CA not yet provisioned" on the first
	// `Provision` call. Without this, `mintMTLSCerts`
	// (internal/bootstrap/provisioner.go:226) silently
	// swallows the error and the installer's
	// `writeMTLSCerts` skips the cert push, leaving the
	// v0.8.31+ agent on the node without
	// `/etc/aegis/agent.{crt,key,ca.pem}`. The agent
	// hard-fails to start without those files
	// (cmd/aegis-agent/grpc.go: "gRPC mTLS required but
	// load failed: read cert /etc/aegis/agent.crt: no
	// such file or directory"). One call at boot is
	// enough: `Service.EnsureRoot` is idempotent and
	// caches the result in `cachedRoot`; subsequent
	// `RootCertPEM()` calls return the same in-memory
	// cert without round-tripping through the Store.
	if _, err := a.AgentCA.EnsureRoot(ctx); err != nil {
		return nil, fmt.Errorf("app: ensure agentca root: %w", err)
	}

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

	// v0.8.13+: the inbounds service validates
	// every inbound.TemplateID against the
	// templates service (template must exist;
	// protocol must match). Wire the templates
	// service into inbounds after both are
	// constructed. The setter is nil-safe: a
	// nil templates service is the v0.8.0-v0.8.12
	// contract and the validation is a no-op in
	// that case (no inbound ever has a
	// template_id pre-v0.8.13).
	a.Inbounds.WithTemplates(a.InboundTemplates)

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

	// 9. Wire the webhooks store. The age envelope
	//    was built at the top of Build (step 0) and
	//    is shared with the agentca service (step 4)
	//    and the nodes service (step 10). The webhooks
	//    backend is the same `pg` / `memory` switch
	//    the panel has used since v0.7.0; the cipher
	//    selection is already done by the time we get
	//    here.
	var webhooksStore webhooks.Store
	switch cfg.WebhooksBackend {
	case "pg":
		webhooksStore = webhooks.NewPgStore(pool, cipher)
		logger.Info().
			Int("recipients", len(cfg.WebhooksSecretAgeRecipients)).
			Str("key_file", cfg.WebhooksSecretAgeKeyFile).
			Msg("webhooks: using pgx-backed store (PgStore, age-encrypted secret)")
	default:
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
	// WithCreds docstring).
	//
	// v0.8.28.9 (#289/C2): the WithCreds call MOVED
	// to step 14c, after a.Credentials is built. It
	// previously sat HERE, where a.Credentials was
	// still nil (Credentials is constructed in step
	// 14c, below) — the nil-safe WithCreds accepted
	// it and every production render silently ran
	// on the Phase 1 params fallback, ignoring the
	// per-(user, inbound) credentials table.

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
	// v0.8.28.9 (#289/C2): NOW Credentials exists —
	// wire the Phase 2 multi-user render source into
	// the subscription service. The call previously
	// lived in step 12 (subscription build), passing
	// the not-yet-assigned nil a.Credentials; see the
	// comment there. The app_test.go smoke build
	// asserts the source is non-nil so a future
	// reordering cannot silently regress this.
	a.Subs.WithCreds(a.Credentials)

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
		// v0.8.30: mTLS cert issuer. The closure
		// reads the per-node cert material from the
		// `agentca.Service` (in memory after
		// `EnsureNodeCerts`) and the root from
		// `RootCertPEM()`. The bootstrap package
		// cannot import the `agentca` package
		// directly without a cycle, so the
		// dependency inverts at boot.
		MTLSCerts: func(ctx context.Context, nodeID uuid.UUID, addr string) (bootstrap.MTLSCerts, error) {
			rootPEM, err := a.AgentCA.RootCertPEM()
			if err != nil {
				return bootstrap.MTLSCerts{}, fmt.Errorf("bootstrap: mint mTLS: root CA not yet provisioned: %w", err)
			}
			issued, err := a.AgentCA.EnsureNodeCerts(ctx, nodeID, addr)
			if err != nil {
				return bootstrap.MTLSCerts{}, fmt.Errorf("bootstrap: mint mTLS for node %s: %w", nodeID, err)
			}
			return bootstrap.MTLSCerts{
				ServerCertPEM: issued.ServerCertPEM,
				ServerKeyPEM:  issued.ServerKeyPEM,
				RootCertPEM:   rootPEM,
			}, nil
		},
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
			BackupsCron:    cfg.BackupsCron,
		},
		backupsStore, pool,
	)
	a.Backups.WithWebhooks(a.Webhooks)
	a.Backups.WithAudits(a.Audits)

	// 16b. Backups scheduler. The cron expression is
	//      read from `AEGIS_BACKUPS_CRON` (env) via
	//      `cfg.BackupsCron` (already on the Config
	//      struct). Empty = manual-only mode (the
	//      operator can still trigger backups via
	//      `POST /api/v1/backups` and the admin UI;
	//      the cron is also re-loadable at runtime via
	//      `a.Backups.ReloadCron(...)`). The cron
	//      field was wired in v0.9.x but never plumbed
	//      here, so the in-process scheduler was dead
	//      code at runtime (#302). Pre-fix, the
	//      "scheduled backups never fire" path was
	//      only documented in KNOWN_LIMITATIONS
	//      v0.9.1 #1 - this fix closes the gap.
	//
	//      The goroutine is the same pattern as the
	//      webhook retry worker (step 11) - a child
	//      context that we cancel in Close so the
	//      ticker stops on shutdown. ParseCron errors
	//      fail loud (the panel refuses to start with
	//      a malformed schedule) - silent fallback
	//      would leave the operator without a
	//      diagnostic when their backup window misses.
	if cfg.BackupsCron != "" {
		schedCtx, schedCancel := context.WithCancel(ctx)
		if _, parseErr := backups.ParseCron(cfg.BackupsCron); parseErr != nil {
			schedCancel()
			return nil, fmt.Errorf("backups: invalid AEGIS_BACKUPS_CRON=%q: %w", cfg.BackupsCron, parseErr)
		}
		a.backupsWorkerCancel = schedCancel
		go func() {
			defer schedCancel()
			if runErr := a.Backups.Run(schedCtx, cfg.BackupsCron); runErr != nil {
				log.Error().Err(runErr).Msg("backups: scheduler exited")
			}
		}()
		logger.Info().
			Str("cron", cfg.BackupsCron).
			Msg("backups: scheduler started")
	} else {
		logger.Info().Msg("backups: scheduler DISABLED (AEGIS_BACKUPS_CRON is empty); manual triggers only")
	}
	logger.Info().
		Str("dir", cfg.BackupsDir).
		Int("retention_days", cfg.BackupsRetentionDays).
		Int("max_count", cfg.BackupsMaxCount).
		Bool("allow_ui_restore", cfg.BackupsAllowUIRestore).
		Msg("backups: service initialised")
	// Boot-time footgun guard. The 2026-08-30 "backups
	// disappeared from UI" incident was silent because
	// (a) the local store returns [] on a missing index
	// with no error, (b) NewOSBackend auto-creates the
	// dir so the service "succeeds", and (c) the
	// relative default ("./var/backups") resolved to
	// a path that the volume mount did not populate. A
	// loud WARN at boot makes the footgun visible in
	// `docker logs aegis-panel | grep boot:` instead of
	// being discovered hours later from an empty UI list.
	// See warnRelativeDefaultDir for the full rationale.
	warnRelativeDefaultDir(logger, "AEGIS_BACKUPS_DIR", cfg.BackupsDir)

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
	// v0.8.32.2: track the goroutine with a
	// WaitGroup so App.Close() can wait for the
	// last in-flight Apply to finish before
	// closing the pg pool out from under it.
	// Pre-fix there was no Wait, so Close
	// returned while goroutines were still
	// holding transactions, and the next line
	// (`a.Pool.Close()`) closed the pool out
	// from under them — data-loss + race on
	// `-race` builds.
	a.batchedApplierWg.Add(1)
	go func() {
		defer a.batchedApplierWg.Done()
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
// every BatchedApplier goroutine, waits for them
// to drain, then releases the agent client, the
// agentca Service, and the pg pool. Safe to call
// on a nil receiver and safe to call multiple
// times (idempotent).
//
// Shutdown order matters:
//
//  1. Cancel every long-lived goroutine (webhook
//     worker, backups worker, per-applier
//     contexts). The BatchedApplier goroutines
//     use their per-applier ctx to stop calling
//     Apply at the next flush boundary.
//  2. Wait for the BatchedApplier goroutines to
//     exit, capped at 10s. This is the new
//     behaviour — pre-fix there was no Wait, so
//     Close returned while goroutines were still
//     holding transactions, and the next line
//     (`a.Pool.Close()`) closed the pool out from
//     under them. Result: data loss + race
//     detector fires on `-race` builds.
//  3. Close the agentgrpc.Client. After step 2
//     the BatchedApplier has finished its last
//     in-flight Apply; the connection pool is
//     no longer in use. Pre-fix the wiring helper
//     closed the client at helper-return time
//     (a `defer client.Close()` two lines after
//     `agentgrpc.New`), so the first Apply hit
//     a dead client.
//  4. Close the agentca Service. The service
//     holds a long-lived Store (the root CA
//     in-memory cache and the per-row cert
//     table). Pre-fix `Build` closed it via
//     `defer`, so any consumer that read
//     `RootCertPEM()` after Build saw a closed
//     receiver.
//  5. Close the pg pool. Last, because every
//     prior Close may have triggered a final
//     audit row INSERT (the mTLS bootstrap in
//     particular writes a final log row when
//     the installer's upload-and-swap completes).
func (a *App) Close() {
	if a == nil {
		return
	}
	if a.webhooksWorkerCancel != nil {
		a.webhooksWorkerCancel()
		a.webhooksWorkerCancel = nil
	}
	if a.backupsWorkerCancel != nil {
		a.backupsWorkerCancel()
		a.backupsWorkerCancel = nil
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
	// Wait for the BatchedApplier goroutines to
	// finish their last in-flight Apply. Capped at
	// 10s — a normal shutdown completes in <100ms;
	// anything beyond that is a stuck node and we
	// should not block the process indefinitely.
	// The pattern is the canonical Go "wait with
	// timeout" idiom: spawn a watcher goroutine,
	// race the watcher against `time.After`.
	done := make(chan struct{})
	go func() {
		a.batchedApplierWg.Wait()
		close(done)
	}()
	select {
	case <-done:
		// Happy path. Continue to the next
		// teardown step.
	case <-time.After(10 * time.Second):
		// BatchedApplier goroutines that did
		// not finish within the timeout are
		// abandoned. The underlying connection
		// pool is still open at this point
		// (we close it next), so a goroutine
		// that wakes up after this will hit a
		// closed pool and fail loudly — not
		// silently, and not on a live query.
		log.Error().Dur("timeout", 10*time.Second).Msg("app: BatchedApplier goroutines did not finish in time; abandoning")
	}
	if a.agentClient != nil {
		if err := a.agentClient.Close(); err != nil {
			log.Warn().Err(err).Msg("app: agent client close")
		}
		a.agentClient = nil
	}
	if a.AgentCA != nil {
		if err := a.AgentCA.Close(); err != nil {
			log.Warn().Err(err).Msg("app: agentca close")
		}
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

// warnRelativeDefaultDir emits a loud WARN at boot
// when an env var with a relative `envDefault` (e.g.
// `AEGIS_BACKUPS_DIR` -> `./var/backups`) was NOT
// overridden in the deploy's env file. The default is
// fine in dev (panel CWD = repo root) but the panel's
// CWD in production is `/` (distroless) and the
// resolved path almost never matches the docker volume
// mount. The 2026-08-30 "backups disappeared from UI"
// incident on prod was silent — the local store's
// `readIndex` returned `[]` on a missing `_index.json`
// with no error, the panel booted cleanly, and the
// first hint was the empty UI list. This warn makes
// the footgun loud at the first `docker logs aegis-panel`
// after deploy.
//
// `envName` is the env var to check (e.g.
// "AEGIS_BACKUPS_DIR"). `resolvedDir` is the value the
// panel is actually using (i.e. the config field, which
// is the default when the env var is unset). The helper
// intentionally does NOT check whether the resolved
// path exists or is non-empty: NewOSBackend /
// NewMemoryBackend auto-create the dir, so a
// successful init does not imply the operator
// intended this path.
func warnRelativeDefaultDir(logger zerolog.Logger, envName, resolvedDir string) {
	if os.Getenv(envName) != "" {
		return
	}
	cwd, _ := os.Getwd()
	logger.Warn().
		Str("env", envName).
		Str("resolved_dir", resolvedDir).
		Str("container_cwd", cwd).
		Msg("boot: " + envName + " is NOT set; the panel is using the relative default " +
			"from config. In production this resolves to <container_cwd>/" + resolvedDir +
			" which almost never matches the docker volume mount. Set " + envName +
			" to the absolute container path (the right side of the deploy -v mount) " +
			"and restart, otherwise the service will silently read from an empty or " +
			"freshly-created dir (e.g. backups will appear to disappear from the UI).")
}
