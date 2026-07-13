// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package router wires together the v1 HTTP routes. Each module
// (auth, users, nodes, hosts, subscriptions, …) will register its own
// subrouter here in Phase 0 / Phase 1.

package router

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/auth"
	"github.com/QAdversif/AegisPanel/internal/config"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// Build returns the v1 http.Handler for Aegis. The auth subrouter is
// wired into /api/v1/auth; its protected endpoints sit behind
// auth.Service.Middleware() and surface the verified Claims on the
// request context for downstream handlers. Other module routers
// (nodes, …) are mounted here too — see comments inline.
func Build(cfg *config.Config, authSvc *auth.Service, nodesSvc *nodes.Service) http.Handler {
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

		// Nodes CRUD — Phase 0. All routes are protected by the
		// auth middleware + ScopeNodes requirement (applied
		// inside nodes.Router itself).
		r.Mount("/nodes", nodes.Router(nodesSvc, authSvc.Middleware()))

		// OpenAPI spec + minimal self-contained index page.
		mountSwagger(r)

		// Module routers will be mounted here in Phase 0+:
		//   r.Mount("/users",        users.Router(cfg))
		//   r.Mount("/hosts",        hosts.Router(cfg))
		//   r.Mount("/subscriptions", subscriptions.Router(cfg))
		//   r.Mount("/cabinet",      cabinet.Router(cfg))
		//   r.Mount("/webhooks",     webhooks.Router(cfg))
	})

	log.Info().Msg("v1 router initialised (auth + nodes mounted)")
	return r
}
