// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis backend — Go module manifest.
//
// Aegis is a self-hosted VPN control panel (multi-core: sing-box / Xray /
// Hysteria 2). See ../ARCHITECTURE.md for the full design.

module github.com/QAdversif/AegisPanel

go 1.22

require (
	// HTTP router
	github.com/go-chi/chi/v5 v5.1.0

	// PostgreSQL driver + connection pool
	github.com/jackc/pgx/v5 v5.7.1

	// Redis client
	github.com/redis/go-redis/v9 v9.7.0

	// NATS messaging (event bus + JetStream)
	github.com/nats-io/nats.go v1.37.0

	// Structured logging
	github.com/rs/zerolog v1.33.0

	// Configuration from env / files
	github.com/caarlos0/env/v11 v11.2.2
	github.com/joho/godotenv v1.5.1

	// Validation
	github.com/go-playground/validator/v10 v10.22.1

	// JWT
	github.com/golang-jwt/jwt/v5 v5.2.1

	// Password hashing
	golang.org/x/crypto v0.27.0

	// UUIDs
	github.com/google/uuid v1.6.0

	// OpenAPI spec generation
	github.com/swaggo/swag v1.16.4

	// Metrics
	github.com/prometheus/client_golang v1.20.5

	// Migrations
	github.com/pressly/goose/v3 v3.22.1
)
