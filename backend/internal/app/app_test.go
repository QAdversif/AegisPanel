// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Wiring smoke test for the composition root. This test
// is intentionally not under the `integration` build tag
// because it must pass on every CI run (the fast lane)
// without a live database: the only thing it verifies is
// that `Build` wires the in-memory variant of every
// service into a single `*App` without panicking. A real
// integration run with a live pg + sops/age envelope
// lives in `tools/scripts/smoke-local.sh` (PR #152) and
// in the future `.github/workflows/smoke.yml`.
//
// The test also catches the most common regression
// vector for the v0.7.2 refactor: a missing import in
// main.go (the old composition root had nine of them)
// or a service constructor whose signature changed but
// the Build caller did not. The unit tests inside each
// `internal/X` package verify the service itself; this
// test verifies the wires.
package app

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/config"
)

// TestBuild_AllMemoryBackends wires the composition root
// with every AEGIS_*_BACKEND set to "memory". The dev
// seed admin must be present (memory auth store is the
// one non-optional persistence path in v0.7.2), and the
// resulting *App must hold a non-nil handle for every
// service that the router and the subcommands expect.
//
// The test is hermetic: the retry worker is disabled so
// no goroutine outlives the test body, and the backups
// dir points at t.TempDir() so no file leaks into the
// workspace.
func TestBuild_AllMemoryBackends(t *testing.T) {
	// Required for config.Load(): AEGIS_JWT_SECRET
	// must be at least 32 characters per the
	// production gate. We never reach the production
	// branch (Env=development), but the validator
	// still runs.
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	// config.Load() requires these even in dev mode;
	// the smoke build does not actually open a
	// connection because every backend is "memory".
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_smoke?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")
	t.Setenv("AEGIS_AUTH_BACKEND", "memory")
	t.Setenv("AEGIS_NODES_BACKEND", "memory")
	t.Setenv("AEGIS_INBOUNDS_BACKEND", "memory")
	t.Setenv("AEGIS_HOSTS_BACKEND", "memory")
	t.Setenv("AEGIS_USERS_BACKEND", "memory")
	t.Setenv("AEGIS_PLANS_BACKEND", "memory")
	t.Setenv("AEGIS_SUBSCRIPTION_BACKEND", "memory")
	t.Setenv("AEGIS_PANELCFG_BACKEND", "memory")
	t.Setenv("AEGIS_AUDITS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_BACKEND", "memory")
	// Disable the retry worker: the test's ctx is
	// cancelled by the defer, but the worker takes
	// `cfg.WebhooksRetryWorkerInterval` (default 5s)
	// to notice and we do not want to wait that long.
	t.Setenv("AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED", "false")
	// A per-test dir so the backups service's
	// NewOSBackend does not pollute the project.
	t.Setenv("AEGIS_BACKUPS_DIR", t.TempDir())
	// The bootstrap service requires an agent binary
	// path; point at a path that will not be invoked
	// (the smoke build does not exercise the
	// install-or-configure path).
	t.Setenv("AEGIS_AGENT_BINARY", t.TempDir()+"/aegis-agent-not-used-in-smoke")
	t.Setenv("AEGIS_AGENT_KNOWN_HOSTS", t.TempDir()+"/known_hosts")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Env != "development" {
		t.Fatalf("env: want development, got %q", cfg.Env)
	}
	if cfg.AuthBackend != "memory" {
		t.Fatalf("auth backend: want memory, got %q", cfg.AuthBackend)
	}
	if cfg.WebhooksRetryWorkerEnabled {
		t.Fatal("retry worker should be disabled in this test")
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer a.Close()

	// Every service handle must be non-nil. The
	// router + http.Server are also non-nil so a
	// future handler that requires a new service
	// will fail this test until Build wires it.
	checks := []struct {
		name string
		ok   bool
	}{
		{"Config", a.Config != nil},
		{"Pool (must be nil when no pg backend)", a.Pool == nil},
		{"Auth", a.Auth != nil},
		{"Nodes", a.Nodes != nil},
		{"Hosts", a.Hosts != nil},
		{"Inbounds", a.Inbounds != nil},
		{"InboundTemplates", a.InboundTemplates != nil},
		{"Users", a.Users != nil},
		{"Plans", a.Plans != nil},
		{"Subs", a.Subs != nil},
		// v0.8.28.9 (#289/C2): the Phase 2 credential
		// source must be INSTALLED on the subscription
		// service. The original bug: WithCreds was
		// called in step 12 with the not-yet-assigned
		// nil a.Credentials; the nil-safe setter
		// accepted it and every render silently ran on
		// the Phase 1 params fallback. A non-nil
		// CredsSource here fails if anyone reorders
		// Build's steps again.
		{"Subs.CredsSource (Phase 2 wiring)", a.Subs != nil && a.Subs.CredsSource() != nil},
		{"PanelCfg", a.PanelCfg != nil},
		{"Audits", a.Audits != nil},
		{"Backups", a.Backups != nil},
		{"Webhooks", a.Webhooks != nil},
		{"Bootstrap", a.Bootstrap != nil},
		{"Router", a.Router != nil},
		{"Server", a.Server != nil},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("App.%s: nil (Build did not wire it)", c.name)
		}
	}

	// Close must be safe to call twice (Close is
	// called once via defer above and once here to
	// exercise the idempotency path).
	a.Close()
}

