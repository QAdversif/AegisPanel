// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package obs initialises tracing, metrics, and request-scoped logging.
//
// This is a placeholder for Phase 0 — real OpenTelemetry / Prometheus
// wiring lands once the boot order is stable (see ARCHITECTURE.md §14).

package obs

import (
	"context"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/config"
)

// CleanupFunc releases observability resources (flush, close, etc.).
type CleanupFunc func(ctx context.Context) error

// Init wires up the standard observability stack. The returned cleanup
// function must be called before the process exits.
func Init(cfg *config.Config) (CleanupFunc, error) {
	log.Info().Msg("observability: minimal init (tracing+metrics land in Phase 1)")
	return func(_ context.Context) error { return nil }, nil
}

// Middleware attaches request-scoped logging + Prometheus metrics to a
// standard http.Handler.
func Middleware(next http.Handler) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/", withLogger(next))
	return mux
}

func withLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Debug().
			Str("method", r.Method).
			Str("path", r.URL.Path).
			Str("remote", r.RemoteAddr).
			Msg("request")
		next.ServeHTTP(w, r)
	})
}
