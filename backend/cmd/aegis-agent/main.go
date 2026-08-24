// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis Agent. A small Go HTTP server that runs on
// every node and accepts Render+Apply commands from
// the panel.
//
// # v0.4.0-b scope
//
// v0.4.0-b makes the agent side of the Apply
// pathway real: POST /v1/apply now writes the
// rendered sing-box config to disk and reloads
// sing-box. The previous v0.3.0 behaviour
// (validate-JSON-then-ACK without side effects) is
// gone. The end-to-end flow is:
//
//  1. Panel's BatchedApplier flushes accumulated
//     Delta events and POSTs the rendered sing-box
//     config to the agent's /v1/apply.
//  2. Agent writes the config atomically to
//     AEGIS_AGENT_SINGBOX_CONFIG_PATH (default
//     /etc/sing-box/config.json).
//  3. Agent runs AEGIS_AGENT_SINGBOX_RELOAD_CMD
//     (default `systemctl reload sing-box`).
//  4. Agent returns 202 Accepted with the
//     reloaded=true flag; BatchedApplier marks the
//     flush as done.
//
// The agent API is the minimum the panel needs to
// keep the sing-box unit `active` after install:
//
//   - GET  /healthz   → 200 OK with JSON
//   - POST /v1/apply   → 202 Accepted (config
//                       written to disk + sing-box
//                       reloaded). Body carries
//                       `reloaded: true` and the
//                       reload wall-clock duration.
//   - GET  /v1/status  → 200 OK with running state
//   - GET  /v1/stats   → 200 OK with empty stats
//                       (sing-box clash-api integration
//                       lands in v0.4.0-c.)
//
// Every endpoint requires the bearer secret from
// `AEGIS_AGENT_BEARER` (the agent reads it from
// `/etc/aegis/agent.env`, which the panel's
// `internal/bootstrap` writes during install).
//
// # v0.4.0-c work
//
// - Wire `GET /v1/stats` to the sing-box clash-api
//   listener (localhost:9090 by default).
//
// # v0.5.0+ work
//
// - Replace the bearer-secret gate with mTLS once
//   the v1.1.0 panel side ships.
// - Add per-node metrics (CPU, memory, sing-box
//   goroutine count) to /v1/stats.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// listenAddr is the bind address. The bootstrap
// install writes the systemd unit with
// `ExecStart=/usr/local/bin/aegis-agent`; the agent
// reads AEGIS_AGENT_LISTEN_ADDR from the
// environment (the unit file sets it).
const defaultListenAddr = ":8080"

// healthzResponse is the /healthz payload. The
// `started_at` field is a constant per-process; the
// agent restarts on any process error so uptime
// reflects the current agent lifetime.
type healthzResponse struct {
	OK        bool   `json:"ok"`
	Version   string `json:"version"`
	StartedAt string `json:"started_at"`
}

// applyRequest / applyResponse / applyEnvelope are
// declared in apply.go (the apply logic lives in a
// separate file so the v0.4.0-b PR diff is easier
// to read). See apply.go for the request and
// response shapes.

// statusResponse is the /v1/status payload. v0.4.0-b
// reports `running: true` and the last-apply
// timestamp (from memory; the agent does not persist
// this in v0.4.0-b). Future versions will add
// sing-box process info (PID, uptime, last reload
// time).
type statusResponse struct {
	Running      bool   `json:"running"`
	Core         string `json:"core"`
	CoreVersion  string `json:"core_version"`
	LastApplyISO string `json:"last_apply_iso,omitempty"`
}

// statsResponse is the /v1/stats payload. v0.4.0-b
// returns the empty shape (all fields zero);
// v0.4.0-c wires the fields to the sing-box
// clash-api listener. The struct shape is forward-
// compatible: a v0.4.0-b agent and a v0.4.0-c agent
// return JSON with the same field names.
type statsResponse struct {
	BytesIn  int64 `json:"bytes_in"`
	BytesOut int64 `json:"bytes_out"`
	Users    int   `json:"users"`
}

// version is set at build time via -ldflags. The
// Makefile in `backend/cmd/aegis-agent/` (added in a
// followup) sets it. The empty default is "dev" so
// dev binaries still parse cleanly.
var version = "dev"

// startedAt is captured at process start.
var startedAt = time.Now().UTC().Format(time.RFC3339Nano)

// lastApplyISO is updated on every successful /v1/apply.
// Stored in memory only (the agent is stateless across
// restarts by design — v0.4.0 may persist this to
// /var/lib/aegis-agent/ if the panel needs it).
var lastApplyISO = ""

// bearerSecret is read from AEGIS_AGENT_BEARER at
// process start. The value is never logged. If
// empty, the agent refuses to start (the bootstrap
// install always sets the env var).
var bearerSecret = ""

