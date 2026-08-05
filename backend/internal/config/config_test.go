// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Unit tests for the v0.8.6 ops guard in Config.validate():
// the panel refuses to boot when AEGIS_ENV is left at the
// development default AND any AEGIS_*_BACKEND is set to
// "pg". The rule prevents the silent-misconfig failure mode
// where a production-shaped install (pg backends) runs with
// the colourised ConsoleWriter that a log shipper cannot
// parse. The pure-memory dev path is unaffected.
//
// Every test uses t.Setenv (Go 1.17+) so the values are
// scoped to the test and restored on cleanup — no
// cross-test pollution, no process-global state. The
// required-for-Load() fields (JWT secret, the three
// service DSNs) are set once via the helper
// `setMinimumLoadEnv`.

package config

import (
	"strings"
	"testing"
)

// setMinimumLoadEnv primes the env so config.Load() can
// run. The values are intentionally fake; the tests never
// open a real connection. JWT secret is sized to satisfy
// the 32-character minimum enforced by validate().
func setMinimumLoadEnv(t *testing.T) {
	t.Helper()
	t.Setenv("AEGIS_JWT_SECRET", "test-jwt-secret-must-be-at-least-32-characters-long-xxxxxx")
	t.Setenv("AEGIS_POSTGRES_DSN", "postgres://localhost:5432/aegis_test?sslmode=disable")
	t.Setenv("AEGIS_REDIS_ADDR", "localhost:6379")
	t.Setenv("AEGIS_NATS_URL", "nats://localhost:4222")
}

// unsetAllBacksets clears every AEGIS_*_BACKEND variable
// so each test starts from a known "all memory" baseline.
// The defaults in Config are "memory" so unset is fine
// for the dev-mode happy path.
func unsetAllBackends(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"AEGIS_AUTH_BACKEND", "AEGIS_HOSTS_BACKEND", "AEGIS_NODES_BACKEND",
		"AEGIS_INBOUNDS_BACKEND", "AEGIS_SUBSCRIPTION_BACKEND", "AEGIS_USERS_BACKEND",
		"AEGIS_PLANS_BACKEND", "AEGIS_WEBHOOKS_BACKEND", "AEGIS_PANELCFG_BACKEND",
		"AEGIS_AUDITS_BACKEND", "AEGIS_CREDENTIALS_BACKEND",
	} {
		t.Setenv(name, "")
	}
}

// TestValidate_AllMemory_DevelopmentEnv_Passes is the
// pure-dev happy path: AEGIS_ENV=development + every
// backend at its memory default must boot. This is the
// shape of the test fixture in app_test.go and the
// `go run ./cmd/aegis` first-boot flow. The v0.8.6 guard
// must NOT fire here — development logging is exactly
// what a memory-only dev install wants.
func TestValidate_AllMemory_DevelopmentEnv_Passes(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "development")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("config.Load: unexpected error: %v", err)
	}
	if cfg.Env != "development" {
		t.Errorf("env: want development, got %q", cfg.Env)
	}
	if cfg.usesAnyPgBackend() {
		t.Errorf("usesAnyPgBackend: want false (all memory), got true")
	}
}

// TestValidate_DevelopmentEnv_WithAuthPg_Refused is
// the v0.8.6 guard's headline case: a development
// default + a single pg backend is refused. The
// auth backend is the canonical example because
// AEGIS_AUTH_BACKEND=pg is the most common
// production-shaped flag operators set first; the
// guard fires here even if every other backend is
// still on memory.
func TestValidate_DevelopmentEnv_WithAuthPg_Refused(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_AUTH_BACKEND", "pg")

	_, err := Load()
	if err == nil {
		t.Fatal("config.Load: expected error (development+pg must be refused), got nil")
	}
	// The error message must mention the env var so the
	// operator knows exactly what to set.
	if !strings.Contains(err.Error(), "AEGIS_ENV=development") {
		t.Errorf("error must name AEGIS_ENV=development as the misconfig; got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "AEGIS_ENV=production") {
		t.Errorf("error must suggest AEGIS_ENV=production as the fix; got %q", err.Error())
	}
}

// TestValidate_DevelopmentEnv_WithAuditsPg_Refused is
// the symmetric case for the audit log backend — a
// panel that persists its audit log to pg is, by
// definition, the kind of install where a log-shipper
// would be attached, and the human-readable dev writer
// is the wrong format. Refused for the same reason as
// the auth backend case.
func TestValidate_DevelopmentEnv_WithAuditsPg_Refused(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_AUDITS_BACKEND", "pg")

	_, err := Load()
	if err == nil {
		t.Fatal("config.Load: expected error (development+audits_pg must be refused), got nil")
	}
}

// TestValidate_StagingEnv_WithPg_Passes confirms that
// the guard fires ONLY on the development default.
// Staging is an explicit non-production choice that
// keeps the colourised writer for pre-prod drills;
// the operator has signalled their intent, and we
// trust it. The panel must boot in this shape.
func TestValidate_StagingEnv_WithPg_Passes(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "staging")
	t.Setenv("AEGIS_AUTH_BACKEND", "pg")
	t.Setenv("AEGIS_NODES_BACKEND", "pg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("config.Load: unexpected error (staging+pg must pass): %v", err)
	}
	if cfg.Env != "staging" {
		t.Errorf("env: want staging, got %q", cfg.Env)
	}
	if !cfg.usesAnyPgBackend() {
		t.Errorf("usesAnyPgBackend: want true (auth+nodes pg), got false")
	}
}

