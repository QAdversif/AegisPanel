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
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"

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
}

// Service is the public surface for the package.
// All methods are safe for concurrent use.
type Service struct {
	cfg      Config
	store    Store
	pgPool   *pgxpool.Pool // for the metadata counts and schema version; nil is OK (counts are skipped)
	clock    func() time.Time
	webhooks *webhooks.Service // v0.7.x: outbound event surface. May be nil (see WithWebhooks).

	// dumpFn is the function Create calls to obtain
	// a ReadCloser over the pg_dump output stream.
	// The default is a real pg_dump subprocess (see
	// New); tests inject a fake. The function MUST
	// return a non-nil ReadCloser on success and
	// close cleanly on context cancellation.
	dumpFn func(ctx context.Context) (io.ReadCloser, error)

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
func New(cfg Config, store Store, pool *pgxpool.Pool) *Service {
	if cfg.PgDumpBin == "" {
		cfg.PgDumpBin = "/usr/bin/pg_dump"
	}
	if cfg.PgRestoreBin == "" {
		cfg.PgRestoreBin = "/usr/bin/pg_restore"
	}
	s := &Service{cfg: cfg, store: store, pgPool: pool, clock: time.Now}
	s.dumpFn = s.realDump
	return s
}

// WithWebhooks installs the outbound event service.
// See plans.Service.WithWebhooks for the rationale.
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// SetDumpFn injects a custom dump function. The
// default is a real pg_dump subprocess; tests
// override this to produce a deterministic
// ReadCloser without invoking the system binary.
func (s *Service) SetDumpFn(fn func(ctx context.Context) (io.ReadCloser, error)) {
	s.dumpFn = fn
}

// SetClock injects a deterministic clock for tests.
// The default is time.Now.
func (s *Service) SetClock(now func() time.Time) { s.clock = now }

// Store returns the underlying Store. The HTTP
// handler does not need this; it is exposed for the
// future CLI binary.
func (s *Service) Store() Store { return s.store }

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
	case StatusFailed:
		webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventBackupFailed, row)
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
	return s.store.Delete(ctx, id)
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

// Restore runs `pg_restore --clean --if-exists`
// against the configured DSN from the dump file at
// id. Returns ErrBackupDisabled if AllowUIRestore
// is false (the HTTP handler uses this to gate the
// UI button; the CLI binary bypasses it via a
// direct method call without the flag, but in v0.5.0
// the CLI binary is not in scope yet).
//
// Restore is dangerous: it drops and recreates every
// object in the dump. The caller is responsible for
// having stopped the panel before invoking this.
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
	// pg_restore is the local apt-installed
	// client (`/usr/bin/pg_restore` by default,
	// configurable via AEGIS_BACKUPS_PG_RESTORE
	// or the future install_panel role). The
	// dumpPath is server-generated; the DSN is
	// from the panel's own config. G204 fires on
	// the joined command line; the actual
	// injection surface is empty in v0.5.0.
	cmd := exec.CommandContext(ctx, s.cfg.PgRestoreBin, // #nosec G204,G702 -- pg_restore binary is config-controlled
		"--clean", "--if-exists",
		"--dbname="+dsnDatabase(s.cfg.PostgresDSN),
		"--no-password",
		dumpPath,
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+dsnPassword(s.cfg.PostgresDSN))
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("backups: pg_restore: %w: %s", err, string(out))
	}
	return nil
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

// realDump is the default dumpFn: spawns `pg_dump`
// and returns a ReadCloser over its stdout.
func (s *Service) realDump(ctx context.Context) (io.ReadCloser, error) {
	if _, err := os.Stat(s.cfg.PgDumpBin); err != nil {
		return nil, fmt.Errorf("backups: pg_dump not found at %s: %w", s.cfg.PgDumpBin, err)
	}
	// pg_dump is the local apt-installed
	// client (`/usr/bin/pg_dump` by default,
	// configurable via AEGIS_BACKUPS_PG_DUMP or
	// the future install_panel role). The DSN
	// is the panel's own. G204 fires on the
	// joined command line; the actual injection
	// surface is empty in v0.5.0.
	cmd := exec.CommandContext(ctx, s.cfg.PgDumpBin, // #nosec G204,G702 -- pg_dump binary is config-controlled
		"-Fc",
		"--dbname="+dsnDatabase(s.cfg.PostgresDSN),
		"--no-password",
	)
	cmd.Env = append(os.Environ(), "PGPASSWORD="+dsnPassword(s.cfg.PostgresDSN))
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	// stderr is captured for error reporting; the
	// pipe is closed when the cmd exits, so we
	// stash it in a strings.Builder and surface it
	// on failure.
	stderr := &stderrBuf{buf: &strings.Builder{}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &pgDumpReader{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

// pgDumpReader wraps the pg_dump subprocess's stdout
// pipe. The Close method waits for the subprocess to
// exit (so a context-cancel reader gets the stderr
// output as part of the error path).
type pgDumpReader struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *stderrBuf
}

func (r *pgDumpReader) Read(p []byte) (int, error) { return r.stdout.Read(p) }
func (r *pgDumpReader) Close() error {
	_ = r.stdout.Close()
	err := r.cmd.Wait()
	if err != nil {
		return fmt.Errorf("pg_dump: %w (stderr: %s)", err, r.stderr.buf.String())
	}
	return nil
}

// stderrBuf is a thread-safe wrapper around
// strings.Builder for use as an io.Writer from
// concurrent goroutines. The pg_dump subprocess
// writes to stderr from one goroutine; the cmd.Wait
// caller may read the buffer from another.
type stderrBuf struct {
	mu  sync.Mutex
	buf *strings.Builder
}

func (s *stderrBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.Write(p)
}

// runDumpToFile streams the dump function's output
// into <backupsDir>/<id>.dump.gz. The dump function
// is `s.dumpFn`; the test injection point.
func (s *Service) runDumpToFile(ctx context.Context, id string) (string, error) {
	if s.cfg.BackupsDir == "" {
		return "", errors.New("backups: empty BackupsDir")
	}
	dumpPath := filepath.Join(s.cfg.BackupsDir, id+".dump.gz")
	out, err := os.OpenFile(dumpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	defer closeQuiet(out)

	// The dump goes through a gzip layer so the
	// .dump.gz convention holds even when the
	// underlying dumpFn returns a raw stream. The
	// custom-format pg_dump output is already
	// compressed; the gzip here is metadata-cheap
	// and makes the .dump.gz filename honest.
	gz, err := newGzipWriter(out)
	if err != nil {
		return "", err
	}
	defer closeQuiet(gz)

	src, err := s.dumpFn(ctx)
	if err != nil {
		return "", err
	}
	defer closeQuiet(src)
	if _, err := io.Copy(gz, src); err != nil {
		_ = os.Remove(dumpPath)
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

// dsnDatabase and dsnPassword parse the Postgres
// DSN. We don't use url.ParseQuery because pgx's DSN
// format is "key=value key=value" (no &), not a
// query string. A real pgx DSN parser is a future
// improvement; for v0.5.0 the operator supplies a
// DSN in the form
//
//	postgres://user:pass@host:port/dbname?sslmode=disable
//
// which url.Parse handles correctly.
func dsnDatabase(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(u.Path, "/")
}

func dsnPassword(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return ""
	}
	pw, _ := u.User.Password()
	return pw
}

// _ keeps the uuid import live for the day the
// Service grows per-restore audit fields.
var _ = uuid.Nil
