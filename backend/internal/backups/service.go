// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service: the orchestrator. Wraps the Store and
// the pg_dump subprocess with single-flight
// concurrency, retention, and the optional in-process
// scheduler.
//
// The Service does NOT touch the panel's HTTP layer
// directly — that's the Handler's job. The Service
// is also the only thing the CLI binary
// (`cmd/aegis-pg-backup`, future PR) calls into for
// the create / list / delete operations.

package backups

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/audits"
	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// ErrBackupInProgress is returned by Create when
// another backup is already running. The operator
// should retry after a moment; the in-flight
// backup is single-flight by design (see
// package doc.go).
var ErrBackupInProgress = errors.New("backups: another backup is in progress")

// ErrBackupDisabled is returned by Restore when the
// Service was started with AllowUIRestore=false and
// the caller is the HTTP handler (the CLI binary
// is allowed to bypass this check). The HTTP layer
// maps this to 403 with a doc-link to the operator
// guide.
var ErrBackupDisabled = errors.New("backups: UI restore is disabled (set AEGIS_ALLOW_UI_RESTORE=true to enable)")

// Config is the Service configuration. Populated
// from the panel's main config and from
// `AEGIS_ALLOW_UI_RESTORE` at boot.
type Config struct {
	// PostgresDSN is the DSN the panel uses to reach
	// Postgres. The Service passes it to the
	// `pg_dump -d` flag, so it must be a connection
	// string the local `pg_dump` binary can use
	// (the same shape as the panel's
	// `AEGIS_POSTGRES_DSN`).
	PostgresDSN string

	// PgDumpBin is the path to the `pg_dump` binary.
	// Defaults to `/usr/bin/pg_dump` if empty; the
	// role `install_panel` (or a future
	// `install_pgdump_client` role) installs the
	// `postgresql-client` apt package which provides
	// the binary.
	PgDumpBin string

	// PgRestoreBin is the path to the `pg_restore`
	// binary. Same default behaviour as PgDumpBin.
	PgRestoreBin string

	// BackupsDir is the local directory holding
	// the dump files and the index. The LocalStore
	// is constructed against this directory's
	// osBackend at boot.
	BackupsDir string

	// AllowUIRestore gates the HTTP-level Restore
	// endpoint. CLI invocations are never blocked.
	// Default false in production.
	AllowUIRestore bool

	// RetentionDays is the maximum age (from
	// CreatedAt) of any retained backup. The
	// Cleanup method removes anything older.
	// Zero or negative disables the age check.
	RetentionDays int

	// MaxCount is the maximum number of backups to
	// keep. The Cleanup method trims to the most
	// recent N if there are more. Zero or negative
	// disables the count check.
	MaxCount int

	// BackupsCron is the initial 5-field Vixie
	// cron expression the in-process scheduler
	// fires on. Empty = manual-only mode (the
	// scheduler goroutine is NOT started). The
	// ReloadCron method can replace the running
	// scheduler's cron at runtime; this field is
	// the boot-time value, used by Run() and the
	// manual-only fallback in ReloadCron.
	//
	// v0.9.x: added as part of the
	// "hot-reload + admin UI surface for the
	// schedule" task. The handler /backups/schedule
	// surfaces this value to the admin UI.
	BackupsCron string
}