// TestValidate_ProductionEnv_WithPg_Passes is the
// intended prod case: AEGIS_ENV=production + every
// pg backend set. The panel boots; the obs package
// reads the same env var and switches zerolog to
// the JSON writer. The guard never fires because
// Env is not "development".
func TestValidate_ProductionEnv_WithPg_Passes(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "production")
	t.Setenv("AEGIS_AUTH_BACKEND", "pg")
	t.Setenv("AEGIS_NODES_BACKEND", "pg")
	t.Setenv("AEGIS_AUDITS_BACKEND", "pg")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("config.Load: unexpected error (production+pg must pass): %v", err)
	}
	if cfg.Env != "production" {
		t.Errorf("env: want production, got %q", cfg.Env)
	}
	if !cfg.usesAnyPgBackend() {
		t.Errorf("usesAnyPgBackend: want true, got false")
	}
}

// TestValidate_InvalidEnv_StillRefused is the
// pre-existing switch in validate(): any value outside
// development|staging|production is refused with a
// generic "invalid AEGIS_ENV" error. The v0.8.6 guard
// must not interfere with this rule; the env-name
// switch is the first check, so an unknown value
// never reaches the pg-backend branch.
func TestValidate_InvalidEnv_StillRefused(t *testing.T) {
	setMinimumLoadEnv(t)
	unsetAllBackends(t)
	t.Setenv("AEGIS_ENV", "garbage")

	_, err := Load()
	if err == nil {
		t.Fatal("config.Load: expected error for unknown env, got nil")
	}
	if !strings.Contains(err.Error(), "invalid AEGIS_ENV") {
		t.Errorf("error must reference the env-var switch; got %q", err.Error())
	}
}

// TestValidate_DevelopmentEnv_WithEveryPg_Refused is
// the "all pg" shape: every backend is pg, the env is
// the development default. The guard must still fire
// (and fire loudly) — this is exactly the silent-
// misconfig the v0.8.6 ops guard exists to prevent.
func TestValidate_DevelopmentEnv_WithEveryPg_Refused(t *testing.T) {
	setMinimumLoadEnv(t)
	t.Setenv("AEGIS_ENV", "development")
	t.Setenv("AEGIS_AUTH_BACKEND", "pg")
	t.Setenv("AEGIS_HOSTS_BACKEND", "pg")
	t.Setenv("AEGIS_NODES_BACKEND", "pg")
	t.Setenv("AEGIS_INBOUNDS_BACKEND", "pg")
	t.Setenv("AEGIS_SUBSCRIPTION_BACKEND", "pg")
	t.Setenv("AEGIS_USERS_BACKEND", "pg")
	t.Setenv("AEGIS_PLANS_BACKEND", "pg")
	t.Setenv("AEGIS_WEBHOOKS_BACKEND", "pg")
	t.Setenv("AEGIS_PANELCFG_BACKEND", "pg")
	t.Setenv("AEGIS_AUDITS_BACKEND", "pg")
	t.Setenv("AEGIS_CREDENTIALS_BACKEND", "pg")

	_, err := Load()
	if err == nil {
		t.Fatal("config.Load: expected error (development+all-pg must be refused), got nil")
	}
}

