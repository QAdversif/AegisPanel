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
	"github.com/QAdversif/AegisPanel/internal/bootstrap"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
	"github.com/QAdversif/AegisPanel/internal/panelcfg"
	"github.com/QAdversif/AegisPanel/internal/ratelimit"
	"github.com/QAdversif/AegisPanel/internal/subscription"
)

// Build returns the v1 http.Handler for Aegis. The auth subrouter is
// wired into /api/v1/auth; its protected endpoints sit behind
// auth.Service.Middleware() and surface the verified Claims on the
// request context for downstream handlers. Other module routers
// (nodes, …) are mounted here too — see comments inline.
func Build(
	cfg *config.Config,
	authSvc *auth.Service,
	nodesSvc *nodes.Service,
	hostsSvc *hosts.Service,
	inboundsSvc *inbounds.Service,
	subscriptionSvc *subscription.Service,
	panelCfgSvc *panelcfg.Service,
	auditsSvc *audits.Service,
	bootstrapSvc *bootstrap.Service,
	subLimiter *ratelimit.Limiter,
) http.Handler {
	r := chi.NewRouter()

	// Built-in middlewares (recover, real IP, request ID, logger).
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
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

		// Hosts CRUD — Phase 1. Hosts reference nodes by id,
		// so the hosts service is constructed in main.go with
		// the nodes service as a dependency.
		r.Mount("/hosts", hosts.Router(hostsSvc, authSvc.Middleware()))

		// Users CRUD — admin surface. List / get / create /
		// patch / rotate-token. The /api/v1/users/{id}/sub
		// sub-token endpoint (the public, per-user
		// subscription URL) is mounted separately above
		// under the subscription Router; that path is
		// unauthenticated by design.
		r.Mount("/users", subscription.AdminRouter(subscriptionSvc, authSvc.Middleware()))

		// Subscription URL — the public endpoint that
		// turns a sub_token into a base64 / sing-box /
		// Clash / html payload. Mounted under /sub so
		// the route is short for the operator's
		// documentation. The default mount at
		// /api/v1/sub/<token> stays live in parallel
		// with the rotated mount at the top level
		// (added below) so the panel always serves
		// subscriptions, even when the operator has
		// not yet rotated the sub_path.
		r.Mount("/sub", subscription.RouterWithLimiter(subscriptionSvc, subLimiter))

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
	if active, err := panelCfgSvc.GetActive(context.Background()); err == nil && active.SubPath != "" {
		r.Mount("/"+active.SubPath+"/sub", subscription.RouterWithLimiter(subscriptionSvc, subLimiter))
	} else if err != nil {
		log.Warn().Err(err).Msg("router: panelcfg read failed; rotated sub_path mount skipped")
	}

	log.Info().Msg("v1 router initialised (auth + nodes + subscription mounted)")
	return r
}
