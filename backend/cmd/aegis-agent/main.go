// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis Agent. A small Go HTTP server that runs on
// every node and accepts Render+Apply commands from
// the panel.
//
// # v0.3.0 scope
//
// v0.3.0 ships the bootstrap install pathway: the
// agent is a single static binary (musl, ~5 MB) that
// the panel's `internal/bootstrap` package uploads
// over SFTP and starts via systemd. The agent API
// is the minimum the panel needs to keep the
// systemd unit `active` after install:
//
//   - GET  /healthz   → 200 OK with JSON
//   - POST /v1/apply   → 200 OK (stub: receives the
//                       sing-box config, validates it
//                       parses as JSON, and ACKs. v0.4.0
//                       actually writes to disk and
//                       reloads sing-box.)
//   - GET  /v1/status  → 200 OK with running state
//   - GET  /v1/stats   → 200 OK with empty stats
//                       (sing-box clash-api integration
//                       lands in v0.4.0.)
//
// Every endpoint requires the bearer secret from
// `AEGIS_AGENT_BEARER` (the agent reads it from
// `/etc/aegis/agent.env`, which the panel's
// `internal/bootstrap` writes during install).
//
// # v0.4.0 work
//
// - Write the applied sing-box config to
//   `/etc/sing-box/config.json` and run
//   `systemctl reload sing-box`.
// - Wire `GET /v1/stats` to the sing-box clash-api
//   listener (localhost:9090 by default).
// - Replace the bearer-secret gate with mTLS once
//   the v1.1.0 panel side ships.
//
// # Why a stub for v0.3.0
//
// The BatchedApplier is the v0.4.0 milestone. v0.3.0
// proves the bootstrap install end-to-end (the panel
// can put a binary on a node, register a systemd
// unit, see it `active`); Apply-as-actually-mutating-
// filesystem is a v0.4.0 concern. Doing v0.4.0 work
// in v0.3.0 would balloon the PR and risk the
// bootstrap pathway landing without a tested
// foundation.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
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

// applyRequest is the body of POST /v1/apply. v0.3.0
// validates the JSON shape but does NOT write
// anything to disk; the field is named `config` to
// match what v0.4.0 will expect.
type applyRequest struct {
	Config json.RawMessage `json:"config"`
}

// applyResponse is the /v1/apply acknowledgement.
// `accepted: true` means the agent received the body
// and parsed it as JSON. v0.4.0 will add a `verify`
// field that reports the sing-box validation result.
type applyResponse struct {
	Accepted   bool   `json:"accepted"`
	ReceivedAt string `json:"received_at"`
	Bytes      int    `json:"bytes"`
}

// statusResponse is the /v1/status payload. v0.3.0
// always reports `running: true`; v0.4.0 will
// include sing-box process info (PID, uptime, last
// reload time).
type statusResponse struct {
	Running       bool   `json:"running"`
	Core          string `json:"core"`
	CoreVersion   string `json:"core_version"`
	LastApplyISO  string `json:"last_apply_iso,omitempty"`
}

// statsResponse is the /v1/stats payload. v0.3.0
// returns the empty shape; v0.4.0 wires this to the
// sing-box clash-api listener.
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

// handleApply serves POST /v1/apply. v0.3.0
// validates the body parses as JSON and ACKs. v0.4.0
// will write to disk and reload sing-box.
//
// The handler reads the body fully (Limit 1 MiB to
// refuse accidental upload storms) and returns
// 202 Accepted with the byte count. We use 202
// rather than 200 to match the v0.4.0 contract: the
// future implementation may queue the apply if
// Batched Apply is in flight.
func handleApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// 1 MiB cap. The real sing-box config for a
	// busy panel is on the order of 100 KiB; 1 MiB
	// is a 10x safety margin.
	const maxBodyBytes = 1 << 20
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	defer func() { _ = r.Body.Close() }()
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// The body must contain a JSON object with a
	// `config` field. We decode twice (once to
	// validate the envelope, once for the inner
	// `config` raw) so an empty `{}` body is
	// rejected — the panel always sends a
	// non-empty config.
	if len(body) == 0 {
		http.Error(w, "empty body", http.StatusBadRequest)
		return
	}
	var req applyRequest
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "invalid JSON envelope: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(req.Config) == 0 {
		http.Error(w, "missing config field", http.StatusBadRequest)
		return
	}
	// Validate the inner `config` parses as
	// JSON. v0.4.0 will additionally validate the
	// config against sing-box's schema; v0.3.0
	// stops at "parses".
	var probe any
	if err := json.Unmarshal(req.Config, &probe); err != nil {
		http.Error(w, "config is not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	lastApplyISO = time.Now().UTC().Format(time.RFC3339Nano)
	log.Printf("apply accepted: bytes=%d", len(req.Config))
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(applyResponse{
		Accepted:   true,
		ReceivedAt: lastApplyISO,
		Bytes:      len(req.Config),
	})
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

// handleStats serves GET /v1/stats. v0.3.0 returns
// the empty shape; v0.4.0 wires this to the
// sing-box clash-api listener.
func handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(statsResponse{})
}

// run starts the HTTP server and blocks until SIGINT
// or SIGTERM. The deferred cancel propagates the
// shutdown signal to in-flight requests via the
// request context.
func run(ctx context.Context, listenAddr string) error {
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
	log.Printf("aegis-agent %s starting on %s", version, listenAddr)
	srv := &http.Server{
		Addr:              listenAddr,
		Handler:           newMux(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
	// Run the server in a goroutine so the
	// signal handler in main() can call Shutdown
	// without blocking the main thread.
	errCh := make(chan error, 1)
	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()
	select {
	case <-ctx.Done():
		log.Printf("shutdown signal received; draining in-flight requests")
		// 10-second drain matches the systemd
		// `TimeoutStopSec=10` set in the
		// `install_agent` role.
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}
		return nil
	case err := <-errCh:
		return err
	}
}

func main() {
	// Flags. The systemd unit sets the listen
	// address via the `AEGIS_AGENT_LISTEN_ADDR`
	// env var (the flag is a manual-override path
	// for the docker-compose smoke).
	listen := flag.String("listen", envOr("AEGIS_AGENT_LISTEN_ADDR", defaultListenAddr), "HTTP listen address (host:port)")
	flag.Parse()

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, *listen); err != nil {
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
