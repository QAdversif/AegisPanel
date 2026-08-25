// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration
//
// Regression guard for the Go `nodes.AgentTransport`
// closed set ↔ `nodes_agent_transport_check` CHECK
// constraint agreement.
//
// # Why this test exists
//
// v0.8.31 added migration 0024 with the CHECK
// constraint:
//
//	CHECK (agent_transport IN ('http', 'grpc'))
//
// The Go-side mirror in `store.go` defines:
//
//	AgentTransportHTTP  = "http"
//	AgentTransportGRPC  = "grpc"
//
// And `validateAgentTransport` in `service.go`
// rejects any other value at the Service boundary.
//
// The risk: a future migration that adds a new
// transport value (e.g. `dual` from the v0.8.30
// reserved set, or a `webtransport` value) updates
// only one of the three layers and the others
// silently drift. The single
// `TestPgStore_SetAgentTransport_OK` test only
// exercises `AgentTransportGRPC` and would not
// catch a regression on the other allowed value
// or on the unknown-value rejection path.
//
// This test pins:
//
//  1. Both members of the closed set round-trip
//     through `Store.Create` + `GetByID`.
//  2. An unknown value is rejected by the SQL
//     CHECK (the safety net for the
//     `validateAgentTransport` Go-side guard).
//
// If the CHECK ever drifts from the Go model
// again (or vice-versa), this test fails loudly
// with the offending value, and CI blocks the
// PR that introduced the drift.

package nodes

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/QAdversif/AegisPanel/testutil"
)

// allAgentTransports is the canonical closed
// set. Any drift between this list, the CHECK
// constraint in migration 0024, and the
// `validateAgentTransport` switch in service.go
// is a bug.
var allAgentTransports = []string{
	AgentTransportHTTP,
	AgentTransportGRPC,
}

// TestPgStore_Create_AllAgentTransportsPassCheck
// is the regression guard. It iterates the closed
// transport enum and calls Create for every
// value. A failure here means the CHECK constraint
// and the Go const set have drifted apart.
func TestPgStore_Create_AllAgentTransportsPassCheck(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	ctx := context.Background()

	for _, tr := range allAgentTransports {
		tr := tr
		t.Run(tr, func(t *testing.T) {
			n := &Node{
				ID:             uuid.New(),
				Name:           "tr-" + tr + "-" + uuid.New().String()[:8],
				Region:         "eu",
				State:          StateNew,
				Address:        "10.0.0.1:22",
				AgentTransport: tr,
			}
			if err := store.Create(ctx, n); err != nil {
				t.Fatalf("Create with AgentTransport=%q failed: %v. "+
					"This is the regression guard for the "+
					"Go const set ↔ nodes_agent_transport_check "+
					"CHECK constraint alignment. If the CHECK "+
					"is missing the new value, check that "+
					"migration 0024_add_nodes_agent_transport.sql "+
					"was applied.", tr, err)
			}
			got, err := store.GetByID(ctx, n.ID)
			if err != nil {
				t.Fatalf("GetByID: %v", err)
			}
			if got.AgentTransport != tr {
				t.Fatalf("round-trip AgentTransport = %q, want %q", got.AgentTransport, tr)
			}
		})
	}
}

// TestPgStore_Create_RejectsUnknownAgentTransport
// is the SQL CHECK safety net. The Service-layer
// `validateAgentTransport` is the primary gate,
// but the SQL CHECK is the backstop for any
// future refactor that bypasses the Go validator
// (a CLI bypass, a raw SQL import, a future
// admin UI button that calls the Store directly
// without going through Service). A successful
// insert with an unknown value is a bug.
func TestPgStore_Create_RejectsUnknownAgentTransport(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	ctx := context.Background()

	// "dual" was reserved for v0.8.30 and never
	// landed in the schema; the CHECK is the
	// reason. If a future migration lifts the
	// reservation, this test must be updated to
	// match.
	bad := "dual"
	n := &Node{
		ID:             uuid.New(),
		Name:           "tr-bad-" + uuid.New().String()[:8],
		Region:         "eu",
		State:          StateNew,
		Address:        "10.0.0.1:22",
		AgentTransport: bad,
	}
	err := store.Create(ctx, n)
	if err == nil {
		t.Fatalf("Create with AgentTransport=%q succeeded; "+
			"expected SQL CHECK (migration 0024) to reject. "+
			"Either the CHECK was removed or the test is "+
			"missing a value to assert against.", bad)
	}
	// The error must surface a CHECK violation
	// (SQLSTATE 23514), not e.g. a connection
	// failure or a panic. A non-23514 error means
	// the SQL surface rejected the row for a
	// different reason, which is a different bug.
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		t.Fatalf("err = %v, want *pgconn.PgError (SQLSTATE 23514)", err)
	}
	if pgErr.Code != "23514" {
		t.Fatalf("SQLSTATE = %q, want %q (check_violation)", pgErr.Code, "23514")
	}
	if !strings.Contains(pgErr.Message, "agent_transport") {
		t.Fatalf("SQL message = %q, expected to mention 'agent_transport'", pgErr.Message)
	}
}
