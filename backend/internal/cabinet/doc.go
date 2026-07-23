// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package cabinet is reserved for the user-facing Cabinet API
// described in ARCHITECTURE.md §13 (and the v9 roadmap §21 Phase 2
// entry for v1.2.0, "реальный users CRUD + plans + traffic limits
// + Cabinet API"). The end-user / subscriber surface — login,
// subscription URL fetch, traffic stats, plan change — sits behind
// a different auth model than the admin surface mounted under
// `/api/v1` (which the `internal/auth` package covers).
//
// Intentionally empty in v0.3.0. Adding types or imports here
// without a corresponding ADR is a smell — flag it in code
// review.
package cabinet