// Service is the public surface for the package.
// All methods are safe for concurrent use.
type Service struct {
	cfg      Config
	store    Store
	pgPool   *pgxpool.Pool // for the metadata counts and schema version; nil is OK (counts are skipped)
	clock    func() time.Time
	webhooks *webhooks.Service // v0.7.x: outbound event surface. May be nil (see WithWebhooks).
	audits   *audits.Service   // v0.7.x deferred call-site.

	// sched is the scheduler struct, created by
	// Run() (when the operator sets AEGIS_BACKUPS_CRON
	// to a non-empty value and the main() wires up
	// `go svc.Run(ctx, cron)`) OR by ReloadCron (when
	// the operator changes the schedule at runtime).
	// nil = the scheduler has never been initialised;
	// handleGetSchedule surfaces this as
	// `scheduleActive: false`.
	//
	// v0.9.x: promoted from a local var in Run() to a
	// struct field so ReloadCron can swap the cron
	// expression in place under a mutex without
	// touching the goroutine. The goroutine itself
	// still reads s.sched.cron via the locked pointer
	// (see scheduler.maybeFire) — the swap is atomic
	// from the goroutine's perspective.
	sched *scheduler

	// configMu guards cfg.BackupsCron against
	// concurrent writes from ReloadCron (which
	// updates the field when no scheduler is
	// running) and reads from the HTTP handler
	// (handleGetSchedule). The sched field has its
	// own mu for the cron swap path.
	//
	// v0.9.x: added together with the sched field.
	configMu sync.RWMutex

	// dumper and restorer are the producer-side
	// interfaces the Service delegates to. The
	// production wiring (New) installs a
	// pgBinaries instance configured with the
	// panel's pg_dump / pg_restore paths; tests
	// inject fakes via SetDumper / SetRestorer.
	//
	// Pre-PR-#228 this was a `dumpFn func(ctx)
	// (io.ReadCloser, error)` field with the
	// production impl hardcoded in `realDump`.
	// Splitting the responsibility into
	// Dumper / Restorer interfaces lets the
	// Service treat the dump subprocess as a
	// black box (it does, e.g. no longer
	// construct pg_dump argv itself — that lives
	// in pgBinaries / pgDumpArgs) and lets
	// future remote-dump providers slot in
	// without a Service change.
	dumper   Dumper
	restorer Restorer

	// inflight is held by Create for the duration of
	// the pg_dump subprocess. A second Create
	// arriving while the lock is held returns
	// ErrBackupInProgress.
	inflight sync.Mutex
}

// New returns a Service with the given store and
// pool. The pool may be nil; in that case the
// Service skips the per-backup metadata counts and
// reports zero for `node_count` / `user_count` /
// `host_count` in the resulting Backup row.
//
// New installs a production pgBinaries Dumper /
// Restorer configured with cfg.PgDumpBin /
// cfg.PgRestoreBin (defaulting to /usr/bin/pg_dump
// and /usr/bin/pg_restore). Tests override via
// SetDumper / SetRestorer.
func New(cfg Config, store Store, pool *pgxpool.Pool) *Service {
	pb := newPgBinaries(cfg.PgDumpBin, cfg.PgRestoreBin)
	// Mirror the resolved paths back into cfg so
	// the Service's own field reflects the
	// production defaults.
	cfg.PgDumpBin = pb.dumpPath
	cfg.PgRestoreBin = pb.restorePath
	return &Service{
		cfg:      cfg,
		store:    store,
		pgPool:   pool,
		clock:    time.Now,
		dumper:   pb,
		restorer: pb,
	}
}

// WithWebhooks installs the outbound event service.
// See plans.Service.WithWebhooks for the rationale.
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// WithAudits installs the audit-log writer. Same
// nil-safe pattern as WithWebhooks. The audit
// surface for backups records three events:
//
//   - `backup.create` when the running row is
//     first inserted (the operator-initiated
//     action; the actor is the HTTP handler's
//     authenticated principal, or `system`
//     for the v0.5.0 scheduled cron);
//   - `backup.complete` / `backup.fail` at the
//     terminal transition (the system actor;
//     these are operational events, not
//     user-initiated);
//   - `backup.delete` when the operator removes
//     a row (the actor is the HTTP handler's
//     authenticated principal).
//
// The Create-event record carries the running
// row state, the terminal-event record carries
// the final OK/failed row, and the delete-event
// record carries the pre-delete row.
func (s *Service) WithAudits(svc *audits.Service) *Service {
	s.audits = svc
	return s
}

// SetDumper injects a custom Dumper. The default
// is a real pgBinaries wrapping pg_dump; tests
// override this to produce a deterministic
// ReadCloser without invoking the system binary.
func (s *Service) SetDumper(d Dumper) { s.dumper = d }

// SetRestorer injects a custom Restorer. The
// default is a real pgBinaries wrapping
// pg_restore; tests override this to skip the
// subprocess entirely.
func (s *Service) SetRestorer(r Restorer) { s.restorer = r }