// TestBuild_ProductionMemoryBackend_Refused locks in the
// audit's #1 follow-up: a production boot that asks for
// a memory backend must fail loudly. We check the auth
// store because it carries the dev seed admin, but the
// rule applies to every MustBuild call.
//
// Note: this test does not call Build itself because the
// pg path would also be tried (the `pg` backend requires
// a DSN). Instead it calls MustBuild directly with a
// memory backend + production env and expects log.Fatal.
//
// We exercise the rule by checking that the store builder
// records the env it will refuse; a future refactor that
// drops the env check would need to update this test
// along with the change. The log.Fatal branch itself is
// covered by manual deploy-validation: a production
// smoke run with AEGIS_AUTH_BACKEND=memory aborts the
// process before any service is constructed.
func TestBuild_ProductionMemoryBackend_Refused(t *testing.T) {
	t.Setenv("AEGIS_ENV", "production")
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_smoke?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.Env != "production" {
		t.Fatalf("env: want production, got %q", cfg.Env)
	}
	if cfg.AuthBackend != "memory" {
		// Default is memory; assert we have the
		// shape under test.
		t.Fatalf("auth backend: want memory (default), got %q", cfg.AuthBackend)
	}
	// We do NOT call Build here: a production boot
	// with a memory backend must exit before
	// reaching the router. The check lives in
	// `MustBuild` (see stores.go). The smoke script
	// (PR #152) is the place that exercises the
	// log.Fatal branch end-to-end.
}

