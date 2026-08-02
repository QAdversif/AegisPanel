// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Generic store selector. Every Aegis service
// follows the same pattern:
//
//	switch cfg.XBackend {
//	case "pg":  store = X.NewPgStore(pool)
//	default:    store = X.NewMemoryStore()
//	}
//
// Nine copies of that switch lived in the
// original main.go and made every new service
// a 12-line change. `MustBuild[T]` collapses the
// pattern into one helper plus a one-line
// caller, and the production-vs-memory check is
// centralised so a future "memory in production"
// ban only has to be added once.
//
// The helper is generic so the caller does not
// have to type-assert the result. The pool may be
// nil when every backend is memory; MustBuild
// refuses to construct a pg store in that case
// because the rest of the wiring assumes nil-pool
// means "no pg backend anywhere".
package app

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

// StoreBuilder describes how to construct one
// store. `Name` is the lowercase identifier used
// in log lines; `Backend` is the AEGIS_*_BACKEND
// env value; `PgCtor` and `MemCtor` are the two
// constructors. The pool argument is supplied
// separately so the caller can hold the same pool
// across many stores.
type StoreBuilder[T any] struct {
	Name    string
	Backend string
	PgCtor  func(*pgxpool.Pool) T
	MemCtor func() T
	// Env is read so a memory store in production
	// fails the boot rather than ship. Only the
	// auth store is required to be persistent in
	// production today, but the policy is
	// enforced uniformly here so a future
	// service that adds a sensitive field gets
	// the check for free.
	Env string
}

// MustBuild picks the right constructor for the
// configured backend and returns the resulting
// store. The function returns by value (Go
// generics cannot address a `var T` to fill it
// without a pointer dance), and the pointer is
// already what the service constructors expect
// (e.g. `*nodes.MemoryStore` and `*nodes.PgStore`
// both satisfy `nodes.Store`).
//
// On a fatal mismatch (pg backend requested, no
// pool) the helper calls `log.Fatal` because that
// is a boot-time configuration error the operator
// must see immediately; we do not return an
// error because the call site in main.go would
// have to handle it, and a silent fallback to
// memory is the worst possible behaviour for a
// production install.
func MustBuild[T any](pool *pgxpool.Pool, b StoreBuilder[T]) T {
	switch b.Backend {
	case "pg":
		if pool == nil {
			log.Fatal().
				Str("store", b.Name).
				Str("backend", b.Backend).
				Msg("pg backend requested but the pool is nil (no AEGIS_*_BACKEND was set to pg? bug in needsPg?)")
		}
		log.Info().
			Str("store", b.Name).
			Msg("using pgx-backed store (PgStore)")
		return b.PgCtor(pool)
	default:
		if b.Env == "production" {
			log.Fatal().
				Str("store", b.Name).
				Str("backend", b.Backend).
				Msg("memory backend forbidden in production; set AEGIS_<STORE>_BACKEND=pg")
		}
		log.Warn().
			Str("store", b.Name).
			Msg("using in-memory store (MemoryStore, dev only)")
		return b.MemCtor()
	}
}