// SetClock injects a deterministic clock for tests.
// The default is time.Now.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// Store returns the underlying Store. The HTTP
// handler does not need this; it is exposed for the
// future CLI binary.
func (s *Service) Store() Store { return s.store }

// Cfg returns the Service's configuration by value.
// The handler reads RetentionDays / MaxCount from
// this snapshot when rendering /backups/schedule.
// The caller MUST NOT mutate the returned value —
// the fields are passed by value for read-only
// access; the only legitimate mutator is
// ReloadCron, which acquires s.configMu and writes
// s.cfg.BackupsCron (a field not surfaced through
// Cfg here because the handler reads the live cron
// from Schedule() instead).
//
// v0.9.x: added so the /backups/schedule handler
// can surface the retention policy without
// reaching into the Service's unexported field.
func (s *Service) Cfg() Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.cfg
}

// Create runs a fresh backup. The full lifecycle:
//  1. Allocate an ID.
//  2. Insert a `running` row.
//  3. Stream pg_dump into <backupsDir>/<id>.dump.gz.
//  4. Hash the file; write the .sha256 sidecar.
//  5. Update the row to `ok` with the size, hash,
//     schema version, and metadata counts.
//  6. Run a retention Cleanup pass.
//
// Returns ErrBackupInProgress if another Create is
// already running. Returns the inserted row (with
// `Path` populated server-side) on success.
//
// The pg_dump subprocess is cancelled if the parent
// context is cancelled; in that case the row is
// marked `failed` with the cancellation error and
// the partial file is deleted.
func (s *Service) Create(ctx context.Context, trigger Trigger) (*Backup, error) {
	if !s.inflight.TryLock() {
		return nil, ErrBackupInProgress
	}
	defer s.inflight.Unlock()

	now := s.clock().UTC()
	id := newBackupID(now)
	row := &Backup{
		ID:        id,
		CreatedAt: now,
		Trigger:   trigger,
		Status:    StatusRunning,
		Path:      id + ".dump.gz",
	}
	if err := s.store.Insert(ctx, row); err != nil {
		return nil, fmt.Errorf("backups: insert running row: %w", err)
	}
	// v0.7.x: the running row is committed,
	// fire the "created" event so receivers
	// (slack, pagerduty) can show that a backup
	// is in flight.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventBackupCreated, row)
	// v0.7.x deferred: record the audit row.
	// The actor is the authenticated principal
	// (HTTP path) or `system` (the v0.5.0
	// scheduled cron) — RecordFromContext picks
	// this up from the JWT claims when present.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "backup.create",
		ResourceType: "backup",
		ResourceID:   row.ID,
		After:        row,
	})

	dumpPath, err := s.runDumpToFile(ctx, id)
	if err != nil {
		row.Status = StatusFailed
		row.SetError(err)
		_ = s.store.Update(ctx, row)
		// v0.7.x: see the final dispatch below
		// — we centralise the failed-event
		// emit at the function exit so the
		// dispatcher logs every transition
		// once.
		s.dispatchBackupTerminal(ctx, row)
		return row, fmt.Errorf("backups: dump: %w", err)
	}

	// Stat the produced file to get SizeBytes, then
	// hash it.
	st, err := os.Stat(dumpPath)
	if err != nil {
		row.Status = StatusFailed
		row.SetError(err)
		_ = s.store.Update(ctx, row)
		_ = os.Remove(dumpPath)
		s.dispatchBackupTerminal(ctx, row)
		return row, err
	}
	size := st.Size()
	hash, err := hashFile(dumpPath)
	if err != nil {
		row.Status = StatusFailed
		row.SetError(err)
		_ = s.store.Update(ctx, row)
		_ = os.Remove(dumpPath)
		s.dispatchBackupTerminal(ctx, row)
		return row, err
	}
	if err := writeSidecar(dumpPath+".sha256", hash); err != nil {
		row.Status = StatusFailed
		row.SetError(err)
		_ = s.store.Update(ctx, row)
		_ = os.Remove(dumpPath)
		s.dispatchBackupTerminal(ctx, row)
		return row, err
	}

	row.SizeBytes = size
	row.ChecksumSHA256 = hash
	row.SchemaVersion, row.NodeCount, row.UserCount, row.HostCount = s.populateCounts(ctx)
	row.Status = StatusOK
	if err := s.store.Update(ctx, row); err != nil {
		// The backup file IS on disk and the
		// counts are populated, but the
		// final row state could not be
		// persisted. We treat this as a
		// failure for the event surface
		// (the next reconciliation cron will
		// pick up the partial state).
		row.Status = StatusFailed
		row.SetError(err)
		_ = s.store.Update(ctx, row)
		s.dispatchBackupTerminal(ctx, row)
		return row, err
	}

	// Retention pass. Errors here are non-fatal
	// (the backup succeeded; we just couldn't trim
	// old ones). Logged.
	if err := s.Cleanup(ctx); err != nil {
		log.Warn().Err(err).Msg("backups: post-create cleanup failed (non-fatal)")
	}

	// v0.7.x: success — fire the "completed"
	// event. Receivers see the final row with
	// SizeBytes, ChecksumSHA256, and the
	// metadata counts filled in.
	s.dispatchBackupTerminal(ctx, row)
	return row, nil
}