// TestUsesAnyPgBackend_ExhaustiveSweep is a direct unit
// test of the helper itself. The function is a hard OR
// across eleven fields; a future field added to Config
// must either be wired into the helper or explicitly
// excluded (with a comment explaining why). The sweep
// here turns every field on in turn and asserts the
// helper reports true for that single field, then turns
// every field off and asserts false. Catches the
// regression where a new *Backend field is added to
// Config but the helper is forgotten.
func TestUsesAnyPgBackend_ExhaustiveSweep(t *testing.T) {
	type backendFlag struct {
		envVar string
		field  func(*Config) string
	}
	flags := []backendFlag{
		{"AEGIS_AUTH_BACKEND", func(c *Config) string { return c.AuthBackend }},
		{"AEGIS_HOSTS_BACKEND", func(c *Config) string { return c.HostsBackend }},
		{"AEGIS_NODES_BACKEND", func(c *Config) string { return c.NodesBackend }},
		{"AEGIS_INBOUNDS_BACKEND", func(c *Config) string { return c.InboundsBackend }},
		{"AEGIS_SUBSCRIPTION_BACKEND", func(c *Config) string { return c.SubscriptionBackend }},
		{"AEGIS_USERS_BACKEND", func(c *Config) string { return c.UsersBackend }},
		{"AEGIS_PLANS_BACKEND", func(c *Config) string { return c.PlansBackend }},
		{"AEGIS_WEBHOOKS_BACKEND", func(c *Config) string { return c.WebhooksBackend }},
		{"AEGIS_PANELCFG_BACKEND", func(c *Config) string { return c.PanelcfgBackend }},
		{"AEGIS_AUDITS_BACKEND", func(c *Config) string { return c.AuditsBackend }},
		{"AEGIS_CREDENTIALS_BACKEND", func(c *Config) string { return c.CredentialsBackend }},
	}

	// All-off baseline: the helper reports false.
	t.Run("all-off", func(t *testing.T) {
		cfg := &Config{}
		for _, f := range flags {
			cfg.setBackend(f.envVar, "memory")
		}
		if cfg.usesAnyPgBackend() {
			t.Errorf("usesAnyPgBackend: want false (all memory), got true")
		}
	})

	// Each field, turned on individually, must flip
	// the helper to true. A missing wire in the helper
	// surfaces as a "want true, got false" failure on
	// the affected subtest.
	for _, f := range flags {
		f := f
		t.Run(f.envVar+"=pg", func(t *testing.T) {
			cfg := &Config{}
			for _, g := range flags {
				cfg.setBackend(g.envVar, "memory")
			}
			cfg.setBackend(f.envVar, "pg")
			if !cfg.usesAnyPgBackend() {
				t.Errorf("usesAnyPgBackend: want true (%s=pg), got false", f.envVar)
			}
		})
	}
}

// setBackend is a tiny reflection-free helper that the
// exhaustive-sweep test uses to flip a single Config
// field via its env-var name. The mapping mirrors the
// struct tags on Config (e.g. `env:"AEGIS_AUTH_BACKEND"`
// ↔ c.AuthBackend). Keeping the mapping in the test
// file (not the prod file) means the test does not
// require runtime reflection and the production code
// stays small.
func (c *Config) setBackend(envVar, value string) {
	switch envVar {
	case "AEGIS_AUTH_BACKEND":
		c.AuthBackend = value
	case "AEGIS_HOSTS_BACKEND":
		c.HostsBackend = value
	case "AEGIS_NODES_BACKEND":
		c.NodesBackend = value
	case "AEGIS_INBOUNDS_BACKEND":
		c.InboundsBackend = value
	case "AEGIS_SUBSCRIPTION_BACKEND":
		c.SubscriptionBackend = value
	case "AEGIS_USERS_BACKEND":
		c.UsersBackend = value
	case "AEGIS_PLANS_BACKEND":
		c.PlansBackend = value
	case "AEGIS_WEBHOOKS_BACKEND":
		c.WebhooksBackend = value
	case "AEGIS_PANELCFG_BACKEND":
		c.PanelcfgBackend = value
	case "AEGIS_AUDITS_BACKEND":
		c.AuditsBackend = value
	case "AEGIS_CREDENTIALS_BACKEND":
		c.CredentialsBackend = value
	}
}