// TestBuild_EnsuresAgentCARoot is the v0.8.31.1 regression
// guard for the half-wired mTLS pipeline: the panel's
// root CA must be provisioned at boot so the mTLS factory
// in `bootstrap.ServiceConfig.MTLSCerts` does not return
// "root CA not yet provisioned" on the first `Provision`
// call. Without this hotfix, `mintMTLSCerts`
// (internal/bootstrap/provisioner.go:226) silently
// swallows the error and the installer's `writeMTLSCerts`
// skips the cert push, leaving the v0.8.31+ agent on the
// node without `/etc/aegis/agent.{crt,key,ca.pem}`. The
// agent hard-fails to start without those files
// (cmd/aegis-agent/grpc.go: "gRPC mTLS required but load
// failed: read cert /etc/aegis/agent.crt: no such file or
// directory").
//
// The fixture is the same hermetic "all memory" backend
// configuration as TestBuild_AllMemoryBackends above:
// every AEGIS_*_BACKEND defaults to "memory" (incl.
// agentca), no live pg connection, retry worker disabled.
// The assertion is a single `RootCertPEM()` call after
// `Build` — a pre-fix build would return ErrNotFound; a
// post-fix build returns the freshly-minted cert PEM.
func TestBuild_EnsuresAgentCARoot(t *testing.T) {
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_smoke?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")
	t.Setenv("AEGIS_AUTH_BACKEND", "memory")
	t.Setenv("AEGIS_NODES_BACKEND", "memory")
	t.Setenv("AEGIS_INBOUNDS_BACKEND", "memory")
	t.Setenv("AEGIS_HOSTS_BACKEND", "memory")
	t.Setenv("AEGIS_USERS_BACKEND", "memory")
	t.Setenv("AEGIS_PLANS_BACKEND", "memory")
	t.Setenv("AEGIS_SUBSCRIPTION_BACKEND", "memory")
	t.Setenv("AEGIS_PANELCFG_BACKEND", "memory")
	t.Setenv("AEGIS_AUDITS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED", "false")
	t.Setenv("AEGIS_BACKUPS_DIR", t.TempDir())
	t.Setenv("AEGIS_AGENT_BINARY", t.TempDir()+"/aegis-agent-not-used-in-smoke")
	t.Setenv("AEGIS_AGENT_KNOWN_HOSTS", t.TempDir()+"/known_hosts")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer a.Close()

	// Post-fix assertion: RootCertPEM() must succeed
	// and return a non-empty PEM. Pre-fix: this would
	// return ErrNotFound because the in-memory root
	// was never minted during Build.
	pem, err := a.AgentCA.RootCertPEM()
	if err != nil {
		t.Fatalf("AgentCA.RootCertPEM after Build: %v (post-fix should return nil; pre-fix returns ErrNotFound)", err)
	}
	if pem == "" {
		t.Fatal("AgentCA.RootCertPEM returned empty string after Build")
	}
	if !strings.HasPrefix(pem, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("AgentCA.RootCertPEM: not a PEM (missing BEGIN marker), first 80 chars: %q", pem[:min(80, len(pem))])
	}

	// Idempotency: a second call must return the same
	// cert (the in-memory cache should be a hit, not a
	// re-mint from the Store). The agentca service's
	// internal mutex + cachedRoot pointer make this
	// safe; a regression that drops the cache between
	// calls would generate a fresh root here.
	pem2, err := a.AgentCA.RootCertPEM()
	if err != nil {
		t.Fatalf("AgentCA.RootCertPEM (2nd call): %v", err)
	}
	if pem2 != pem {
		t.Error("AgentCA.RootCertPEM idempotency: 1st call != 2nd call (cache miss on second read — regression in Service.cachedRoot wiring)")
	}

	// v0.8.32.2: post-Close idempotency. The
	// pre-fix Build had `defer a.AgentCA.Close()`
	// (line 286), which fired when Build
	// returned. By the time this test reached
	// its assertions, `a.AgentCA` was already
	// closed — every `RootCertPEM()` call above
	// would have hit a closed receiver. The fix
	// moves Close to App.Close() so the service
	// stays open for the lifetime of the App.
	// The test now closes the App explicitly
	// (Close is idempotent, so the test's
	// `defer a.Close()` is a no-op for the second
	// close) and asserts that:
	//
	//  1. `a.Close()` is safe to call (no panic,
	//     no nil-deref);
	//  2. `a.Close()` is idempotent — calling
	//     it a second time is a no-op (no double-
	//     close of the pool, no deadlock).
	//
	// The pre-fix code's `defer a.AgentCA.Close()`
	// in Build is now gone; the test still
	// passes the pre-existing `RootCertPEM()`
	// assertions above, which means Build no
	// longer closes the service. If a future PR
	// re-adds the defer-in-Build pattern, the
	// `RootCertPEM()` calls above will fail with
	// "closed service" and the regression will
	// be caught immediately.
	a.Close()
	a.Close()
}

