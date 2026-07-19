// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package config loads runtime configuration from environment variables
// and (optionally) a `.env` file. The struct is populated by
// github.com/caarlos0/env and validated up-front in Load().

package config

import (
	"fmt"
	"time"

	"github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

// Config is the top-level runtime configuration for the Aegis panel.
type Config struct {
	// Environment: "development" / "staging" / "production".
	Env string `env:"AEGIS_ENV" envDefault:"development"`

	// Build info (injected at build time via ldflags).
	GitCommit string `env:"AEGIS_GIT_COMMIT" envDefault:"dev"`
	BuildTime string `env:"AEGIS_BUILD_TIME" envDefault:""`

	// HTTP listener. Caddy terminates TLS in front of this address.
	HTTPAddr string `env:"AEGIS_HTTP_ADDR" envDefault:":8080"`

	// Shutdown grace window before forcing exit.
	ShutdownTimeout time.Duration `env:"AEGIS_SHUTDOWN_TIMEOUT" envDefault:"30s"`

	// PostgreSQL connection string (pgx-compatible).
	PostgresDSN string `env:"AEGIS_POSTGRES_DSN,required"`

	// Redis connection string.
	RedisAddr string `env:"AEGIS_REDIS_ADDR,required"`
	RedisDB   int    `env:"AEGIS_REDIS_DB" envDefault:"0"`

	// NATS connection string (event bus).
	NATSURL string `env:"AEGIS_NATS_URL,required"`

	// ClickHouse DSN for metrics (Phase 1+).
	ClickHouseDSN string `env:"AEGIS_CLICKHOUSE_DSN" envDefault:""`

	// Secret path for JWT signing.
	JWTSecret string `env:"AEGIS_JWT_SECRET,required"`

	// Encrypted secrets (private keys, DB credentials). SOPS / Vault.
	SecretsBackend string `env:"AEGIS_SECRETS_BACKEND" envDefault:"sops"`

	// Auto-generated on first boot if empty. Used to derive
	// /s3cr3t-p4n3l-<hex> and /s3cr3t-sub-<hex> paths.
	PanelPathSecret string `env:"AEGIS_PATH_SECRET" envDefault:""`

	// Caddy admin API URL (used to reload Caddyfile at runtime).
	CaddyAdminURL string `env:"AEGIS_CADDY_ADMIN_URL" envDefault:"http://127.0.0.1:2019"`

	// AuthBackend selects the persistence layer for the auth
	// service. "memory" (default) keeps users + refresh tokens
	// in RAM — dev only. "pg" uses the PostgreSQL backend
	// (PgStore) and runs goose migrations on boot.
	AuthBackend string `env:"AEGIS_AUTH_BACKEND" envDefault:"memory"`

	// HostsBackend selects the persistence layer for the
	// hosts service. "memory" (default) embeds endpoints
	// in the Host struct — fine for dev / unit tests.
	// "pg" uses the PostgreSQL backend (PgStore) which
	// stores endpoints in a separate host_endpoints
	// table. The broader Phase 1 pg migration runs on
	// boot when this is "pg".
	HostsBackend string `env:"AEGIS_HOSTS_BACKEND" envDefault:"memory"`

	// NodesBackend selects the persistence layer for the
	// nodes service. "memory" (default) keeps nodes in
	// RAM — dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the `nodes` and `node_tags`
	// tables (migrations 0001 and 0005). The broader
	// Phase 1 pg migration runs on boot when this is
	// "pg".
	NodesBackend string `env:"AEGIS_NODES_BACKEND" envDefault:"memory"`

	// InboundsBackend selects the persistence layer for the
	// inbounds service. "memory" (default) keeps inbounds
	// in RAM — dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the `inbounds` table (migration
	// 0003). The broader Phase 1 pg migration runs on
	// boot when this is "pg".
	InboundsBackend string `env:"AEGIS_INBOUNDS_BACKEND" envDefault:"memory"`

	// SubscriptionBackend selects the persistence layer for
	// the subscription service. "memory" (default) keeps
	// users / plans / host_pools in RAM — dev only.
	// "pg" uses the PostgreSQL backend (PgStore) backed
	// by the `users`, `plans`, `plan_pool`, `host_pools`,
	// and `host_pool_members` tables (migration 0001)
	// plus the `users.sub_token_prev` columns (migration
	// 0011). The broader Phase 1 pg migration runs on
	// boot when this is "pg".
	SubscriptionBackend string `env:"AEGIS_SUBSCRIPTION_BACKEND" envDefault:"memory"`

	// PanelcfgBackend selects the persistence layer for the
	// panel-wide config service. "memory" (default) keeps
	// the panel_path_config rows in RAM — dev only.
	// "pg" uses the PostgreSQL backend (PgStore) backed
	// by the `panel_path_config` table (migration 0010).
	PanelcfgBackend string `env:"AEGIS_PANELCFG_BACKEND" envDefault:"memory"`

	// AuditsBackend selects the persistence layer for the
	// audit log. "memory" (default) keeps entries in
	// RAM — dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the existing `audit_log` table
	// from migration 0001. The pg path is the only
	// mode that survives a restart; the dev seed
	// leaves an empty list on every boot.
	AuditsBackend string `env:"AEGIS_AUDITS_BACKEND" envDefault:"memory"`

	// Decoy-site storage root (defaults to /var/www/decoy on panel host).
	DecoyRoot string `env:"AEGIS_DECOY_ROOT" envDefault:"/var/www/decoy"`

	// SubscriptionRateLimitRPS is the sustained
	// requests-per-second per sub_token the
	// subscription endpoint allows. 0 disables
	// rate limiting. The default (1 rps) is
	// generous for a single legitimate user with
	// multiple devices (a phone, a laptop, a
	// tablet, a desktop) that all wake up at
	// once after a 24h client poll cycle.
	SubscriptionRateLimitRPS float64 `env:"AEGIS_SUBSCRIPTION_RATELIMIT_RPS" envDefault:"1"`

	// SubscriptionRateLimitBurst is the maximum
	// bucket size per sub_token. A brand-new
	// sub_token can immediately make this many
	// requests; subsequent traffic is shaped by
	// RPS. 5 is the default: enough for "phone
	// wakes up + laptop wakes up + manual refresh
	// from the admin UI" without forcing a 429.
	SubscriptionRateLimitBurst float64 `env:"AEGIS_SUBSCRIPTION_RATELIMIT_BURST" envDefault:"5"`

	// SubscriptionRateLimitMaxKeys caps the
	// in-memory bucket map. Past the cap, the
	// least-recently-seen key is evicted. 0
	// disables the cap (the bucket map grows
	// unbounded; OK for a single-replica panel
	// with at most a few thousand unique tokens,
	// not OK for a long-running production
	// install with sub_token rotation churn).
	// 50k is a safe default.
	SubscriptionRateLimitMaxKeys int `env:"AEGIS_SUBSCRIPTION_RATELIMIT_MAX_KEYS" envDefault:"50000"`
}

// Load reads `.env` (if present) and then parses the environment.
// It returns a fully populated Config or an error describing what's wrong.
func Load() (*Config, error) {
	// .env is optional — ignore the "not found" error.
	_ = godotenv.Load()

	cfg := &Config{}
	if err := env.Parse(cfg); err != nil {
		return nil, fmt.Errorf("parse env: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return cfg, nil
}

func (c *Config) validate() error {
	switch c.Env {
	case "development", "staging", "production":
	default:
		return fmt.Errorf("invalid AEGIS_ENV=%q (want development|staging|production)", c.Env)
	}
	if c.HTTPAddr == "" {
		return fmt.Errorf("AEGIS_HTTP_ADDR must be set")
	}
	if len(c.JWTSecret) < 32 {
		return fmt.Errorf("AEGIS_JWT_SECRET must be at least 32 characters")
	}
	return nil
}
