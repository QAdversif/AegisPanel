// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package db is the single point at which the Aegis panel
// opens its PostgreSQL connection pool. Before this package
// existed, cmd/aegis/main.go opened the same pool four times
// (once per service: auth, hosts, nodes, inbounds) and the
// runMigrate subcommand opened a fifth copy. Every copy
// used the same DSN, the same defaults, the same error
// handling — copy-pasted.
//
// Open is the only function the runtime needs. Tests get
// their pools through testutil.MustNewPool, which has its
// own drop+create + advisory-lock dance and does not use
// this helper. (A future "testutil.Open()" that re-uses
// ParseConfig from this file would be a one-liner; we keep
// testutil independent today so a failure in this package
// does not break the integration suite.)
//
// # Pool configuration
//
// pgxpool.ParseConfig already applies sensible defaults
// (max 4 conns, 1h lifetime, 30m idle, 1m health check
// period). The DSN may override any of these via the
// pool_* parameters documented on ParseConfig; we do not
// second-guess the operator. We do add a one-shot Ping
// after NewWithConfig so a misconfigured DSN fails the
// boot immediately rather than on the first query.
package db

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Open parses the DSN, opens the pool, and pings the
// server. It returns a ready-to-use pool; the caller owns
// the Close call (typically via defer).
//
// On any error (bad DSN, unreachable server, auth failure)
// the partially-opened pool is closed before returning, so
// the caller never has to clean up after a failure.
//
// The pool honours the operator-supplied pool_max_conns,
// pool_min_conns, pool_max_conn_lifetime, and friends via
// the standard pgxpool DSN syntax. We do not apply our
// own defaults on top — the upstream defaults are
// reasonable and overriding them silently would surprise
// the operator.
func Open(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse DSN: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("pgxpool.New: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		// Close the partially-opened pool so the
		// caller does not have to. pgxpool.New
		// can leave goroutines running until
		// Close is called.
		pool.Close()
		return nil, fmt.Errorf("ping: %w", err)
	}
	return pool, nil
}