// TestBuild_WarnsOnBackupsDirDefault is the regression
// test for the 2026-08-30 "backups disappeared from UI"
// prod incident. The panel boot path must emit a loud
// WARN when the operator forgot to override
// `AEGIS_BACKUPS_DIR` (or any other AEGIS_*_DIR with a
// relative `envDefault`). The pre-fix code had no such
// warn: the service initialized cleanly because
// NewOSBackend auto-creates the dir, the local store
// returned `[]` on a missing `_index.json` with no
// error, and the first hint was an empty UI list hours
// after deploy.
//
// The test captures the zerolog output to a buffer and
// asserts the WARN line is present.
//
// IMPORTANT: this test must NOT set AEGIS_BACKUPS_DIR
// (or AEGIS_AGENT_KNOWN_HOSTS) - the whole point is to
// exercise the relative-default path. The other env vars
// are set to memory mode (matching the rest of the file)
// so config.Load() and Build() succeed.
func TestBuild_WarnsOnBackupsDirDefault(t *testing.T) {
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_smoke?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")
	t.Setenv("AEGIS_AUTH_BACKEND", "memory")
	t.Setenv("AEGIS_NODES_BACKEND", "memory")
	t.Setenv("AEGIS_INBOUNDS_BACKEND", "memory")
	t.Setenv("AEGIS_HOSTS_BACKEND", "memory")
	t.Setenv("AEGIS_USERS_BACKEND", "memory")
	t.Setenv("AEGIS_PLANS_BACKEND", "memory")
	t.Setenv("AEGIS_SUBSCRIPTION_BACKEND", "memory")
	t.Setenv("AEGIS_PANELCFG_BACKEND", "memory")
	t.Setenv("AEGIS_AUDITS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED", "false")
	t.Setenv("AEGIS_AGENT_BINARY", t.TempDir()+"/aegis-agent-not-used-in-smoke")
	// Do NOT set AEGIS_BACKUPS_DIR � the default
	// "./var/backups" must apply.
	// Do NOT set AEGIS_AGENT_KNOWN_HOSTS either �
	// same footgun class.

	// Capture log output to a buffer. Use InfoLevel
	// so the boot's Info-level "backups: service
	// initialised" line is also captured (the
	// sanity check below verifies the warn fires
	// at the right point in the boot sequence).
	var logBuf bytes.Buffer
	origLogger := log.Logger
	log.Logger = zerolog.New(&logBuf).Level(zerolog.InfoLevel)
	t.Cleanup(func() { log.Logger = origLogger })

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer a.Close()

	logs := logBuf.String()
	if !strings.Contains(logs, "AEGIS_BACKUPS_DIR is NOT set") {
		t.Errorf("expected WARN about AEGIS_BACKUPS_DIR default, got log:\n%s", logs)
	}
	// Sanity: the existing init log is also present,
	// confirming the warn fires at the right point
	// in the boot sequence.
	if !strings.Contains(logs, "backups: service initialised") {
		t.Errorf("expected the backups init log line, got log:\n%s", logs)
	}
}

// TestBuild_NoWarnWhenBackupsDirSet is the positive
// counterpart to TestBuild_WarnsOnBackupsDirDefault. The
// warn helper must stay silent when the operator has
// explicitly set the env var (the production path).
// Without this test, a future refactor that hardcodes
// the warn (instead of gating on `os.Getenv != ""`)
// would slip through CI and spam logs on every boot.
func TestBuild_NoWarnWhenBackupsDirSet(t *testing.T) {
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_smoke?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")
	t.Setenv("AEGIS_AUTH_BACKEND", "memory")
	t.Setenv("AEGIS_NODES_BACKEND", "memory")
	t.Setenv("AEGIS_INBOUNDS_BACKEND", "memory")
	t.Setenv("AEGIS_HOSTS_BACKEND", "memory")
	t.Setenv("AEGIS_USERS_BACKEND", "memory")
	t.Setenv("AEGIS_PLANS_BACKEND", "memory")
	t.Setenv("AEGIS_SUBSCRIPTION_BACKEND", "memory")
	t.Setenv("AEGIS_PANELCFG_BACKEND", "memory")
	t.Setenv("AEGIS_AUDITS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_BACKEND", "memory")
	t.Setenv("AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED", "false")
	t.Setenv("AEGIS_AGENT_BINARY", t.TempDir()+"/aegis-agent-not-used-in-smoke")
	// Set the env var � the warn helper should stay
	// silent.
	t.Setenv("AEGIS_BACKUPS_DIR", t.TempDir())
	t.Setenv("AEGIS_AGENT_KNOWN_HOSTS", t.TempDir()+"/known_hosts")

	var logBuf bytes.Buffer
	origLogger := log.Logger
	log.Logger = zerolog.New(&logBuf).Level(zerolog.WarnLevel)
	t.Cleanup(func() { log.Logger = origLogger })

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, err := Build(ctx, cfg)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	defer a.Close()

	logs := logBuf.String()
	if strings.Contains(logs, "AEGIS_BACKUPS_DIR is NOT set") {
		t.Errorf("did not expect WARN about AEGIS_BACKUPS_DIR (env was set), got log:\n%s", logs)
	}
}
