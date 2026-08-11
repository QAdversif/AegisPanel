// SPDX-License-Identifier: AGPL-3.0-or-later
//
// pgBinaries: the production Dumper + Restorer
// implementation. Wraps the local `pg_dump` /
// `pg_restore` client binaries (installed in the
// panel image via the distroless `tooling` stage
// of the multi-stage Dockerfile; see PR #222 and
// #226). Both binaries are config-controlled —
// the v0.5.0 contract is `AEGIS_BACKUPS_PG_DUMP`
// and `AEGIS_BACKUPS_PG_RESTORE`; the Service
// passes the resolved paths in Config.PgDumpBin /
// Config.PgRestoreBin.
//
// The DSN handling is the post-PR-#228 design:
// pgDumpArgs / pgRestoreArgs are pure functions
// that build argv + the PGPASSWORD env value from
// the DSN. The password is moved out of the URL
// into PGPASSWORD so the subprocess argv (visible
// to any local user via /proc/<pid>/cmdline) does
// not leak credentials. The pure-function split
// keeps argv construction table-testable without
// an exec mock.

package backups

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
)

// pgBinaries is the production Dumper + Restorer.
// Stateless from the Service's point of view; the
// Service passes the DSN per call. The two binary
// paths come from the boot-time Config.
type pgBinaries struct {
	dumpPath    string
	restorePath string
}

// newPgBinaries returns a pgBinaries with the
// given paths; empty strings default to
// `/usr/bin/pg_dump` and `/usr/bin/pg_restore`
// (the production image install location).
func newPgBinaries(dumpPath, restorePath string) *pgBinaries {
	if dumpPath == "" {
		dumpPath = "/usr/bin/pg_dump"
	}
	if restorePath == "" {
		restorePath = "/usr/bin/pg_restore"
	}
	return &pgBinaries{dumpPath: dumpPath, restorePath: restorePath}
}

// Compile-time interface checks.
var (
	_ Dumper    = (*pgBinaries)(nil)
	_ Restorer  = (*pgBinaries)(nil)
)

// Dump spawns `pg_dump -Fc` against the given DSN
// and returns a ReadCloser over its stdout. The
// caller MUST close the returned ReadCloser; the
// Close error is the operation result (it captures
// the subprocess exit code and stderr).
func (b *pgBinaries) Dump(ctx context.Context, dsn string) (io.ReadCloser, error) {
	if _, err := os.Stat(b.dumpPath); err != nil {
		return nil, fmt.Errorf("backups: pg_dump not found at %s: %w", b.dumpPath, err)
	}
	args, pgpw, err := pgDumpArgs(dsn)
	if err != nil {
		return nil, fmt.Errorf("backups: build pg_dump argv: %w", err)
	}
	// #nosec G204,G702 -- pg_dump binary is config-controlled; argv
	// is built by pgDumpArgs from a server-side DSN, not user input.
	cmd := exec.CommandContext(ctx, b.dumpPath, args...)
	if pgpw != "" {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pgpw)
	}
	return newPgDumpReader(cmd)
}