// dispatchBackupTerminal emits the
// backup.completed or backup.failed event based
// on the row's final Status. Called once per
// Create invocation, after the row's terminal
// state has been persisted. The webhook field
// is nil in unit tests; production wiring
// (cmd/aegis/main.go) installs the real
// service via WithWebhooks.
func (s *Service) dispatchBackupTerminal(ctx context.Context, row *Backup) {
	switch row.Status {
	case StatusOK:
		webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventBackupCompleted, row)
		// v0.7.x deferred: record the audit
		// row for the OK transition. The
		// actor is `system` (the scheduled
		// cron or the operator's manual
		// "wait until OK" poll); there is
		// no human-in-the-loop at the
		// terminal point.
		audits.RecordFromContext(ctx, s.audits, audits.Entry{
			Action:       "backup.complete",
			ResourceType: "backup",
			ResourceID:   row.ID,
			After:        row,
		})
	case StatusFailed:
		webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventBackupFailed, row)
		// v0.7.x deferred: record the audit
		// row for the failed transition.
		// The before-state is the running
		// row (with no error), the
		// after-state is the failed row
		// (with the error set).
		audits.RecordFromContext(ctx, s.audits, audits.Entry{
			Action:       "backup.fail",
			ResourceType: "backup",
			ResourceID:   row.ID,
			After:        row,
		})
	}
	// StatusRunning is never terminal; the
	// running row is announced via
	// EventBackupCreated at insert time.
}

// Get returns a single row by ID. Pass-through to
// the Store.
func (s *Service) Get(ctx context.Context, id string) (*Backup, error) {
	return s.store.Get(ctx, id)
}

// List returns every row, newest first.
func (s *Service) List(ctx context.Context) ([]*Backup, error) {
	return s.store.List(ctx)
}

// Delete removes the row AND its associated dump
// file + sidecar. Idempotent on missing rows.
func (s *Service) Delete(ctx context.Context, id string) error {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil
		}
		return err
	}
	dumpPath := filepath.Join(s.cfg.BackupsDir, row.Path)
	// `row.Path` is server-side (the Service
	// generated it from a timestamp + random
	// tail in newBackupID) so the path-traversal
	// taint does not apply here. The .sha256
	// sidecar is the canonical name suffix.
	if err := os.Remove(dumpPath); err != nil && !os.IsNotExist(err) { // #nosec G703 -- row.Path is server-generated
		return err
	}
	if err := os.Remove(dumpPath + ".sha256"); err != nil && !os.IsNotExist(err) { // #nosec G703 -- canonical sidecar suffix
		return err
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x deferred: record the audit row.
	// Before is the row we just removed; After
	// is nil (the row is gone). The actor is
	// the HTTP handler's authenticated
	// principal.
	audits.RecordFromContext(ctx, s.audits, audits.Entry{
		Action:       "backup.delete",
		ResourceType: "backup",
		ResourceID:   id,
		Before:       row,
	})
	return nil
}

