// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package caddy is reserved for the Aegis-side Caddy integration
// that powers the v1.7.0 "Decoy sites v1" milestone (and the
// v2.8.0 full decoy implementation). The reference config in
// `deploy/caddy/Caddyfile` is hand-maintained today; v1.7.0
// renders it from the panel and writes it through Caddy's
// admin API so the operator can rotate decoys without SSH.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package caddy