// Restore runs `pg_restore --clean --if-exists`
// against the given DSN, loading the dump file at
// dumpPath. Returns an error wrapping the
// subprocess's combined output on failure.
func (b *pgBinaries) Restore(ctx context.Context, dsn, dumpPath string) error {
	if _, err := os.Stat(b.restorePath); err != nil {
		return fmt.Errorf("backups: pg_restore not found at %s: %w", b.restorePath, err)
	}
	if _, err := os.Stat(dumpPath); err != nil {
		return fmt.Errorf("backups: dump file not found at %s: %w", dumpPath, err)
	}
	args, pgpw, err := pgRestoreArgs(dsn)
	if err != nil {
		return fmt.Errorf("backups: build pg_restore argv: %w", err)
	}
	// #nosec G204,G702 -- pg_restore binary is config-controlled; argv
	// is built by pgRestoreArgs from a server-side DSN, not user input.
	cmd := exec.CommandContext(ctx, b.restorePath, args...)
	if pgpw != "" {
		cmd.Env = append(os.Environ(), "PGPASSWORD="+pgpw)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("backups: pg_restore: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// pgDumpArgs builds the `pg_dump` argv and the
// PGPASSWORD env value from a DSN. It is a pure
// function (no I/O, no globals) so the argv shape
// is table-testable in pg_binaries_test.go.
//
// Returns:
//
//   - args: the full argv slice passed to exec.Command
//   - pgpw: the PGPASSWORD value, or "" if the DSN
//     has no password. Callers MUST NOT include this
//     in the argv; it goes into the command's Env.
//   - err: non-nil only on a malformed URL.
//
// Two DSN shapes are supported:
//
//  1. URL form (`postgres://user:pw@host:port/db?...`):
//     password is extracted into PGPASSWORD; the
//     URL minus the password is passed to --dbname.
//     libpq will pick up PGPASSWORD and merge it
//     with the conninfo URL.
//
//  2. key=value form (`host=… port=… user=… password=…
//     dbname=…`): url.Parse treats this as an opaque
//     relative URL with no scheme, so we pass the
//     whole DSN through to --dbname unchanged and
//     return an empty PGPASSWORD. pg_dump / libpq
//     parses key=value natively.
//
// A DSN that url.Parse accepts but which carries an
// unknown scheme (e.g. `mysql://…`) is rejected:
// passing it to pg_dump would be a silent misconfig.
func pgDumpArgs(dsn string) (args []string, pgpw string, err error) {
	u, perr := url.Parse(dsn)
	if perr != nil || u.Scheme == "" {
		// key=value form, or any DSN that lacks
		// a URL scheme — pass through.
		return []string{"-Fc", "--dbname=" + dsn, "--no-password"}, "", nil
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, "", fmt.Errorf("backups: unsupported DSN scheme %q (want postgres:// or postgresql://)", u.Scheme)
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			pgpw = pw
		}
		// Replace the Userinfo with just the
		// username (no password) so the resulting
		// URL is safe to pass via argv.
		if uname := u.User.Username(); uname != "" {
			u.User = url.User(uname)
		} else {
			u.User = nil
		}
	}
	return []string{"-Fc", "--dbname=" + u.String(), "--no-password"}, pgpw, nil
}

// pgRestoreArgs is the Restore-time counterpart to
// pgDumpArgs. Same DSN-handling rules; same password
// hygiene (PGPASSWORD env, never argv).
func pgRestoreArgs(dsn string) (args []string, pgpw string, err error) {
	u, perr := url.Parse(dsn)
	if perr != nil || u.Scheme == "" {
		return []string{"--clean", "--if-exists", "--dbname=" + dsn, "--no-password"}, "", nil
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return nil, "", fmt.Errorf("backups: unsupported DSN scheme %q (want postgres:// or postgresql://)", u.Scheme)
	}
	if u.User != nil {
		if pw, ok := u.User.Password(); ok {
			pgpw = pw
		}
		if uname := u.User.Username(); uname != "" {
			u.User = url.User(uname)
		} else {
			u.User = nil
		}
	}
	return []string{"--clean", "--if-exists", "--dbname=" + u.String(), "--no-password"}, pgpw, nil
}

// pgDumpReader wraps a pg_dump subprocess's stdout
// pipe. The Close method drains stderr and waits
// for the subprocess to exit, returning an error
// wrapping the exit code and stderr text on
// non-zero exit. This is the operation result the
// Service propagates as the backup's terminal
// error.
type pgDumpReader struct {
	cmd    *exec.Cmd
	stdout io.ReadCloser
	stderr *stderrBuf
}

// newPgDumpReader wires the subprocess's stdout to
// a pipe the caller reads, and captures stderr in
// a thread-safe buffer for the eventual Close
// error message. The subprocess is started
// synchronously; on Start failure the cmd / pipes
// are torn down and the error is returned.
func newPgDumpReader(cmd *exec.Cmd) (io.ReadCloser, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr := &stderrBuf{buf: &strings.Builder{}}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdout.Close()
		return nil, err
	}
	return &pgDumpReader{cmd: cmd, stdout: stdout, stderr: stderr}, nil
}

// Read proxies to the subprocess's stdout pipe.
func (r *pgDumpReader) Read(p []byte) (int, error) {
	return r.stdout.Read(p)
}

// Close drains the stdout pipe (caller's
// responsibility per io.ReadCloser contract) and
// waits for the subprocess to exit. The returned
// error is the operation result: a non-nil error
// means pg_dump exited non-zero (the error wraps
// the exit code and the captured stderr).
func (r *pgDumpReader) Close() error {
	_ = r.stdout.Close()
	err := r.cmd.Wait()
	if err != nil {
		return fmt.Errorf("pg_dump: %w (stderr: %s)", err, strings.TrimSpace(r.stderr.buf.String()))
	}
	return nil
}

// stderrBuf wraps a strings.Builder for use as
// an io.Writer from the pg_dump subprocess. The
// subprocess writes to stderr from a single
// goroutine; the cmd.Wait caller reads the buffer
// from a different goroutine but only after
// Wait returns, which is sequenced after the
// subprocess has closed its stderr file
// descriptor. The single-writer / single-reader
// pattern is therefore race-free without an
// explicit mutex; a future refactor that
// introduces a true concurrent reader must add
// a sync.Mutex here.
type stderrBuf struct {
	buf *strings.Builder
}

// Write is the io.Writer entry point.
func (s *stderrBuf) Write(p []byte) (int, error) {
	return s.buf.Write(p)
}

// String returns the captured stderr as a string.
// Safe to call only after the subprocess has
// exited (i.e. after cmd.Wait has returned).
func (s *stderrBuf) String() string {
	return s.buf.String()
}
