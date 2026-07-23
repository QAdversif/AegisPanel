// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package events is reserved for the in-process event bus that
// powers audit-log fan-out and (eventually) the multi-region
// panel replication described in the v9 roadmap §21 Phase 4+
// backlog ("Multi-region panel с CRDT или read-replica").
//
// In v0.3.0 the audit log writes directly to Postgres via the
// `internal/audits` package and reads from a single panel
// process, so no in-process bus is needed. The first concrete
// use case that justifies this package is multi-region
// replication — the Phase 4 backlog item. v2.0+ territory.
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package events
