// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Regression guard for the Go `nodes.State` enum ↔ `nodes.state`
// CHECK constraint agreement.
//
// # Why this test exists
//
// The original migration 0001_initial.sql defined the
// `nodes_state_check` constraint on a stale, enterprise-y
// allow-list:
//
//	('provisioning', 'active', 'degraded', 'suspended',
//	 'decommissioned')
//
// The Phase 0 Go model (internal/nodes/node.go) used a
// different, operator-facing set:
//
//	StateNew, StateOnline, StateDraining, StateOffline,
//	StateDisabled
//
// The discrepancy was latent for v0.4.0 because the
// MemoryStore never touched the constraint, and the live
// deploy ran on memory. PR #37 added migration
// `0006_nodes_state_v2.sql` to align the constraint with
// the Go model so the PgStore's first Create does not
// fail with `23514 — check_violation`.
//
// The risk: a future migration that resets the constraint
// (or a hot-fix that points the live deploy at a fresh
// database without re-applying 0006) silently breaks
// every Create. The single `TestPgStore_Create_RoundTrip`
// test only exercises `StateNew` and would not catch a
// regression on the other four values.
//
// This test explicitly drives every member of the closed
// state enum through `Store.Create`. If the CHECK ever
// drifts from the Go model again, this test fails
// loudly with the offending state value, and CI blocks
// the PR that introduced the drift.
package nodes

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// allStates is the canonical closed set. Any drift between
// this list and the CHECK constraint in
// `0006_nodes_state_v2.sql` is a bug.
var allStates = []State{
	StateNew,
	StateOnline,
	StateDraining,
	StateOffline,
	StateDisabled,
}

// TestPgStore_Create_AllStatesPassStateCheck is the regression
// guard. It iterates the closed state enum and calls Create
// for every value. A failure here means the CHECK constraint
// in the schema and the Go const set have drifted apart.
func TestPgStore_Create_AllStatesPassStateCheck(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	ctx := context.Background()

	for _, st := range allStates {
		st := st
		t.Run(string(st), func(t *testing.T) {
			n := &Node{
				ID:      uuid.New(),
				Name:    "state-" + string(st) + "-" + uuid.New().String()[:8],
				Region:  "eu",
				State:   st,
				Address: "10.0.0.1:22",
			}
			if err := store.Create(ctx, n); err != nil {
				t.Fatalf("Create with State=%q failed: %v. "+
					"This is the regression guard for the "+
					"Go enum ↔ nodes_state_check CHECK "+
					"constraint alignment. If the CHECK is "+
					"missing the new value, check that "+
					"migrations up to and including "+
					"0006_nodes_state_v2.sql were "+
					"applied.", string(st), err)
			}
		})
	}
}
