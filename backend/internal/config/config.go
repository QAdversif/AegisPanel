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

	// AgentCABackend selects the persistence layer for the
	// agentca service (v0.8.30 mTLS cert bootstrap).
	// "memory" (default) keeps the root CA + per-node
	// certs in RAM — dev only (a panel restart loses
	// them). "pg" uses the PostgreSQL backend
	// (PgStore) backed by the `agentca` + `nodes.mtls_*`
	// columns (migration 0023). The mTLS handshake in
	// v0.8.30 PR 2 requires the pg backend in prod
	// (a panel restart without the certs on disk
	// would re-issue on next Apply, which the agent's
	// pinned trust store would reject).
	AgentCABackend string `env:"AEGIS_AGENTCA_BACKEND" envDefault:"memory"`

	// InboundsBackend selects the persistence layer for the
	// inbounds service. "memory" (default) keeps inbounds
	// in RAM — dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the `inbounds` table (migration
	// 0003). The broader Phase 1 pg migration runs on
	// boot when this is "pg".
	InboundsBackend string `env:"AEGIS_INBOUNDS_BACKEND" envDefault:"memory"`

	// SubscriptionBackend selects the persistence layer for
	// the subscription service. "memory" (default) keeps
	// plans / host_pools in RAM — dev only. "pg" uses the
	// PostgreSQL backend (PgStore) backed by the `plans`,
	// `plan_pool`, `host_pools`, and `host_pool_members`
	// tables (migration 0001). The user-CRUD surface is
	// independent — see UsersBackend below.
	//
	// As of d-refactor.2 the `users` table is no longer
	// read by the subscription service; the user-CRUD
	// store is constructed separately under UsersBackend
	// and the subscription Service receives it as a
	// dependency.
	SubscriptionBackend string `env:"AEGIS_SUBSCRIPTION_BACKEND" envDefault:"memory"`

	// UsersBackend selects the persistence layer for the
	// users service (the d.1 / d-refactor.1 user-CRUD
	// surface). "memory" (default) keeps rows in RAM —
	// dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the `users` table (migration
	// 0001) plus the `users.sub_token_prev` columns
	// (migration 0011). The boot path runs the
	// migrations on "pg" mode.
	//
	// The users package is a d-refactor.2 split-out from
	// the subscription package; before d-refactor.2 there
	// was no separate config flag (the subscription
	// backend covered both).
	UsersBackend string `env:"AEGIS_USERS_BACKEND" envDefault:"memory"`

	// PlansBackend selects the persistence layer for the
	// plans service (the v0.6.0 plan-CRUD surface).
	// "memory" (default) keeps rows in RAM — dev only.
	// "pg" uses the PostgreSQL backend (PgStore) backed
	// by the `plans` table (migration 0001). The
	// `plan_pool` join table is NOT touched by this
	// store in v0.6.0; the subscription package keeps
	// its read-only view of plan_pool for the render
	// path. v0.6.x folds plan_pool into this store and
	// lets subscription delegate to it.
	PlansBackend string `env:"AEGIS_PLANS_BACKEND" envDefault:"memory"`

	// WebhooksBackend selects the persistence layer for
	// the outgoing-webhook surface (v0.7.0).
	// "memory" (default) keeps endpoints, delivery
	// history, and the DLQ in RAM — dev only.
	// "pg" uses the PostgreSQL backend (PgStore)
	// backed by the `webhook_endpoints` table
	// (migration 0001, with `updated_at` added in
	// migration 0015 and a UNIQUE constraint on `url`
	// added in migration 0016), the
	// `webhook_deliveries` table (migration 0014),
	// the `webhook_dlq` table (migration 0014), and
	// the `webhook_pending_retries` work queue
	// (migration 0017, v0.7.x).
	WebhooksBackend string `env:"AEGIS_WEBHOOKS_BACKEND" envDefault:"memory"`

	// WebhooksRetryWorkerEnabled toggles the v0.7.x
	// background retry worker. The default (true)
	// matches the operational expectation: once the
	// panel has a webhook configured, retries should
	// fire on the documented schedule (1s, 5s, 25s,
	// 2m15s, 11m15s) without operator intervention.
	// Operators running a read-only / passive
	// replica (a future v0.8+ HA mode) can disable
	// the worker on the secondary with
	// AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED=false.
	WebhooksRetryWorkerEnabled bool `env:"AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED" envDefault:"true"`

	// WebhooksRetryWorkerInterval is the wall-clock
	// period between worker ticks. 5s is a safe
	// default: the smallest retry interval is 1s, so
	// 5s gives the worker enough resolution to
	// pick up a 1s-retry within one tick, while
	// keeping the query load on the
	// `webhook_pending_retries` table low.
	// Operators with a much higher retry volume
	// can drop this to 1s.
	WebhooksRetryWorkerInterval time.Duration `env:"AEGIS_WEBHOOKS_RETRY_WORKER_INTERVAL" envDefault:"5s"`

	// WebhooksSecretAgeRecipients is the
	// comma-separated list of age public keys
	// (`age1...`) the panel uses to SEAL new
	// webhook endpoint secrets. The
	// `webhook_endpoints.secret_ciphertext`
	// column (migration 0018) stores the
	// ciphertext; on read the panel's identity
	// (below) opens it. Multiple recipients are
	// supported for key rotation: the operator
	// can seal new endpoints to a new key
	// alongside the old one, then rotate the
	// identity at leisure.
	//
	// Required when AEGIS_WEBHOOKS_BACKEND=pg
	// (the panel refuses to boot in pg mode
	// without at least one recipient).
	WebhooksSecretAgeRecipients []string `env:"AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS" envSeparator:","`

	// WebhooksSecretAgeKeyFile is the path to
	// the panel's age identity file (the
	// standard `age-keygen` output: one
	// `AGE-SECRET-KEY-1...` line, optional
	// `# public key: ...` comment). The file
	// is mode 0600 in production; the operator
	// shares the same file with the sops CLI
	// (PR #119) so the panel and the operator
	// tooling speak the same key.
	//
	// Required when AEGIS_WEBHOOKS_BACKEND=pg.
	WebhooksSecretAgeKeyFile string `env:"AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE" envDefault:""`

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

	// CredentialsBackend selects the persistence layer
	// for the per-(user, inbound) credential join
	// table introduced in v0.7.x for the Phase 2
	// multi-user sing-box render (ARCHITECTURE.md
	// §7.5). "memory" (default) keeps rows in RAM —
	// dev only. "pg" uses the PostgreSQL backend
	// (PgStore) backed by the new
	// `user_inbound_credentials` table (migration
	// 0019). The pg path is the only mode that
	// survives a restart.
	//
	// The table is intentionally unused in this PR —
	// the sing-box renderer, the builder, and the
	// BatchedApplier fan-out all stay on the Phase 1
	// single-credential path (`inbounds.params["uuid"]`
	// / `["password"]`). The follow-up PRs
	// (`feat(cores): multi-user sing-box render` and
	// `feat(builder): narrow fan-out to per-user nodes`)
	// will start reading this table; until then the
	// table sits empty and there is no operator-
	// facing behaviour change.
	CredentialsBackend string `env:"AEGIS_CREDENTIALS_BACKEND" envDefault:"memory"`

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

	// AgentBinaryPath is the local filesystem
	// path of the `aegis-agent` binary the panel
	// uploads to a node during `POST /v1/nodes/{id}/provision`.
	// v0.3.0 ships the binary in the same monorepo
	// (`backend/cmd/aegis-agent/`) and the release
	// pipeline builds it next to the panel binary.
	//
	// Required: there is no fallback. A missing
	// or unreadable file fails the provision
	// call with a 5xx (the operator can either
	// build the agent locally or mount a
	// pre-built image in production).
	AgentBinaryPath string `env:"AEGIS_AGENT_BINARY,required" envDefault:"./bin/aegis-agent"`

	// AgentSSHPort is the default SSH port the
	// installer uses when the operator does not
	// supply a per-call override. 22 is the SSH
	// standard; the constant lives here so a
	// future "non-standard port" deploy
	// (operators running sshd on 2222 to dodge
	// naive scanners) is a single env-var change.
	AgentSSHPort int `env:"AEGIS_AGENT_SSH_PORT" envDefault:"22"`

	// AgentSSHUser is the default SSH user the
	// installer uses when the operator does not
	// supply a per-call override. `root` is the
	// default because the bootstrap install
	// writes `/usr/local/bin/`, `/etc/aegis/`,
	// and `/etc/systemd/system/` — three paths
	// that require root on a stock Linux box.
	// Operators running sshd as a non-root user
	// (and granting passwordless sudo) can
	// override here.
	AgentSSHUser string `env:"AEGIS_AGENT_SSH_USER" envDefault:"root"`

	// AgentKnownHosts is the panel-side
	// `known_hosts` file. The bootstrap installer
	// appends a new entry on first contact
	// (TOFU) when the operator picks
	// `tofu_policy=accept-and-append`. The file
	// must be writable by the panel process; the
	// installer creates it if absent.
	AgentKnownHosts string `env:"AEGIS_AGENT_KNOWN_HOSTS" envDefault:"./var/known_hosts"`

	// BackupsDir is the local directory where the
	// `backups` package stores its dump files
	// (`<id>.dump.gz`) and the JSON metadata index
	// (`_index.json`). The directory is auto-created
	// at boot with mode 0700; the operator's
	// `configure_secrets` role (#119) and a future
	// `install_panel` role update are responsible
	// for ownership (`aegis-deploy:aegis-deploy`).
	// In dev the default `./var/backups` is
	// fine; production sets
	// `/var/lib/aegis/backups` via the sops+age
	// secrets file or the systemd unit's
	// EnvironmentFile.
	BackupsDir string `env:"AEGIS_BACKUPS_DIR" envDefault:"./var/backups"`

	// BackupsAllowUIRestore gates the HTTP-level
	// restore endpoint. Default false — the
	// restore button in the UI is hidden, and
	// the POST /api/v1/backups/{id}/restore
	// call returns 403. The CLI binary
	// (cmd/aegis-pg-restore, future PR) bypasses
	// the HTTP path entirely and calls
	// Service.Restore directly.
	BackupsAllowUIRestore bool `env:"AEGIS_BACKUPS_ALLOW_UI_RESTORE" envDefault:"false"`

	// BackupsRetentionDays is the maximum age (in
	// days) of any retained backup. The Cleanup
	// pass at the end of every Create removes
	// anything older. Zero or negative disables
	// the age cap (use MaxCount instead, or both
	// at zero to retain forever).
	BackupsRetentionDays int `env:"AEGIS_BACKUPS_RETENTION_DAYS" envDefault:"30"`

	// BackupsMaxCount is the maximum number of
	// backups to keep. The Cleanup pass trims to
	// the most recent N if there are more. Zero
	// or negative disables the count cap.
	BackupsMaxCount int `env:"AEGIS_BACKUPS_MAX_COUNT" envDefault:"0"`

	// BackupsCron is a 5-field cron expression
	// (M H DoM Mo DoW) the in-process scheduler
	// ticks against. Empty disables the
	// scheduler (manual-only mode). The parser
	// supports wildcards and specific values
	// only — no `*/N` step syntax and no
	// `1-5` ranges in v0.5.0. The operator
	// typically writes "0 2 * * *" (every day
	// at 02:00 panel-local time).
	BackupsCron string `env:"AEGIS_BACKUPS_CRON" envDefault:""`

	// BatchedApplierEnabled is the master switch for
	// the v0.4.0-b / v0.5.0 outbound render-and-apply
	// path. When true (the default), the panel builds
	// one BatchedApplier per online node at boot,
	// the user / inbound mutating handlers Enqueue
	// deltas, and the FlushFn re-renders the
	// sing-box config and POSTs it to the agent.
	// When false, the v0.4.0-a behaviour is
	// preserved: the panel keeps the inbound CRUD
	// in its DB but never pushes a config to the
	// agent. Operators who run the panel side-by-side
	// with an external config manager (Ansible,
	// Terraform) set this to false to prevent the
	// panel from clobbering the externally-managed
	// config.
	BatchedApplierEnabled bool `env:"AEGIS_BATCHED_APPLIER_ENABLED" envDefault:"true"`
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
	// v0.8.6 ops guard: a panel that talks to a real
	// PostgreSQL for any of its services is, by
	// definition, not a "I forgot to set AEGIS_ENV"
	// dev-mode boot. The development default
	// (`envDefault:"development"`) is a colourised
	// ConsoleWriter that a log shipper cannot parse,
	// so a pg-backed install that runs without an
	// explicit env value is silently producing
	// un-parseable logs. Force the operator to make
	// the choice: `AEGIS_ENV=production` for the
	// shipped panel image, `AEGIS_ENV=staging` for
	// pre-prod drills. A pure-memory dev install
	// (the test fixture, the `go run ./cmd/aegis`
	// first-boot exploration) still boots fine.
	if c.Env == "development" && c.usesAnyPgBackend() {
		return fmt.Errorf(
			"AEGIS_ENV=development is not allowed when any AEGIS_*_BACKEND=pg " +
				"(set AEGIS_ENV=production or AEGIS_ENV=staging to confirm logging intent; " +
				"a memory-only dev install does not need this flag)")
	}
	return nil
}

// usesAnyPgBackend reports whether the panel is configured
// to talk to a real PostgreSQL for at least one of its
// persistence surfaces. The function is intentionally a
// hard OR across every `*Backend` field — a single pg
// surface (e.g. AEGIS_AUDITS_BACKEND=pg) is enough to
// classify the install as "production-shaped" for the
// purpose of the v0.8.6 log-format guard. The function
// has no opinion on BatchedApplier / retry-worker flags;
// those are runtime toggles, not persistence choices.
func (c *Config) usesAnyPgBackend() bool {
	return c.AuthBackend == "pg" ||
		c.HostsBackend == "pg" ||
		c.NodesBackend == "pg" ||
		c.InboundsBackend == "pg" ||
		c.SubscriptionBackend == "pg" ||
		c.UsersBackend == "pg" ||
		c.PlansBackend == "pg" ||
		c.WebhooksBackend == "pg" ||
		c.PanelcfgBackend == "pg" ||
		c.AuditsBackend == "pg" ||
		c.CredentialsBackend == "pg" ||
		c.AgentCABackend == "pg"
}
