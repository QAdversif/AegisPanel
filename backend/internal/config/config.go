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

	// Decoy-site storage root (defaults to /var/www/decoy on panel host).
	DecoyRoot string `env:"AEGIS_DECOY_ROOT" envDefault:"/var/www/decoy"`
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
