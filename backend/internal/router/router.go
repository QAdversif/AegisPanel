// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package router wires together the v1 HTTP routes. Each module
// (auth, users, nodes, hosts, subscriptions, …) will register its own
// subrouter here in Phase 0 / Phase 1.

package router

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/backups"
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/credentials"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/inboundtemplates"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/panelcfg"
	"github.com/QAdversif/AegisPanel/internal/plans"
	"github.com/QAdversif/AegisPanel/internal/ratelimit"
	"github.com/QAdversif/AegisPanel/internal/subscription"
	"github.com/QAdversif/AegisPanel/internal/users"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// Build returns the v1 http.Handler for Aegis. The auth subrouter is
// wired into /api/v1/auth; its protected endpoints sit behind
// auth.Service.Middleware() and surface the verified Claims on the
// request context for downstream handlers. Other module routers
// (nodes, …) are mounted here too — see comments inline.
//
// `ctx` is the boot context from `app.Build`. The only
// construction-time I/O is the panelcfg read for the
// rotated sub_path mount; using the boot context means
// a SIGINT during boot aborts the read instead of
// blocking on a stale connection.
func Build(
	ctx context.Context,
	cfg *config.Config,
	authSvc *auth.Service,
	nodesSvc *nodes.Service,
	hostsSvc *hosts.Service,
	inboundsSvc *inbounds.Service,
	inboundTemplatesSvc *inboundtemplates.Service,
	subscriptionSvc *subscription.Service,
	usersSvc *users.Service,
	panelCfgSvc *panelcfg.Service,
	auditsSvc *audits.Service,
	plansSvc *plans.Service,
	bootstrapSvc *bootstrap.Service,
	backupsSvc *backups.Service,
	webhooksSvc *webhooks.Service,
	credentialsSvc *credentials.Service,
	subLimiter *ratelimit.Limiter,
) http.Handler {
	r := chi.NewRouter()

	// Built-in middlewares (recover, real IP, request ID, logger).
	//
	// Real-IP extraction uses the chi v5.3 ClientIPFrom* family
	// (read the resolved IP with `middleware.GetClientIP(ctx)`).
	// The previous `middleware.RealIP` is deprecated in chi
	// v5.3.x because it mutates `r.RemoteAddr` to the leftmost
	// X-Forwarded-For value, which any unauthenticated client can
	// forge (GHSA-3fxj-6jh8-hvhx + GHSAs cited in the deprecation
	// notice). The replacement is two composed middlewares:
	//
	//   - ClientIPFromHeader("X-Real-IP") — trust the X-Real-IP
	//     header that Caddy (and any other reverse proxy using the
	//     conventional realip pattern) overwrites on every request.
	//   - ClientIPFromRemoteAddr — fall back to the TCP peer
	//     when the header is missing (dev mode, direct exposure
	//     behind a load balancer that strips headers, etc.).
	//
	// The order matters: ClientIPFromHeader runs first; if it
	// finds a parseable value it sets the context IP, and the
	// subsequent ClientIPFromRemoteAddr is a no-op. If the header
	// is absent, ClientIPFromRemoteAddr fills in the TCP peer.
	r.Use(middleware.RequestID)
	r.Use(middleware.ClientIPFromHeader("X-Real-IP"))
	r.Use(middleware.ClientIPFromRemoteAddr)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Heartbeat("/healthz"))

	r.Route("/api/v1", func(r chi.Router) {
		// Healthcheck + readiness.
		r.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok","version":"dev"}`))
		})

		// Auth surface: login, refresh, me. Mounted unconditionally
		// in Phase 0 — Phase 1+ will mount it conditionally on cfg.AuthEnabled.
		r.Mount("/auth", authSvc.Mount())

		// Cores catalog — public, no auth. The UI and any
		// client integration need to know which providers are
		// wired in and what each one supports before login.
		cores.Mount(r)

		// Nodes CRUD — Phase 0. All routes are protected by the
		// auth middleware + ScopeNodes requirement (applied
		// inside nodes.Router itself).
		r.Mount("/nodes", nodes.Router(nodesSvc, authSvc.Middleware(), bootstrapSvc))

		// Per-node inbounds — Phase 1. The inbounds router
		// is mounted under the nodeId URL parameter so
		// every inbound is naturally scoped to a node.
		// The {nodeId} path parameter is set by the parent
		// route and read inside inbounds.Router via
		// chi.URLParam.
		r.Mount("/nodes/{nodeId}/inbounds", inbounds.Router(inboundsSvc, authSvc.Middleware()))

		// Panel-wide inbounds — flat list across all
		// nodes. Used by the admin UI's create/edit
		// dialog to preload the full inbound map in a
		// single round-trip (instead of N per-node
		// requests). Per-id reads stay on the per-node
		// router above so the {nodeId} scope check
		// keeps the URL contract honest.
		r.Mount("/inbounds", inbounds.TopLevelRouter(inboundsSvc, authSvc.Middleware()))

		// v0.8.x: inbound templates — panel-wide
		// named `Params` defaults. Same ScopeNodes
		// guard as the inbounds router (the
		// templates are a panel-level feature
		// that affects per-node inbounds). The
		// service is constructed in main.go and
		// passed in; see internal/inboundtemplates
		// for the model / store / service split.
		r.Mount("/inbound-templates", inboundtemplates.Router(inboundTemplatesSvc, authSvc.Middleware()))

		// Hosts CRUD — Phase 1. Hosts reference nodes by id,
		// so the hosts service is constructed in main.go with
		// the nodes service as a dependency.
		r.Mount("/hosts", hosts.Router(hostsSvc, authSvc.Middleware()))

		// Users CRUD — admin surface. List / get / create /
		// patch / rotate-token. The user-CRUD surface
		// lives in the users package (d-refactor.3);
		// the mount takes the *users.Service directly.
		r.Mount("/users", users.AdminRouter(usersSvc, authSvc.Middleware()))

		// Plans CRUD — admin surface. List / get / create /
		// patch / delete. The plan-CRUD surface lives in
		// the plans package (v0.6.0). Every operator
		// role (admin / operator / viewer) gets
		// ScopePlans so the catalog is readable from
		// every role (the UsersView needs it to resolve
		// a `plan_id` to a name).
		r.Mount("/plans", plans.AdminRouter(plansSvc, authSvc.Middleware()))

		// Outgoing webhooks — admin surface. List /
		// get / create / patch / delete endpoints, plus
		// the per-endpoint delivery history, the
		// cross-endpoint DLQ, the test-event endpoint,
		// and the manual DLQ-replay endpoint. The
		// package lives in `internal/webhooks` (v0.7.0);
		// every operator role (admin / operator /
		// viewer) gets ScopeWebhooks so the endpoint
		// health widget is visible from every role
		// (the WebhooksView shows the last-delivery
		// snapshot for every endpoint).
		r.Mount("/webhooks", webhooks.AdminRouter(webhooksSvc, authSvc.Middleware()))

		// Subscription URL — the public endpoint that
		// turns a sub_token into a base64 / sing-box /
		// Clash / html payload. Mounted under /sub so
		// the route is short for the operator's
		// documentation. The default mount at
		// /api/v1/sub/<token> stays live in parallel
		// with the rotated mount at the top level
		// (added below) so the panel always serves
		// subscriptions, even when the operator has
		// not yet rotated the sub_path. The render
		// handler consults usersSvc for the
		// sub_token-→-user lookup (d-refactor.3).
		r.Mount("/sub", subscription.RouterWithLimiter(subscriptionSvc, usersSvc, subLimiter))

		// Panel-wide config (the rotating sub_path).
		// Admin-only. GET the active row, POST
		// /rotate for a fresh random path,
		// /rotate-to for an explicit path, /reset to
		// restore the default empty sub_path.
		r.Mount("/panelcfg", panelcfg.Router(panelCfgSvc, authSvc.Middleware()))

		// Audit log. Read-only. GET / lists entries
		// (with filters); GET /{id} returns the
		// full entry with before/after. v0.3+ adds
		// the mutating-handler write call-sites.
		r.Mount("/audits", audits.Router(auditsSvc, authSvc.Middleware()))

		// Backups — v0.5.0 (#120). Mounts the
		// internal/backups handler at
		// /api/v1/backups. All endpoints require
		// ScopeBackups; the scope is granted only
		// to the `admin` role. The handler is a
		// thin wrapper around the backups.Service;
		// the actual pg_dump subprocess, retention
		// cleanup, and (optional) scheduler live
		// in the Service.
		r.Mount("/backups", backups.NewHandler(backupsSvc, authSvc.Middleware()).Mount())

		// Credentials CRUD — admin surface (v0.8.2).
		// List / get / create / rotate (PATCH) / delete,
		// plus the per-user / per-inbound cross-cut
		// reads. The package lives in
		// `internal/credentials` (Phase 2 multi-user
		// sing-box render data model from PR #167; the
		// HTTP surface lands here). Every operator
		// role (admin / operator / viewer) gets
		// ScopeCredentials so the credentials table
		// is visible from every role (the operator
		// needs to see "is user X set up correctly on
		// inbound Y?" the same way they need to see
		// the plan catalog).
		r.Mount("/credentials", credentials.AdminRouter(credentialsSvc, authSvc.Middleware()))

		// OpenAPI spec + minimal self-contained index page.
		mountSwagger(r)

		// Module routers will be mounted here in Phase 0+:
		//   r.Mount("/users",        users.Router(cfg))
		//   r.Mount("/hosts",        hosts.Router(cfg))
		//   r.Mount("/subscriptions", subscriptions.Router(cfg))
		//   r.Mount("/cabinet",      cabinet.Router(cfg))
		//   r.Mount("/webhooks",     webhooks.Router(cfg))
	})

	// Rotated sub_path mount — sits at the top level
	// of the router (NOT under /api/v1) because the
	// sub_path itself is the operator's chosen
	// top-level prefix. A rotated panel serves
	// subscriptions at `https://panel/<sub_path>/sub/
	// <token>`, where `<sub_path>` is the 16-char
	// hex string the operator generated. The
	// sub_path is read from the DB once at Build
	// time; Phase 1 will add a TTL cache so a
	// rotation takes effect without a router
	// restart.
	//
	// The default mount at /api/v1/sub stays live
	// in parallel so the panel always serves
	// subscriptions, even if the active sub_path
	// is the empty default. The empty default is
	// a no-op (it would mount at `/sub/sub/<token>`,
	// which is wrong; the router skips the mount
	// when the path is empty).
	if active, err := panelCfgSvc.GetActive(ctx); err == nil && active.SubPath != "" {
		r.Mount("/"+active.SubPath+"/sub", subscription.RouterWithLimiter(subscriptionSvc, usersSvc, subLimiter))
	} else if err != nil {
		log.Warn().Err(err).Msg("router: panelcfg read failed; rotated sub_path mount skipped")
	}

	log.Info().Msg("v1 router initialised (auth + nodes + subscription mounted)")
	return r
}