// Open returns a ReadCloser over the dump file. The
// caller MUST close it. The returned error is
// ErrNotFound when the row is gone or the file is
// missing on disk.
func (s *Service) Open(ctx context.Context, id string) (io.ReadCloser, error) {
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	// `row.Path` is server-side (the Service
	// generated it from a timestamp + random
	// tail in newBackupID) so the path-traversal
	// taint does not apply here.
	dumpPath := filepath.Join(s.cfg.BackupsDir, row.Path)
	f, err := os.Open(dumpPath) // #nosec G703 -- row.Path is server-generated
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Restore runs the configured Restorer against the
// dump file at id. Returns ErrBackupDisabled if
// AllowUIRestore is false (the HTTP handler uses
// this to gate the UI button; the CLI binary
// bypasses it via a direct method call without the
// flag, but in v0.5.0 the CLI binary is not in
// scope yet).
//
// Restore is dangerous: it drops and recreates
// every object in the dump. The caller is
// responsible for having stopped the panel before
// invoking this.
//
// Pre-PR-#228 this method called `pg_restore`
// inline with the same broken `--dbname=`
// DSN-stripping bug as Create. The fix is to
// delegate to the injected Restorer, which the
// production wiring (New) installs as
// pgBinaries. AllowUIRestore stays a Service-level
// gate; the Restorer interface has no knowledge
// of UI / CLI policy.
func (s *Service) Restore(ctx context.Context, id string) error {
	if !s.cfg.AllowUIRestore {
		return ErrBackupDisabled
	}
	row, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	// `row.Path` is server-side (the Service
	// generated it from a timestamp + random
	// tail in newBackupID) so the path-traversal
	// taint does not apply here.
	dumpPath := filepath.Join(s.cfg.BackupsDir, row.Path)
	return s.restorer.Restore(ctx, s.cfg.PostgresDSN, dumpPath)
}

// ReloadCron replaces the running scheduler's cron
// expression at runtime. A valid 5-field Vixie
// expression is parsed and the new Cron is swapped
// in under s.sched.mu; the running goroutine reads
// s.sched.cron through the same lock and picks up
// the change on its next tick.
//
// An invalid expression is rejected with the parser
// error, and the previous expression remains in
// effect (no swap, no scheduler re-init). The
// hot-reload is idempotent — calling with the same
// expression is a no-op (the cron is re-parsed and
// re-installed, semantically identical).
//
// If the scheduler has never been initialised (the
// operator booted the panel with an empty
// AEGIS_BACKUPS_CRON and has not called Run() yet),
// ReloadCron creates a fresh scheduler struct in
// place. The goroutine is NOT started — the operator
// still has to wire up `go svc.Run(ctx, expr)` from
// main(), which will see the existing scheduler and
// the new cron. The cron swap is a no-op for a fresh
// Service until the goroutine starts, but the
// handler can now read the expression off the
// scheduler struct.
//
// v0.9.x: the POST endpoint for hot-reload is
// deferred to v0.9.1 per the Tier 1 #3 plan. For
// now, ReloadCron is exposed as a Service method
// for the future `aegis admin backup schedule
// reload` CLI and for any in-process callers; the
// operator's path remains "edit AEGIS_BACKUPS_CRON
// in the env file and restart the panel".
func (s *Service) ReloadCron(ctx context.Context, expr string) error {
	c, err := ParseCron(expr)
	if err != nil {
		return err
	}
	if s.sched == nil {
		// Cold-start path. The operator changed
		// the schedule before the scheduler
		// goroutine was ever started. Install a
		// fresh scheduler struct so the handler
		// (handleGetSchedule) can surface the
		// expression. Run() will replace the
		// cron field when the goroutine starts
		// (or honour it if the operator does
		// `go svc.Run(ctx, s.cfg.BackupsCron)`
		// without re-typing the expression).
		s.sched = &scheduler{svc: s, expr: expr, cron: c}
		s.configMu.Lock()
		s.cfg.BackupsCron = expr
		s.configMu.Unlock()
		log.Info().Str("cron", expr).Msg("backups: scheduler initialised from ReloadCron (no goroutine yet)")
		return nil
	}
	s.sched.mu.Lock()
	s.sched.cron = c
	s.sched.expr = expr
	s.sched.mu.Unlock()
	// Mirror the new expression into cfg so a
	// subsequent panel restart picks it up via
	// the standard AEGIS_BACKUPS_CRON path.
	s.configMu.Lock()
	s.cfg.BackupsCron = expr
	s.configMu.Unlock()
	log.Info().Str("cron", expr).Msg("backups: scheduler cron reloaded at runtime")
	return nil
}

// Schedule returns a snapshot of the current
// scheduler's expression and parsed Cron. The
// pointer is nil when no scheduler has been
// initialised (cold-start, manual-only mode).
// The Cron pointer is the same object the
// scheduler goroutine matches against; treat it
// as read-only.
//
// v0.9.x: read-side helper for the
// /backups/schedule handler. Returns the live
// values, NOT cfg.BackupsCron, so a hot-reload
// is visible immediately without a restart.
func (s *Service) Schedule() (expr string, cron *Cron, active bool) {
	if s.sched == nil {
		s.configMu.RLock()
		defer s.configMu.RUnlock()
		return s.cfg.BackupsCron, nil, false
	}
	s.sched.mu.Lock()
	defer s.sched.mu.Unlock()
	return s.sched.expr, s.sched.cron, true
}

// Cleanup applies RetentionDays and MaxCount.
// Cleanup is invoked automatically at the end of
// Create; the operator can also call it directly
// (the future CLI binary will).
func (s *Service) Cleanup(ctx context.Context) error {
	rows, err := s.store.List(ctx)
	if err != nil {
		return err
	}

	// The list is newest-first already. We keep
	// everything that is within the retention
	// window AND within the count cap.
	now := s.clock().UTC()
	cutoff := time.Time{}
	if s.cfg.RetentionDays > 0 {
		cutoff = now.Add(-time.Duration(s.cfg.RetentionDays) * 24 * time.Hour)
	}
	drop := make([]string, 0)
	for i, r := range rows {
		// Cap by count: keep the first MaxCount rows.
		if s.cfg.MaxCount > 0 && i >= s.cfg.MaxCount {
			drop = append(drop, r.ID)
			continue
		}
		// Cap by age: skip rows older than cutoff.
		if !cutoff.IsZero() && r.CreatedAt.Before(cutoff) {
			drop = append(drop, r.ID)
			continue
		}
	}
	for _, id := range drop {
		if err := s.Delete(ctx, id); err != nil {
			log.Warn().Err(err).Str("backup_id", id).Msg("backups: retention delete failed")
		}
	}
	return nil
}

// populateCounts reads the per-backup metadata from
// the live DB. If the panel doesn't share the
// Service's pool (s.pgPool == nil), the counts
// default to zero and the schema version is left
// as 0; the UI shows "metadata unavailable" in
// that case.
func (s *Service) populateCounts(ctx context.Context) (schema, nodes, users, hosts int) {
	if s.pgPool == nil {
		return
	}
	_ = s.pgPool.QueryRow(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schema)
	_ = s.pgPool.QueryRow(ctx, `SELECT COUNT(*) FROM nodes`).Scan(&nodes)
	_ = s.pgPool.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&users)
	_ = s.pgPool.QueryRow(ctx, `SELECT COUNT(*) FROM hosts`).Scan(&hosts)
	return
}