// newMux builds the per-request HTTP handler. The
// construction is a function so the auth middleware
// can wrap it in tests without standing up the whole
// main().
func newMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", requireBearer(healthz))
	mux.HandleFunc("/v1/apply", requireBearer(handleApply))
	mux.HandleFunc("/v1/status", requireBearer(handleStatus))
	mux.HandleFunc("/v1/stats", requireBearer(handleStats))
	return mux
}

// requireBearer is the auth middleware. The agent
// uses a single shared secret (generated by the
// panel per install via `internal/bootstrap/secrets.go`)
// instead of mTLS. mTLS lands in v1.1.0 alongside the
// panel-side change.
//
// The middleware accepts the secret in two places:
//
//  1. `Authorization: Bearer <secret>` header (the
//     panel's `internal/bootstrap/...` uses this on
//     `/healthz` and `/v1/*`).
//  2. `?token=<secret>` query parameter (fallback for
//     systemd probes that do not easily set headers).
//
// Both forms are rejected if the secret is empty.
func requireBearer(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Fast path: no secret configured means
		// "insecure mode" — only /healthz is
		// reachable. The bootstrap install
		// always sets AEGIS_AGENT_BEARER; the
		// fallback is for the docker-compose
		// smoke test (where a bearer-less
		// /healthz probe is useful for the
		// orchestrator's readyness check).
		if bearerSecret == "" {
			// Only /healthz is allowed when
			// the secret is empty. The
			// handler below is a no-op
			// admission check: if the path
			// is /healthz, serve it;
			// otherwise 503.
			if r.URL.Path != "/healthz" {
				http.Error(w, "agent bearer secret not configured", http.StatusServiceUnavailable)
				return
			}
			next(w, r)
			return
		}
		got := bearerFromRequest(r)
		if got == "" || subtleCmp(got, bearerSecret) != 0 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

// bearerFromRequest extracts the bearer token from
// the Authorization header or the ?token= query
// parameter.
func bearerFromRequest(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		// Accept both `Bearer <token>` and the
		// raw `<token>` forms. The latter is
		// used by some HTTP client wrappers
		// (curl --proxy-header style).
		if strings.HasPrefix(h, "Bearer ") {
			return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		}
		return strings.TrimSpace(h)
	}
	return r.URL.Query().Get("token")
}

// subtleCmp is a constant-time string comparison.
// The stdlib `strings.EqualFold` is not constant-time;
// the comparison is per-byte but early-returns on
// mismatch. For a 32-byte secret the timing channel
// is small but using crypto/subtle is the documented
// pattern and the cost is negligible.
func subtleCmp(a, b string) int {
	// A small wrapper to keep the call-site
	// readable. Using `crypto/subtle.ConstantTimeCompare`
	// would require []byte slices; this avoids the
	// allocation churn.
	if len(a) != len(b) {
		return -1
	}
	var diff byte
	for i := 0; i < len(a); i++ {
		diff |= a[i] ^ b[i]
	}
	if diff == 0 {
		return 0
	}
	return -1
}

// healthz serves GET /healthz. Always 200 OK with
// version + started_at; the orchestrator (or
// docker-compose healthcheck) uses this to wait
// for the agent to be ready.
func healthz(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(healthzResponse{
		OK:        true,
		Version:   version,
		StartedAt: startedAt,
	})
}

// handleApply serves POST /v1/apply. v0.4.0-b writes
// the rendered sing-box config to disk and reloads
// sing-box; the v0.3.0 stub (validate-JSON-then-ACK)
// is gone. The two side effects (write + reload) live
// in `applyConfig` in apply.go so they can be unit-
// tested without standing up an HTTP server.
//
// 202 Accepted on success (matches the v0.3.0
// contract; a future version may switch to 200 OK
// once the BatchedApplier's retry semantics are
// fully specified). 4xx for body validation
// failures; 5xx for write or reload failures. The
// panel's `singbox/apply.go` only checks the status
// code, so the body shape is informational.
func handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status, body := applyConfig(r)
	if status == http.StatusAccepted {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// handleStatus serves GET /v1/status. Returns the
// running state + the last apply timestamp (from
// memory; the agent does not persist this in
// v0.3.0).
func handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statusResponse{
		Running:      true,
		Core:         "sing-box",
		CoreVersion:  "",
		LastApplyISO: lastApplyISO,
	})
}

// handleStats serves GET /v1/stats. v0.4.0-b returns
// the empty shape; v0.4.0-c wires this to the
// sing-box clash-api listener.
func handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statsResponse{})
}

