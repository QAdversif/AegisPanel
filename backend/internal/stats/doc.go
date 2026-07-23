// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package stats is reserved for the per-user traffic accounting
// described in the v9 roadmap §21 Phase 2 entry for v1.8.0
// ("Per-user traffic → ClickHouse (если выбран) или остаётся в
// Postgres").
//
// In v0.3.0 the only traffic-shaped signal is the audit log;
// per-user byte counters are out of scope. The migration plan
// for v1.8.0 is "stay in Postgres unless the data set grows
// past a documented threshold" — defer the package import and
// the table schema until that decision lands.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package stats
