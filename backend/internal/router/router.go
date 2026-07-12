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

	"github.com/aegispanel/aegis/internal/config"
)

// Build returns the v1 http.Handler for Aegis.
func Build(cfg *config.Config) http.Handler {
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

		// Module routers will be mounted here in Phase 0+:
		//   r.Mount("/auth",         auth.Router(cfg))
		//   r.Mount("/users",        users.Router(cfg))
		//   r.Mount("/nodes",        nodes.Router(cfg))
		//   r.Mount("/hosts",        hosts.Router(cfg))
		//   r.Mount("/subscriptions", subscriptions.Router(cfg))
		//   r.Mount("/cabinet",      cabinet.Router(cfg))
		//   r.Mount("/webhooks",     webhooks.Router(cfg))
	})

	log.Info().Msg("v1 router initialised (modules to be mounted)")
	return r
}
