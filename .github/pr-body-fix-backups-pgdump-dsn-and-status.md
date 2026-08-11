## What

Three silent bugs in the backups subsystem, found by
the v0.8.17 live smoke test, fixed architecturally.

### Bug A — DSN stripped to bare db name (the obvious one)

`service.go:560-562` (pre-PR) called
`pg_dump -Fc --dbname=aegis --no-password` with
`dsnDatabase()` reducing the DSN to the db name.
Without host / user / port pg_dump tried a local
Unix socket that does not exist in the panel
container → exit 1 → silent failure.

`service.go:479` (Restore) had the same shape.

### Bug B — pg_dump exit code discarded

`runDumpToFile:648` (pre-PR) used
`defer closeQuiet(src)` to satisfy errcheck. The
`src` is `pgDumpReader`, whose `Close()` is the
subprocess's exit-code result. `closeQuiet` is
intentionally best-effort (its docstring says so)
and silences the close error with `log.Warn`.

The chain:

1. `io.Copy(gz, src)` returns `nil` on EOF
   (EOF is not an error in `io.Copy`).
2. `src.Close()` returns the pg_dump exit error.
3. `defer closeQuiet(src)` discards it.
4. `runDumpToFile` returns `(dumpPath, nil)`.
5. `Create` marks the row `status=ok` with
   `size=23` (gzip header + empty deflate + CRC).

The user-visible symptom: a "successful" backup
row in the UI with a tiny / empty dump file. The
operator only sees the problem when they try to
restore.

### Bug C — `closeQuiet` over-broad

`closeQuiet` is the right call for `defer f.Close()`
on file handles whose close error is never
actionable. It is the **wrong** call for the
pg_dump reader whose Close is the operation
result. Pre-PR used it for both — a structural
mix-up, not just a missing check.

## Fix (architectural, not a band-aid)

### 1. Split `dumpFn` into Dumper / Restorer interfaces

New `dumper.go`:

```go
type Dumper interface {
    Dump(ctx context.Context, dsn string) (io.ReadCloser, error)
}
type Restorer interface {
    Restore(ctx context.Context, dsn, dumpPath string) error
}
```

The Service holds `dumper` / `restorer` as fields.
Production wiring (`New`) installs a `pgBinaries`
configured with `cfg.PgDumpBin` / `cfg.PgRestoreBin`.
Tests inject fakes via `SetDumper` / `SetRestorer`.
`SetDumpFn` is gone (pre-PR test injection
replaced).

### 2. `pgBinaries` holds the pg_dump / pg_restore knowledge

New `pg_binaries.go`. Two responsibilities:

  - Build argv + PGPASSWORD env from a DSN.
  - Spawn the subprocess, wire stdout / stderr
    pipes, return a `pgDumpReader` whose Close
    surfaces the exit code.

### 3. Pure `pgDumpArgs` / `pgRestoreArgs` for testability

```go
func pgDumpArgs(dsn string) (args []string, pgpw string, err error)
```

Pure function: URL → argv + PGPASSWORD. **Password
is moved out of the URL into the env, never the
argv.** The argv-shape is table-testable without
an exec shim (which is non-trivial in Go stdlib).
Two DSN shapes are supported:

  - URL: `postgres://user:pw@host:port/db?…`
    → `--dbname=postgres://user@host:port/db?…`
    + `PGPASSWORD=pw`
  - key=value: `host=… port=… user=… dbname=…`
    → passthrough, no PGPASSWORD

Unknown schemes (e.g. `mysql://`) are rejected
with a clear error — passing them to pg_dump
would be a silent misconfig.

### 4. `runDumpToFile` propagates `src.Close()` as the result

```go
src, err := s.dumper.Dump(ctx, s.cfg.PostgresDSN)
if err != nil { return "", err }
if _, err := io.Copy(gz, src); err != nil {
    _ = src.Close()        // drain the subprocess
    return "", err
}
if err := src.Close(); err != nil {  // exit-code check
    return "", err
}
```

`closeQuiet` is reserved for the file handle
and gzip writer (best-effort). The output file
is removed on any error via a single named-return
deferred cleanup (handles the Windows "file
locked by open handle" race).

### 5. `Restore` goes through the same `Restorer` interface

`Service.Restore` keeps the `AllowUIRestore`
gate (a Service-level policy, not a binary-level
concern). Delegates to `s.restorer.Restore(ctx,
dsn, dumpPath)`. Same DSN fix as Create.

## Test coverage

New `pg_binaries_test.go`: table-driven tests of
`pgDumpArgs` and `pgRestoreArgs` covering

  - URL DSN with password (extraction + leak check)
  - URL DSN without password
  - URL DSN with empty userinfo
  - `postgresql://` scheme (libpq canonical)
  - key=value DSN (passthrough)
  - URL with %-encoded password
  - URL with IPv6 host
  - Unsupported scheme → error

Each case for the password-bearing URLs also
asserts **the password does not appear in the
argv** — the security invariant.

`TestServiceCreateFailureOnCloseError` (new):
regression test that locks in the Bug B fix.
A `fakeDump` whose Close returns an error must
produce a `status=failed` row and a removed
dump file. Pre-PR this test would have failed
because `closeQuiet` discarded the error.

The 3 pre-PR tests that used `SetDumpFn` were
mechanically migrated to `SetDumper` +
`fakeDumper` (4 file edits, no semantic change).
`TestDSNParse` (which tested the removed
`dsnDatabase`) is gone; its coverage moved to
`pgDumpArgs` cases in the new file.

## Files

```
A backend/internal/backups/dumper.go                       ( 62 lines, new )
A backend/internal/backups/pg_binaries.go                  (252 lines, new )
A backend/internal/backups/pg_binaries_test.go             (175 lines, new )
M backend/internal/backups/service.go                      (-128 / +107 lines)
M backend/internal/backups/service_test.go                 ( +104 / -39 lines)
M backend/internal/backups/dispatcher_test.go              (   +6 /  -4 lines)
M backend/internal/backups/audit_dispatcher_test.go        (   +8 / -20 lines)
M backend/internal/backups/schedule_test.go                (  -39 lines, removed obsolete TestDSNParse)
```

## Verification

  - `go test ./...` — all 27 backend packages pass
  - `go vet ./...` — clean
  - `go build ./...` — clean

## Follow-ups (NOT in this PR)

  - Live smoke test step in `release.yml` that
    runs `pg_dump --version` and a tiny backup
    against the new image BEFORE publish. Three
    silent bugs (v0.8.15 / v0.8.16 / v0.8.17)
    would have been caught by this.
  - The 24h restore-drill on a fresh VM (Tier 1
    MVP-1.0 gate per `docs/gap-analysis-v0.8.15.md`).