// runDumpToFile streams the Dumper's output into
// <backupsDir>/<id>.dump.gz. The Dumper is
// `s.dumper`; the test injection point is
// SetDumper.
//
// Pre-PR-#228 this used `defer closeQuiet(src)` to
// drop the pg_dump subprocess's Close error, which
// meant a failed pg_dump run was reported as
// status=ok with a 0-byte (or near-zero) dump file.
// The fix: src.Close() is the operation's terminal
// result and is checked explicitly. closeQuiet is
// reserved for best-effort handle cleanup (the
// output file, the gzip writer) where a close
// error after a successful write is never
// actionable.
//
// The named return + central cleanup defer is
// load-bearing: the file handle `out` must be
// closed BEFORE the function tries to remove the
// file on error, otherwise the remove races with
// the still-open handle (Windows: "process cannot
// access the file because it is being used by
// another process"). The defer block closes `out`
// first, then conditionally removes the file.
func (s *Service) runDumpToFile(ctx context.Context, id string) (dumpPathOut string, errOut error) {
	if s.cfg.BackupsDir == "" {
		return "", errors.New("backups: empty BackupsDir")
	}
	dumpPath := filepath.Join(s.cfg.BackupsDir, id+".dump.gz")
	out, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	// Single deferred cleanup: always close the
	// file handle, and on a failed run remove the
	// file from disk. The ordering is critical —
	// closeQuiet runs first, then the conditional
	// remove, because Windows / macOS refuse to
	// delete a file with an open write handle.
	defer func() {
		closeQuiet(out)
		if errOut != nil {
			_ = os.Remove(dumpPath)
		}
	}()

	// The dump goes through a gzip layer so the
	// .dump.gz convention holds even when the
	// underlying Dumper returns a raw stream. The
	// custom-format pg_dump output is already
	// compressed; the gzip here is metadata-cheap
	// and makes the .dump.gz filename honest.
	gz, err := newGzipWriter(out)
	if err != nil {
		return "", err
	}
	defer closeQuiet(gz)

	src, err := s.dumper.Dump(ctx, s.cfg.PostgresDSN)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(gz, src); err != nil {
		// Drain the dump reader so the
		// subprocess is reaped and its
		// stderr is captured, but ignore
		// the result — the io.Copy error is
		// the primary signal.
		_ = src.Close()
		return "", err
	}
	// src.Close() is the pg_dump subprocess's
	// exit code; a non-nil error here means the
	// dump did NOT succeed and the file on disk
	// is not a valid backup. The Service.Create
	// caller (Create) treats this as a hard
	// failure, marks the row status=failed,
	// and fires the backup.failed event.
	if err := src.Close(); err != nil {
		return "", err
	}
	if err := gz.Close(); err != nil {
		return "", err
	}
	return dumpPath, nil
}