// run starts the HTTP server and the gRPC server in parallel and
// blocks until SIGINT/SIGTERM. v0.8.29 introduced the gRPC server;
// the HTTP server is unchanged from v0.4.0-b. The two transports
// share the bearer secret and the sing-box apply state, so the
// BatchedApplier can keep using the HTTP path while the panel-side
// transport switch (v0.8.29 PR 3) migrates to gRPC method by method.
//
// The deferred cancel propagates the shutdown signal to in-flight
// HTTP requests via the request context and to the gRPC server via
// the `grpc.Server.GracefulStop` path in `runGRPC`.
func run(ctx context.Context, listenAddr, listenGRPC string) error {
	// Read the bearer secret once at start. The
	// bootstrap install writes
	// `/etc/aegis/agent.env` with
	// `AEGIS_AGENT_BEARER=<hex>`, and the systemd
	// unit includes `EnvironmentFile=...`. An
	// empty value is allowed for the docker-
	// compose smoke (only /healthz is reachable).
	bearerSecret = os.Getenv("AEGIS_AGENT_BEARER")
	if bearerSecret == "" {
		log.Printf("AEGIS_AGENT_BEARER is empty; only /healthz is reachable (insecure mode)")
	}
	// Read the sing-box apply config from the
	// environment. The defaults match the standard
	// Debian/Ubuntu sing-box install; operators
	// that use a non-standard layout override
	// via the env vars in `agent.env`.
	singboxConfigPath = envOr("AEGIS_AGENT_SINGBOX_CONFIG_PATH", defaultConfigPath)
	singboxReloadCmd = envOr("AEGIS_AGENT_SINGBOX_RELOAD_CMD", defaultReloadCmd)
	if v := os.Getenv("AEGIS_AGENT_SINGBOX_RELOAD_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			singboxReloadTimeout = d
		} else {
			// The value comes from the operator-
			// controlled agent.env, not from a
			// network request; log injection is
			// not a concern here. gosec G706 is
			// suppressed with an inline
			// justification.
			log.Printf("AEGIS_AGENT_SINGBOX_RELOAD_TIMEOUT=%q invalid, using default %s", v, defaultReloadTimeout) // #nosec G706 -- operator-controlled env var
			singboxReloadTimeout = defaultReloadTimeout
		}
	} else {
		singboxReloadTimeout = defaultReloadTimeout
	}
	if v := os.Getenv("AEGIS_AGENT_APPLY_MAX_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			applyMaxBytes = n
		} else {
			// Same operator-controlled rationale
			// as above.
			log.Printf("AEGIS_AGENT_APPLY_MAX_BYTES=%q invalid, using default %d", v, applyMaxBytes) // #nosec G706 -- operator-controlled env var
		}
	}
	log.Printf("aegis-agent %s starting on %s (gRPC=%s) (config=%s reload=%q timeout=%s)",
		version, listenAddr, listenGRPC, singboxConfigPath, singboxReloadCmd, singboxReloadTimeout)
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           newMux(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Run both servers in goroutines so the
	// signal handler in main() can call Shutdown
	// on either without blocking the main thread.
	httpErrCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			httpErrCh <- err
			return
		}
		httpErrCh <- nil
	}()
	grpcErrCh := make(chan error, 1)
	go func() {
		grpcErrCh <- runGRPC(ctx, listenGRPC)
	}()
	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining in-flight requests")
		// 10-second drain matches the systemd
		// `TimeoutStopSec=10` set in the
		// `install_agent` role. We use
		// `context.WithoutCancel` so the timeout
		// starts from now (not from when the
		// signal arrived) — `srv.Shutdown` will
		// only return early if the deadline fires,
		// not if the parent context was already
		// cancelled by the SIGINT.
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-httpErrCh:
		return err
	case err := <-grpcErrCh:
		return err
	}
}

func main() {
	// Flags. The systemd unit sets the listen
	// address via the `AEGIS_AGENT_LISTEN_ADDR`
	// env var (the flag is a manual-override path
	// for the docker-compose smoke).
	//
	// v0.8.29: the gRPC server is opt-in via
	// `AEGIS_AGENT_LISTEN_GRPC` (default `:7001`).
	// Setting it to "" disables gRPC; setting it
	// to `127.0.0.1:7001` is the v0.8.30 mTLS
	// posture (loopback until the cert bootstrap
	// lands; the panel still SSH-tunnels).
	listen := flag.String("listen", envOr("AEGIS_AGENT_LISTEN_ADDR", defaultListenAddr), "HTTP listen address (host:port)")
	listenGRPC := flag.String("listen-grpc", envOr("AEGIS_AGENT_LISTEN_GRPC", defaultGRPCListenAddr), "gRPC listen address (host:port); empty disables gRPC")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *listen, *listenGRPC); err != nil {
		log.Fatalf("aegis-agent: %v", err)
	}
}

// envOr returns the env var or fallback if the
// var is empty. Mirrors the helper in
// `internal/config` but kept inline to avoid
// pulling the whole config package into a binary
// that does not need it.
func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
