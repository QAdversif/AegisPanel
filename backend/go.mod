// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis backend — Go module manifest.
//
// Aegis is a self-hosted VPN control panel (multi-core: sing-box / Xray /
// Hysteria 2). See ../ARCHITECTURE.md for the full design.

module github.com/QAdversif/AegisPanel

go 1.25.0

require (
	// Configuration from env / files
	github.com/caarlos0/env/v11 v11.2.2
	// HTTP router
	github.com/go-chi/chi/v5 v5.1.0
	github.com/joho/godotenv v1.5.1

	// Metrics
	github.com/prometheus/client_golang v1.20.5

	// Structured logging
	github.com/rs/zerolog v1.33.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/klauspost/compress v1.17.9 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.55.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	golang.org/x/sys v0.45.0 // indirect
	google.golang.org/protobuf v1.34.2 // indirect
)