// newGzipWriter returns a gzip writer that
// transparently degrades to a passthrough writer
// when the file already ends in .dump (not .dump.gz).
// In v0.5.0 the file always ends in .dump.gz so the
// gzip path is always taken. The wrapper exists so a
// future "skip gzip" config can flip a flag without
// changing the call sites.
func newGzipWriter(w io.Writer) (io.WriteCloser, error) {
	return newGzipWriterImpl(w)
}

// hashFile returns the hex SHA-256 of the file at
// path. The file is streamed so 100MB+ dumps do not
// blow up memory.
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer closeQuiet(f)
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// writeSidecar writes `<path>.sha256` containing
// the hex digest. The convention matches
// `sha256sum -c`: one line, "<hex>  <basename>\n".
func writeSidecar(path, hexDigest string) error {
	contents := fmt.Sprintf("%s  %s\n", hexDigest, filepath.Base(path[:len(path)-len(".sha256")]))
	return os.WriteFile(path, []byte(contents), 0o600)
}

// newBackupID returns a chronologically-sortable ID:
// "bck_<14-char-base32-of-unixtime>_<8-char-hex>".
// The 14-char base32 covers ~1.2k years from epoch
// at second resolution with 6 chars of sub-second
// precision; the 8-char hex tail is from crypto/rand.
//
// The ID is also a valid filename on every common
// filesystem (no slashes, no spaces, no NUL).
func newBackupID(now time.Time) string {
	ns := now.UnixNano()
	// 14 chars of base32 = 70 bits; we encode the
	// nanosecond timestamp with leading-zero padding
	// and a 6-char suffix of the 9-digit sub-second.
	bigTs := big.NewInt(ns)
	encoded := bigTs.Text(32) // base32 lowercase, but we want uppercase
	encoded = strings.ToUpper(encoded)
	if len(encoded) > 14 {
		encoded = encoded[len(encoded)-14:]
	} else {
		encoded = strings.Repeat("0", 14-len(encoded)) + encoded
	}
	randTail := make([]byte, 4)
	_, _ = rand.Read(randTail)
	tail := hex.EncodeToString(randTail)
	return "bck_" + encoded + "_" + tail
}

// _ keeps the uuid import live for the day the
// Service grows per-restore audit fields.
var _ = uuid.Nil
