// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package backups implements the v0.5.0 panel-side
// backup subsystem (PR #120). The package is
// filesystem-first: a single LocalStore rooted at
// /var/lib/aegis/backups/ holds the encrypted pg_dump
// files; a future S3Store (v0.5.x+ follow-up) can
// implement the same Store interface and slot in
// without changing the Service or the HTTP layer.
//
// # Why filesystem-first
//
// Phase 2 hardening (per the project audit, 2026-07-29)
// called out that the panel currently has no
// automated backup path. A working filesystem store
// closes the immediate "operator has no way to
// recover from DB loss" gap. S3 (or any other
// S3-compatible blob store) is a follow-up; the
// abstraction here is intentionally tiny so a v0.5.x
// PR can add it without touching the Service, the
// HTTP handler, or the in-process scheduler.
//
// # Why a custom in-process scheduler
//
// The panel is a single static binary. Putting the
// backup schedule inside the binary (a small goroutine
// that ticks every minute and fires on cron match)
// keeps the deploy story simple — there is no
// separate cron daemon, no separate systemd timer,
// just one container. The cost is that the schedule
// only fires while the panel is running. That is
// fine for a panel that should always be running;
// the alternative (cron-on-host calling a CLI
// subcommand) is strictly more complex.
//
// # Why restore is CLI-only (not from the UI)
//
// The UI exposes Download and Delete but never Restore.
// A restore from the UI is a destructive operation
// against a running panel: it must stop accepting
// requests, drain the connection pool, run
// `pg_restore --clean --if-exists`, re-open the pool,
// re-apply migrations, and then resume. Doing this
// safely from an HTTP request is possible (the
// Service.Restore method exists) but the operator
// audit log would show "admin user X clicked
// Restore" with no way to know they meant a
// particular backup file. The CLI binary
// `cmd/aegis-pg-restore` (separate package, future
// PR) forces the operator to type the exact path
// and `--confirm-yes-i-know-what-im-doing`, which is
// the right friction for a destructive op.
//
// The `AEGIS_ALLOW_UI_RESTORE=true` env knob in the
// Service config lets an operator opt-in to UI
// restore in a dev environment without changing
// code. The default is OFF in production.
//
// # Schema
//
// The backup file format is `pg_dump -Fc` (custom
// format, supports parallel restore via
// `pg_restore -j`). A sidecar `<id>.sha256` file
// holds the SHA-256 of the dump, written before the
// Backup row is marked `Status=ok`. The smoke test
// re-verifies the checksum on Open.
//
// # Concurrency
//
// `Service.Create` is single-flight: only one backup
// can run at a time across the whole panel. A second
// concurrent `Create` returns `ErrBackupInProgress`
// rather than queueing — the operator should retry
// after a moment. The mutex lives on the Service,
// not on the Store, because the Store is meant to be
// stateless (in a future S3-backed implementation,
// the local mutex does not need to know the
// request rate).
package backups
