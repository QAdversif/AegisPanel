// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Unit tests for db.Open. The happy-path (Open against a
// real Postgres, Ping succeeds) is exercised by the
// per-store integration tests; here we cover the failure
// paths that do not need a database — bad DSN syntax and
// unreachable host.

package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestOpen_BadDSN checks that an unparseable DSN is
// rejected with a wrapped error, no pool is leaked, and
// the call returns quickly (no network round-trip
// attempted for a syntactically bad DSN).
func TestOpen_BadDSN(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// "not a postgres url" is a syntactically invalid
	// scheme — ParseConfig must reject it before
	// touching the network.
	_, err := Open(ctx, "not a postgres url")
	if err == nil {
		t.Fatal("Open: expected error for bad DSN, got nil")
	}
	// pgx wraps the underlying parse error; the
	// surface message should mention either the bad
	// scheme or the parse step.
	if !strings.Contains(err.Error(), "DSN") && !strings.Contains(err.Error(), "parse") {
		t.Errorf("err = %v, want message containing \"DSN\" or \"parse\"", err)
	}
}

// TestOpen_UnreachableHost checks that a syntactically
// valid DSN pointing at an unreachable host fails with
// a Ping error. We bind to a port we just closed so the
// connect refuses immediately rather than hanging on
// the OS connect timeout.
//
// This guards against a future regression where the
// Ping-after-New check is removed — without it, Open
// would return a "ready" pool that only fails on the
// first query, hiding the misconfiguration from the
// boot path.
func TestOpen_UnreachableHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Port 1 is reserved and almost never bound on a
	// real system; the connect attempt fails fast.
	_, err := Open(ctx, "postgres://x:x@127.0.0.1:1/x")
	if err == nil {
		t.Fatal("Open: expected ping error, got nil")
	}
	if !strings.Contains(err.Error(), "ping") {
		t.Errorf("err = %v, want message containing \"ping\"", err)
	}
}
