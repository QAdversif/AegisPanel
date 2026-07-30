// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package obs initialises tracing, metrics, and request-scoped logging.
//
// This is a placeholder for Phase 0 — real OpenTelemetry / Prometheus
// wiring lands once the boot order is stable (see ARCHITECTURE.md §14).

package obs

import (
	"context"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/config"
)

// AEGISEnvProduction is the value the AEGIS_ENV env var takes
// when the panel should emit JSON-formatted logs (one record
// per line, suitable for log shippers). Any other value
// (the default "development", "staging", ...) produces the
// human-readable console writer for dev-time ergonomics.
const AEGISEnvProduction = "production"

// CleanupFunc releases observability resources (flush, close, etc.).
type CleanupFunc func(ctx context.Context) error

// ConfigureLogger sets the global zerolog output format. The rule
// is binary:
//
//   - AEGIS_ENV=production  →  JSON to stderr (one record per line)
//   - anything else         →  ConsoleWriter to stderr (colorised, RFC3339)
//
// The function reads AEGIS_ENV directly (NOT from cfg) so it can
// be called BEFORE config.Load() — that way, if config.Load()
// itself logs.Fatal on a misconfiguration, the error line is
// already in the right format. Config validation is the first
// thing the boot does; the format must be ready before that.
//
// This is safe to call multiple times (the second call just
// overwrites the global logger). Tests rely on the global
// logger being reset to the default between subtests.
func ConfigureLogger() {
	configureLoggerTo(os.Stderr)
}

// configureLoggerTo is the testable seam: it applies the same
// rule as ConfigureLogger but writes to the provided writer
// instead of os.Stderr. Production code calls ConfigureLogger;
// tests use this directly to capture output.
func configureLoggerTo(w io.Writer) {
	if os.Getenv("AEGIS_ENV") == AEGISEnvProduction {
		log.Logger = zerolog.New(w).
			With().Timestamp().Logger()
		return
	}
	log.Logger = log.Output(zerolog.ConsoleWriter{Out: w, TimeFormat: time.RFC3339}).
		With().Timestamp().Logger()
}

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
