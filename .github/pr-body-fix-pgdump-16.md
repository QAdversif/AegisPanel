## What

The v0.8.18 live smoke test caught a fourth
silent backup bug: pg_dump 15 (shipped in
the panel image) refuses to dump a
PostgreSQL 16 server. The panel's `pg_dump`
binary is from the `postgresql-client-15`
package that ships with Debian 12 bookworm;
the production server is `postgres:16-alpine`.
The pg_dump / postgres major versions must
match — pg_dump exits 1 with "server
version mismatch" otherwise.

This is the v0.8.18 counterpart of the
v0.8.15 / v0.8.16 / v0.8.17 chain: another
silent production failure, only visible
because the v0.8.18 fix made the
Service's `src.Close()` error surface as
`status=failed` in the row JSON.

## Fix

Add the PGDG apt repo to the tooling stage
and install `postgresql-client-16`. PGDG
(`https://apt.postgresql.org/`) is the
official PostgreSQL apt repository and is
the standard way to ship a current pg_dump
on bookworm. After the install, the same
v0.8.17 pattern applies: `rm` the
`/usr/bin/pg_dump` symlink-to-`pg_wrapper`,
`cp` the actual ELF binary from
`/usr/lib/postgresql/16/bin/pg_dump` over
it, then `COPY` both into the runtime image.

The PGDG list file and the GPG key are
removed in the same `RUN` so the image does
not contain arbitrary third-party trust
anchors long-term.

## Files

- `backend/Dockerfile` — tooling stage
  installs PGDG key + repo, then
  `postgresql-client-16`. Symlink
  substitution updates from
  `/usr/lib/postgresql/15/bin/pg_dump` to
  `/usr/lib/postgresql/16/bin/pg_dump`.
  The runtime `COPY` lines are updated to
  match. The pre-existing copy-the-whole
  `/usr/lib/x86_64-linux-gnu` pattern
  carries the libpq / libssl / libldap .so
  files needed by the 16.x binary.

## Verification

The next v0.8.19 release will go through
the same v0.8.18 live smoke test path:
cosign-verify, deploy, then
`POST /api/v1/backups/` and confirm
`status=ok` with a non-trivial `size_bytes`
(matching the on-disk `.dump.gz` size,
not the 23-byte empty-dump signature of
v0.8.15–v0.8.18).

The CI `containers` job builds the multi-arch
image end-to-end. If the PGDG key URL, the
repo URL, or the package name change, the
build fails at the `apt-get install` step —
the hard-gate smoke test the user has been
asking for.
