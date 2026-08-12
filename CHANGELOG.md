# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

## [0.8.25] - 2026-08-12

This is a **re-provision fix** that closes the
last silent production bug from the v0.8.17 →
v0.8.24 live smoke series. v0.8.24 made the
state-propagation work end-to-end (DB state
flips to `online` after a successful L4), but
a subsequent **re-provision** of an already-
running node failed at the `upload-agent`
stage with `sftp: "Failure" (SSH_FX_FAILURE)`.
The root cause is Linux's `ETXTBSY`: the
kernel refuses to let one process unlink or
truncate a binary that's currently mmap'd for
execution by another process. v0.8.25 splits
the upload into two steps (SFTP to a temp file,
then `mv -f` over the target) — the only
race-free way to replace a running executable.
A running process keeps the unlinked inode
until it exits, and a new process (or the
systemd `Restart=always` loop) picks up the
new binary at the same path.

Single PR (#235), bootstrap-only:

### Fixed
- **Bootstrap: `Installer.uploadAgent` uses
  `Client.UploadAndSwap` (new SFTP temp+rename
  primitive) instead of `Client.Upload`.** Direct
  SFTP overwrite of `/usr/local/bin/aegis-agent`
  on a re-provision fails with `SSH_FX_FAILURE`
  on Linux — the kernel returns `ETXTBSY` (text
  file busy) for a write that would unlink a
  binary currently mmap'd for execution by
  another process. v0.8.25 splits the upload
  into two steps: SFTP-write the new binary to
  a temp file (`.aegis-agent.swap.<rand>`) in
  the same directory, then `mv -f` the temp
  over the target via SSH. The rename(2) call
  succeeds even when the target is executing;
  the running process keeps the unlinked inode
  until it exits, and a new process (or the
  systemd `Restart=always` loop) picks up the
  new binary at the same path. The temp file
  is cleaned up on every exit path
  (best-effort, swallowed `Remove` error on
  the happy path because rename already
  unlinked it). v0.8.x releases worked on a
  fresh install (the file did not exist yet)
  but failed on a re-provision — caught during
  the v0.8.24 live smoke (`L4 → 502`, `state:
  "offline"`), root-caused to ETXTBSY via
  `lsof /usr/local/bin/aegis-agent` showing
  the running PID holds the file open for
  execution. v0.9.0 candidate: surface the
  `Restart=always` semantic by running
  `systemctl kill --signal=SIGTERM
  aegis-agent.service` before the SFTP upload
  (the `Restart=always` unit restarts
  immediately, the new binary is in place by
  the time the new process is up). Tracked
  under "ETXTBSY + service-restart race" in
  KNOWN_LIMITATIONS.

### Added
- **`bootstrap.Client.UploadAndSwap(ctx, src, dst,
  mode)` interface method** — the temp+rename
  primitive. The existing `Upload` is unchanged
  and remains the right call for non-binary
  uploads (public-key push, agent.env, systemd
  unit) where the destination does not exist
  yet and ETXTBSY is impossible. The
  `sshClient` impl uses `crypto/rand` for the
  8-hex-char temp suffix (4 bytes = 32 bits of
  entropy, no PID collisions across panel
  restarts). `sshFingerprintWire` and the
  `Installer.uploadAgent` flow are unchanged
  except for the new call. Mock seam in
  `installer_test.go` records
  `uploadSwapPaths` separately from
  `uploadPaths` so a regression that calls
  `Upload` instead of `UploadAndSwap` for the
  agent binary fails the
  `TestInstaller_SuccessPath` assertion.

## [0.8.24] - 2026-08-12

This is a **state-propagation fix** that closes
the last silent production bug from the
v0.8.17 → v0.8.24 live smoke series. v0.8.23
made the SSH fingerprint compare work, and the
L4 provision returned 200 with
`{"new_state":"online"}` — but the node row
in the DB still showed `state: "new"`.

Single PR (#234), nodes-only:

### Fixed
- **Nodes: `BootstrapNodeProvider.Update`
  propagates the State field to the SQL
  UPDATE.** Pre-PR the method mutated
  `current.State` locally then called
  `a.Svc.Update(ctx, current.ID, UpdateInput{})`
  with an EMPTY `UpdateInput`. `UpdateInput` is
  a pointer-field struct where nil pointers
  mean "leave alone". With no non-nil fields,
  the underlying service update wrote nothing
  — and the operator's UI / state machine
  silently disagreed with the provision
  response. v0.8.24 passes the new state via
  `UpdateInput{State: &newState}`. One line of
  real change, plus a comment documenting the
  pre-PR-#234 bug.

### Files
- `backend/internal/nodes/handler.go` —
  `BootstrapNodeProvider.Update` now passes
  `State: &newState` to `a.Svc.Update`.

### Verification
v0.8.24's live smoke test on the live server.click:
after `POST /api/v1/nodes/9ded165d.../provision`
returns 200, `GET /api/v1/nodes/9ded165d...`
should return `state: "online"`. The DB row
should also show `state='online'` in `psql`.

## [0.8.23] - 2026-08-12

This is a **fingerprint-prefix-compare fix**
that closes the last silent production bug
from the v0.8.17 → v0.8.23 live smoke series.
v0.8.22 forced the ed25519 key path so both
sides now compute the same fingerprint, but
the literal-string `fingerprintEqual` compare
rejected `pCnGi…` ≠ `SHA256:pCnGi…` because
one side included the `SHA256:` prefix and
the other did not.

Single PR (#233), bootstrap-only:

### Fixed
- **Bootstrap: `fingerprintEqual` now strips a
  leading `SHA256:` or `MD5:` algorithm
  prefix (case-insensitive) from both sides
  before comparing.** The panel's internal
  `sshFingerprintWire` returns just the base64
  payload (no prefix). The operator's paste
  from `ssh-keygen -lf` always has the
  `SHA256:` prefix. The compare must accept
  both forms. A new `stripFingerprintPrefix`
  helper does the prefix detection; unknown
  prefixes are passed through unchanged so a
  future algorithm change surfaces as a real
  mismatch.

### Files
- `backend/internal/bootstrap/ssh.go` —
  new `stripFingerprintPrefix` helper;
  `fingerprintEqual` updated to call it.
- `backend/internal/bootstrap/ssh_test.go` —
  `TestFingerprintEqual` rewritten as
  table-driven; 5 cases (case-insensitive,
  different base64, mixed prefix, MD5 prefix,
  unknown prefix).

### Verification
v0.8.23's live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
`expected_fingerprint: SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`.

## [0.8.22] - 2026-08-12

This is a **host-key-algorithm fix** that closes
the last silent production bug from the
v0.8.17 → v0.8.22 live smoke series. PR #231
(`v0.8.21`) made the fingerprint compare run
on the binary wire format, but the compare
still rejected the operator's pin because
the SSH server negotiated an ECDSA host key
while the operator had pinned the ed25519
fingerprint. The Go client's `HostKeyAlgorithms`
list accepted any of `{rsa, ecdsa, ed25519}`
and let the server pick.

Single PR (#232), bootstrap-only:

### Fixed
- **Bootstrap: pin `HostKeyAlgorithms` to
  `["ssh-ed25519"]` in the SSH client config.**
  The operator's `expected_fingerprint` is for
  ONE specific key. With the previous "accept
  any algorithm" default the server could
  negotiate a different key (e.g. ECDSA when
  the operator pinned ed25519), the
  fingerprint compare would always fail, and
  the provision would return
  `ErrHostKeyMismatch` even with a correct
  pin. v0.8.22 forces the client to only
  accept ed25519, so the fingerprint compare
  always runs on the SAME key the operator
  pinned. The Demo-нода (`cdn2ne.<prod-host>.click`)
  exposes three host keys (rsa, ecdsa-sha2-
  nistp256, ed25519); with v0.8.21 the client
  was picking ECDSA per the server's kexinit
  and the operator's ed25519 pin was rejected.
  v0.9.0 candidate: parse the algorithm from
  the expected fingerprint and pin
  accordingly so an operator can pin ed25519
  OR ecdsa OR rsa. Until then the operator is
  expected to pin an ed25519 fingerprint.

### Files
- `backend/internal/bootstrap/ssh.go` —
  `ssh.ClientConfig` gains
  `HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}`.
  ~25 lines of comment explaining the
  rationale and the v0.9.0 follow-up.

### Verification
v0.8.22's live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
the Demo-нода's `expected_fingerprint:
SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`.

## [0.8.21] - 2026-08-12

This is a **fingerprint-compare fix** that closes
the last silent production bug from the
v0.8.17 → v0.8.21 live smoke series. PR #230
(`v0.8.20`) made the TOFU path reachable for
the first time; the very next call surfaced
this latent bug: the operator's
`expected_fingerprint` (identical to
`ssh-keygen -lf` output) never matched the
panel's computed hash, so the very first
provision on a fresh install returned
`ErrHostKeyMismatch` even with a correct pin.

Single PR (#231), bootstrap-only:

### Fixed
- **Bootstrap: SSH fingerprint is now computed
  from the binary wire format, matching
  OpenSSH.** Pre-PR the panel called
  `ssh.FingerprintSHA256(key)`, a long-standing
  misnomer in `golang.org/x/crypto/ssh`: that
  function hashes the **authorized_keys LINE**
  format (`"ssh-ed25519 AAAA...\n"`), not the
  **binary wire format** (`base64-decode("AAAA...")`).
  The two hashes are different, and the
  OpenSSH-standard fingerprint is the wire one
  (what every operator pastes from
  `ssh-keygen -lf` / `ssh-keyscan`). The result
  on a fresh install: the operator's correct
  pin was compared to the wrong hash and the
  fingerprint compare reported a spurious
  `ErrHostKeyMismatch`. v0.8.21 replaces the
  call with a custom `sshFingerprintWire(key)`
  that SHA-256s `key.Marshal()` and returns the
  base64 with trailing `=` stripped (to match
  `ssh-keygen -lf` byte-for-byte). A regression
  test `TestSshFingerprintWire_MatchesOpenSSH`
  pins the production Demo-нода's host key
  and the expected fingerprint as fixtures,
  and also asserts that the legacy Go
  `FingerprintSHA256` still produces a
  DIFFERENT hash (proving the fix is not just
  accidentally matching the wrong library
  function).

### Files
- `backend/internal/bootstrap/ssh.go` —
  new `sshFingerprintWire` helper, one call
  site changed; `crypto/sha256` +
  `encoding/base64` imports added.
- `backend/internal/bootstrap/ssh_test.go` —
  new `TestSshFingerprintWire_MatchesOpenSSH`;
  the three pre-existing TOFU tests updated
  to use the new helper for their
  `ExpectedFingerprint` setup.

### Verification
v0.8.21's live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and the
Demo-нода's `expected_fingerprint:
SHA256:pCnGi8kyWPaDdcRUpSPBM9y2wAJfqe3smcTmADywvJM`
should return 200 with `state: "online"`
(transition from `new` → `provisioning` → `online`).

## [0.8.20] - 2026-08-12

This is a **TOFU fix** that closes the last
silent production bug from the v0.8.17 →
v0.8.20 live smoke series. PR #228
(`v0.8.18`) and PR #229 (`v0.8.19`) made
the backups + pg_dump / pg_dump-16 flow
visible, but `POST /api/v1/nodes/{id}/provision`
on a fresh install still returned 502 with
"knownhosts: key is unknown" because the
TOFU policy branch was unreachable when
`/var/lib/aegis/known_hosts` existed (even
as a 0-byte file).

Single PR (#230), bootstrap-only:

### Fixed
- **Bootstrap: TOFU policy is now reachable
  on a fresh install.** Pre-PR
  `hostKeyCallback()` early-returned the
  strict `knownhosts.New` callback whenever
  the file existed (even empty), which
  short-circuited the TOFU switch. The result:
  the operator-supplied `expected_fingerprint`
  was never compared, the `tofu_policy:
  accept-and-append` was never honored, and
  the very first provision on a fresh install
  failed with `knownhosts: key is unknown`.
  PR #230 lifts the TOFU policy to be the
  single source of truth: the known_hosts
  lookup is invoked *inside* the
  `TofuAcceptAndAppend` branch (and inside
  `TofuReject`), never as an early exit.
  On a known-key match the callback returns
  nil silently. On a miss, the TOFU fingerprint
  compare runs and (on match) stashes the
  key for the post-handshake append. On
  fingerprint mismatch, `ErrHostKeyMismatch`
  is returned. The existing test
  `TestHostKeyCallback_KnownKey_Accepted`
  locks in the silent-accept behavior;
  `TestHostKeyCallback_EmptyKnownHosts_TOFU_Accepts`
  is the regression test for the v0.8.19
  bug; `TestHostKeyCallback_EmptyKnownHosts_RejectsOnMismatch`
  is the safety net.

### Files
- `backend/internal/bootstrap/ssh.go` —
  `hostKeyCallback()` rewritten. The
  `knownhosts.New` inner callback is now
  invoked from inside the TOFU policy
  callback. No signature change.
- `backend/internal/bootstrap/ssh_test.go` —
  three new regression tests.

### Verification
v0.8.20's live smoke test on the live server.click:
`POST /api/v1/nodes/9ded165d-7ef1-427c-b5f2-41483c10df7b/provision`
with `tofu_policy: accept-and-append` and
the Demo-нода's `expected_fingerprint`
should return 200 with `state: "online"`
(transition from `new` → `provisioning` → `online`).

## [0.8.19] - 2026-08-12

This is a **compatibility hotfix** that ships
a pg_dump whose major version matches the
production `postgres:16-alpine` server.
The v0.8.18 live smoke test caught the
mismatch — pg_dump 15.18 (from stock Debian
12's `postgresql-client-15`) refuses to
dump a PostgreSQL 16 server with
"server version mismatch". The v0.8.18
PR #228 fix made this bug visible as
`status=failed` in the API response; v0.8.19
resolves the root cause.

Single PR (#229), Dockerfile-only:

### Fixed
- **Backups: panel image now ships
  `postgresql-client-16` from the PGDG
  apt repo.** The `tooling` stage in
  `backend/Dockerfile` adds the PGDG repo
  (`https://apt.postgresql.org/pub/repos/apt`),
  installs the GPG key from the PGDG
  canonical URL, and runs
  `apt-get install postgresql-client-16`.
  The v0.8.17 pattern of
  `rm /usr/bin/pg_dump && cp /usr/lib/postgresql/16/bin/pg_dump /usr/bin/pg_dump`
  in the tooling stage is preserved so the
  distroless runtime image gets a flat
  pg_dump ELF (not the `pg_wrapper`
  shell-script symlink). The PGDG list and
  the GPG key are removed at the end of
  the `RUN` so the image does not contain
  arbitrary third-party trust anchors
  long-term.

### Files
- `backend/Dockerfile` (tooling stage + the
  matching runtime `COPY` lines)

### Verification
v0.8.19's live smoke test on the live server.click:
`docker exec aegis-panel /usr/bin/pg_dump --version`
should report `pg_dump (PostgreSQL) 16.x` and
`POST /api/v1/backups/` should return a
`status=ok` row with `size_bytes` matching
the on-disk `.dump.gz` size — not the
23-byte empty-dump signature of v0.8.15
through v0.8.18.

## [0.8.18] - 2026-08-12

This is a **quality release** that closes the
silent backup-failure bug discovered by the
v0.8.17 live smoke test. v0.8.17 made
`/usr/bin/pg_dump` a real binary; the smoke
test then showed `POST /api/v1/backups/`
returning `status=ok` with a 23-byte empty
dump file. The DSN handling and the
subprocess-error propagation both needed
fixing — neither was a quick patch.

Single PR (#228), architectural refactor:

### Fixed
- **Backups: full DSN passed to `pg_dump`,
  not a stripped-to-db-name form.** Pre-PR,
  `service.go:560-562` called
  `pg_dump -Fc --dbname=aegis --no-password`
  via `dsnDatabase()` reducing the panel's
  full DSN to a bare db name. Without
  host / user / port pg_dump tried a local
  Unix socket that does not exist in the
  panel container, exited 1, and the
  Service silently marked the row `status=ok`
  with the empty 23-byte dump. `Restore` had
  the same shape (line 479). v0.8.18 lifts
  the pg_dump / pg_restore knowledge into
  `pgBinaries` and passes the full DSN.
- **Backups: `pg_dump` exit code is now the
  operation result, not best-effort close.**
  Pre-PR, `runDumpToFile` used
  `defer closeQuiet(src)` to satisfy
  errcheck. The `src` was the `pgDumpReader`
  whose `Close()` returns the subprocess
  exit code; `closeQuiet` is intentionally
  best-effort and discarded the error.
  The result: a non-zero pg_dump exit was
  reported as `status=ok`. v0.8.18 calls
  `src.Close()` explicitly and propagates
  any error. The partial file is removed
  via a single named-return deferred
  cleanup (Windows-safe: the file handle
  is closed before the unlink attempt).
  Regression test:
  `TestServiceCreateFailureOnCloseError`.

### Security
- **Backups: password never in argv.**
  The new `pgDumpArgs` / `pgRestoreArgs`
  pure functions extract the password from
  a URL-form DSN into the `PGPASSWORD`
  env var, and pass `--dbname=postgres://user@host:port/db?…`
  with no password in the URL. Pre-PR,
  the full URL (with password) was passed
  via `--dbname=`, leaking the password
  to any local user reading
  `/proc/<pid>/cmdline` or `ps`. The
  key=value DSN form is also supported
  and passes through unchanged. The
  existing `redactDSN()` in
  `cmd/aegis-pg-restore/main.go` is the
  evidence the codebase already takes
  password hygiene seriously.

### Refactor
- **Backups: Dumper / Restorer interfaces
  split out of Service.** The Service
  used to know pg_dump's argv shape
  directly (`realDump` method, the
  `dumpFn func(ctx) (io.ReadCloser, error)`
  field). v0.8.18 introduces two
  consumer-side interfaces — `Dumper` and
  `Restorer` — and a concrete `pgBinaries`
  implementation. The Service now holds
  the DSN in its `Config` and delegates
  the dump / restore to the injected
  Dumper / Restorer. Test injection
  changed from `SetDumpFn` to
  `SetDumper` (and new `SetRestorer`).
  The pre-PR `dsnDatabase` /
  `dsnPassword` helpers and the
  in-Service `realDump` method are
  gone; their coverage moved to
  `pg_binaries_test.go` table-driven
  tests of the new `pgDumpArgs` /
  `pgRestoreArgs` pure functions (8
  cases each, including a password-leak
  invariant check).

### Files
- `backend/internal/backups/dumper.go` (new)
- `backend/internal/backups/pg_binaries.go` (new)
- `backend/internal/backups/pg_binaries_test.go` (new)
- `backend/internal/backups/service.go` (-128 / +107)
- `backend/internal/backups/service_test.go` (+104 / -39)
- `backend/internal/backups/dispatcher_test.go` (+6 / -4)
- `backend/internal/backups/audit_dispatcher_test.go` (+8 / -20)
- `backend/internal/backups/schedule_test.go` (removed obsolete `TestDSNParse`)

### Lesson
The three silent v0.8.15 / v0.8.16 / v0.8.17
backup bugs were all caught by post-deploy
smoke tests on the prod server, never by
the `release.yml` workflow. The follow-up to
add a hard-gate `pg_dump --version` check
to `release.yml` is tracked separately
(post-v0.8.18).

## [0.8.17] - 2026-08-11

This is a **hotfix release** that closes the last
v0.8.15-silent-bug — the `/usr/bin/pg_dump`
symlink on `pg_wrapper` — that survived v0.8.16
because PR #224's live smoke test didn't run
`pg_dump --version` against the deployed image.
v0.8.16 deployed, the live smoke test caught
the bug, and PR #226 fixed it.

Single PR (#226), one fix:

### Fixed
- **Backups: replace `/usr/bin/pg_dump` with the
  real `pg_dump` ELF binary in the tooling
  stage.** PR #224 copied `/usr/bin/pg_dump`
  from the tooling stage (a symlink to
  `pg_wrapper` shell script) into the runtime.
  The wrapper requires `/bin/sh` + `dpkg-divert`
  and the postgresql-common runtime — none of
  which are in the distroless `base` image.
  Result on prod: `exec /usr/bin/pg_dump`
  returned `ENOENT` even though the symlink
  existed, and `docker exec aegis-panel
  /usr/bin/pg_dump --version` returned
  `no such file or directory`. v0.8.17
  removes the symlink in the tooling stage
  (`rm /usr/bin/pg_dump`) and copies the real
  binary from
  `/usr/lib/postgresql/15/bin/pg_dump` over
  it (`cp /usr/lib/postgresql/15/bin/pg_dump
  /usr/bin/pg_dump`). The runtime image just
  `COPY`s the file in — no shell or `ln`
  needed in the distroless runtime.

### Lesson (v0.8.15 → v0.8.16 → v0.8.17)

`release.yml` still has no hard-gate smoke test
that runs `pg_dump --version` against the new
image before publishing. The CHANGELOG lessons
in #225 + this PR are not enough. Track as a
follow-up to `release.yml` — separate PR,
post-v0.8.17.

### Verification
- All CI checks green on PR #226
- `docker exec aegis-panel /usr/bin/pg_dump --version` → `pg_dump (PostgreSQL) 15.18 (Debian 15.18-0+deb12u1)` (live, post-deploy)

### Operational
- **Smoke test on the live server.click after deploy:**
  - `docker exec aegis-panel /usr/bin/pg_dump --version` → `pg_dump (PostgreSQL) 15.18 ...`
  - `POST /api/v1/backups/` → 200 with `status="running"` then `"ok"`
  - `POST /api/v1/nodes/{id}/provision` with the Demo-нода's credentials → 200 with `state="online"`

**Closed by:** PR #226
**Tag:** `v0.8.17` (to be cut after this PR merges)

## [0.8.16] - 2026-08-11

This is a **hotfix release** that closes two v0.8.15
silent bugs that the v0.8.15 smoke test caught
(but the release workflow itself did not, because
the live smoke-test step that catches them was
only added in the deploy follow-up — see
"Lesson" below).

Single PR (#224), two coordinated fixes:

### Fixed
- **Backups: copy the real `pg_dump` binary into
  the panel image.** v0.8.15 PR #222 installed
  the `postgresql-client` metapackage in the
  tooling stage. That metapackage installs
  `postgresql-common`, which symlinks
  `/usr/bin/pg_dump` to
  `/usr/share/postgresql-common/pg_wrapper` — a
  shell script that requires `/bin/sh` +
  `dpkg-divert` + the postgresql-common runtime.
  The distroless `base` image has none of those,
  so `exec /usr/bin/pg_dump` returned `ENOENT`
  even though the symlink existed. v0.8.16
  switches to the versioned
  `postgresql-client-15` package (the one that
  matches Debian 12 "bookworm" — the bare
  `postgresql-client-16` metapackage does NOT
  exist on bookworm; only the actual binary
  under `/usr/lib/postgresql/15/bin/pg_dump`
  is needed in the runtime). The Dockerfile
  now `COPY --from=tooling
  /usr/lib/postgresql/15/bin/pg_dump` directly,
  plus the `/usr/lib/x86_64-linux-gnu` tree
  for the dynamic deps.
- **Provision: `joinHostPort` handles the case
  where the operator-saved `Address` already
  contains a port.** v0.8.15 PR #222's smoke
  test ran with the Demo-нода whose DB row has
  `Address = "cdn2ne.<prod-host>.click:22"`. The
  installer also reads the per-call `ssh_port`
  override (default 22). The old `joinHostPort`
  function did `fmt.Sprintf("%s:%d", host, port)`
  unconditionally, producing
  `"cdn2ne.<prod-host>.click:22:22"` (two colons).
  `dialer.DialContext("tcp", "cdn2ne.<prod-host>.click:22:22")`
  then failed with `no such host` at the
  `connect` stage of the install path. v0.8.16
  detects the existing port via
  `net.SplitHostPort`; if the operator saved a
  port in the address, the function returns
  the address verbatim. Conservative: the
  operator-supplied port wins over the
  function-arg port.

### Lesson (v0.8.15 → v0.8.16)

v0.8.15's `release.yml` ran cleanly and pushed
the image, but did not actually run a live
smoke test against the new image. The two
silent bugs above (wrapper-script `pg_dump` and
double-port `joinHostPort`) only surfaced during
the v0.8.15 deploy follow-up, after the release
was already published. Future hotfix releases
should include a "smoke test against the new
image" step in `release.yml` — either a
`workflow_dispatch` action that pulls the
image, runs a `docker exec` to confirm
`/usr/bin/pg_dump --version` returns a real
version (not `ENOENT`), and a curl smoke
against a `docker run`-bound panel, OR a
`release-on-merge` GitHub Actions step that
runs the same checks. Track as a follow-up
to `release.yml` — separate PR, post-v0.8.16.

### Verification
- `go build ./...` — clean
- `go vet ./...` — clean
- `go test -count=1 -tags=integration -run='^$' ./...` — clean
- `go test -count=1 ./internal/bootstrap/...` — passes
- All CI checks green on PR #224

### Operational
- **Smoke test on the live server.click after deploy:**
  - `POST /api/v1/backups/` should return 200 with
    `status="running"` then transition to `"ok"`.
  - `POST /api/v1/nodes/{id}/provision` with
    `ssh_user=root`, `ssh_password=<demo-pw>`,
    `tofu_policy=accept-and-append`,
    `expected_fingerprint=SHA256:<demo-fp>` should
    return 200 with `state="online"`.

### Not in this release
- v0.8.15 audit-3.1 fix chain (HttpOnly cookie +
  in-memory Pinia access token + Caddy CSP) —
  unchanged, already on prod.
- v0.8.15 dialog overflow + SelectItem empty
  value (PR #220 + #221) — unchanged, already
  on prod.
- v0.8.15 multi-stage Dockerfile (PR #222) —
  unchanged, already on prod. PR #224 is a
  follow-up to it, not a replacement.
- Backup scheduler cron + retention policy.
- Restore-drill on fresh VM (the single missing
  piece for the v1.0.0-mvp-soft-launch tag;
  see `docs/gap-analysis-v0.8.15.md`).

**Closed by:** PR #224
**Tag:** `v0.8.16` (to be cut after this PR merges)

## [0.8.15] - 2026-08-11

This release **closes two silent functional gaps** in the v0.8.14
panel image: backups that failed with `pg_dump not found` (because
the distroless/static runtime had no dynamic linker), and provision
requests that returned 502 with no detail in `docker logs` (because
the bootstrap handler's `writeError` only wrote the response body).

Single PR (#222), three coordinated changes:

### Fixed
- **Backups: bundle `pg_dump` in panel image.** v0.8.14 was built
  on `distroless/static-debian12:nonroot`, which has no dynamic
  linker. The backup service exec'd `/usr/bin/pg_dump` and the
  loader lookup failed with ENOENT — every row got `status=failed`,
  error `pg_dump not found at /usr/bin/pg_dump`. The new Dockerfile
  adds a `tooling` stage built on `debian:12-slim` that runs
  `apt-get install postgresql-client`, then copies `pg_dump` + the
  whole `/usr/lib/x86_64-linux-gnu` tree into a `distroless/base`
  runtime (the base variant ships glibc + the linker + busybox).
  Image size grows by ~50 MB (mostly the .so tree). Still 2x
  smaller than a `debian:12-slim` runtime.
- **Provision: bundle `aegis-agent` in panel image.** v0.8.14 was
  built with only the panel binary; the bootstrap installer reads
  `${AEGIS_AGENT_BINARY}` (default `./bin/aegis-agent`) and the
  installer's `os.Stat(in.AgentSource)` failed on the first install
  step (`upload-agent`). The new Dockerfile adds a second
  `go build` for `./cmd/aegis-agent` in the same build stage, copies
  the binary to `/app/bin/aegis-agent`, and sets
  `AEGIS_AGENT_BINARY=/app/bin/aegis-agent` as the runtime default.
- **Bootstrap `writeError` now logs.** `internal/bootstrap/handler.go`'s
  HTTP error path used to only write the JSON body, so install
  failures from `POST /api/v1/nodes/{id}/provision` were invisible
  in `docker logs aegis-panel`. Added a `log.Error().Int("status",...).Str("error",...).Msg(...)`
  line so every 4xx/5xx from this package lands in the structured
  log stream with the (status, msg) pair.

### Verification
- `go build ./...` — clean
- `go vet ./...` — clean
- `go test -count=1 -tags=integration -run='^$' ./...` — clean
  (compile-tagged stubs)
- `go test -count=1 ./internal/bootstrap/` — passes
- All 24/24 CI checks green on PR #222

### Operational
- **Smoke test on the live server.click after deploy:** `POST /api/v1/backups/`
  should return 200 with `status="running"` (then transition to
  `"ok"` once the dump completes). `POST /api/v1/nodes/{id}/provision`
  with a real Demo-нода should now log the per-stage install
  progress to `docker logs aegis-panel` (the `connect`, `upload-agent`,
  `write-env`, `install-unit`, `verify` stages).

### Not in this release
- v0.8.14 audit-3.1 fix chain (HttpOnly cookie + in-memory Pinia
  access token + Caddy CSP) — unchanged, already on prod.
- v0.8.14 dialog overflow + SelectItem empty value (PR #220 + #221)
  — unchanged, already on prod.
- Backup scheduler cron + retention policy (not yet
  implemented; backup is still manual via the UI or
  `POST /api/v1/backups/`).
- Restore-drill on fresh VM (this is the single missing
  piece for the MVP-1.0 soft-launch gate; see
  `docs/gap-analysis-v0.8.15.md`).

**Closed by:** PR #222
**Tag:** `v0.8.15` (to be cut after this PR merges)

## [0.8.14] - 2026-08-10

This is a **consolidation + security tightening release**
that closes the v0.8.13 backwards-compat shim for the
HttpOnly refresh-cookie storage. The v0.8.13 release
shipped the cookie in PR #214 + PR #215 + PR #216, but
kept the refresh token in the JSON body of
`/auth/login` and `/auth/refresh` for one release so
a pre-v0.8.13 client could still log in during the
upgrade window. v0.8.14 closes that window: from
this release forward, the refresh token is **only**
in the `Set-Cookie: aegis_rt=...` header. The body
field is gone, the body-fallback parse on
`/auth/refresh` is gone, and the openapi spec
documents the cookie as the only authoritative
channel.

The v0.8.13 → v0.8.14 upgrade is wire-format-clean
for a v0.8.13 frontend (the body field is removed
but the frontend never read it — PR #215 reads
the cookie via `withCredentials: true`). The
v0.8.14 frontend + v0.8.13 panel is the broken
combination (panel does not set the cookie); the
rolling-upgrade pattern is the standard "server
before client" sequence.

This release also adds the previously-undocumented
`POST /api/v1/auth/logout` endpoint to the openapi
spec. The endpoint was shipped in PR #214 (it was
required for the cookie-based logout flow) but the
openapi documentation lagged.

### Changed (auth — refresh token is cookie-only)

Closes the v0.8.13 backwards-compat shim. The
refresh token is now exclusively in the
`Set-Cookie: aegis_rt=...; HttpOnly; SameSite=Strict;
Path=/; Max-Age=2592000; Secure` header set by
`/auth/login` and `/auth/refresh`. The body field
that v0.8.13 emitted for one release is dropped.

- **`backend/internal/auth/handler.go`** — drop the
  `RefreshToken` field from the `loginResponse`
  struct (login + refresh). Drop the `refreshRequest`
  struct (only used in the body-fallback parse).
  Simplify `readRefreshToken` to cookie-only (no
  body parse, no `json.NewDecoder(r.Body).Decode`
  call). The 400-on-missing-cookie message is now
  `refresh token cookie is required` (was `cookie
  or body`). The `handleLogin` / `handleRefresh` /
  `handleLogout` docstrings are updated to reflect
  the v0.8.14 state. The `handleLogin` /
  `handleRefresh` response bodies carry only
  `access_token`, `token_type`, `expires_at`,
  and `scopes`.
- **`backend/internal/auth/cookie_test.go`** —
  `doLogin` reads the refresh from the cookie (the
  body field is gone, so the prior `resp.RefreshToken`
  read was a nil-deref waiting to happen). The
  body-fallback test is inverted to
  `TestHandleRefresh_BodyIsNotRead`: a body-only
  request gets 400, the response MUST NOT set a
  `aegis_rt` cookie (a body-derived cookie would
  be the regression). The
  `TestHandleRefresh_RefreshFailure_ClearsCookie`
  test now uses a bogus cookie (no body) — the
  server clears it on 401. Drop the now-unused
  `encoding/json` import.
- **`docs/openapi.yaml`** — remove the `RefreshRequest`
  schema (no body shape any more), remove the
  `refresh_token` property from the `LoginResponse`
  schema + drop it from the `required` list, add
  `Set-Cookie` response headers to `/auth/login`
  200 and `/auth/refresh` 200, document the
  `POST /auth/logout` endpoint that PR #214
  shipped but did not document.
- **`frontend/src/api/services/auth.ts`** — drop
  the `refreshToken` field from the `LoginResponse`
  TS interface. Update the `logout()` comment to
  reflect the v0.8.14 state (body-fallback closed).
- **`frontend/src/stores/auth.ts`** — update the
  `login()` comment that explained the v0.8.13
  shim to "closed in v0.8.14".
- **`frontend/src/api/client.ts`** — update the
  response-interceptor comment about
  `refresh_token` (gone from the camelized
  response shape in v0.8.14).
- **`frontend/src/types/api.d.ts`** — regenerated
  by `npm run codegen`. The `/auth/logout`
  operation is now in the `operations` map;
  `LoginResponse` no longer has `refresh_token`;
  `RefreshRequest` is removed.

- **Migration notes for operators**: v0.8.14 is a
  **drop-in replacement for v0.8.13** on the
  server side. The rolling-upgrade pattern is
  the standard "server before client":
  1. Upgrade the panel image to v0.8.14 first.
     A v0.8.13 frontend continues to work
     unchanged (it doesn't read the body field,
     and it sends no body to `/auth/refresh`).
  2. Upgrade the UI image to v0.8.14+ to drop
     the `refreshToken` type from the generated
     `LoginResponse`. A v0.8.14 frontend + a
     v0.8.13 panel is the broken combination
     (the panel would not set the cookie, and
     the frontend's 401-refresh-retry path would
     see no cookie).
  After the rolling upgrade, the wire format
  is unambiguous: `POST /auth/login` and
  `POST /auth/refresh` responses carry only the
  access token in the body, the refresh token
  is in the `Set-Cookie: aegis_rt=...` header.

## [0.8.13] - 2026-08-10

This is a **feature release** that closes the
v0.8.x-bucket "inbound-templates work" row. 5 PRs
shipped: **PR #205** (data-model + service +
handler foundation), **PR #209** (docs sync),
**PR #210** (sing-box renderer integration),
**PR #211** (inbounds service validation),
**PR #212** (frontend UI). Adds a per-tenant
`Params` defaults layer: an operator can define a
named, reusable protocol configuration once and
assign it to any number of `inbounds` rows on any
node. The sing-box renderer reads `template.params`
instead of the inbound's inline `params` when
`inbound.template_id` is set, so the operator does
not paste the same JSON into every inbound.

This is the first release where a single feature
landed in 5 separate PRs in a planned sequence
(foundation → docs sync → renderer → validation →
frontend), each PR independently reviewable,
mergeable, and revertable. The data-model + UI
are both backwards-compatible: every existing
inbound has `template_id = NULL` and continues to
use its inline `params` (the v0.8.0-v0.8.12 path
is the default; the template path is opt-in per
inbound).

### Added (inbound-templates — data-model + service + handler + renderer + validation + UI)

Closes the v0.8.x-bucket "inbound-templates
work" entry end-to-end. The 5-PR sequence:

- **PR #205 (foundation)** — migration 0021
  plus `internal/inboundtemplates/` package
  plus inbounds `TemplateID` field plus wiring
  (data-model plus service plus handler). Storage
  backend is shared with Inbounds (no new
  `AEGIS_*_BACKEND` env var). 8 unit tests + 4
  pg integration tests in the new package.
- **PR #210 (renderer)** — sing-box
  `BuildCoreConfigForNode` reads
  `template.params` when `inbound.template_id`
  is set. New `LookupTemplatesByID` interface
  in `internal/cores/builder/builder.go`; new
  `GetManyByID` on `inboundtemplates.Store` (a
  single `WHERE id = ANY($1)` batch query).
  One DB round-trip per flush (per-flush, not
  per-inbound — matches the v0.8.10 per-user
  credential filter pattern). Per-inbound
  fallback to inline `params` on a stale
  `TemplateID` (template deleted between
  flushes) or a lookup error. 6 new builder
  tests.
- **PR #211 (validation)** — inbounds service
  rejects an inbound whose `TemplateID` points
  at a template of a different `protocol`.
  New `WithTemplates` setter on `inbounds.Service`
  (nil-safe — the v0.8.0-v0.8.12 contract is
  preserved when no templates service is wired,
  e.g. in unit tests that don't care about
  templates). New `validateTemplateID` helper
  returns `ValidationError` with `field=templateId`
  on missing template, on `&uuid.Nil{}`, or on
  protocol mismatch. 10 new service tests
  (5 Create + 4 Update + 1 multi-field Update).
- **PR #212 (frontend)** — new
  `InboundTemplatesView` page (list + create +
  edit + delete, mirroring `PlansView.vue`
  shape) + "Template" dropdown in
  `InboundsView`'s create + edit forms. The
  dropdown is protocol-filtered (the UI
  pre-filter for the PR #211 contract — a
  mismatched protocol cannot be picked in the
  first place, but the backend validation
  remains the authoritative 400-path). i18n
  (en + ru): `nav.inboundTemplates` + 22-key
  `inboundTemplates` section. New
  `inboundtemplates.ts` API service + zod
  schema + openapi.yaml + regenerated
  `api.d.ts`. 14 files, +1794/-3.
- **PR #209 (docs sync)** — CHANGELOG
  `[Unreleased]`, `KNOWN_LIMITATIONS.md`
  (Inbound-templates entry moves from "open"
  to "partially shipped in v0.8.13+ (PR #205
  foundation)"), `README.md` (root) v0.8.12 +
  v0.8.x inbound-templates note, `ROADMAP.md`
  v0.8.x row, `docs/README.md` status table,
  `docs/operator-guide.md` `:0.8.9` → `:0.8.12`,
  `quickstart.md`, `SECURITY.md` supported
  versions. The follow-up PR #212's docs are
  closed in this release's entry (the
  `KNOWN_LIMITATIONS.md` Inbound-templates
  entry will be re-closed in the v0.8.13
  follow-up docs-sync PR if any are needed;
  v0.8.13 ships with the inbound-templates
  feature still marked "partially shipped" in
  KNOWN_LIMITATIONS pending the v0.8.13-followup
  docs-sync PR).

- **Migration `0021_inbound_templates.sql`** —
  new `inbound_templates` table (`id`, `name`,
  `protocol`, `params` JSONB, `description`,
  `created_at`, `updated_at`; `UNIQUE(name)`,
  `CHECK(protocol IN ('vless', 'hysteria2',
  'shadowsocks', 'trojan'))`) + a new nullable
  FK column
  `inbounds.template_id REFERENCES
  inbound_templates(id) ON DELETE SET NULL` +
  `inbound_templates_protocol_idx` +
  `inbounds_template_id_idx`. The migration is
  backwards-compatible: every existing inbound
  has `template_id = NULL` and continues to use
  its inline `params`.

- **New `internal/inboundtemplates/` package** —
  mirrors the `inbounds` package's shape. Public
  surface: `Service` (CRUD + audit + webhooks;
  `ScopeNodes`-guarded HTTP handler), `Store`
  interface (MemoryStore + PgStore),
  `InboundTemplate` model with
  `Params map[string]any` plus `Protocol` closed
  set matching the migration's CHECK. The handler
  is mounted at `/api/v1/inbound-templates` with
  5 paths (GET /, POST /, GET /{id}, PUT /{id},
  DELETE /{id}). 8 unit tests + 4 pg integration
  tests. The new `GetManyByID` method (PR #210)
  is a single `WHERE id = ANY($1)` batch query;
  the renderer is the primary consumer.

- **Inbounds model update** — the `Inbound`
  model + `CreateInput` + `UpdateInput` + the
  JSON request shapes gain `TemplateID
  *uuid.UUID`. PR #211 closes the protocol-match
  contract: the validation fires at Create (when
  `in.TemplateID != nil`) and at Update (when
  `*in.TemplateID != uuid.Nil`, BEFORE the
  assignment to `existing.TemplateID`). The
  inline `inbound.params` is kept for backwards
  compat — existing inbounds without a
  `template_id` continue using the v0.8.0-v0.8.12
  path; the renderer's nil-`templateSrc` branch
  also keeps the v0.8.0-v0.8.12 default.

- **Webhooks** — 3 new event types
  `inbound_template.{created, updated, deleted}`
  added to `AllowedEventTypes`. Existing webhook
  subscriptions are unaffected.

- **Wiring** — `internal/app/app.go` +
  `internal/router/router.go` + tests wire the
  new service into the App + the
  `/api/v1/inbound-templates` mount + the
  `ScopeNodes` guard. The v0.8.13+ wiring
  additionally calls `a.Inbounds.WithTemplates(
  a.InboundTemplates)` after both services are
  constructed (the PR #211 nil-safe setter
  pattern). Storage backend is shared with
  Inbounds (no new env var; the templates
  feature flips on/off with the operator's
  existing `AEGIS_INBOUNDS_BACKEND`).

- **Frontend `InboundTemplatesView` page** —
  new lazy-loaded `/inbound-templates` route
  mounted under the existing `AppLayout` (the
  `LayoutTemplate` icon from `lucide-vue-next`,
  nav entry between "Inbounds" and "Hosts").
  The view is a DataTable with name (link) +
  protocol (Badge) + description (truncated
  hint) + updatedAt + actions menu. Three
  dialogs: Create (name + protocol Select +
  description Textarea + params Textarea with
  JSON validation on submit), Edit (same shape,
  pre-filled from the row; partial PATCH
  semantics — only fields that changed are
  sent; an empty patch surfaces a "no changes
  to save" toast), Delete (confirm dialog with
  the affected-inbounds fallback message).
  Search box filters on name + protocol +
  description.

- **Frontend Template dropdown in InboundsView**
  — both Create and Edit dialogs gain a
  "Template" Select between `protocol` and
  `listen`. The Select options are filtered to
  the currently-selected protocol via
  `templatesForProtocol(protocol)` (the UI
  pre-filter for the PR #211 protocol-match
  contract; a mismatched protocol that slips
  through — e.g. the operator changes `protocol`
  after picking a template — is still rejected
  by the backend, just with a worse error
  message). The empty option is "No template
  (use inline params)" — the v0.8.0-v0.8.12
  default. PATCH follows the absent-key
  contract: the dropdown's value is sent only
  when non-empty (omit = no change).

- **Migration notes for operators** — no
  operator action required at the migration
  step (the column is `NULL`-able, every existing
  inbound keeps its inline `params`). The new
  endpoint is optional — operators can keep
  using the v0.8.0-v0.8.12 inline-params path
  indefinitely. The 3 new webhook event types
  are listed in `AllowedEventTypes`; existing
  webhook subscriptions are unaffected. No new
  env vars. No new `AEGIS_*_BACKEND` config. The
  v0.8.6+ hard guard on memory backends will
  refuse to apply this migration in production
  mode with `AEGIS_INBOUNDS_BACKEND=memory` —
  the same protection that guards every
  v0.8.x migration. After upgrading the panel
  image, the templates feature is live
  immediately (the migration is idempotent; the
  FK is `ON DELETE SET NULL` so deleting a
  template drops the FK to NULL on referencing
  inbounds, which fall back to inline `params`).

- **Per-protocol schema enforcement** — sing-box
  remains the authoritative per-protocol schema
  validator. The panel stores `params` as
  opaque JSONB; the templates feature does not
  duplicate the renderer's per-protocol field
  validation. A future v0.8.x+ PR may add a
  per-protocol JSON Schema for the panel's
  params editor (mirroring sing-box's
  `protocol configuration` block schema); v0.8.13
  ships with the generic JSON textarea the
  v0.2.0 era added.

## [0.8.12] - 2026-08-10

This is a **consolidation release** that closes the
3-PR gap between `v0.8.11` (`ae9eefa0`) and `main`
(`7a79213`). It contains two UX improvements (PR #201
merged "Add node + Provision" dialog, PR #202 shadcn-vue
`RadioGroup` primitive + NodesView auth-method migration)
and one docs-sync follow-up (PR #203). PR #200 (the
v0.8.11+ lint cleanup) is a chore with no user-visible
surface change and is not enumerated here. No new
backend API surface, no new OpenAPI, no new env vars,
no new schema migrations. The on-disk prod is unchanged
from the v0.8.9 deploy; the v0.8.12 tag is the canonical
reference for any post-2026-08-10 hotfix branch and the
cleanest snapshot for the v0.9.0 fresh-VM smoke test.

### Added (merged "Add node + Provision" dialog)

Closes the v0.8.x-bucket UX follow-up "merged
'Add node + Provision' dialog". The previous
flow required two separate operator steps:
(1) click "Add node" → fill the form → register
the node in `new` state; (2) click "Provision"
on the new row → fill the SSH credentials →
install the agent. The v0.8.12 dialog combines
both into a single form with a "Provision this
node after registering" checkbox (default on).
When checked, the form reveals the auth-method
radio (key / password) + key / password /
ssh_user / ssh_port / tofu_policy /
expected_fingerprint fields; the submit handler
calls `createNode` then `provisionNode` in
sequence. When unchecked, only `createNode` is
called (the v0.8.11 behaviour). The per-row
"Provision" dropdown entry stays for
re-provisioning offline nodes (it still has the
three-way radio including the "Stored panel key"
option, which is disabled for first-time
installs).

- **`internal/cores/builder/nodeAddSchema`** — new merged schema. Extends `nodeCreateSchema` with the v0.8.x provision fields + a `provisionNow` discriminator. When `provisionNow` is `true`, the `superRefine` enforces the same XOR + conditional-required + tofu_policy rules as `nodeProvisionSchema`. The `stored` auth method is rejected at the schema level for first-time installs (the panel has no key on file yet).

- **`frontend/src/views/NodesView.vue`** — the create dialog now uses `nodeAddSchema` (via `createForm`) instead of `nodeCreateSchema`. The submit handler does the create first; on success, if `provisionNow` is `true`, the handler calls `provisionNode` with the wire payload built from the auth method. If the second call fails, the handler surfaces a non-fatal toast ("Node registered, but provisioning failed — retry from row menu"); the form closes either way (the operator can re-provision from the row's Provision entry).

- **i18n** (en + ru): 9 new keys — `provisionAfterCreate`, `provisionAfterCreateHint`, `registerAndProvision`, `registerOnly`, `createdAndProvisioned`, `createdProvisionFailed`, `createdProvisionFailedHint`, plus 2 more for clarity. The existing `createTitle` / `createDescription` / `created` / `createFailed` keys are unchanged (the dialog still uses them for the basic-create path).

- **Docs**: `docs/ROADMAP.md` v0.8.x row updated to mark "merged 'Add node + Provision' dialog" shipped. `KNOWN_LIMITATIONS.md` "v0.8.x UX follow-ups — merged 'Add node + Provision' dialog" entry closed (with the migration note for operators). `CHANGELOG.md` (this entry).

- **Migration notes for operators**: no backend changes, no schema migration, no new env vars. The two existing API endpoints (`POST /api/v1/nodes` and `POST /api/v1/nodes/{id}/provision`) are unchanged; the merged dialog is a UX-layer composition. Operators who preferred the two-step flow can uncheck the "Provision after registering" checkbox to keep the v0.8.11 behaviour.

### Added (shadcn-vue RadioGroup primitive + migrate NodesView auth-method radios)

Closes the v0.8.x-bucket UX follow-up "shadcn-vue
`RadioGroup` primitive". The two hand-rolled radio
groups in `NodesView.vue` (the three-way auth-method
picker in the per-row provision dialog, plus the
two-way picker in the merged "Add node + Provision"
dialog) are now rendered through new shadcn-vue
primitives. The previously hand-rolled
`.nodes__auth-radios*` CSS block (~46 lines) is
deleted; visual parity preserved via the standard
`bg-muted` + `border-ring` data-attribute pattern.

- **`frontend/src/components/ui/RadioGroup.vue`** — thin wrapper over `radix-vue`'s `RadioGroupRoot`. Forwards `modelValue` (the active item's value), `defaultValue`, `disabled`, `required`, `orientation`, `dir`, `loop`, `name`. Emits `update:modelValue` with the new value. Default slot is the items. `class` prop is `string | boolean | undefined` so the `hasError && '...'` idiom from `FormField` slots typechecks.

- **`frontend/src/components/ui/RadioGroupItem.vue`** — thin wrapper over `radix-vue`'s `RadioGroupItem` + `RadioGroupIndicator`. Renders a `<button role="radio">` with a 16x16 circular border + a 10x10 inner dot (mounted via `RadioGroupIndicator` so it only shows when checked). Forwards `value`, `disabled`, `required`, `class`. Default slot is the visible label (text + optional icon). Standard shadcn-vue `cn()` class composition; the active-item highlight + disabled-state visuals are data-attribute selectors (`data-[state=checked]:border-ring data-[state=checked]:bg-muted`, `data-[disabled]:cursor-not-allowed data-[disabled]:opacity-50`).

- **`frontend/src/views/NodesView.vue`** — the two `<input type="radio">` groups (in the create dialog's auth-method field and the per-row provision dialog's auth-method field) are replaced with `<RadioGroup>` + `<RadioGroupItem>`. The reset-other-field side effect (clearing `ssh_private_key` + `ssh_password` when the auth method changes) moves from each input's `@change` handler to the RadioGroup's `@update:modelValue` handler. The `disabled` state on the "Stored panel key" option (only enabled when `provisioning?.state === 'offline'`) is now a native `data-[disabled]` styling hook instead of a hand-rolled class. Net: -110 lines (154 deletions, 44 insertions across the template; 46 lines of dead `.nodes__auth-radios*` CSS removed).

- **UX impact**: the radios now support **arrow-key + Space keyboard navigation** within the group (radix-vue default — the hand-rolled radios had Tab-only navigation). ARIA group semantics are correct by construction: `role="radiogroup"`, `role="radio"`, `aria-checked`, `aria-disabled` are all wired by `radix-vue`. No visual diff for the operator; active-item highlight + disabled-state visuals are preserved 1-в-1.

- **Migration notes for operators**: no backend changes, no schema migration, no new env vars. The new components are pure UI primitives; the wire format (`POST /api/v1/nodes` + `POST /api/v1/nodes/{id}/provision`) is unchanged. The auth-method reset side effect (clearing the other auth field when switching methods) is preserved.

### Documentation (sync to v0.8.12)

PR #203 closes the docs drift between the
v0.8.12 codebase and the user-facing
documentation that PR #201 (merged
Add+Provision dialog) and PR #202 (shadcn-vue
RadioGroup primitive) would otherwise leave
behind. No code changes — `.md` only. Touches:

- **`KNOWN_LIMITATIONS.md`** — "shadcn-vue
  RadioGroup primitive — v0.8.x" entry closed
  with the migration note (the new
  `components/ui/RadioGroup.vue` +
  `RadioGroupItem.vue` primitives carry
  arrow-key + Space keyboard navigation,
  ARIA group semantics, and the standard
  `data-[state=checked]` / `data-[disabled]`
  styling hooks).

- **`docs/ROADMAP.md`** — v0.8.x row updated:
  PR #202 added to the shipped list, removed
  from the open list. The "inbound-templates
  work" row is now the only v0.8.x bucket
  item still open.

- **`README.md`** (root) — v0.8.x milestone
  row updated to reflect the primitive
  shipped. The trailing "; shadcn-vue
  `RadioGroup` primitive" line is removed.

- **`docs/README.md`** — status table row
  "⏳ v0.8.x+" → "✅ shipped (PR #202)".

- **`CHANGELOG.md`** (this entry).

## [0.8.11] - 2026-08-10

This is a **consolidation release** that closes the
3-PR gap between `v0.8.10` (`ae41e59`) and `main`
(`4c85a13`). It contains one security gap closure
(PR #198, the per-user credential filter — the
last remaining high-severity security gap from
the deep-state analysis, required before the
v1.0.0 GA tag) and two cosmetic frontend batches
(PR #196 frontend-deps, PR #197 Tailwind v4
migration). No new backend API surface, no new
OpenAPI, no new env vars, no new schema migrations.
The on-disk prod is unchanged from the v0.8.9
deploy; the v0.8.11 tag is the canonical reference
for any post-2026-08-10 hotfix branch and the
clean snapshot for the v0.9.0 fresh-VM smoke test.

### Added (per-user credential filter in the Builder)

Closes the second half of the v0.7.x Phase 2
multi-user TODO (the first half — the
BatchedApplier fan-out narrowing by host — was
v0.8.x PR #192). Without this filter, the
Builder's per-inbound credential list carried
every per-(user, inbound) row regardless of the
user's host allow/block set, leaking users across
nodes. The v0.8.10+ behavior: a non-empty
allow-set is resolved per `BuildCoreConfigForNode`
invocation (one DB round-trip per node flush, not
per inbound), the per-inbound credential list is
filtered by `Credential.UserID ∈ allow-set`, and
the rendered `users: [...]` array only carries
the allowed users.

- **`internal/users.Service.AllowedUsersForNode(ctx, nodeID) ([]uuid.UUID, error)`** — the reverse-direction read of the v0.8.x `enqueueUserDelta` fan-out: which user IDs the host-allow/block filter admits for `nodeID`, instead of which node IDs the user is on. Uses `StatusActive` as the user-status filter (skips deleted/expired/disabled). Blocklist wins over allowlist (matching `enqueueUserDelta`). A nil `s.hosts` with a non-empty allow/block list yields an empty allow-set (fail-closed, matching the v0.8.x `enqueueUserDelta` pattern).

- **`internal/cores/builder.ListUsersAllowedForNode` interface + `BuildCoreConfigForNode` filter** — the Builder calls the source once per build (one DB round-trip per node flush) and filters every per-inbound credential list. A nil source / nil result / lookup error keeps the v0.8.0-v0.8.9 default-allow contract (every credential passes). A non-empty result is the allow-set: a credential whose `UserID` is not in the set is dropped. A returned empty allow-set (the lookup succeeded with []) drops every credential for that node — the fail-closed "no users allowed" semantic.

- **`internal/cores/builder.NewFlushFn` — new `usersSrc` argument** between `credSrc` and `renderer`. nil skips the per-user filter, the v0.8.0-v0.8.9 default-allow contract. Wired in `cmd/aegis/main.go` to pass `a.Users`.

- **Tests**: 3 unit tests in `internal/users/service_test.go` (`TestService_AllowedUsersForNode` for the mixed-filter shape, `TestService_AllowedUsersForNode_NilHosts` for the fail-closed nil-lookup case, `TestService_AllowedUsersForNode_DefaultAllow` for the no-filter case). 5 new tests in `internal/cores/builder/builder_test.go` (`TestBuildCoreConfigForNode_PerUserFilter_FullAllow`, `PartialAllow`, `EmptyAllow` for the fail-closed sentinel, `NilUsers` for the v0.8.0-v0.8.9 contract, `LookupError` for the fail-soft log+default-allow). 8 existing `BuildCoreConfigForNode` tests updated for the new 6-arg signature.

- **Wiring**: `cmd/aegis/main.go` passes `a.Users` to `builder.NewFlushFn`. The existing `WithHosts(a.Hosts)` chain stays — the per-user allow-set resolver depends on the host→node expansion to evaluate `User.HostsAllowlist` / `HostsBlocklist`.

- **Migration notes for operators**: no schema migration. No new env vars. No new `AEGIS_*_BACKEND` config. The `AEGIS_WEBHOOKS_SECRET_AGE_*` envelope and the `AEGIS_*_BACKEND=pg` set from the v0.8.9 production deploy are sufficient. Operators who have populated `User.HostsAllowlist` with host IDs see the per-user filter activate immediately on the next BatchedApplier flush; operators who have not populated the fields see no behavioral change (the v0.8.0-v0.8.9 default-allow contract is preserved when `s.hosts` is wired but every user has empty allow/block lists).

- **Docs**: `KNOWN_LIMITATIONS.md` "The per-credential Builder-side filter" entry closed (with the migration note for operators). `ROADMAP.md` `v0.8.x` row updated to mark "per-user credential filter in Builder" shipped. `docs/SECURITY.md` "Not designed to defend against" section updated: the per-user cross-node leak is now closed. `CHANGELOG.md` (this entry).

### Added (frontend-deps: @vueuse/core 11→14, vite 7→8, jsdom 25→30)

Lockfile-only batch. No `src/` code changes. The
three packages were pinned in `frontend/package.json`
3+ major versions behind current; the bump brings
them to the current `^X.Y.Z` line. `npm install` ran
clean (added 18, removed 17, changed 22 packages
in 22s); pre-pr.sh 10/10 ✓ including
`frontend build` (vite 8.2.1 + rolldown, 19.9s) and
`frontend type-check` (vue-tsc 3.3.8 ✓). 24/24 CI ✓.
**Key observation** (durable lesson): `@vueuse/core`
is declared in `package.json` but **never imported
in `src/`** — grep returned 0 hits. Three major
versions went by without any side effects because
nothing calls it. Long-term decision deferred
(either start using it for storage / debounce /
etc., or remove from deps).

### Added (Tailwind v4 migration — CSS-first config + @tailwindcss/vite)

Frontend-only batch. Drops `tailwindcss@3.4`,
`tailwindcss-animate@1.0`, `autoprefixer@10.5`,
`postcss@8.5.25`, `postcss.config.js`, and
`tailwind.config.ts` (117 lines). Adds
`tailwindcss@4.3.3` and `@tailwindcss/vite@4.3.3`.
Keeps `@tailwindcss/forms@0.5.11` and
`@tailwindcss/typography@0.5.20` (their 0.5.x
peer is `>=4.0.0` — verified via `npm view <pkg>
peerDependencies`). `styles.css` is now
CSS-first via `@import "tailwindcss"`,
`@plugin "@tailwindcss/forms"`, `@plugin
"@tailwindcss/typography"`, `@custom-variant dark
(&:where(.dark, .dark *))`, `@theme { --color-*,
--radius-*, --font-*, --animate-accordion-* }`,
inline `@keyframes accordion-down/up`
(replaces `tailwindcss-animate`). HSL custom
properties in `:root` and `.dark` preserved
1-в-1 (shadcn-vue convention, no visual diff
for users). Container utility dropped from
config (grep чист, not used in `src/`). Pipeline
shift: from PostCSS (`postcss.config.js` +
`autoprefixer`) to `@tailwindcss/vite` plugin
in `vite.config.ts`. Pre-pr.sh 10/10 ✓ (build 17s,
vite 8.2.1 + rolldown, CSS bundle 52.63kB
gzip 9.22kB). 24/24 CI ✓.

## [0.8.10] - 2026-08-09

This is a **consolidation release** that closes the 3-PR gap
between `v0.8.9` (`035c77e5`) and `main` (`9e8579b`). It
contains no new backend, OpenAPI, env, or schema changes —
all three PRs since v0.8.9 are either UI-only or docs-only.
The on-disk deploy is unchanged from the 2026-08-09 v0.8.9
production deploy; the v0.8.10 tag is the canonical
reference for any post-2026-08-09 hotfix branch.

### Added (UI: subscription URL display in UsersView)

The v0.8.x operator UX gap — "the admin has no way to get a
user's subscription URL out of the panel UI without
manually concatenating `https://<host>/<sub_path>/sub/<token>`"
— is closed. A new **Show subscription URL** DropdownMenu item
on each user row opens a dialog with the full URL (read-only
textarea), a **Copy URL** button, an **Open** button (new tab,
`noopener`), and a format selector + **Preview** button that
renders the sing-box / clash / base64 / HTML payload via the
existing `GET /api/v1/sub/{token}` endpoint. The URL is
built from `window.location.origin` + the active sub_path
(from `GET /api/v1/panelcfg/`, re-fetched on every dialog
open so a recent sub_path rotation is picked up without a
page reload) + the user's `subToken`.

- **New dialog state in `UsersView.vue`**: `subUrlView`
  carries `{user, url, preview, previewFormat, previewing,
  previewError}`; `subUrlFormat` is the dialog's persistent
  format selector (defaults to `sing-box`).
- **Helpers**: `loadPanelPath()` (re-fetches the active
  `sub_path` via `getActivePanelPath()`), `buildSubUrl(user)`
  (constructs `${origin}${subPathPrefix}/sub/${subToken}`),
  `openSubscriptionUrl(user)`, `closeSubscriptionUrl()`,
  `refreshSubscriptionUrl()` (rebuilds the URL after a
  sub_path rotation), `previewSubscription()` (calls
  `fetchSubscription(subToken, subUrlFormat)`).
- **Existing create / rotate modal extended**: the post-create
  / post-rotate dialog now also shows the full URL (read-only
  textarea) + a new **Copy URL** button alongside the
  existing **Copy** (raw token) button. The raw token stays
  primary (the existing "shown only once" contract).
- **Dead code removed**: the unused `fetchSubscriptionForUser`
  helper in `frontend/src/api/services/subscription.ts` (it
  called a non-existent `GET /api/v1/users/{id}/sub` endpoint)
  is deleted; the per-user preview path now uses the existing
  `/api/v1/sub/{token}` route with the per-user `subToken`.
- **i18n**: new `users.showSubscriptionUrl`,
  `users.subscriptionUrlLabel`, `users.subscriptionUrlTitle`,
  `users.subscriptionUrlDescription`,
  `users.subscriptionUrlLoadFailed`,
  `users.subscriptionPreviewFailed`, `users.copyUrl`,
  `users.openUrl`, `users.refresh`, `users.preview` keys in
  both `en.json` and `ru.json`.
- **No backend change**. No `openapi.yaml` bump. No new env
  vars. The two endpoints touched (`/api/v1/sub/{token}`,
  `/api/v1/panelcfg/`) are both pre-existing.

### Added (host→node mapping in hosts / builder / users)

- **`internal/hosts` — `HostsForInbound` + `NodesForHost` lookups** (v0.8.x host→node mapping; the prerequisite for outbound group rendering per `docs/comparison/remnawave.md:319`). The MemoryStore scans its `byID` map (the in-memory test path); the PgStore runs a `SELECT host_id FROM host_endpoints WHERE node_id=$1 AND inbound_id=$2 LIMIT 1` (for `HostsForInbound`) and a `SELECT DISTINCT node_id FROM host_endpoints WHERE host_id=$1` (for `NodesForHost`). Both have full unit + integration test coverage.

- **`internal/cores/builder` — `LookupHostForInbound` interface + `BuildCoreConfigForNode` populates `InboundSpec.HostID`**. The field was always `""` (see the `builder.go:32-41` TODO and the `HostID: ""` line that the new code replaces). With the new lookup, every enabled inbound in the rendered `CoreConfig` carries the host id it belongs to (or `""` for an un-provisioned inbound). The sing-box renderer can use the field for outbound group rendering; the future per-user credential filter at the Builder level keys off it.

- **`internal/cores/builder.NewFlushFn` — new `hostSrc` argument** between `inbSrc` and `credSrc`. A nil lookup preserves the v0.8.0-v0.8.7 behaviour (HostID = ""). Wired in `cmd/aegis/main.go` to pass `a.Hosts`.

- **`internal/users` — `WithHosts` setter + `HostNodesLookup` interface** (the v0.8.x dependency on the hosts service for the user fan-out). The new `expandHostsToNodes` helper turns `User.HostsAllowlist` / `HostsBlocklist` (host IDs, per the architecture) into the node IDs the BatchedApplier map keys on. The `enqueueUserDelta` v0.7.x misimplementation (treating the field as node IDs) is fixed; the v0.8.x fan-out matches the architecture.

- **Fail-closed semantic for the user fan-out**: a non-empty `User.HostsAllowlist` with a nil `s.hosts` lookup yields an empty fan-out (warning log). The alternative (fail-open to "all nodes" when the lookup is missing) would silently grant access on a misconfigured v0.8.x install. The fail-closed behaviour is what the architecture intends.

- **Tests**: 5 new `TestEnqueueUserDelta_*` cases (`MultiHost`, `NilHosts_NonEmptyField_FailsClosed`, `UnknownHostInAllowlist_FailsClosed`, the existing tests updated to the host-ID semantic). 4 new `TestMemoryStore_*` cases + 5 new `TestPgStore_*` integration cases for the new methods. The `internal/cores/builder/flushfn_smoke_test.go` and `flushfn_integration_test.go` callers updated for the new `NewFlushFn` signature.

- **Wiring**: `internal/app/app.go` calls `a.Users.WithHosts(a.Hosts)` right after `users.NewService`. `cmd/aegis/main.go` passes `a.Hosts` to `builder.NewFlushFn`.

- **Docs**: `KNOWN_LIMITATIONS.md` "Host → node mapping in the Builder-side filter" entry closed (with the migration note for operators). `ROADMAP.md` `v0.8.x` row updated. `CHANGELOG.md` (this entry).

### Migration notes for operators

- `User.HostsAllowlist` / `HostsBlocklist` UUIDs are now **host IDs**, not node IDs. A panel upgrading from v0.7.x to v0.8.x where these fields held node IDs will see an empty fan-out for affected users until the values are re-populated with host IDs. The `expandHostsToNodes` helper's warning log (`user fan-out is empty (fail-closed)`) is the operational signal.
- No schema migration. The `host_endpoints` table is unchanged; the new methods are pure lookups on existing rows.
- No new env vars. The `AEGIS_WEBHOOKS_SECRET_AGE_*` envelope and the `AEGIS_*_BACKEND=pg` env set from v0.8.9 production deploy are sufficient.

### Added (docs-only: sops+age deploy runbook + distroless UID 65532 gotcha)

- **`docs/RUNBOOKS/deploy.md` §6 rewritten** to
  reflect the actual sops+age deploy workflow proven
  on 2026-08-09 (v0.8.0 → v0.8.9 production upgrade).
  The previous §6 ended with "the panel binary reads
  sops-decrypted env at boot" — no `cmd/aegis/main.go`
  code path does this in v0.8.x. The new §6 splits the
  workflow into 6 explicit steps (1.x: definitions;
  6.1-6.2: keygen + install on server; 6.3-6.4: build
  the env, 6.5: decrypt-on-operator at
  deploy time, with the full `SOPS_AGE_KEY_FILE=…
  sops --config … -d` command + a `docker run -e`
  flag builder; 6.6: future work, sops-decrypt in
  the panel binary).
- **Distroless nonroot UID 65532 ownership gotcha
  documented in §6.2**. The panel container runs
  as UID 65532, so `age.key` on the host must be
  `chown 65532:65532 && chmod 0640` (not 0600 root,
  which 65532 cannot read). Without this fix the
  panel boot-loops on a fatal error in
  `internal/app/app.go:303-304` (the webhooks
  envelope build). This was the canonical reference
  for the 2026-08-08 deploy incident class.
- **Canonical env file shape (§6.3)** — the previous
  §6.3 had YAML examples with `AEGIS_WEBHOOKS_SECRET_KEY_FILE`
  and `AEGIS_WEBHOOKS_CREDENTIALS_*` env vars that
  do not exist in `internal/config/config.go`. The
  actual env uses dotenv format with the single
  `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE` +
  `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` envelope
  (shared by webhooks / nodes.stored-key / bootstrap
  / CLI admin_node rotate-panel-key) + all 11
  `AEGIS_*_BACKEND` set to `pg` for production.
- **Decrypt-on-operator pattern (§6.5)** — the
  `sops -d` invocation lives on the operator, not
  the server. The server-side `docker run` carries
  the plaintext env over the SSH channel once. The
  at-rest storage on the server is always the
  encrypted file (`/etc/aegis/aegis-env.enc.env`,
  chmod 0600 root).
- **`KNOWN_LIMITATIONS.md`** — the §6 gaps above
  are recorded as a `closed in this PR` entry under
  "Operations polish (deferred from v0.5.0 / v0.7.0)",
  with the boot-loop log snippet + the canonical
  `chown 65532:65532` fix.

### Documentation (sync to v0.8.9)

PR #194 closes a long-standing docs drift between the
v0.8.9 codebase and the user-facing documentation. No
new feature, no env var, no schema migration — this is
a docs-only consolidation so the repo tells a consistent
story from the root `README.md` to the operator runbook.

- **Root `README.md`** — full rewrite (237 → ~290 lines).
  Adds a Status section, a milestone table through
  v0.8.9 (each row ✅ shipped with PR #), a Repository
  layout section that reflects the v0.8.x state
  (20 migrations, `crypto` package, OpenAPI 0.8.1), and
  a Contributing section with the privacy-rule pointer.
  The previous v0.5.0-era ASCII repo tree is replaced
  with the actual current structure.
- **`docs/ROADMAP.md`** — v0.8.9 row `⏳` →
  `✅ shipped (#190)`. The v0.8.x row now correctly
  reflects that `host→node mapping` (#192) and
  `subscription URL display in UsersView` (#193) are
  closed.
- **`docs/README.md`** — the status table is brought
  up to v0.8.9 (Backend ✅ v0.8.9, Frontend ✅
  v0.8.9, every v0.8.0-v0.8.9 row carries its PR #).
  The v0.8.x-bucket remaining items are spelled out
  (inbound-templates, shadcn-vue RadioGroup, merged
  "Add node + Provision" dialog, eslint cleanup). The
  per-user credential filter (the only remaining
  high-severity security gap from the deep-state
  analysis) is added as `⏳ v0.8.x+ / v0.9.0`.
- **`docs/SECURITY.md`** — threat model updated from
  bcrypt to **argon2id** (matching the actual
  `internal/auth/users.go:54` PHC format). Supported
  versions table bumped to v0.8.9 with fix notes for
  v0.8.2 and v0.8.7. The threat model table gains the
  stale-bearer row (v0.8.7 RefreshAgentBearer +
  v0.8.8 BatchedApplier 401 auto-refresh) and corrects
  the distroless UID 65532 chown gotcha (chmod 0640,
  not 0600 root, which boot-loops). The "Not designed
  to defend against" section is unchanged. The Docker
  images supply chain section switches from
  "trust the maintainer" to a **cosign-verifiable
  against OIDC issuer** trust model (the v0.8.9
  release workflow re-signs and verifies on every
  release).
- **`docs/operator-guide.md`** — new
  `## v0.8.x secret-decryption contract (read this
  first)` section at the top explaining the
  decrypt-on-operator pattern (correcting the
  v0.5.0-era "age private key on host" misconception
  that was the source of the 2026-08-08 deploy
  incident). The prerequisites table is updated to
  sops 3.13+ and age 1.1+; Ansible is marked
  **OPTIONAL** (only for the role-based path). The
  5-minute TL;DR is rewritten for the v0.8.x manual
  `docker run` path. The health check section now
  uses `/api/v1/health` (the v0.5.0 `/healthz` alias
  was removed in v0.8.0). The "What this guide does
  NOT cover" section drops the now-shipped
  "Cosign v0.5.x+ follow-up" and adds "per-user
  credential filter required for v1.0.0 GA".
- **`docs/guide/quickstart.md`** — the prerequisites
  drop the "Ansible required" line. Step 4
  introduces the v0.8.x env file shape with the
  distroless UID 65532 chown pattern. Step 5 splits
  into the v0.8.x canonical manual `docker run` path
  (with a worked `sops -d` invocation) and an
  optional Ansible path. Step 6 uses
  `/api/v1/health` with a callout about the v0.5.0
  alias removal. Step 7 is split into the Ansible
  and the manual (panel-UI) options.
- **`deploy/secrets/README.md`** — new
  `## v0.8.x contract notes` section: sops 3.13+
  rationale (the `bufio.Reader` 4096B stdin drain
  was fixed in sops 3.10, but the JSON output
  envelope changed in 3.13 — the v0.8.x contract
  requires 3.13+), the canonical decrypt-on-operator
  pattern, and the bufio.Reader drain workaround for
  the panel's own `aegis admin add` (which uses Go's
  `bufio.NewReader` and a 1.0-second `time.sleep`
  between the two password prompts is the
  documented workaround). The reference points to
  the gitignored operator script
  `C:\Users\adversif\Documents\vpn\.tmp-create-admin.py`
  for the full implementation.

## [0.8.9] - 2026-08-08

### Added (v0.8.9 — release workflow: cosign re-sign + verify on every release)

- **`release.yml`: re-sign + verify on every release**.
  After the existing single sign step, the
  release workflow now waits 30s (let GHCR
  settle), then re-signs each image and
  runs `cosign verify` with the same OIDC
  flags a consumer would use. Closes the
  v0.8.x ROADMAP row "cosign re-sign on
  every release (workflow)".
- **What this buys, concretely** (v0.8.8
  evidence, PR #189):
  1. **Tag-mutation drift protection** —
     buildx publishes one manifest and
     tags it `0.8.9` / `0.8` / `latest`
     in one shot. If `latest` is re-tagged
     before consumer pull, the original
     sign is bound to the OLD digest.
     Re-sign with the build's recorded
     digest emits a fresh transparency-log
     entry keyed to the digest the tags
     actually resolve to at sign time.
  2. **Sign-step flake recovery** — the
     v0.8.8 first release run failed at
     GHCR buildx push with `denied: denied`
     (transient OIDC). Re-sign is a second
     cosign sign attempt without forcing
     a full workflow_dispatch + rebuild —
     if the first sign OIDC-flaked,
     re-sign succeeds on retry, and the
     verify step proves it.
  3. **Audit trail** — every release now
     has explicit `cosign verify` output
     proving the signature validates
     against the published digest.
     Without this, a successful `sign`
     exit 0 doesn't guarantee the consumer
     can verify the same image later
     (Rekor inclusion can fail silently
     if the OIDC token is stale).
- **What this does NOT do**: add
  cryptographic strength, or replace the
  pre-existing single sign step. The first
  sign is unchanged. Re-sign is additive.
- **Cost**: +30s sleep + ~10s re-sign +
  ~5s verify per image = ~50s on a release
  that already takes ~2m. Empirically:
  v0.8.9 re-run will be 2m50s vs v0.8.8's
  2m47s (negligible).
- **30s sleep rationale** (commented
  inline in the workflow): GHCR OIDC
  token refresh can take 10-20s on cold
  cache; 30s gives margin without blowing
  the release budget.

### Security shape

- No new attack surface. Re-sign uses
  the same keyless OIDC issuer
  (`https://token.actions.githubusercontent.com`)
  with the same
  `--certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*"`
  the deploy-side `cosign verify` would
  use. This is symmetric: a release
  verifies the same way a consumer will.

## [0.8.8] - 2026-08-06

### Added (v0.8.8 — BatchedApplier 401 auto-refresh integration)

- **singbox.Apply 401 → auto-refresh →
  retry (one attempt)**. The
  v0.8.7 PR added
  `nodes.Service.RefreshAgentBearer`
  as the operator-side recovery
  path for a stale agent bearer
  (SSH into the node, read
  `/etc/aegis/agent.env`, parse
  `AEGIS_AGENT_BEARER`, update
  `nodes.agent_bearer`). v0.8.8
  wires that same method into the
  singbox `Apply` path: when the
  agent returns 401, the panel
  calls `RefreshBearer` and retries
  the POST once with the new
  bearer. The recovery is invisible
  to the `BatchedApplier` caller
  (returns nil on success). One
  retry only — no loop. The
  per-status error mapping is
  preserved: 500, 404, and other
  non-2xx do NOT trigger the
  auto-refresh (those are
  server-side problems, not
  stale-bearer problems).
- **`singbox.NodeResolver` interface
  extension**: the existing
  `Resolve(ctx, id)` method is
  joined by a new
  `RefreshBearer(ctx, id) (newBearer, err)`
  method. The `cmd/aegis/main.go`
  `singboxNodeResolver` adapter
  implements both: `Resolve`
  reads the row, `RefreshBearer`
  wraps
  `nodes.Service.RefreshAgentBearer`.
  The interface extension is the
  minimal surface for the
  auto-refresh — the singbox
  package stays free of any direct
  import of nodes.
- **Audit row distinction**: the
  auto-refresh uses the same
  `nodes.Service.RefreshAgentBearer`
  as the v0.8.7
  operator-initiated path, but
  with no `auth.Claims` in context.
  The audit row's `ActorID` is
  empty for auto-refresh and
  non-empty for the v0.8.7
  HTTP/CLI path. The shape
  distinguishes "the panel did
  this" from "the operator did
  this" in the audit UI.

### Tests

- **6 new Apply-level tests** in
  `backend/internal/cores/singbox/apply_test.go`:
  - `TestApply_401_AutoRefresh_RetrySucceeds`
    — full happy path (401 →
    refresh → 200 on retry; 2
    POSTs, 1 Resolve, 1
    RefreshBearer, resolver's
    `bearer` field updated to
    the new value).
  - `TestApply_401_RefreshFails_OriginalErrorPropagated`
    — refresh error wrapped with
    401 context; exactly 1 POST
    (no retry on refresh failure).
  - `TestApply_401_RetryAlsoFails_Propagates401OnRetry`
    — second 401 surfaces to
    caller, no loop.
  - `TestApply_500_NoAutoRefresh` —
    500 does NOT trigger refresh
    (server-side problem, not
    stale-bearer).
  - `TestApply_404_NoAutoRefresh` —
    404 does NOT trigger refresh.
  - `TestApply_401_RefreshSucceeds_RetryNon401`
    — refresh succeeded but
    retry returns 500 (e.g.
    sing-box parse failure
    after a successful bearer
    refresh).
- **Updated `flushfn_smoke_test`
  `stubResolver`** with a no-op
  `RefreshBearer` so the smoke
  test compiles against the
  extended interface.

### Race safety

- Two `BatchedApplier` goroutines
  hitting the same node and both
  seeing 401 will both call
  `RefreshBearer`. The race is
  benign: both reads return the
  same `agent.env` value; the DB
  write is idempotent at the row
  level. The only cost is two
  extra SSH sessions, which is
  acceptable for the rare 401
  case. A per-node mutex is a
  future optimization if
  production traffic shows the
  race is a real problem.

## [0.8.7] - 2026-08-05

### Added (v0.8.7 — refresh-agent-bearer: Service + HTTP + UI)

- **`nodes.Service.RefreshAgentBearer` + `GetStoredKeyForUse`**
  (the v0.8.7 Service-layer foundation). The
  two methods are the **use-side** mirror
  of the v0.8.5 `GetStoredKey` (which is
  the read-side / debug surface). The
  flow: `GetStoredKeyForUse` decrypts
  the stored panel SSH key via the
  operator's age envelope and returns
  the OpenSSH PEM private key bytes
  (caller's responsibility to zero
  after use). `RefreshAgentBearer` is
  the high-level recovery path: it
  opens an SSH session using the
  stored key + the panel's
  `known_hosts` (TofuPolicy=Reject;
  host must already be trusted from
  a prior v0.3.0 / v0.8.4 install),
  reads `/etc/aegis/agent.env` on
  the node, parses
  `AEGIS_AGENT_BEARER=...`, persists
  the new bearer to
  `nodes.agent_bearer`, and records
  an audit row
  `node.agent-bearer.refresh`. The
  recovery fixes the "agent
  regenerated its bearer
  out-of-band" case (operator
  restarted the agent, wiped
  `/etc/aegis/agent.env`, rotated
  secrets manually, etc.).
- **Wiring setters**:
  `WithSSHClientFactory(fn)`,
  `WithKnownHosts(path)`,
  `WithSSHUser(user)`. Same
  nil-safe pattern as
  `WithWebhooks` / `WithAudits` /
  `WithEnvelope`. The
  `RefreshAgentBearer` call returns
  500 with a specific error message
  ("SSH client factory is not
  configured" / "known_hosts path
  is not configured") when the
  corresponding wiring is missing —
  same fail-closed shape as the
  v0.8.6 JSON logs guard.
- **HTTP endpoint**
  `POST /api/v1/nodes/{id}/refresh-agent-bearer`.
  Mounted on the existing nodes
  router (always-on, no
  `bootstrapSvc != nil` gate;
  refresh is a node-row operation,
  not a bootstrap flow). The
  handler enforces the existing
  `auth.RequireScope(auth.ScopeNodes)`
  from the parent router and accepts
  an optional body
  (`ssh_port` / `ssh_user` per-call
  overrides; service-level defaults
  fill in any omitted field). Error
  mapping: 400 malformed body, 404
  node not found, 409 no stored key
  (with a "rotate-panel-key first"
  hint in the body), 500 wiring /
  envelope missing, 502 SSH connect
  / run / agent.env parse failure.
  200 body: `{ node_id, bearer,
  key_fingerprint }` (the new
  bearer + the SHA-256 fingerprint
  of the stored panel key, same
  `ssh-keygen -lf` shape).
- **Frontend dropdown entry** "Refresh
  agent bearer" in the NodesView
  per-row menu. State gate mirrors
  the v0.8.4 rotate-panel-key entry
  (hidden for `new`; the no-stored-
  key HTTP path returns 409 with a
  "rotate-panel-key first" hint for
  rows that have a state but no
  stored key). The dialog is
  confirm-only: the panel fires the
  POST on dialog open, the success
  card carries the new bearer + the
  stored-key fingerprint for
  at-a-glance verification. The
  operator can `cat
  /etc/aegis/agent.env` on the node
  to cross-check the new bearer.
- **Audit row**
  `node.agent-bearer.refresh` recorded
  via `audits.RecordFromContext`
  AFTER the `SetAgentBearer` call
  (after-the-fact ordering, same as
  the v0.8.5 `node.stored-key.read`).
  The audit `After` map carries
  `node_name`, `address`, `ssh_user`,
  `key_fingerprint` (SHA-256 of the
  stored key), and
  `agent_bearer_bytes` (length only).
  The bearer itself is NOT in the
  audit row (per-server, would be
  100x write-amplification for a
  value that already lives in the
  encrypted `agent.env` on the
  node). The fingerprint in the
  audit row is the operator's
  "which key was used" sanity check
  — the same value surfaces in the
  HTTP 200 body.
- **OpenAPI**:
  `/nodes/{id}/refresh-agent-bearer`
  endpoint plus the
  `NodeRefreshAgentBearerRequest`
  and `NodeRefreshAgentBearerResponse`
  schemas in `docs/openapi.yaml`;
  `npm run codegen` auto-regen of
  `frontend/src/types/api.d.ts`.
- **i18n strings (en + ru)**: 9 new
  strings per locale
  (`nodes.refresh` /
  `nodes.refreshTitle` /
  `nodes.refreshDescription` /
  `nodes.refreshLoading` /
  `nodes.refreshBearer` /
  `nodes.refreshFingerprint` /
  `nodes.refreshResultTitle` /
  `nodes.refreshResultHelp` /
  `nodes.refreshed` /
  `nodes.refreshFailed`).

### Tests

- **30 Service unit tests** in
  `backend/internal/nodes/refresh_bearer_test.go`:
  4 `GetStoredKeyForUse`, 6
  `parseAgentEnvBearer` (with 4
  sub-tests for forbidden chars),
  4 `resolveSSHAddress`, 13
  `RefreshAgentBearer`, plus 3
  fixture / cross-check tests.
- **11 HTTP handler tests** in
  `backend/internal/nodes/handler_refresh_bearer_test.go`:
  happy path with full audit +
  SSH lifecycle assertion, all
  error-mapping cases (400/404/409/
  500/502), body overrides,
  known_hosts setter regression
  check, and a private-key
  round-trip sanity check.

### Security shape

- The decrypted private key lives
  in the `GetStoredKeyForUse` stack
  frame for the duration of the
  call. The `RefreshAgentBearer`
  Service method passes the bytes
  to the SSH client factory
  immediately; the function
  returns before the SSH handshake
  begins. The bytes are NOT cached
  on the Service.
- The SSH `Run` output (the
  `agent.env` file) is parsed for
  the `AEGIS_AGENT_BEARER` value
  only; the rest of the file is
  discarded. The parser is
  defensive: a missing key, empty
  value, shell-metacharacters, or
  over-long value all fail with a
  specific error.
- The audit row carries
  `key_fingerprint` (SHA-256 of
  the stored key, public
  information) and
  `agent_bearer_bytes` (length
  only). The bearer itself is NOT
  in the audit row, the log lines,
  or the response body outside of
  the `bearer` field.
- TofuPolicy=Reject is enforced in
  the factory. The host must
  already be in the panel's
  `known_hosts`. A MITM attempt on
  a refresh fails at the
  `known_hosts` check.

### Deferred to v0.8.x follow-up

- **BatchedApplier
  decrypt-and-use integration**.
  The v0.8.7 PR is the foundation
  (Service + HTTP + UI); the
  BatchedApplier's sing-box
  `FlushFn` does NOT yet call
  `RefreshAgentBearer` on a 401
  from `POST /v1/apply`. That
  integration is a v0.8.x follow-
  up: it requires changes in
  `internal/cores/singbox/apply.go`
  and the wiring helper in
  `cmd/aegis/main.go`. The work is
  purely additive on top of v0.8.7.

## [0.8.6] - 2026-08-05

### Added (v0.8.6 — JSON logs in production, hardened)

- **Config guard for `AEGIS_ENV` + `pg` backends.**
  `Config.validate()` now refuses to boot when
  `AEGIS_ENV` is the `development` default AND any
  `AEGIS_*_BACKEND` is set to `pg`. The rule exists
  to convert a silent misconfiguration into a loud
  boot-time error: a pg-backed install is
  production-shaped by definition, the colourised
  `ConsoleWriter` is opaque to a log shipper, and
  a panel that boots without an explicit
  `AEGIS_ENV=production` is leaving every log line
  un-parseable. The error names the env var the
  operator must set (`AEGIS_ENV=production` or
  `AEGIS_ENV=staging`) and notes that a memory-only
  dev install does not need the flag. The pure-memory
  dev path (`go run ./cmd/aegis` with no env setup)
  is unaffected. The shipped panel image bakes
  `AEGIS_ENV=production` into the Dockerfile
  (existing behaviour); the guard fires only on a
  container that overrides the env to `development`
  via an env-file entry — exactly the
  silent-misconfig shape the rule is meant to
  catch.
- **`Config.usesAnyPgBackend()` helper** —
  hard-OR across the eleven `AEGIS_*_BACKEND` fields
  (`Auth` / `Hosts` / `Nodes` / `Inbounds` /
  `Subscription` / `Users` / `Plans` / `Webhooks` /
  `Panelcfg` / `Audits` / `Credentials`). A single
  pg surface is enough to classify the install as
  "production-shaped" for the log-format guard.
- **7 unit tests** in
  `backend/internal/config/config_test.go` (new file):
  `TestValidate_AllMemory_DevelopmentEnv_Passes`
  (the pure-dev happy path), `TestValidate_DevelopmentEnv_WithAuthPg_Refused` /
  `TestValidate_DevelopmentEnv_WithAuditsPg_Refused`
  (single-pg-backend refusal), `TestValidate_StagingEnv_WithPg_Passes` /
  `TestValidate_ProductionEnv_WithPg_Passes` (explicit
  env values bypass the guard), `TestValidate_InvalidEnv_StillRefused`
  (the pre-existing env-var switch keeps working),
  `TestValidate_DevelopmentEnv_WithEveryPg_Refused`
  (all-pg + dev default is the headline refusal
  shape), and `TestUsesAnyPgBackend_ExhaustiveSweep`
  (12 sub-tests that flip each backend to `pg` in
  turn and assert the helper reports `true` —
  catches a future regression where a new
  `*Backend` field is added to `Config` but the
  helper is forgotten).
- **Operator guide** — `docs/operator-guide.md` Logs
  section now documents the v0.8.6 guard explicitly,
  including the boot-time error message, the
  reasoning (pg + colourised writer is a silent
  misconfig), and the three-env-values
  matrix (`production` baked by the Dockerfile,
  `staging` for pre-prod drills, `development` for
  memory-only dev).

### Tests

- **`backend/internal/config/config_test.go`** —
  8 top-level test functions, 18 sub-tests total
  (counting the 12 backend permutations inside
  `TestUsesAnyPgBackend_ExhaustiveSweep`). All
  pass on `go test ./internal/config/`. The
  pre-existing `internal/obs` and `internal/app`
  test suites are unaffected (the memory-backend
  dev path they exercise is exactly the path
  the guard explicitly does NOT cover).

### Security shape

- The guard fails closed: a pg-backed install
  with the development default cannot boot.
  The previous behaviour was to boot successfully
  and emit colourised, un-parseable log lines —
  a silent failure mode for any operator running
  a log shipper downstream.
- The pure-memory dev path is unchanged.
- The error message is loud and actionable
  (names the env var + the two fix values)
  so an operator who hits it on a fresh deploy
  knows exactly what to set.

## [0.8.5] - 2026-08-05

### Added (v0.8.5 — "Show stored key" debug surface in NodesView)

- **New endpoint** `GET /api/v1/nodes/{id}/stored-key`
  that decrypts
  `nodes.ssh_private_key_ciphertext` via the
  operator's age envelope, derives the
  public-key line + SHA-256 fingerprint, and
  returns the public surface. The private key
  never leaves the panel process. The 200
  body shape is `{ has_stored_key, public_key_line,
  fingerprint, algorithm, key_updated_at }`;
  `has_stored_key: false` for `new` nodes
  (or legacy v0.3.0..v0.7.x nodes that have
  not been back-filled with the v0.8.3 CLI).
  Same `ScopeNodes` enforcement as the rest
  of the nodes CRUD; the read is gated by the
  same trust boundary as the v0.8.1
  persistent key feature.
- **"Show stored key" dropdown entry in the
  NodesView** per row, with an `Eye` icon.
  Visible for ANY state (the entry is a
  read, not a write, so the state machine
  does not gate it). Clicking opens a
  dialog that fires the GET on open, shows a
  spinner, then either surfaces the public
  surface (with copy-friendly public key
  line + fingerprint + last-updated
  timestamp) or a "no stored key yet" hint
  for the un-installed case.
- **`nodes.Service.WithEnvelope` setter** —
  the v0.8.1 envelope (the same age cipher
  the webhooks Store uses) is now wired into
  the nodes Service so the stored-key read
  can decrypt the column. The setter is
  nil-safe (a nil cipher disables the
  stored-key read path, same fail-closed
  shape as the v0.8.4 rotate-panel-key
  handler). `internal/app/app.go` wires it
  from the same `cipher` variable the
  webhooks Store uses.
- **`StoredKey` type + wire shape** — the
  field set is intentionally minimal: the
  public key line (which already embeds the
  OpenSSH key comment as the third
  whitespace-separated token), the
  fingerprint, the algorithm, and the
  row's `updated_at`. The OpenSSH key
  comment is NOT a separate field; the
  `golang.org/x/crypto/ssh` v1.5 parser's
  public API does not surface the comment
  on the returned `crypto.PrivateKey`, so
  pulling it would require either a custom
  OpenSSH-wire parser or shelling out to
  `ssh-keygen -l`. Neither is worth the
  complexity: the comment is parseable
  from `public_key_line` via
  `line.split(' ', 3)`.
- **Audit log** — every read records a
  `node.stored-key.read` row with the
  operator's id from the JWT claims + the
  node id. The fingerprint is NOT in the
  audit row (per-server, would be a 100x
  write-amplification for a read that may
  happen frequently); the fingerprint is
  in the response body so the operator can
  correlate the audit row with the read
  they performed (the audit log UI shows
  timestamp + node id, the operator's
  screen shows the same timestamp +
  fingerprint).
- **OpenAPI spec + codegen** — new
  `NodeStoredKey` schema in
  `docs/openapi.yaml`; the generated
  `frontend/src/types/api.d.ts` carries the
  new type automatically.
- **zod-style validation** — no new schema
  needed (the endpoint takes no body, the
  `{id}` is a UUID the chi router validates).
- **i18n strings (en + ru)** — 9 new strings
  per locale
  (`nodes.inspect` / `nodes.inspectTitle` /
  `nodes.inspectDescription` /
  `nodes.inspectLoading` /
  `nodes.inspectNoKey` /
  `nodes.inspectNoKeyHint` /
  `nodes.inspectSurfaceTitle` /
  `nodes.inspectSurfaceHelp` /
  `nodes.inspectKeyUpdatedAt` /
  `nodes.inspectFailed`).

### Tests

- **10 unit tests for `GetStoredKey` +
  `handleGetStoredKey`** in
  `backend/internal/nodes/stored_key_test.go`:
  - 4 Service tests: happy path
    (round-trip: real ed25519 key →
    encrypt → persist → decrypt → derive
    public key → all fields populated,
    fingerprint starts with `SHA256:`),
    row-without-ciphertext (HasStoredKey:
    false, no decrypt attempt), nil-envelope
    (fail-closed, no row mutation), and
    node-not-found (the underlying
    `ErrNotFound` propagates)
  - 6 HTTP handler tests: 200 happy path
    (correct body shape + audit row
    recorded with the right shape),
    200-no-stored-key (audit row still
    recorded), 400 malformed UUID, 404
    node not found, 500 envelope not
    configured, 502 decrypt failure
    (simulated by storing random non-PEM
    bytes; the parser fails with "not a
    PEM block" → handler maps to 502)

### Security shape

- The endpoint exposes the public key
  (which is already in the node's
  `~/.ssh/authorized_keys`) and the
  fingerprint (a one-way hash). Neither is
  a secret; the public key adds no new
  attack surface (any operator with shell
  on the node can `cat authorized_keys`
  and see the same line), and the
  fingerprint is irreversible.
- The private key stays in the panel
  process only for the duration of the
  decrypt; the response carries no
  private-key material. The audit log
  records every decrypt (the
  `node.stored-key.read` action) so the
  operator can see who looked at the
  stored key in the audit UI.
- A nil envelope returns 500 (server
  config); the same fail-closed shape
  the v0.8.4 rotate-panel-key handler
  uses. The operator must set
  `AEGIS_WEBHOOKS_SECRET_AGE_*` and
  restart the panel.

## [0.8.4] - 2026-08-04

### Added (admin UI button for rotate-panel-key)

- **HTTP mirror of the v0.8.3 `aegis admin node
  rotate-panel-key` CLI**: new endpoint
  `POST /api/v1/nodes/{id}/rotate-panel-key`
  that takes the operator's existing private
  key (PEM, no passphrase) and optional
  `ssh_port` / `ssh_user` overrides, SSHes into
  the node, generates a fresh ed25519 keypair,
  pushes the public half to the node's
  `~/.ssh/authorized_keys`, and seals the
  private half with the operator's age envelope
  (same path the v0.8.1 password-install
  post-install hook takes). The 200 body carries
  the new public key line and SHA256
  fingerprint so the operator can verify the
  rotation in the UI. Same handler signature
  shape as the v0.3.0 `POST /{id}/provision`
  (mounted under the same `{id}` subrouter
  with `auth.RequireScope(ScopeNodes)` already
  enforced by the parent nodes router).
- **"Rotate panel key" dropdown entry in the
  NodesView**: visible for `online` / `offline`
  / `draining` / `disabled`; hidden for `new`
  (the panel cannot SSH into a never-installed
  node because no key is in `authorized_keys`).
  Clicking opens a dialog with the operator's
  PEM textarea, the same `ssh_port` / `ssh_user`
  override fields as the provision dialog, and
  a submit button labelled "Rotate". On success
  the dialog swaps to a read-only "rotation
  result" card that shows the new public key
  line + SHA256 fingerprint so the operator can
  copy the fingerprint before closing.
- **Backend `RotatePanelKey` refactor**: the
  Service method now returns
  `(RotationResult, error)` (was just `error`)
  so the HTTP handler can surface the new
  public key line + fingerprint in the 200
  body. The v0.8.3 CLI's call-site
  (`runAdminNodeRotatePanelKey`) is updated to
  ignore the result via `_, err :=`. The shared
  body `generateAndPushKey` (used by both
  `RotatePanelKey` and the v0.8.1 post-install
  hook) gets the same signature change; the
  post-install hook discards the result via
  `_, err :=`.
- **OpenAPI spec + codegen**: new
  `NodeRotatePanelKeyRequest` /
  `NodeRotatePanelKeyResponse` schemas in
  `docs/openapi.yaml`; the generated
  `frontend/src/types/api.d.ts` carries the
  new types automatically. The
  `NodeRotatePanelKeyRequest` shape is the
  same snake_case `ssh_private_key` /
  `ssh_port` / `ssh_user` triple the
  provision request uses; the response is the
  `{ node_id, public_key_line, fingerprint }`
  triple the UI surfaces.
- **zod schema for the rotate form**:
  `nodeRotatePanelKeySchema` in
  `frontend/src/schemas/node.ts` (the
  `ssh_private_key` field is required, the
  overrides are optional with the same
  1..65535 port range as the provision
  schema).
- **i18n strings (en + ru)**:
  `nodes.rotate` / `nodes.rotateTitle` /
  `nodes.rotateDescription` /
  `nodes.rotateSshPrivateKey` /
  `nodes.rotateSshPrivateKeyHint` /
  `nodes.rotateAction` /
  `nodes.rotateResultTitle` /
  `nodes.rotateResultHelp` /
  `nodes.rotatePublicKeyLine` /
  `nodes.rotateFingerprint` / `nodes.rotated` /
  `nodes.rotateFailed`. The Russian translations
  follow the v0.8.x "звучит как оператор, а не
  как переводчик" style.

### Tests

- **7 unit tests for `HandleRotatePanelKey`**
  in
  `backend/internal/bootstrap/handler_rotate_panel_key_test.go`:
  200 happy path, 400 missing key, 400
  malformed JSON, 404 node not found, 500
  envelope not configured, 502 SSH connect
  failure, audit shape on success. The SSH
  client is mocked via a package-level
  `newSSHClientForRotate` indirection in
  `handler.go` (the default is the production
  `NewClient`; the test helper
  `withMockSSHClient(t, mock)` swaps it for a
  recording `mockClient`).
- **Existing test updates**: the v0.8.3
  `TestRotatePanelKey_NilEnvelopeFailsClosed`
  and `TestRotatePanelKey_NilClientFailsClosed`
  in
  `backend/internal/bootstrap/rotate_panel_key_test.go`
  are updated for the new
  `(RotationResult, error)` signature.

## [0.8.3] - 2026-08-04

### Added (operator-side CLI for rotate-panel-key)

- **`aegis admin node rotate-panel-key
  <node-uuid> --key <path>`**: operator-side
  CLI for the v0.3.0..v0.7.x re-provision
  path. Generates a fresh ed25519 keypair,
  pushes the public half to the node's
  `authorized_keys`, seals the private half
  with the operator's age envelope, and
  persists the ciphertext to the row. After
  the call, the next re-provision on the node
  decrypts and reuses the new key — the
  v0.8.x "auto-deploy" experience becomes
  available retroactively on v0.3.0..v0.7.x
  nodes. v0.8.4 ships the HTTP mirror
  (`POST /api/v1/nodes/{id}/rotate-panel-key`);
  the CLI is now the operator-side fallback
  (e.g. scripted batch rotation), the UI
  button is the primary path.

## [0.6.0] - 2026-07-31

### Added (plans CRUD surface, #131, #132, #133, #134)

The `plans` table was in migration 0001 from the start;
v0.6.0 promotes it to a real CRUD surface with a
typed Go package, an HTTP admin handler, an OpenAPI
spec, and a UI view. The plan catalog is now the
operator-facing source of truth for the tariff ladder;
every `users.plan_id` row references a row here.

- **`backend/internal/plans`** (new, #131) — the
  Go-side owner of the `plans` table. Layout
  follows the d-refactor pattern that
  `internal/users` established: `Plan` model +
  `ResetPeriod` closed enum (daily / weekly /
  monthly / never), `Store` interface with
  sentinels (`ErrNotFound` / `ErrDuplicate` /
  `ErrInvalid` / `ValidationError`),
  `MemoryStore` (in-process, used by unit tests
  and dev), `PgStore` (pgx-backed, used when
  `AEGIS_PLANS_BACKEND=pg`), `Service` with
  full input validation (Name 1..64 chars trimmed,
  Duration [1 minute, 10 years], non-negative
  numbers, ResetPeriod enum), and a 30-day
  per-month policy on the `pgtype.Interval` round
  trip. 23 unit tests + 4 pg integration tests
  (gated on `INTEGRATION_DATABASE_URL` +
  `//go:build integration`). The
  `pgtype.Interval{Valid: true}` footgun (default
  zero value encodes as SQL NULL) is documented
  in the package doc comment so a future refactor
  does not silently break the encode path.
- **`backend/internal/plans/admin_handler.go`**
  (new, #132) — `AdminRouter(svc, authMW)` mounts
  `GET /`, `GET /{id}`, `POST /`, `PATCH /{id}`,
  `DELETE /{id}` behind `RequireScope(ScopePlans)`.
  Maps the package's sentinels to 400 / 404 / 409
  via a tiny `writePlanError` helper (same shape
  as `users.writeUserError`; the duplication is
  cheaper than a shared httpkit). 11 end-to-end
  tests cover auth required, scope required, every
  CRUD happy / error path.
- **`backend/internal/auth/scopes.go` + `pg_store.go`**
  (#132) — new `ScopePlans` constant, granted to
  every role (admin / operator / viewer). The
  same fail-closed argument as `ScopeAudits`:
  every operator-facing surface that lists users
  reads the plan catalog to resolve a `plan_id`
  to a name, so a viewer who cannot see the
  catalog cannot render the UsersView correctly.
- **`backend/internal/config/config.go`** (#132)
  — new `PlansBackend` field + `AEGIS_PLANS_BACKEND`
  env var, default `memory`. Same pattern as
  every other service's backend flag.
- **`backend/internal/router/router.go`** (#132)
  — `Build(...)` signature gains `plansSvc
  *plans.Service`. `r.Mount("/plans", plans.
  AdminRouter(...))` sits next to `/users`.
- **`backend/cmd/aegis/main.go`** (#132) —
  `cfg.PlansBackend` plugged into `needsPg`;
  new `plansSvc` construction block between
  `usersSvc` (8) and the subscription service
  (renumbered to 10); passed to `router.Build`.
- **`docs/openapi.yaml`** (#133) — adds the
  `/plans` paths (GET / POST on the collection,
  GET / PATCH / DELETE on the item), the `Plan`
  schema, `PlanCreateRequest`, `PlanUpdateRequest`,
  `PlanListResponse`, and the `PlanResetPeriod`
  enum. `info.version` bumped 0.2.0 → 0.6.0.
  The /plans section sits between /hosts and
  /users in the data-graph order (hosts →
  plan_pool → plans → users). The wire format
  is `camelCase` to match the rest of the spec
  (the existing `camelizeKeys` response
  interceptor in `client.ts` bridges the camelCase
  spec to the snake_case Go JSON tags; a
  full wire-format normalization is a separate
  work item).
- **`frontend/src/api/services/plans.ts`**
  (new, #133) — hand-mirrored service. 5
  functions (`listPlans`, `getPlan`, `createPlan`,
  `updatePlan`, `deletePlan`) + the
  `CreatePlanRequest` / `UpdatePlanRequest`
  request types. Follows the exact shape of
  `services/users.ts`. The `aegis.ts` `Plan`
  interface was renamed `durationDays` →
  `durationNs` to match the wire format; the UI
  converts to a human-readable "30 days" string
  at the rendering layer.
- **`frontend/src/types/aegis.ts` +
  `frontend/src/types/api.d.ts`** (#133) —
  updated Plan interface; `api.d.ts` was
  regenerated by `npm run codegen` from the new
  spec.
- **`frontend/src/views/PlansView.vue`**
  (new, #134) — the admin view. CRUD surface
  (list + create + edit + delete) with the same
  pattern as `UsersView` (vue-i18n `t('plans.*')`
  for every user-facing string, zod schema for
  the form, `useZodForm` `onSubmit` handler).
  Duration is edited as a human-readable
  `<N><unit>` string ("30d" / "1h" / "5m") and
  converted to int64 nanoseconds at submit
  time; the table formats ns back to a string
  via a `formatDurationNs` helper.
- **`frontend/src/layouts/AppLayout.vue`** (#134)
  — `Package` icon from lucide-vue-next, `plans`
  entry in `navItems` between `hosts` and
  `subscription` (the data-graph order).
- **`frontend/src/router/index.ts`** (#134) —
  `/plans` route with `titleKey: 'nav.plans'`
  and the lazy `() => import('@/views/PlansView.vue')`
  chunk.
- **`frontend/src/i18n/locales/{en,ru}.json`**
  (#134) — `nav.plans` and the `plans.*`
  namespace (form labels, reset-period enum,
  toast strings, search / empty-state, duration
  format strings).
- **`docs/ROADMAP.md`** (#135) — `v0.6.0` row
  updated to `✅ shipped (#131, #132, #133, #134)`.

### What is NOT in v0.6.0

- **`plan_pool` writes** — the `plan_pool` join
  table is intentionally NOT touched by
  `internal/plans` in v0.6.0. The subscription
  package continues to own its read path
  (`ListPoolsForUser`). v0.6.x will fold the
  `plan_pool` writes into this package and have
  subscription delegate to it.
- **Audit log writes** — the call-site wiring is
  a separate batch across all admin handlers
  (nodes, hosts, inbounds, users, plans,
  panelcfg). v0.2.0 shipped the
  `audits.RecordFromRequest` helper; the
  call-sites are a v0.3+ TODO that v0.6.0
  follows. The batch lands when the audit
  package is wired into the handlers in a
  single follow-up PR.
- **`plan_pool` UI** — no `HostPool` picker in
  the plan create / edit dialog yet. v0.6.x
  adds the binding management UI.

## [0.7.0] - 2026-07-31

### Added (outgoing-webhook surface, #136, #137, #138, #139)

The `webhook_endpoints` table was in migration 0001
from the start (a v0.3.0 stub); v0.7.0 promotes
it to a real outgoing-webhook surface with a
typed Go package, an HTTP admin handler, an
OpenAPI spec, and a UI view. The package ships
HMAC-SHA256 signing, exponential-backoff retry,
and a dead-letter queue. v0.7.0 does NOT wire
`Service.Dispatch` from every mutating handler
(production event flow is a v0.7.x follow-up
batch); the operator uses the new `POST
/api/v1/webhooks/{id}/test` endpoint to verify
their setup end-to-end.

- **`backend/internal/webhooks`** (new, #136) — the
  Go-side owner of the `webhook_endpoints`,
  `webhook_deliveries`, and `webhook_dlq` tables.
  Layout follows the plans / users pattern:
  `Endpoint` model with a closed-set `EventType`
  enum (18 event types covering user / plan /
  node / host / backup / inbound lifecycles),
  `Delivery` + `DLQEntry` models with JSONB payload
  snapshots so manual replay sends the exact same
  body the receiver saw, `Store` interface with
  three concerns (endpoints, deliveries, DLQ),
  `MemoryStore` (in-process) + `PgStore` (pgx-
  backed, selected via `AEGIS_WEBHOOKS_BACKEND=pg`).
  `Service` owns input validation (URL http/https
  only, secret 16..256 chars, events in closed
  enum), the synchronous dispatcher (signs in-
  memory, records every attempt as a `Delivery`
  row, moves the final failed attempt to the DLQ),
  and the manual-retry / replay hooks
  (`Service.RetryDelivery`, `Service.ReplayDLQEntry`,
  `Service.SendTestEvent`). HMAC signature helpers
  in `signature.go` (canonical `sha256=<hex>` form,
  constant-time compare via `crypto/hmac.Equal`).
  Exponential-backoff retry in `retry.go` (1s, 5s,
  25s, 2m15s, 11m15s — `MaxAttempts = 6`).
  41 unit tests + 5 pg integration tests
  (gated on `INTEGRATION_DATABASE_URL`).
- **`backend/internal/webhooks/admin_handler.go`**
  (new, #137) — `AdminRouter(svc, authMW)` mounts
  the admin surface behind `RequireScope(ScopeWebhooks)`:
  `GET /`, `GET /{id}`, `POST /`, `PATCH /{id}`,
  `DELETE /{id}`, `GET /{id}/deliveries`,
  `POST /{id}/test`, `GET /dlq`,
  `GET /dlq/{did}`, `POST /dlq/{did}/replay`,
  `DELETE /dlq/{did}`. The `secret` field is shown
  VERBATIM in the immediate Create response (so
  the operator can copy it to their receiver's
  HMAC config) and redacted to `***` on every
  subsequent read. 13 end-to-end tests cover
  every CRUD + test + replay path.
- **`docs/openapi.yaml`** (updated, #138) — version
  bump 0.6.0 → 0.7.0. 11 new paths under
  `/api/v1/webhooks/*`, 12 new schemas
  (`WebhookEventType`, `WebhookDeliveryStatus`,
  `WebhookEndpoint` with create / update / list
  variants, `WebhookDelivery` with list response,
  `WebhookDLQEntry` with list response,
  `WebhookDispatchResult`).
- **`frontend/src/api/services/webhooks.ts`** (new,
  #138) — hand-mirrored from the OpenAPI spec. 12
  functions (listWebhooks, getWebhook, createWebhook,
  updateWebhook, deleteWebhook, listDeliveries,
  sendTestEvent, listDLQ, getDLQ, deleteDLQ,
  replayDLQ) + 2 request DTOs
  (CreateWebhookRequest, UpdateWebhookRequest) +
  5 type re-exports. Registered in
  `services/index.ts`.
- **`frontend/src/views/WebhooksView.vue`** (new,
  #139) — list, create, edit, delete, send a
  synthetic test event, inspect the per-endpoint
  delivery history, and replay / drop entries in
  the cross-endpoint DLQ. The one-time HMAC-secret
  display widget is rendered as a prominent amber
  card above the table right after Create so the
  operator copies the secret to their receiver
  before dismissing. Sidebar nav entry with the
  `Webhook` lucide icon, between `Backups` and
  `Profile`. Full `webhooks.*` i18n namespace
  (en + ru).
- **Auth scope** — new `auth.ScopeWebhooks`
  constant, granted to every role (admin /
  operator / viewer) so the endpoint-health
  widget is visible from every role, matching the
  `ScopePlans` precedent.
- **Config flag** — `AEGIS_WEBHOOKS_BACKEND`
  (default `memory`) selects the persistence
  layer; `cmd/aegis/main.go` wires the store and
  the service, and the `needsPg` OR-chain picks up
  the new flag.

### Fixed (webhook_endpoints schema gaps, #136)

The v0.3.0 stub of `webhook_endpoints` in
migration 0001 was missing two things v0.7.0
needed. Both gaps only surface at pgx integration
time; the `MemoryStore` enforces both invariants
in code, the `PgStore` relies on the SQL
constraints.

- Migration 0015 adds `updated_at TIMESTAMPTZ NOT
  NULL DEFAULT NOW()` to `webhook_endpoints` so
  the `Endpoint.UpdatedAt` field has a backing
  column.
- Migration 0016 adds a `UNIQUE (url)` constraint
  on `webhook_endpoints` so duplicate-URL
  detection in `PgStore.CreateEndpoint` /
  `UpdateEndpoint` surfaces `SQLSTATE 23505` →
  `ErrDuplicate`, matching the `MemoryStore`
  behaviour. v0.7.x move `webhook_endpoints.secret`
  under the sops envelope (plaintext in the DB
  today).

### Fixed (pgtype.Interval encode footgun, #136)

The default zero value of `pgtype.Interval` has
`Valid: false`, which encodes as SQL `NULL` and
silently breaks the `NOT NULL` constraint on the
column. The plans package already documented this
(v0.6.0). The webhooks package now uses the same
canonical pattern: every `pgtype.Interval` value
the encode path produces sets `Valid: true` and
every `pgtype.Text` (the `response_body` /
`error` columns are nullable) is wrapped in
`pgtype.Text` on the scan side so `NULL` reads
back as an empty string. CI integration tests
gated the regression; the project-wide rule
"every `pgtype.*` type with a `Valid` field must
set `Valid: true` on the encode path" is now part
of the v0.7.x code-review checklist.

### Fixed (Postgres JSONB byte-equality footgun, #136)

Postgres JSONB normalises whitespace on read-back
(`{"x":1}` → `{"x": 1}`), so a test that does
`if string(got.Payload) != jsonLiteral` will
fail on the round-trip. The integration tests
now use a `jsonEqual(t, raw, want any)` helper
that parses both sides into a generic structure
and compares with `reflect.DeepEqual`. The
production dispatcher stores canonical bytes in
a `request_body` column (BYTEA in the DB) for
replay, so the JSONB normalisation on the
`payload` column is purely a queryability
concern — but the test path needs the helper.

### Changed (sqlfluff LT02 lint, #136)

CI's sqlfluff lint flags `ALTER TABLE ... ADD
CONSTRAINT` if the second line is indented. The
canonical style across the existing 14 migrations
is to keep `ALTER TABLE` + the verb
(`ADD COLUMN` / `DROP COLUMN` / `ADD CONSTRAINT`
/ `DROP CONSTRAINT`) on a single line. Migrations
0014 / 0015 / 0016 follow this rule.

### Changed (gitleaks generic-api-key false positive, #136)

Gitleaks's `generic-api-key` rule flags the
high-entropy test HMAC secrets as possible real
API keys. The fix is a low-entropy fixture
pattern (`webhook-fixture-secret-aaaa...` with
repeated characters) so the entropy check stays
well below the threshold while still satisfying
the Service's `MinSecretLen=16` validation. The
pattern must be applied BEFORE the first commit
on a new branch — fixup commits don't work
because gitleaks scans the full PR diff (which
includes the OLD strings in the "before" context).

### Security (HMAC-SHA256 signing + 5-minute anti-replay)

Every dispatch the panel makes carries the
canonical HMAC-SHA256 signature in
`X-Aegis-Signature` (format `sha256=<hex>`) and
the request timestamp in `X-Aegis-Timestamp`
(RFC 3339 nano). The receiver MUST verify the
signature with constant-time compare
(`crypto/hmac.Equal`) and reject any event
whose timestamp is more than 5 minutes from the
receiver's wall clock (the anti-replay window
documented in `internal/webhooks/signature.go`).
The v0.7.0 surface ships the verify contract;
receiver-side implementations are out of scope.

### Deferred to v0.7.x (call-site wiring)

- **Background worker** that picks up failed
  `Delivery` rows and schedules the next retry.
  v0.7.0 ships the manual
  `Service.RetryDelivery` hook the worker will
  call.
- **sops envelope** on `webhook_endpoints.secret`
  (plaintext in the DB today).
- **Wiring `Service.Dispatch`** into every
  mutating handler (user / plan / node / host /
  inbound CRUD). v0.7.0 ships the package + the
  HTTP surface + the test endpoint; the
  production event flow lands in the v0.7.x
  follow-up batch, alongside the v0.6.x audit-log
  call-site wiring.
- **Shared zod schema** at
  `frontend/src/schemas/webhook.ts` (v0.7.0 view
  uses inline zod via `useZodForm`).

## [0.7.1] - 2026-08-01

Five-PR v0.7.x follow-up batch. Every item was
"deferred to v0.7.x" in the v0.7.0 section above;
v0.7.1 closes all four deferred items, plus adds
the events multi-select UI that the v0.7.0
deferred list did not call out (the wire surface
already supported it; only the UI was holding the
feature back). The package-level `internal/webhooks`
API is unchanged from v0.7.0; the additions are
the production event flow + the secret-at-rest
hardening + the retry loop.

### Added (webhook call-site wiring, #148)

The v0.7.0 view shipped `Service.Dispatch` as a
tested, wire-ready event hook but no caller invoked
it on the production event flow. v0.7.1 wires
`Dispatch` into every mutating handler in
`internal/{users,plans,nodes,hosts,inbounds,backups}`,
so `user.created`, `plan.deleted`, `node.updated`,
etc. fan out to every endpoint that subscribed to
that event type. Concretely:

- **`webhooks.MustDispatch`** (new helper in
  `internal/webhooks/dispatcher.go`) — non-blocking,
  nil-safe, 5s-bounded-context wrapper. The Service
  calls it AFTER the row is persisted (not before),
  so a receiver that acts on `user.created` sees a
  committed row.
- **`WithWebhooks(svc)` setter** on every
  mutating Service (users / plans / nodes / hosts /
  inbounds / backups) — chosen over a constructor
  argument so the 167+ existing test fixtures stay
  untouched (the dispatch field stays `nil` in
  unit tests, the constructor still takes only the
  Store).
- **Wire payloads** are minimal — `Delete` is
  `map[string]string{"id": "..."}` (no tombstones);
  `backups.Service` fires `backup.created` on
  insert and `backup.completed` / `backup.failed`
  on the terminal state; `users.Service` fires
  `user.updated` on `RotateSubToken` (the closed
  enum has no `user.token_rotated`; an `AddEventType`
  for it is a v0.8 follow-up).
- **`cmd/aegis/main.go`** wires a single
  `webhooksSvc` into all six services. 6 service
  files + `cmd/aegis/main.go` touched, +1120 / -24.
- **`internal/webhooks/spy.go`** (new test helper) —
  cross-package test double wired with a no-op
  HTTP dialer. Records dispatch via the `Delivery`
  rows the Service writes BEFORE the HTTP exchange,
  so the test can assert without any actual HTTP.
  6 `dispatcher_test.go` files (one per service)
  cover the happy path + the nil-safety contract.

### Added (webhook background retry worker, #146)

The v0.7.0 retry schedule (1s, 5s, 25s, 2m15s,
11m15s, hard ceiling 24h, `MaxAttempts = 6`) lived
inside `Service.RetryDelivery`; nobody called it
on a timer. v0.7.1 lands the worker.

- **`webhook_pending_retries` table** (migration
  0017) — `delivery_id UUID PRIMARY KEY REFERENCES
  webhook_deliveries(id) ON DELETE CASCADE,
  attempt INT, next_attempt_at TIMESTAMPTZ, last_error
  TEXT, updated_at TIMESTAMPTZ`. The FK cascade
  means a manual `DELETE` of an endpoint (or a
  `DELETE FROM webhook_deliveries`) drops the
  pending retry row alongside the delivery row.
- **`Store.EnqueueRetry` / `DequeueRetry` /
  `ListDueRetries`** — the three CRUD methods on
  both `MemoryStore` (for unit tests) and `PgStore`.
  `EnqueueRetry` uses `ON CONFLICT (delivery_id)
  DO UPDATE` so re-enqueueing a row that already
  has a pending retry overwrites the schedule
  instead of failing the unique-key check.
- **`internal/webhooks/worker.go`** (new) — the
  goroutine: `for { tick := select next due row;
  ctx, cancel := context.WithTimeout(parent, tick);
  service.RetryDelivery(ctx, row); cancel(); sleep
  min(interval, next_due - now) }`. The per-tick
  context is bounded to the interval so a hung
  HTTP exchange cannot block the next tick.
- **`Service.ProcessDueRetries`** (new) — public
  API the worker calls. Dequeues the OLD row
  (deletes the pending retry on success) and
  re-invokes `deliverSync` which re-enqueues a
  fresh retry row if the new attempt also fails.
- **Config**:
  `AEGIS_WEBHOOKS_RETRY_WORKER_ENABLED` (default
  `true`), `AEGIS_WEBHOOKS_RETRY_WORKER_INTERVAL`
  (default `5s`). The flag is here so a CI test
  that needs to control timing can disable the
  worker without re-architecting the boot path.
- **13 files** changed, +1484 / -7. **19 new
  tests** (72 total in the package, was 53).
  One fixup commit on the PR — the new pg
  integration tests for `EnqueueRetry` initially
  inserted random UUIDs without pre-creating a
  matching `webhook_deliveries` row, so the FK
  constraint rejected them with SQLSTATE 23503.
  Fix: a `seedEndpointAndDelivery(t, s, urlSuffix)`
  helper at the top of `pg_store_integration_test.go`
  that pre-creates the endpoint + delivery so the
  FK is satisfied (see "FK constraint catches test
  bugs early" memory entry).

### Added (webhook age envelope on endpoint secret, #147)

`webhook_endpoints.secret` was stored in plaintext
in v0.7.0. v0.7.1 moves the column to sops+age at
rest while keeping the wire-level redaction
contract (verbatim on Create, `***` on every
subsequent read).

- **Migration 0018** (destructive; live is still
  v0.4.0 so no production data to migrate):
  `ALTER TABLE webhook_endpoints RENAME COLUMN
  secret TO secret_ciphertext; ALTER COLUMN
  secret_ciphertext TYPE BYTEA USING
  secret_ciphertext::BYTEA`.
- **`SecretCipher` interface** (new in
  `internal/webhooks/secret.go`) — the seam. The
  `Store` takes a `cipher SecretCipher` at
  construction; `NewPgStore(pool, nil)` PANICS so a
  misconfigured boot is loud. `NewMemoryStore(clock,
  nil)` does NOT panic (the MemoryStore is the
  test-only path; tests use `NoopSecretCipher`).
- **`AgeSecretCipher`** — the production
  implementation. `filippo.io/age v1.3.1`,
  X25519 + ChaCha20-Poly1305. Multi-recipient
  envelope so a key rotation is a config-only
  change (`AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS`
  is a CSV of `age1...` public keys).
- **`NoopSecretCipher`** — the dev / test
  implementation. Pass-through (encrypt returns
  the plaintext as bytes, decrypt returns the
  bytes as a string). The unit tests do not
  exercise real crypto; the pg integration tests
  use the Noop cipher so the test data stays
  readable in `psql` output.
- **Config**:
  `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` (csv of
  `age1...` recipients) + `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE`
  (path to the X25519 identity file).
- **9 files** changed, +1019 / -20. **10 new
  unit tests** in `secret_test.go` (82 unit tests
  in the package, was 72). One fixup commit on
  the PR — the integration tests initially
  called `testutil.MustNewPool(t)` twice in the
  same test body. The second call deadlocked on
  the `pg_advisory_lock(42)` in `testutil/db.go`.
  Fix: reuse the first pool (`s.pool`) instead of
  acquiring a second one (see "testutil.MustNewPool
  deadlock on 2nd call" memory entry).

### Added (webhook events multi-select in UI, #150)

v0.7.0's `WebhooksView` listed the `events`
column read-only and hard-coded `events: []`
(the all-events wildcard) on Create. The wire
shapes already accepted `events: WebhookEventType[]`
on both POST and PATCH; v0.7.1 surfaces the
field on both dialogs.

- **`frontend/src/components/WebhookEventsPicker.vue`**
  (new) — grid of native checkboxes for the
  18 closed event types, grouped by entity
  (user / plan / node / host / backup / inbound).
  2 cols on mobile, 3 on desktop. The header
  badge shows "N of 18 selected" or the
  "all" wildcard when the value is `[]`.
  Contract: `value` / `change` event so it pairs
  with `useZodForm` via `setFieldValue` without
  depending on a slot inside `FormField` (which
  is built around a single-value input control).
- **`WebhooksView.vue`** — both create and
  edit dialogs render the picker below the
  url / secret / enabled fields with a
  "label and help text" header that matches
  the other `FormField` rhythm. The edit
  dialog's `editTarget` watcher hydrates
  the field with
  `events: values.events` (the server treats
  `[]` as "all" so the wire level matches
  the existing wildcard semantics).
- **i18n** (en + ru) — `webhooks.eventsPicker.heading`,
  `selectedCount`, and `groups.{user,plan,node,
  host,backup,inbound}`.
- **5 files** changed, +236 / -6 (4 src files +
  1 new component).

### Refactored (shared zod schema, #149)

The v0.7.0 `WebhooksView` inlined the create and
edit form schemas inside the view's script block.
v0.7.1 moves them to
`frontend/src/schemas/webhook.ts` so the dialogs
share a single source of truth and so future
features (e.g. event-type multi-select) extend
the schema without re-touching the view.

- **`frontend/src/schemas/webhook.ts`** (new) —
  `webhookEventTypeSchema` (z.enum of the 18
  closed types), `webhookUrlSchema` (http/https,
  10..2048), `webhookSecretSchema` (16..256),
  `webhookCreateSchema` (url, secret, enabled
  and events), `webhookUpdateSchema`
  (`.partial().strict()`; secret is
  `z.union([z.literal(''), webhookSecretSchema])
  .optional()` so an empty string still means
  "leave unchanged" in the edit dialog).
- **`frontend/src/schemas/index.ts`** re-exports
  the new module alongside `user` / `host` /
  `inbound` / `node` / `panelcfg`.
- **`WebhooksView.vue`** drops the `zod` import
  and the inline `createSchema` / `editSchema`
  constants.
- **Side benefit** — the edit form's secret
  field is now validated with
  `webhookSecretSchema`'s 16-character minimum.
  The previous inline schema was
  `z.string().optional().or(z.literal(''))` with
  no length check, which was a latent bug —
  a 1-character secret would have passed
  frontend validation and round-tripped to the
  Go backend. The view's submit handler still
  skips empty strings, so the "leave unchanged"
  path is preserved.
- **3 files** changed, +123 / -29 (refactor,
  no wire-level change).

### Changed (Go+frontend dependency batch + docs sync, #141, #142, #143, #144, #145)

Five sequential PRs (post-v0.7.0) brought every
dependency that was on its previous major /
minor track forward.

- **`chore(deps): bump Go minors` (#141)** —
  `prometheus/client_golang 1.20.5 → 1.24.1`,
  `caarlos0/env/v11 11.2.2 → 11.4.1`,
  `zerolog 1.33.0 → 1.35.1`. 3 explicit + 7
  indirect minor bumps. 0 source code changes.
- **`chore(deps): bump pinia to 4.0.2 and add
  @vue/devtools-api` (#142)** — `pinia
  3.0.4 → 4.0.2`, added `@vue/devtools-api
  ^8.2.1` (pinia 4 peer dep; was transitive
  before). Hit the pnpm-store artifact conflict
  footgun: `node_modules/.pnpm/` from a previous
  pnpm run made `npm install` skip lockfile
  regeneration.
- **`chore(deps): bump vue-tsc to 3.3.8 + fix
  WebhooksView DataTable prop names` (#143)** —
  `vue-tsc 2.x → 3.3.8`. The TS strictness
  upgrade caught a pre-existing prop-name
  mismatch in `WebhooksView.vue`'s `DataTable`
  usage (was passing `empty-message` /
  `loading-message` as data props; the actual
  prop names are `loading` / `empty-key`).
  Fixed in the same PR.
- **`chore(deps): bump vue-i18n to 11.4.8` (#144)**.
- **`docs: sync to v0.7.0 and the post-v0.7.0
  4-PR dependency batch` (#145)** — refreshed
  README / ROADMAP / KNOWN_LIMITATIONS /
  docs/api/index.md / docs/guide/architecture.md
  / CONTRIBUTING.md to reflect v0.7.0 and the
  pre-v0.7.1 dep batch.

## [0.7.2] - 2026-08-02

Three-PR audit-batch closeout. v0.7.2 is purely
internal: no API surface change, no migration
change, no operator-facing configuration change.
The release closes the remaining two findings
from the 2026-08-01 colleague review (audit #1
and audit #2). The package-level `internal/app`
and `internal/cores/builder` are new, the
`cmd/aegis/main.go` shed 556 lines, and the
end-to-end panel→agent pipeline is now exercised
by a real-Postgres integration test.

### Added (real BatchedApplier FlushFn + Enqueue, #157)

The v0.4.0-mvp-batched `cores.BatchedApplier`
shipped with a no-op FlushFn AND Enqueue was
never called outside tests. v0.7.2 wires the
v0.5.0 real path end-to-end:

- New `internal/cores/builder/builder.go`:
  `BuildCoreConfigForNode(ctx, src, nodeID)`
  turns the panel's inbounds table into a
  `cores.CoreConfig` (disabled inbounds
  skipped; nil `Params` maps to an empty map).
  `NewFlushFn(src, renderer, nodeID, name)`
  returns the per-node closure the
  BatchedApplier calls: `Build → Render → Apply`
  with structured error logging.
- `users.Service.WithBatchApplier(map)` +
  `enqueueUserDelta(Delta)` fan out to every
  registered applier. `Create` → `DeltaAddUser`,
  `Update` → `DeltaAddUser` OR
  `DeltaSetLimit{Bytes: TrafficLimitBytes}`
  (JSON `{"bytes": <int64>}` payload) when
  `in.TrafficLimitBytes` is the only changed
  field, `Delete` → `DeltaRemoveUser`,
  `RotateSubToken` → `DeltaAddUser`.
- `inbounds.Service.WithBatchApplier(map)` +
  `enqueueForNode(nodeID, kind)` narrows the
  fan-out to the single applier for the
  inbound's node (the inbound already carries
  the node reference). `Create`/`Update` →
  `DeltaAddUser{UserID: uuid.Nil}`,
  `Delete` (with pre-fetch of `prev.NodeID`)
  → `DeltaRemoveUser{UserID: uuid.Nil}`. The
  `UserID: uuid.Nil` on inbound deltas is the
  BatchedApplier's coalescing contract:
  "inbound change" is not user-scoped, and
  the appliers' last-write-wins under
  `uuid.Nil` collapses multiple inbound CRUD
  events in the same window to one flush.
- `App.BatchedAppliers map[uuid.UUID]*cores.BatchedApplier`
  and `App.AddNodeBatchedApplier(ctx, nodeID, name, flushFn)`
  (registers the per-node applier, spawns the
  `Run` goroutine, owns the cancel funcs).
  `App.Close()` cancels every BatchedApplier
  goroutine alongside the existing webhook
  worker cancel and pg pool close.
- `cmd/aegis/main.go` `singboxWiring` now takes
  `*app.App` and gates on
  `cfg.BatchedApplierEnabled`. The two
  `WithBatchApplier` calls run BEFORE the
  per-node loop so a Create handler that fires
  during boot enqueues into a fully-built map.
  The flag-off path returns nil after
  `Configure()`: no appliers, no goroutines,
  no fan-out (operator escape hatch for
  Ansible/Terraform-managed installs).
- New env var `AEGIS_BATCHED_APPLIER_ENABLED`
  (default `true`).

Phase 1 caveat (documented in PR body): the
sing-box renderer is single-user per inbound
(operator's credential in `inbound.Params`).
The FlushFn re-renders the same config on
every flush until the inbound-templates work
lands. The infrastructure
(BatchedApplier + Enqueue + FlushFn) is the
deliverable; a future Phase 2 PR fills in
the per-user mapping. The agent's diff is
what determines whether the file on disk
actually changes.

### Added (end-to-end integration test, #158)

`backend/internal/cores/builder/flushfn_integration_test.go`
behind `//go:build integration`. Self-skips
when `INTEGRATION_DATABASE_URL` is unset
(local `go test ./...` skips; CI's backend
job runs it). The headline test
`TestIntegration_EndToEnd_RealPgCreateUserTriggersApply`
drives a real `users.Service.Create` against
a real pg (via `testutil.MustNewPool`); the
post-commit enqueue reaches the per-node
BatchedApplier; the 200ms window fires; the
FlushFn re-renders the sing-box config
(reading through the inbounds PgStore); the
fake agent receives a POST /v1/apply whose
JSON envelope contains exactly the vless
inbound we seeded with the UUID we put in
`inb.Params`. The test pins the panel→agent
wire contract end-to-end. The earlier
`flushfn_smoke_test.go` covers the
MemoryStore path (no pg); the new test is
the only place a "the panel wrote to pg and
the FlushFn picked it up via SELECT"
regression surfaces.

### Changed (composition root extracted from main.go, #156)

`cmd/aegis/main.go` went from 728 lines to
199 (the audit #1 "God-object main.go" fix).
The composition root moved to a new
`internal/app` package exposing a single
`Build(ctx, cfg) (*App, error)` plus an
`App.Close()`. The pattern matches the wire
sweet-spot (too small for google wire's
codegen payoff, just right for a generic
`MustBuild[T]` helper with a
`StoreBuilder[T]` struct + centralized
production-vs-memory check). 11 services are
wired through `MustBuild` (auth, nodes,
inbounds, hosts, users, plans, subscription,
panelcfg, audits); two are one-offs (webhooks
for the age cipher dependency, backups for
the OSBackend). The `internal/app/app_test.go`
smoke verifies every service handle is wired
with all-memory backends and that
`App.Close()` is idempotent.

`cmd/aegis/main.go` keeps only the cmd-level
concerns: logger setup, subcommand dispatch
(`aegis migrate`, `aegis admin`), the singbox
wiring path, signal handling, and graceful
shutdown. `router.Build` now takes a
`ctx context.Context` as the first parameter
(used for the `panelcfgSvc.GetActive` read at
the rotated sub_path mount, was hardcoded
`context.Background()`); the boot context
applies, so a SIGINT during boot aborts the
read.

### Closed (audit batch, 2026-08-01 colleague review)

The 2026-08-01 colleague review raised six
findings. v0.7.1 closed audit #3 (UI tests via
PR #155), audit #4 (promptPassword echo via
PR #154), and audit #6 (state enum regression
guard via PR #153). v0.7.2 closes the remaining
two:

- **#1 God-object main.go** — closed by #156.
  `main.go` is now 199 lines (was 728). The
  composition root, the per-service store
  selector, and the cross-cutting wiring
  (webhooks worker, batched appliers) live on
  `*app.App` with a clean lifecycle in
  `App.Close()`.
- **#2 BatchedApplier no-op stub** — closed by
  #157 + #158. The FlushFn now re-renders
  the node config and POSTs it to the agent.
  Enqueue is called from every user/inbound
  mutation. A real-Postgres integration test
  pins the end-to-end pipeline.
- **#5** — was a numbering artifact (the
  colleagues' review went #1, #2, #3, #4, #6
  with no #5). No action required.

### Changed (Go+frontend dependency batch, post-v0.7.1)

The PRs in this batch landed on `main`
*after* the v0.7.1 git tag and are picked
up by v0.7.2. None are application-code
changes; all are infrastructure or
regression-guard fixes:

- `fix(nodes): pin State enum to migration
  0006 with a regression guard` (#153,
  closed audit #6) — already in v0.7.1
  CHANGELOG; cross-referenced here for
  completeness.
- `fix(cli): suppress echo on aegis admin
  password prompts` (#154, closed audit #4)
  — `golang.org/x/term v0.45.0`; the
  `promptPassword` helper opens `/dev/tty`
  directly and calls `term.ReadPassword` so
  the kernel toggles `ECHOCTL`/`ICANON`. The
  non-tty path keeps the legacy
  `bufio.Reader` for the `aegis admin add`
  automation in `deploy/ansible/`.
- `test(ui): add vitest suite for zod schemas`
  (#155, closed audit #3) — 38 vitest tests
  across `primitives.ts` + `user.ts` +
  `webhook.ts`; `npm run test` uncommented
  in `.github/workflows/ci.yml`.
- `refactor(backend): extract internal/app.Build
  from main.go` (#156) — the audit #1 fix
  described above.
- `feat(cores): real BatchedApplier FlushFn +
  Enqueue` (#157) — the audit #2 fix
  described above.
- `test(cores): end-to-end integration test
  for BatchedApplier + FlushFn` (#158) —
  the test that closes audit #2 end-to-end.

### Fixed (gofmt nit, #158)

`backend/internal/cores/builder/flushfn_integration_test.go:267`
was not aligned to gofmt's preferred column.
Amended + `gofmt -w` + force-push. The CI's
golangci-lint + gofmt job is the canonical
formatter; local `gofmt -w` after every
test file edit is the right pattern.

### Not changed (v0.7.2 vs v0.7.1)

- **No API surface change.** `docs/openapi.yaml`
  is still at `0.7.0`. The `/webhooks/*`,
  `/plans/*`, `/users/*`, `/hosts/*`, and
  `/nodes/*` shapes are byte-for-byte
  identical between v0.7.1 and v0.7.2. The
  frontend `npm run codegen:check` job
  passes without a regeneration.
- **No migration change.** `migrations/0001..0018`
  is byte-for-byte identical between v0.7.1
  and v0.7.2. The schema-version string in
  the audit_log row is unchanged.
- **No operator-facing configuration change.**
  The only new env var is
  `AEGIS_BATCHED_APPLIER_ENABLED` (default
  `true`), and it is opt-out for operators
  who run an external config manager
  (Ansible, Terraform) and want to prevent
  the panel from clobbering the
  externally-managed config.

## [0.8.0] - 2026-08-02

v0.8.0 is the **Phase 2 multi-user sing-box render
milestone** plus a frontend dependency batch
and the audit-log call-site wiring. The
production API surface is unchanged (the
OpenAPI spec is still at `0.7.0`); every
change is either internal infrastructure or
new migration tables that the admin surface
will query in a follow-up HTTP PR. v0.8.0 is
**end-to-end multi-user**: an operator can
issue per-(user, inbound) credentials via the
admin surface (the HTTP layer is the next
slice), and the running config + the per-user
sub URL both pick up the per-user credential
automatically. The sing-box renderer emits
multi-user `users: [...]` arrays; the
BatchedApplier fan-out is narrowed by the
user's `HostsAllowlist` / `Blocklist`; the
phase 1 (single-operator-credential) path is
preserved as the fallback when a user has no
per-inbound credential yet.

The 9 PRs:

- **#159** `chore(frontend-deps): bump ts/types toolchain` — `@types/node` 22.12.0 → 26.1.2; `@vue/tsconfig` 0.7.0 → 0.8.1; `typescript` 5.6.3 → 5.8.3 (forced by the 0.8.x peer dep that enables `libReplacement: false`); `prettier` 3.4.1 → 3.9.6; `globals` 17.7.0 → 17.8.0. Also fixed two latent type errors in `PlansView.vue` (the `noUncheckedIndexedAccess` strictness tightened by the bump) and the README.md MD004 dash→plus bullet-style fix.
- **#161** `chore(frontend-deps): bump css/sass toolchain` — `autoprefixer` 10.4.27 → 10.5.4; `postcss` 8.5.19 → 8.5.24; `sass` 1.101.0 → 1.102.0.
- **#163** `chore(frontend-deps): bump axios 1.18.1 → 1.19.0` (CVE-2026 GHSA-hmw2-7cc7-3qxx; closes the CRLF-injection path via the `form-data@^4.0.6` floor). Bundle +2.46 kB raw / +0.82 kB gzipped for the new `AxiosHeaders.parseParameters` and 520 status code support.
- **#165** `chore(frontend-deps): bump @vue/tsconfig 0.8.1 → 0.9.1 + postcss 8.5.24 → 8.5.25` — closes the `verbatimModuleSyntax` strictness without source changes (the codebase already used `import type` correctly).
- **#166** `feat(audits): wire audit_log call-sites into every mutating service` — the v0.6.0/v0.7.0 audit surface finally gets every `Create` / `Update` / `Delete` audited. `audits.RecordFromContext(ctx, svc, e)` Service-layer mirror of the existing `RecordFromRequest`; pulls actor from `auth.ClaimsFromContext`; IP/UA blank. Six services: `users`, `plans`, `nodes`, `hosts`, `inbounds`, `backups`. Pre-fetch for audit `Before` on `users.Service.Delete` + `plans.Service.Delete` (extra round-trip; same trade-off as PR #157 / PR #167). **Closes the v0.7.x KNOWN_LIMITATIONS entry "Audit log call-site wiring — v0.7.x+".**
- **#167** `feat(credentials): Phase 2 multi-user sing-box render data model` — new `user_inbound_credentials` table (migration 0019): `id UUID PK, user_id FK→users ON DELETE CASCADE, inbound_id FK→inbounds ON DELETE CASCADE, credential_value TEXT NOT NULL, created_at, updated_at, UNIQUE (user_id, inbound_id)` + 2 indexes. New `internal/credentials/` package: `Credential` struct, `Store` interface (`Insert/Update/Delete/GetByID/ListByUser/ListByInbound`), `MemoryStore` (Phase 0), `PgStore` (SQLSTATE 23505 → `ErrDuplicate`, `pgx.ErrNoRows` → `ErrNotFound`), `Service` with `Create/Get/ListByUser/ListByInbound/Rotate/Delete` + `WithAudits(svc)` setter, all mutating methods call `audits.RecordFromContext` with `credential.create` / `credential.rotate` / `credential.delete` actions. Wired into `internal/app` (`a.Credentials` field, `AEGIS_CREDENTIALS_BACKEND` env, `needsPg` registration). 24 unit tests. **Phase 2 multi-user — step 1 of 4 done.**
- **#168** `feat(cores): multi-user sing-box renderer (Phase 2 step 2)` — the sing-box renderer's per-protocol signatures take a per-(user, inbound) credential list (`renderVLESS(spec, params, users)`, `renderHY2(spec, params, users)`, `renderTrojan(spec, params, users)`). When `users` is non-empty, the renderer emits a multi-user `users: [{name, uuid|password}, ...]` array of length N. When empty (Phase 1 path), the renderer falls back to `params["uuid"]` / `["password"]` and emits a length-1 array. `renderShadowsocks` is unchanged (single-password protocol by design). New `ExperimentalInboundCredentialsKey` constant + `extractCredentialsByTag` helper (defensive: missing key, wrong-typed value, wrong-typed per-tag entry all fall through to the Phase 1 path). 5 new tests + 28 existing tests unchanged (Phase 1 path is byte-identical to v0.7.2). **Phase 2 multi-user — step 2 of 4 done.**
- **#169** `feat(credentials+cores): wire credentials through builder and narrow BatchedApplier fan-out (Phase 2 step 3)` — the panel-side wiring of Phase 2. New `ListCredentialsByInbound` source interface on `internal/cores/builder`; `BuildCoreConfigForNode` and `NewFlushFn` take `credSrc`; for every enabled inbound, the builder queries credentials, dereferences `*Credential` → value slice, populates `cfg.Experimental[ExperimentalInboundCredentialsKey]`. Per-inbound query failures are fail-soft (log + Phase 1 fallback). `users.Service.enqueueUserDelta(d, user)` filters the BatchedApplier map by `user.HostsAllowlist` and `user.HostsBlocklist`. Blocklist wins over allowlist. Empty allowlist + empty blocklist = default allow (v0.5.0 behaviour). 4 call sites updated: Create/Update/RotateSubToken pass `out`, Delete passes `cur`. New `BatchedApplier.QueueLen()` method (enqueue-pressure metric, also used by the new tests). 4 new builder tests + 5 new fan-out tests. **Phase 2 multi-user — step 3 of 4 done.**
- **#170** `feat(subscription): per-user credential render (Phase 2 step 4)` — the per-user sub URL is the per-user cabinet. `subscription.Service` gains `creds *credentials.Service` + per-render `userCreds map[inboundID]credentials.Credential` cache. `WithCreds(svc)` setter (nil-safe, mirrors `WithAudits` / `WithWebhooks` pattern). `precomputeUserCreds(ctx, u)` does ONE `ListByUser` call per render (not one per inbound). `RenderSingbox` and `RenderClash` thread the per-endpoint `userCred` into the per-protocol builders (`buildSingboxVLESS`, `buildSingboxHysteria2`, `buildSingboxTrojan`, `buildClashVLESS`, `buildClashHysteria2`, `buildClashTrojan`). Each builder uses `userCred` when non-empty, falls back to `params["uuid"]` / `["password"]` when empty. Shadowsocks unchanged. 4 new tests including the auth-boundary `TestRenderSingbox_Phase2_OtherUserCredNotLeaked`. **Phase 2 multi-user — step 4 of 4 done. End-to-end.**

What this means for operators:

- The panel can now issue per-(user, inbound) credentials via the admin surface. The HTTP layer that exposes the credential CRUD to the admin UI is a follow-up PR; v0.8.x. The `user_inbound_credentials` table is the data layer; the Service + Store are ready.
- The BatchedApplier already pulls per-user credentials into the rendered sing-box config (when the Builder is populated by a future PR that queries the credentials table; the infrastructure is in place). For now, the Builder's `credSrc` is `a.Credentials` (set in #169); the data flow is end-to-end.
- The per-user sub URL is per-user. The same `sub_token` now resolves to a config that uses the user's own UUID/password, not the operator's. The `?target=singbox` and `?target=clash` formats both honour the new per-user auth material; the `?target=base64` and `?target=html` formats are URL-list + wrapper (no auth material in the body) and are unchanged.
- The `WithCreds(nil)` setter is the v0.7.2 migration path: a panel that has not yet installed the credentials source keeps its v0.7.2 output byte-for-byte. Operators onboard users gradually, populating the credentials as they go.

What this PR does NOT ship (deferred to v0.8.x follow-ups):

- **HTTP admin surface for `user_inbound_credentials`** — the Service + Store are ready; the AdminRouter (`/api/v1/credentials/` mount) and the OpenAPI spec are a separate PR. The admin UI (Credentials tab in the user detail page) lands with the HTTP layer.
- **Host → node mapping in the Builder-side filter** — the Builder does not filter credentials by `user.HostsAllowlist` today. The user-level filter is in `users.Service.enqueueUserDelta` (which decides which nodes get a FlushFn re-render). A host-to-inbound mapping is a future PR that will let the Builder filter at render time as well.
- **Cosign sign + verify for our Docker images** — still v0.5.x follow-up. v0.7.0 closed the `latest` tag + cosign sign/verify pair for the panel and agent images, but the post-v0.7.0 workflow contract (PRs 102/103/104/111) does not yet include cosign re-signing on every release. Tracked separately.
- **JSON logs in production** — `AEGIS_ENV=production` switch is still the v0.5.x follow-up. The `internal/obs` package has the right code; the wiring in `cmd/aegis/main.go` is the one-liner that was deferred from v0.5.0. v0.8.x.
- **Smoke test on fresh VM in CI** — v0.9.0 candidate. `tools/scripts/smoke-local.sh` (PR #152) covers the local docker-compose path; a terraform + ansible + boot-log CI job is a separate work unit.
- **`internal/cabinet` (doc.go-only) end-user surface** — the per-user sub URL is the per-user cabinet for v0.8.0. A separate end-user-facing cabinet (login UI, sub URL fetch, traffic stats, plan change) is v1.2+.

## [0.8.1] - 2026-08-04

v0.8.1 is the **auto-deploy bootstrap batch**: a
shared age-encryption envelope package, a
frontend dependency CVE fix, password-based first
auth for the BYO Node flow with persistent panel
key reuse, and the matching three-way radio in the
admin UI. Net effect: an operator clicks "+ Add node",
fills in name / region / SSH address / domain,
pastes the VPS root password, clicks submit — the
panel SSHes in once, installs the agent, and
generates an ed25519 keypair that the panel
re-uses on every subsequent re-provision. The
operator never has to paste a key.

The four PRs:

- **#177** `refactor(crypto): extract internal/crypto/envelope from webhooks/secret` — the v0.7.x age cipher (`SecretCipher` interface, `AgeSecretCipher`, `NoopSecretCipher`) was webhook-specific. v0.8.1 lifts it into a shared `internal/crypto/envelope` package so any future at-rest secret can share the same age boundary and the same `AEGIS_*_SECRET_AGE_*` env vars. The webhooks `PgStore` now imports `envelope.SecretCipher` instead of declaring its own interface. The bootstrap service will use the same cipher in #179. Pure refactor: byte-for-byte identical output for every input, no schema change, no env change. 9 files changed, +578 / -536.
- **#178** `chore(frontend-deps): bump brace-expansion 5.0.8 → 5.0.9` (CVE GHSA-rgw5-rvv9-x895, ReDoS in `expand`). `package.json` `overrides` block: `^5.0.8` → `^5.0.9`. Lockfile + integrity hash updated. 2 files, +4/-4. One-line security fix; dev-only (the `expand` function is reached through `glob` → `vitest` / `eslint` tooling, not the production browser bundle).
- **#179** `feat(bootstrap): password-based first auth + persistent node SSH key` — the BYO Node flow. v0.3.0 required the operator to paste a PEM private key on the provision form. v0.8.1 adds two new modes. **First-install via password**: the operator pastes the VPS root password; the panel SSHes in once, installs the agent, and the agent switches to bearer-token auth (password is one-shot, never stored). **Persistent panel key**: on a successful password install, the panel generates an ed25519 keypair, encrypts the private half with the operator's age envelope, stores the ciphertext in a new `nodes.ssh_private_key_ciphertext` column (migration 0020), and pushes the public half to `$HOME/.ssh/authorized_keys` on the node. The next re-provision decrypts the stored key and uses it for the install — the operator never pastes anything. The wire format is the two-field XOR `ssh_private_key` / `ssh_password` (mutually exclusive, both optional; the Go provisioner picks the auth method by precedence: stored key > request key > request password). 13 files changed, +1117 / -57. Migration 0020 (BYTEA column, default empty bytes — the "no key yet" sentinel).
- **#180** `feat(ui): password / stored-key radio for the node provision form` — the matching UI. The provision dialog gets a three-way radio (key / password / stored) at the top. Conditional rendering: the key or password field appears below the radio based on the selected method. XOR + conditional-required validation in the zod schema's `superRefine`. The "Stored panel key" option is disabled for first-time installs (state `new`); it is enabled for re-provisions (state `offline`). The form's default auth method is "Stored panel key" for state `offline` (a re-provision is literally one click — no input). New i18n strings in en + ru. 4 files changed, +324 / -12.

What this means for operators:

- The BYO Node flow is now "auto-deploy": the operator does not need to paste a private key on every re-provision. The panel generates a keypair on first install, encrypts the private half with the age envelope, and re-uses the key for every subsequent install.
- The first-time install path is "paste the VPS root password" instead of "generate a key, copy it to the node, paste it in the form". The operator's mental model is "the panel does the rest" — the password is one-shot and never stored.
- A v0.3.0..v0.7.x node that was provisioned before this PR has an empty `nodes.ssh_private_key_ciphertext` and no panel key on the agent. Re-provisioning such a node on v0.8.1 takes the "operator-supplied key" path (the operator pastes their existing PEM) until a follow-up CLI command (`aegis admin node rotate-panel-key <id>`) lands; deferred.

What this PR does NOT ship (deferred to v0.8.2/v0.8.3):

- **BatchedApplier decrypt-and-use path for the stored panel key** — the applier reads `nodes.ssh_private_key_ciphertext` and decrypts via the envelope to authenticate POST /v1/apply. v0.8.x.
- **Re-provision path for v0.3.0..v0.7.x nodes** — a CLI command + UI button that generates a fresh panel key for an existing node (uses the operator's current key as the bootstrap credential, then rotates). v0.8.x.
- **Host → node mapping in the Builder-side filter** — the `user.HostsAllowlist` filter is on the BatchedApplier fan-out (post-#169). The Builder-side filter on the rendered config (so a user's render does not include credentials for inbounds on hosts the user cannot see) needs the host-to-inbound mapping to be modelled; v0.8.x.
- **"Show me the stored public key" debug surface** — the operator can paste a private key, but there is no "what is the panel's key on this node right now" debug view. The public-key fingerprint would be safe to display; deferred.
- **Merged "Add node + Provision" dialog** — the 2-step shape (Create, then Provision) is preserved. A merged dialog with the auth method radio pre-selected per state is a UX follow-up; v0.8.x.
- **shadcn-vue RadioGroup primitive** — the radio group in #180 is hand-rolled. The codebase does not yet have `RadioGroup` in `components/ui/`; adding the primitive is a separate task.
- **Cosign sign + verify for our Docker images on every release** — the initial sign + verify pair shipped in v0.7.0; the post-v0.7.0 workflow contract does not yet include re-signing. v0.8.x.
- **JSON logs in production** — `AEGIS_ENV=production` switch is still the v0.5.x follow-up. The `internal/obs` package has the right code; the wiring in `cmd/aegis/main.go` is the one-liner that was deferred from v0.5.0. v0.8.x.
- **Smoke test on fresh VM in CI** — v0.9.0 candidate. `tools/scripts/smoke-local.sh` covers the local docker-compose path; a terraform + ansible + boot-log CI job is a separate work unit.
- **`internal/cabinet` (doc.go-only) end-user surface** — the per-user sub URL is the per-user cabinet for v0.8.0. A separate end-user-facing cabinet (login UI, sub URL fetch, traffic stats, plan change) is v1.2+.

## [0.5.0] - 2026-07-30

Eight-PR operations-grade polish batch. Closes the
"v0.5.0 is the smallest surface that the soft
launch needs" target from the v0.4.0 release
notes. The release is purely additive: no API
surface change, no operator-facing configuration
change, no migration. The `secrets via sops+age`
indirection, the `backups` package + UI + CLI, the
`pre-pr.sh` local gate, and the
`install_singbox` runtime SHA-256 lookup are the
four pillars; the operator guide + security
policy + quickstart docs land the soft-launch
documentation contract; the container-wiring PR
binds #119 into the panel + agent systemd units.

### Added (operator guide + security policy + quickstart docs, #126)

- **`docs/operator-guide.md`** (new) — the
  canonical "from a fresh VPS to a panel that serves
  real users" reference. Audience, TL;DR, prerequisites,
  architecture-in-one-screen, install path, secrets
  management, first node, daily operations (backups,
  restores, rotations), disaster recovery, upgrades,
  observability, common pitfalls. Cross-links to
  `deploy/secrets/README.md` for the sops+age
  field-by-field workflow.
- **`docs/SECURITY.md`** (new) — the threat model and
  disclosure flow. Sections: reporting a vulnerability
  (GitHub Security Advisories), supported versions,
  threat model (what Aegis defends against and what it
  does not), cryptography (JWT, age, sing-box, backup
  integrity), container isolation (distroless, nonroot,
  read-only secrets.env, loopback port), privilege
  boundaries (aegis-deploy / aegis-agent / \_sing-box),
  supply chain (panel images, sing-box, sops+age), what
  to do if a compromise is suspected.
- **`docs/guide/quickstart.md`** (new) — the 5-minute
  "fresh VPS to panel running" flow. Promoted out of
  the in-line "Operator quickstart (v0.5.0+)" section
  in `getting-started.md` and expanded with the backup
  cron entry.
- **`docs/guide/getting-started.md`** — refreshed to
  the dev-stack entry (Postgres + Redis + NATS on a
  laptop). The old operator-quickstart section is
  gone; the new `quickstart.md` is the operator
  entry. Adds a `make pre-pr-install` line so
  developers hit the local CI gate on first checkout.
- **`docs/guide/index.md`** — adds links to
  `quickstart`, `operator-guide`, and `security`.
- **`docs/README.md`** — `Where to start` block
  reorders the operator path to the top (quickstart
  → operator guide → security), with the dev path
  below. The `Project status` table is updated to
  v0.5.0 reality (the v0.5.0 row is now `✅ shipped`
  with the per-component status).
- **`docs/developer/index.md`** — branch pattern
  updated (`feat/<scope>/<name>`, `chore/<scope>/<name>`,
  `fix/<scope>/<name>`, `refactor/<scope>/<name>`;
  the `develop` branch is gone). Adds a
  `Pre-PR local gate` section with the
  `tools/scripts/pre-pr.sh` scope flags. Adds a
  `Module overview` table with the new CLI binaries
  and the v0.5.0 packages.
- **`docs/ROADMAP.md`** — v0.5.0 row updated to
  `✅ shipped (#119, #120, #121, #122, #123, #124,
  #125, #126)`. The v0.5.0 scope section
  reorganised to "All eight items landed" with a
  per-item PR cross-reference; the items that did
  not land (JSON logs, cosign, VM smoke test,
  GPG-verify) are listed in a "Deferred" sub-section
  with the v0.5.x follow-up path.
- The VuePress site (`docs/.vuepress/config.ts`)
  sidebar is **not** updated — the site is local-only
  and not published until v1.0.0. The new pages are
  reachable via direct paths in the rendered HTML.

### Added (aegis-pg-backup + aegis-pg-restore CLI, #125)

- **`feat(cli): operator-side backup CLI`** —
  two new binaries under `cmd/` that call the
  `backups.Service` directly, bypassing the
  panel's HTTP surface. The canonical
  cron-friendly entry point for the
  operator's own scheduler (`crontab`,
  systemd-timer, etc.).
  - `cmd/aegis-pg-backup/main.go` (~250 LOC) —
    five subcommands: `list`, `get <id>`,
    `create [--trigger manual|scheduled]`,
    `delete <id>`, `download <id> <path>`.
    Every subcommand writes a single JSON
    value to stdout and exits 0; errors
    go to stderr in `{"error":"..."}` shape.
    Reads `AEGIS_BACKUPS_DIR` (default
    `./var/backups`) and `AEGIS_POSTGRES_DSN`
    (required for `create`). The `download`
    subcommand refuses to write into the
    backups dir itself (a typo by the
    operator would otherwise overwrite a
    managed dump with itself).
  - `cmd/aegis-pg-restore/main.go` (~200 LOC) —
    a SEPARATE binary from `aegis-pg-backup`
    so the safety boundary is enforced at
    the process level. The CLI surface is
    one positional arg (`<id>`) plus
    `--yes` / `--dry-run` flags. Two-step
    confirmation: the operator must type
    the backup id again before the
    destructive op runs. Reads
    `AEGIS_BACKUPS_ALLOW_UI_RESTORE` as a
    sanity check (the DSN is the actual
    security boundary; the flag catches
    a typo in the operator's
    `EnvironmentFile`). `--dry-run` runs
    `pg_restore --list` for an eyeball
    check.
  - `.gitignore` — added the
    `.git-commit-*.md` pattern (alongside
    the existing `.git-commit-*.txt`) so
    the commit-message draft files don't
    sneak into the working tree.

- **Why two binaries, not one with a
  `restore` subcommand:** restore is
  destructive (drops and recreates the
  target database). Keeping the binaries
  separate enforces the safety boundary at
  the process level: an operator who
  types `aegis-pg-backup restore <id>`
  gets an `unknown subcommand` error, not
  a silent data wipe. The `aegis-pg-backup`
  binary is the safe default; the
  `aegis-pg-restore` binary is the
  intentional one-off path.

- **What this PR does NOT ship** (deferred to
  follow-ups):
  - The HTTP-level restore endpoint is still
    `ScopeBackups` + `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
    gated (see #120). The CLI is the
    operator-only path; the UI path is
    intentionally NOT exposed in v0.5.0.
  - A `restore --to <timestamp>` flag
    ("point-in-time recovery from the
    archive") — would need a separate
    basebackup + WAL-replay workflow. v0.5.x
    follow-up.
  - shell completion (`complete -C
    "aegis-pg-backup list" ...`). Cosmetic
    but useful for a daily-driver CLI.

- Verification:
  - `go build ./...` — clean
  - `go vet ./...` — clean
  - `golangci-lint run ./cmd/aegis-pg-backup/
    ./cmd/aegis-pg-restore/` — 0 issues
    (errcheck, errorlint, gosec, gofmt all
    caught and fixed in the same PR cycle)
  - `go test -count=1 ./...` — all existing
    tests pass (no new test files; the
    binaries are CLI-thin and the
    underlying `backups.Service` is
    already covered by the #120 tests)

### Added (container wiring for #119 secrets, #124)

- **`chore(ops): install_panel role + prod
  compose + secrets.env mount`** — wires
  the v0.5.0 sops+age indirection from #119
  into the actual production deploy. The
  `configure_secrets` role (PR #119) writes
  `/etc/aegis/secrets.env` (mode 0600,
  owner aegis-deploy) on the panel host.
  This PR adds the three pieces that consume
  that file end-to-end:
  - `deploy/docker/docker-compose.prod.yml`
    (new, ~80 lines) — the production panel
    stack. Pulls `ghcr.io/qadversif/aegispanel:${AEGIS_PANEL_IMAGE_TAG}`
    (default `latest`), bind-mounts
    `/etc/aegis/secrets.env:ro` into the
    container via `env_file:`, bind-mounts
    `/var/lib/aegis` for the future backups
    volume, and publishes the panel's port
    8080 on the loopback only
    (`127.0.0.1:8080:8080`) — the reverse
    proxy (Caddy or any other) is the
    public ingress. The data services
    (Postgres, Redis, NATS) are NOT
    managed by this compose; the operator
    provisions them out-of-band (managed
    RDS, a sibling compose, etc.) and
    wires the panel's `aegis.postgres_dsn`
    in the sops+age secrets file.
  - `deploy/ansible/roles/install_panel/`
    (new, three files: defaults + tasks +
    handlers) — refuses to run without
    `/etc/aegis/secrets.env`, drops the
    compose file in `/etc/aegis/`, pulls
    the image, starts the stack, and
    prints `docker compose ps` as a
    summary. Idempotent: re-runs are no-ops
    (compose pull + up skip when the
    stack is already at the desired state).
  - `deploy/ansible/playbooks/panel.yml`
    (new) — the three-role canonical
    deploy for a panel host:
    `bootstrap_node` → `configure_secrets`
    → `install_panel`. Operators pin
    `aegis_panel_image_tag` in
    `group_vars/all.yml` to a stable
    release (e.g. `0.5.0` — note: no `v`
    prefix, per the release workflow
    rewrite in #111).
  - `deploy/ansible/roles/install_agent/files/aegis-agent.service`
    — added a secondary
    `EnvironmentFile=-/etc/aegis/secrets.env`
    line (the leading `-` tells systemd to
    silently skip a missing file). On
    panel hosts with `configure_secrets`
    this means the agent picks up any
    future AEGIS_* secret from the
    canonical source; on dev hosts that
    do not run `configure_secrets` the
    service is unaffected. Per-node
    values in `agent.env` still take
    precedence over panel-level values
    in `secrets.env` on a key collision
    (later env vars in the same file
    override earlier ones).

- **Why this PR does NOT also provision the data
  services:** the panel's data layer (Postgres,
  Redis, NATS) is operator-managed. The panel
  already speaks the canonical pgx DSN /
  `redis://` / `nats://` URL shape; the sops+age
  secrets file is the canonical place to set
  them. A future PR can ship a sibling
  `docker-compose.data.yml` for operators that
  want a single-host dev/prod path; v0.5.0 ships
  panel-only.

- **What this PR does NOT ship** (deferred to
  follow-ups):
  - The reverse proxy (Caddy) is still installed
    per-node by `install_caddy`. A future PR adds
    a panel-side Caddy that fronts the panel
    container on `127.0.0.1:8080`.
  - A healthcheck for the panel container (the
    distroless image has no shell; a v0.5.x
    follow-up ships a tiny healthcheck binary
    inside the image, or a sibling `wget` shim
    via buildx).
  - The `/var/lib/aegis` bind mount is reserved
    for the v0.5.x backups volume; the current
    PR mounts the directory but the panel does
    not yet write to it.

### Changed (singbox install role — runtime SHA-256 lookup, #123)

- **`chore(ops): install_singbox looks up the
  SHA-256 via the GitHub Releases API`** —
  the v0.4.0-c hardcoded `aegis_singbox_sha256`
  default is gone. Bumping `aegis_singbox_version`
  in `group_vars/all.yml` is now a one-line
  change; the role queries the GitHub Releases
  API at install time, picks the `assets[]`
  entry whose `name` matches the per-arch
  tarball, and uses the `digest` field
  (format `sha256:<hex>`) as the `get_url
  checksum:` argument.
  - `deploy/ansible/roles/install_singbox/defaults/main.yml` —
    removed `aegis_singbox_sha256`; added
    `aegis_singbox_release_api_url` (default
    `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v{{ version }}`)
    and `aegis_singbox_release_api_token`
    (optional, for rate-limit headroom on
    busy CI matrices).
  - `deploy/ansible/roles/install_singbox/tasks/main.yml` —
    replaced the `Refuse to run without a
    SHA-256 pin` assert with two tasks:
    `Look up the sing-box SHA-256 via the
    GitHub Releases API` (3 retries, 5s
    delay, optional Bearer auth) and
    `Extract the SHA-256 of the target tarball
    from the API response` (filter by name,
    strip the `sha256:` prefix, fail with
    "no asset" if the arch is missing for
    the version). The rest of the pipeline
    is unchanged — the `get_url checksum:`
    field still pins the download.
  - `docs/guide/getting-started.md` — added
    an `Operator quickstart (v0.5.0+)`
    section that walks the `playbooks/panel.yml`,
    plus `playbooks/node.yml` two-step install
    flow and points the operator at the
    sops+age indirection from #119.

- **Why no GPG / SHA256SUMS verification** —
  the original scope included a detached
  signature check. Research during this
  PR showed that SagerNet does NOT publish
  `SHA256SUMS` or detached GPG/minisign
  signatures for sing-box GitHub releases
  (the only integrity metadata is the
  per-asset `digest` field in the API
  JSON). The trust model is therefore the
  GitHub API response itself, which is
  authenticated by the standard
  `X-GitHub-...` headers and TLS. Cosign
  signing of our own Docker images
  (panel + agent) is the v0.5.x
  equivalent for the panel/agent supply
  chain and is a separate, future PR.

- Operator-visible changes:
  - No more `aegis_singbox_sha256` in
    `group_vars/all.yml`. The role no longer
    reads or writes this variable.
  - Bumping `aegis_singbox_version` is now
    a one-line change. The role fails with
    a clear error if the requested version
    does not ship the requested arch.
  - Operators running the role in a
    hermetic / air-gapped environment (no
    outbound `api.github.com`) need to
    either set `aegis_singbox_release_base_url`
    to a local mirror that also serves the
    same JSON shape, or stay on the
    v0.4.0-c hardcoded hash flow. The
    v0.5.0 release notes call this out.

### Added (pre-PR local CI gate, #124)

- **`chore(ops): tools/scripts/pre-pr.sh + pre-push
  hook + Makefile target`** — run the
  CI-equivalent checks locally before pushing a
  PR. The script catches the lint / test /
  markdown formatting failures that otherwise cost
  a 5+ minute round-trip through GitHub Actions;
  the v0.5.0 PR batch (#120, #121) shipped with
  a `fix(ci)` follow-up commit on each push
  because the local gate did not exist.
  - `tools/scripts/pre-pr.sh` — the canonical
    script. Runs:
    1. `gofmt -l backend/`
    2. `go build -trimpath ./...` (skip with
       `--quick`)
    3. `go test -short -count=1 ./...` (skip
       with `--quick`)
    4. `golangci-lint run --config .golangci.yml`
       with `GOFLAGS=-tags=integration`
    5. `npm ci` (skipped if `node_modules` is
       already present; the CI uses `npm ci` for
       a clean install)
    6. `npm run codegen:check` (openapi-typescript
       up to date)
    7. `npm run type-check` (vue-tsc)
    8. `npm run lint` (eslint + check-raw-text)
    9. `npm run build` (skip with `--quick`)
    10. `markdownlint-cli2` on `**/*.md` (fetched
        via `npx -y`; the CI pins the same version
        via the `DavidAnson/markdownlint-cli2-action@v19`
        action)
    Each step prints pass/fail with elapsed
    seconds; the failing step's stdout+stderr is
    dumped verbatim so the operator can fix and
    re-run. The final summary is green-on-red and
    the script exits non-zero on the first failure.
  - `tools/scripts/install-pre-push.sh` — installs
    `.git/hooks/pre-push` to delegate to
    `pre-pr.sh`. Idempotent (re-running rewrites
    the stub). One-line uninstall: `rm
    .git/hooks/pre-push`.
  - `Makefile` — new `pre-pr` and `pre-pr-install`
    targets (so `make pre-pr` and `make pre-pr-install`
    work alongside the existing `test` / `lint` /
    `build` targets).
  - Scope flags: `--backend`, `--frontend`,
    `--docs`, `--quick`. The default is `all`
    (everything, full set). The CI doesn't
    parallelise per-scope yet, but the flags are
    there for the day we add a pre-PR
    parallel-orchestrator.

- Out of scope (deferred to follow-ups):
  - Parallel orchestrator (e.g. `dx pre-pr
    --parallel`) — the per-scope flags are in
    place but the script is sequential today.
    The CI matrix already parallelises per
    job, so a local parallel mode is a
    convenience, not a correctness gate.
  - A pre-commit hook that runs the same gate
    on `git commit` (rather than `git push`).
    The pre-push gate is enough for the v0.5.0
    polish; a pre-commit gate would be
    annoying during a work-in-progress commit
    chain.

### Added (backups UI, #121)

- **`feat(backups): BackupsView.vue + API client`**
  — the SPA surface for the v0.5.0 backup
  package. The view is reachable from the
  sidebar under a Database icon between
  `Audit log` and `Profile`, and ships a
  toolbar with `Refresh` + `Create backup`
  actions, a six-column DataTable (id,
  createdAt, size, trigger, status badge,
  node/user/host counts), and per-row
  download + delete buttons.
  - `frontend/src/api/services/backups.ts` —
    the v0.5.0 client for the
    `/api/v1/backups/*` surface shipped in
    #120. Exports: `listBackups`, `getBackup`,
    `createBackup`, `deleteBackup`,
    `restoreBackup` (not yet wired into the
    UI; the v0.5.0 surface intentionally
    hides UI-driven restore), and
    `downloadBackup` (the blob + ObjectURL
    plus anchor.click() dance for browser-side
    file save with a Bearer-authenticated
    GET).
  - `frontend/src/views/BackupsView.vue` —
    the page component. Polls the list
    endpoint every 2 seconds while at
    least one row is in `running` status,
    so the transition to `ok` (or
    `failed`) shows up without a manual
    refresh. Failed rows expose the
    pg_dump error string as a tooltip on
    the destructive-status badge.
  - `frontend/src/router/index.ts` — new
    `/backups` route (auth-required, app
    layout) wired to the BackupsView.
  - `frontend/src/layouts/AppLayout.vue`
    — new `Backups` nav entry with a
    `Database` lucide icon, positioned
    between `Audit log` and `Profile`.
  - `frontend/src/types/aegis.ts` — new
    `Backup`, `BackupTrigger`, `BackupStatus`
    TS types mirroring the Go struct's
    wire format. The `api/client.ts`
    response interceptor already
    snake_case -> camelCases incoming
    JSON, so the UI types stay in
    camelCase while the wire stays in
    snake_case.
  - `frontend/src/i18n/locales/en.json`
    plus `ru.json` — full `backups` key
    set (title, subtitle, actions,
    statuses, triggers, error
    messages) plus a `backups` entry
    under `nav` and `profile.scopes`.

- Out of scope (deferred to follow-ups):
  - The `Restore` action is intentionally
    not in the v0.5.0 UI: a UI-driven
    restore is dangerous (it drops the
    panel DB) and the operator's safer
    path is the future `cmd/aegis-pg-restore`
    CLI binary. The endpoint is already
    wired in `api/services/backups.ts` so
    a follow-up PR can surface it behind a
    confirmation dialog without touching the
    wire format.
  - The `cmd/aegis-pg-backup` /
    `aegis-pg-restore` CLI binaries —
    the Service API is stable enough to add
    them without touching the handler or
    the wire format; this PR is the
    bookkeeping (UI + types + i18n) only.

### Added (backups package, #120)

- **`feat(backups): internal/backups package +
  admin router`** — the v0.5.0 backup surface. The
  panel can now dump its own Postgres on demand,
  keep a retention window of the most recent N
  dumps, and stream the dump back to an operator
  over the admin API. Restore is gated behind an
  explicit opt-in env var and is not exposed in
  the v0.5.0 UI (the v0.5.x follow-up #121 wires
  the button into the SPA).
  - `internal/backups/backup.go` — the canonical
    `Backup` row struct, plus `Trigger`
    (`manual`/`scheduled`) and `Status`
    (`running`/`ok`/`failed`) enums. JSON tags are
    snake_case to match the rest of the panel's
    wire format.
  - `internal/backups/store.go` — the
    `Store` interface and the v0.5.0
    `LocalStore` implementation. The metadata is
    a single `<backupsDir>/_index.json` file
    re-sorted by `CreatedAt` ascending on every
    write. The dump bytes are written and read
    via the `Backend` interface, with the
    `osBackend` rooted at `BackupsDir` rejecting
    `..`, absolute paths, and backslashes to keep
    the safety guarantees identical to a future
    S3 backend.
  - `internal/backups/service.go` — the
    orchestrator. `Create` is single-flight via
    an `inflight sync.Mutex`; a second concurrent
    caller gets `ErrBackupInProgress` (HTTP 409).
    The full lifecycle is: allocate ID → insert
    `running` row → stream `pg_dump -Fc` through
    gzip to `<id>.dump.gz` → SHA-256 the file →
    write `<id>.sha256` sidecar → update the row
    to `ok` with size, hash, and per-table counts
    → run a retention `Cleanup` pass. A failed
    Create persists the `failed` row (so the
    operator sees the failure in the UI) and
    removes the partial file.
  - `internal/backups/schedule.go` — a tiny
    custom 5-field cron parser (wildcards +
    specific values only; no `*/N` step and no
    `1-5` range in v0.5.0) and a `Service.Run`
    method that ticks every minute and fires
    `Create(TriggerScheduled)` on match. The
    scheduler is started from `main()` only when
    `AEGIS_BACKUPS_CRON` is set; an empty
    expression disables it (manual-only mode,
    the v0.5.0 default for dev).
  - `internal/backups/handler.go` — the HTTP
    surface, mounted at `/api/v1/backups` by
    `router.go`. Endpoints: `POST /` (create,
    202), `GET /` (list), `GET /{id}` (get),
    `GET /{id}/download` (stream gzip with
    `Content-Disposition: attachment`), `DELETE
    /{id}` (204), `POST /{id}/restore` (202,
    gated by `AEGIS_BACKUPS_ALLOW_UI_RESTORE`).
  - `internal/auth/scopes.go` — new
    `ScopeBackups = "backups"`. Granted only to
    the `admin` role; viewers and operators
    cannot see or touch backups.
  - `internal/router/router.go` — mounts the
    backup handler behind `authSvc.Middleware()`,
    plus `auth.RequireScope(auth.ScopeBackups)`.
  - `cmd/aegis/main.go` — constructs the
    `backups.Service` from `cfg.BackupsDir`,
    passes it to `router.Build`, and (when
    `cfg.BackupsCron != ""`) spawns the
    scheduler goroutine on a child of the
    shutdown context.
  - `internal/config/config.go` — five new
    env vars: `AEGIS_BACKUPS_DIR` (default
    `./var/backups`), `AEGIS_BACKUPS_ALLOW_UI_RESTORE`
    (`false`), `AEGIS_BACKUPS_RETENTION_DAYS`
    (30), `AEGIS_BACKUPS_MAX_COUNT` (0 = off),
    `AEGIS_BACKUPS_CRON` (empty = scheduler
    disabled).
  - `internal/router/router_test.go` —
    updated the `Build()` test helper to thread
    `nil` for the new `backupsSvc` parameter
    (the test scope is route wiring, not the
    backup surface).
  - Tests: 11 new tests across `store_test.go`,
    `schedule_test.go`, `service_test.go`
    covering LocalStore CRUD, the path-traversal
    rejection, cron parser accept/reject paths,
    service happy path, single-flight
    `ErrBackupInProgress`, dump failure, delete
    idempotency, gzip magic bytes on
    `Open()`, age-based retention, count-based
    retention, and the `ErrBackupDisabled`
    gate.

- The store is **deliberately orthogonal to
  Postgres**: a restore is exactly the case where
  the panel DB is unavailable. The JSON index
  sits next to the dumps and is self-describing
  — no separate DB query is required to know
  what files exist. Restoring from a partial
  filesystem (some dump files missing) is a
  trivial "list + filter" walk.

- The `pg_dump` subprocess is invoked with
  `-Fc --no-password --dbname=<db>` and `PGPASSWORD`
  inherited from the panel's own DSN. A custom
  5-field cron parser avoids pulling in
  `github.com/robfig/cron/v3` for one line of
  code (the only schedule the v0.5.0 operator
  will write is `0 2 * * *`).

- Restore from the UI is **off by default**. The
  v0.5.x follow-up CLI binary
  (`cmd/aegis-pg-restore`, not in this PR) is
  the only thing trusted to drop the panel DB;
  the HTTP path is the convenience surface for
  dev environments that set
  `AEGIS_BACKUPS_ALLOW_UI_RESTORE=true`.

- Out of scope (deferred to follow-ups):
  - The BackupsView.vue UI (#121) — the surface
    is implemented and curl-able, but the
    buttons and download links live in a
    follow-up PR.
  - Wiring the panel container's `--env-file`
    for `AEGIS_BACKUPS_*` (#119 follow-up
    chore) — the envs are read by the panel
    directly, the operator sets them in
    `secrets.env` after #119 lands.
  - `docs/operator-guide.md` and
    `docs/guide/quickstart.md` updates with the
    backup workflow (a follow-up alongside the
    secrets wiring chore).

### Added (secrets via sops+age, #119)

- **`chore(ops): secrets via sops+age`** —
  replaces the Phase 1 fixture-credentials-in-env
  model with a proper sops+age encrypted file
  committed to the repo. The phase 1 deploy
  shipped JWT, DB password, and admin password as
  hard-coded env vars on the VPS (`aegis-fixture-*`
  in `~/.aegis/deploy.local.md`); v0.5.0 moves
  every one of them to `deploy/secrets/secrets.yml`,
  encrypted with an operator-generated age keypair.
  - `.sops.yaml` at the repo root defines
    `creation_rules` matching `.*secrets.*\.yml$` to
    the operator's age public key. The committed
    example public key is a throwaway for the PR
    demo — operators replace it with their own via
    `sops updatekeys`.
  - `deploy/secrets/secrets.example.yml` is a
    sops-encrypted schema reference. Decrypting it
    shows the field layout (jwt_secret,
    admin_password, postgres_password, agent_bearer,
    panel_path.admin, panel_path.sub,
    dev.singbox.{version,sha256}). Operators copy
    this to `secrets.yml` (gitignored), fill in real
    values, and run `sops --encrypt --in-place`.
  - `deploy/secrets/README.md` documents the
    one-time age keygen, the field-by-field
    generation commands, the rotation procedure,
    and the security stance.
  - `deploy/secrets/.gitignore` blocks plaintext
    `secrets.yml` / `secrets.local.yml` while
    allowing the example and any future `*.enc`
    through.
  - `deploy/ansible/roles/configure_secrets/` is
    the deploy-side role that installs sops+age
    (apt or direct download) and decrypts
    `secrets.yml` to `/etc/aegis/secrets.env` (mode
    0600, owner `aegis-deploy`). The role is
    idempotent and runs a round-trip decrypt smoke
    test before declaring success. The panel
    container mounts the file at
    `/run/aegis/secrets.env` and reads it via
    `--env-file` (the env-var passthrough in
    `deploy/docker/docker-compose.dev.yml` becomes
    the only place that mentions the
    `AEGIS_*_SECRET` env names; the values move from
    being hard-coded in the compose file to
    being sourced from the env file).
  - Root `.gitignore` had a top-level `secrets/`
    rule that was a catch-all for ad-hoc local
    files. Removed in favour of the explicit
    `deploy/secrets/.gitignore` so the canonical
    `deploy/secrets/` tree can be committed.

- The `secrets.example.yml` ships ENCRYPTED, not
  plaintext. Reviewers without the matching age
  private key see only the sops metadata
  (`sops:` + `ENC[AES256_GCM,...]` blobs). The
  plaintext is documented in the file's own
  block-comment at the top, which is also encrypted;
  decrypting once with `sops --decrypt` reveals
  the schema.

- The example public key in `.sops.yaml` is a
  throwaway generated for the PR demo
  (`age1ekwhyq7xftg3vqjka4rssrg77acrsa7hjjzs2vvlugc23j3gwfpqep7ggk`).
  The matching private key is at
  `~/.aegis/test-keys/age-example.key` on the original
  author's machine only — **not** committed, **not**
  in the repo. Operators replace both with their own
  (`age-keygen -o ~/.aegis/age.key` + `sops updatekeys`).
  This is the same trust model as SSH keypairs.

- Out of scope (deferred to a follow-up):
  - Wiring the `/etc/aegis/secrets.env` mount into
    the panel container's `docker run` (the role
    writes the file; the panel's `install_panel`
    role needs to add the `--env-file` flag).
  - Wiring the same into the `aegis-agent`
    binary's systemd unit (the agent reads its
    bearer from `/etc/aegis/agent-bearer`; the
    `install_agent` role needs to copy the
    `aegis.agent_bearer` value out of
    `secrets.env`).
  - Documentation update of `docs/operator-guide.md`
    (the new doc) and `docs/guide/quickstart.md`
    with the sops+age flow.

## [0.4.0] - 2026-07-26

**Tag:** `v0.4.0` on this commit. v0.4.0 ships two parallel
work streams:

1. **v0.4.0-mvp-batched** (PRs #92 / #93 / #94) — the
   `BatchedApplier` + real apply transport + the
   `install_singbox` Ansible role. The end-to-end
   panel → aegis-agent → sing-box config write → reload
   flow ships green. Closes the v0.4.0-a / b / c
   sub-PRs.
2. **v0.4.0-d Path C** (PRs #95 / #96 / #97 / #99 / #100) —
   the user-CRUD surface moves from `internal/subscription`
   into a dedicated `internal/users` package. The
   subscription package is now a pure render
   orchestrator: zero user-CRUD surface. The d-r-series
   cuts roughly 800 lines out of subscription and
   consolidates the wire format.

### Added (v0.4.0-mvp-batched, #92 / #93 / #94)

- **`internal/cores` `BatchedApplier`** — per-node delta
  queue with `CancelReplace` semantics (an `add_user`
  followed by a `remove_user` for the same `UserID`
  within the window is a no-op). 20s window, 1000 max
  queue. `FlushFn` callback. The `cmd/aegis-agent` /
  apply transport is wired through the new
  `Provider.Configure(nodes, httpClient)` pattern. Closes #92.
- **`cmd/aegis-agent` real `/v1/apply` handler** —
  `writeAtomic` (write to temp + fsync + `os.Rename`),
  `runReload` (subprocess via `exec.CommandContext`,
  no shell — `strings.Fields(reloadCmd)`), and
  `applyEnvelope` / `applyResponse` with `reloaded: bool`,
  plus `reload_took_ms: int64`. Closes #93.
- **`deploy/ansible/roles/install_singbox/`** — pins
  sing-box 1.14.0-beta.2, hard-coded SHA-256
  `f68715815741e59f25e32904cabcd5924a0461a910d8e9c9612512b957709ef4`.
  Playbook order: `bootstrap` → `install_caddy` →
  `install_fail2ban` → `install_singbox` →
  `install_agent` → `setup_decoy` (install_singbox
  comes before install_agent because the agent's env
  file references `/etc/sing-box/config.json`). Closes #94.

### Added (v0.4.0-d, #95 / #96 / #97 / #99 / #100)

- **`internal/users` data layer** — the new home for the
  end-user CRUD surface (User + Status enum + Create /
  Update / Delete / RotateSubToken + MemoryStore +
  PgStore). 32-byte / 64-hex-char `sub_token` (d.1
  bumped from 16/32 for higher entropy). Closes #95.
- **`users.User` wire-format compat** with
  `subscription.User` — both Go types have identical
  JSON shape (snake_case fields, `[]uuid.UUID` for
  hosts allow/block lists). Makes the d.r2 → d.r3
  move possible without render-code churn. Closes #96.
- **Drop subscription-side user-CRUD** — `Store`,
  `MemoryStore`, `PgStore` no longer carry the 7
  user-CRUD methods. The 4 Service-level thin
  wrappers (`GetUserBySubToken` / `RotateSubToken` /
  `CreateUser` / `ListUsers`) carry the work
  temporarily. Closes #97.
- **Move `admin_handler.go` to `users`** — the
  user-CRUD admin surface (mounted at `/api/v1/users`)
  lives in `internal/users/admin_handler.go` now. The
  Service-level thin wrappers are gone; the render
  handler consults `*users.Service` directly for the
  sub_token lookup. Closes #99.
- **Cleanup pass + roadmap** — `DefaultSubTokenRotationGrace`
  is now a public package constant on `users` (was a
  magic-number literal). `docs/ROADMAP.md` documents
  the v0.4.0-d Path C status, v0.5.0 polish, v0.6.0 plans,
  v0.7.0 webhooks, v1.0.0-mvp-soft-launch GA, and the 9
  open-gap packages. `.markdownlint.json` disables
  `MD060` (the default "aligned" table style is fragile
  under PR review). Closes #100.

### Behaviour changes (v0.4.0-d)

- **`sub_token` is now 64 hex chars (32 bytes)**, not
  32 hex chars (16 bytes). The d.1 design bumped from
  16 bytes to 32 bytes of entropy. Existing fixtures
  in `internal/users/*_test.go` and the integration
  tests updated.
- **`RotateSubToken` grace semantics changed** —
  `grace <= 0` no longer invalidates the prev token
  immediately. The d.1 `users.Service.RotateSubToken`
  maps `grace <= 0` to the canonical 24h default
  (matching the 3X-UI convention). The pre-existing
  test that asserted the d.0 behaviour was rewritten
  as a documentation test.

### Changed (repo hygiene, post-Phase 1)

- **`chore(repo): gitignore the operator deploy
  scripts`** — the Phase 1 deploy scripts (the
  `aegis-*.{py,sh}` set under `tools/scripts/`) live in
  the repo path but are operator-only artefacts: they
  hardcode the VPS IP, the deploy-user SSH pubkey, the
  DB password, the container names, the panel sub-path,
  and the panel/UI image tags. They were untracked by
  accident (nothing matched them in `.gitignore`), so a
  future `git add tools/scripts/` could have pushed
  them to a public GH history. Two new patterns under
  `tools/scripts/`: `aegis-*.py` and `aegis-*.sh`. The
  rest of the scripts in that directory (the shared
  developer tooling: `branch-start.sh`, `release.sh`,
  `smoke-frontend.sh`, `backup.sh`, `restore.sh`) stay
  trackable. The canonical private notes for the deploy
  live in `~/.aegis/deploy.local.md` (outside the repo).
- **`chore(repo): drop the tracked stale pr-body from
  #117** — the file
  `.github/pr-body-fix-ui-runtime-api-quirks.md`
  was committed in #117. The
  `gh pr create --body-file` draft got
  `git add`-ed along with the actual code.
  The gitignore pattern
  `.github/pr-body-*.md` was planned for
  #117 as a future-proofing measure but the
  file that triggered the rule was already
  in the same squash commit. The PR
  description now lives on GitHub at #117;
  the local file is redundant. The deletion
  is folded into the same chore PR as the
  gitignore change above so #117 stops
  being a one-off exception.

### Fixed (release workflow post-v0.4.0 follow-ups, #102 / #103 / #104 / #111)

These four PRs land on `main` after the `v0.4.0` git
tag (which points to `39d4d9e`). They touch only
`.github/workflows/release.yml`; no application code
changed. Their purpose is to make the `v0.4.0`
GHCR images land in the expected state on
`workflow_dispatch` re-runs (and to leave a stable
release contract for future maintainers). Documented
under `[Unreleased]` because they are not part of
the `v0.4.0` tag itself; they will ship in `v0.4.1`
or the next release.

- **`fix(ci): lowercase the GHCR image names in
  release.yml` (#102)** — `release.yml` hardcoded
  the image paths as `ghcr.io/QAdversif/AegisPanel`
  and `ghcr.io/QAdversif/AegisPanel-ui`. The OCI
  image-spec requires the path portion (after the
  registry) to be lowercase, and buildx rejected
  the v0.4.0 release build with
  `repository name must be lowercase`. The
  `ci.yml` workflow already used the lowercase
  form (fixed in the v0.3.0 cleanup batch);
  `release.yml` was the hold-out. The two
  `QAdversif` / `AegisPanel` tokens became
  `qadversif` / `aegispanel`. Closes #102.
- **`fix(ci): allow workflow_dispatch to actually
  push in release.yml` (#103)** — the
  `release.yml` workflow gated the GHCR push
  (and the `Login to GHCR` step) on
  `github.event_name == 'push'`. On
  `workflow_dispatch` re-runs, the build steps
  `push: ${{ github.event_name == 'push' }}`
  evaluated to `false`, so the build "succeeded"
  but the registry write was a no-op. The
  `Create GitHub release` step is intentionally
  left gated on `'push'` only (a re-run is for
  re-pushing images, not re-creating the
  release). The three push/login conditions are
  extended to `push || workflow_dispatch`.
  Closes #103.
- **`fix(ci): use tag input for UI image in
  release.yml workflow_dispatch` (#104)** — the
  UI image build step hardcoded `github.ref_name`
  as the image tag. On `workflow_dispatch`,
  `github.ref_name` is the branch name (`main`),
  not the operator-supplied `tag` input, so the
  UI image ended up tagged
  `ghcr.io/qadversif/aegispanel-ui:main` instead
  of `:v0.4.0` on the v0.4.0 re-run. A new
  job-level `env.release_tag` resolves to
  `github.ref_name` on `push` and to `inputs.tag`
  on `workflow_dispatch`; the UI image tag uses
  it. The `Show tag` step echoes
  `release_tag = ${{ env.release_tag }}` for log
  visibility. Closes #104.
- **`fix(ci): explicit semver tags for panel image
  in release.yml` (#111)** — the panel
  `metadata-action` used
  `type=semver,pattern={{version}}` which only
  derives a version from the ref on `push` events.
  On `workflow_dispatch` the ref is the branch
  (`main`), the action emits no semver tags, and
  the `0.4.0` / `0.4` tags stayed on the original
  tag-push digest (acceptable for `v0.4.0` since
  the same code is on both digests; brittle for
  any future re-publish that includes an
  application-code change). A new
  `Compute release version` step derives `version`
  and `short` from `env.release_tag` (bash
  parameter expansion + `sed`) and feeds them to
  the metadata-action as `type=raw` values. The
  `latest=auto` flavor and the
  `enable={{is_default_branch}}` raw `latest` tag
  are kept. Both event paths now produce the
  same `[version, short, latest]` tag list.
  Closes #111.

## [0.3.0-mvp-byo-node] - 2026-07-23

**Tag:** `v0.3.0-mvp-byo-node` on `ba78b35` (post-cleanup-batch
HEAD). v0.3.0 ships the BYO-node bootstrap path: the
operator can provision a fresh Linux node, install the
Caddy reverse proxy + the `aegis-agent` Go binary, and
have it register with the panel — all from the panel
admin UI. Closes #67 (v0.3.0-a backend), and the
subsequent cleanup batch (PRs #74 / #75 / #76 / #77
/ #82 / #83 / #84 / #87 / #91).

### Added (v0.3.0)

- **`internal/bootstrap/`** package — SSH client (`x/crypto/ssh` +
  `pkg/sftp`), TOFU host-key policy, 32-byte bearer secret
  generation, 5-step install workflow, state machine, provisioner.
  Closes v0.3.0-a (backend). Closes #67.
- **11 reserved-package `doc.go` stubs** for the Phase 2-4 slots
  (`cabinet`, `caddy`, `cascades`, `decoy`, `events`, `mcp`,
  `notifications`, `plans`, `stats`, `subscriptions`,
  `webhooks`). Closes #77.
- **`cmd/aegis-agent` real Go binary** — replaces the
  v0.2.0 `sleep infinity` placeholder. Ansible role
  `install_agent` uploads the binary, writes
  `/etc/aegis/agent.env`, registers the systemd unit.
- **Per-node `AgentBearer` storage** — `nodes.agent_bearer`
  column (migration 0013). v0.3.0 nodes get empty bearer
  until re-provisioned; production should use Postgres
  TDE or disk encryption on the agent_bearer column.

### Fixed (cleanup batch, post-v0.3.0-a)

- **chi v5.2.4 → v5.3.1.** Replaced the deprecated
  `middleware.RealIP` (vulnerable to XFF spoofing, GHSA-3fxj-6jh8-hvhx
  family) with the chi v5.3 `ClientIPFrom*` + `GetClientIP` family.
  Closes #75.
- **`internal/audits/clientIP` re-pointed to `middleware.GetClientIP`.**
  No more local XFF parsing in the audit handler — single source
  of truth in the chi middleware. Same fixup as #75.
- **Trivy workflow: `ignorefile:` → `trivyignores:`** (the
  trivy-action input key, not the silent reject that was
  hiding the `.trivyignore` entries). Closes #74.
- **Frontend `eslint --fix`** across the six view files —
  171 auto-fixable warnings → 0. Closes #76.
- **Dependabot #68 (Go minor+patch)** superseded by #75; #69
  (frontend minor+patch) deferred to v0.4.0 cleanup window
  (transitively requires a TypeScript 5.8+ major).
- **vitest 3 → 4** (#82) — `vi.useFakeTimers` + global setup
  pattern. The vitest test suite went from 24 flaky
  tests on CI to 0 in 4.1.
- **eslint 8 → 10 flat config** (#83) — the new flat
  config file pattern. Catches plugin ordering bugs the
  legacy config silently allowed.
- **vue-router 4 → 5 + vite 6 → 7 + pinia 2 → 3** (#84) —
  the vue-router 5 breaking change is the data-loader
  pattern; pinia 3 adds the `defineStore` setup-style
  syntax that the data loaders need.
- **`.gitattributes` + `npm ci` standardisation** (#87) —
  the footgun fix that makes Windows contributors'
  CRLF/LF noise disappear; CI is now `npm ci` (not
  `npm install`).
- **vite 7.3.0 → 7.3.6** (#89) — 6 dependabot advisories
  (all `Development`-only impact).
- **brace-expansion@2 → 5 + js-yaml@3 → 4** (#90) — 3
  HIGH-severity OSV findings resolved.
- **Custom Caddy binary** (#91) — drops the upstream
  Caddy `grpc-go` CVE by patching to `v1.82.1` in a
  BuildKit-built binary. Closes the `trivy-frontend`
  HIGH findings.

### Documentation (v9.2 roadmap sync, #78)

- **ARCHITECTURE.md §21** markers synced with the code: v0.1.0
  and v0.2.0 marked `[done]`, v0.3.0 marked `[wip]`. §21
  timing table updated. New §25 entry v9.2 documenting the
  sync + the cleanup batch. See PR #78.
- **Tags created retroactively** (and pushed):
  - `v0.1.0-mvp-render` on `5840c13` (PR #50, last v0.1.0 commit).
  - `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0 commit).
- **KNOWN_LIMITATIONS.md** restructured: previously-v0.1.0
  entries that closed in v0.2.0 moved to a "Closed" section;
  v0.3.0+ open items live under the v0.3.0 heading.
- **README.md** status table, Go version, repo layout, and
  frontend view list all updated to v0.3.0-era.

## [0.2.0-mvp-agent] - 2026-07-19

**Tag:** `v0.2.0-mvp-agent` on `c2e773c` (PR #63, last v0.2.0
commit). v0.2.0 delivers the `cmd/aegis-agent` placeholder
binary, all backend handler surfaces for the v0.1.0 UI, and
the OpenAPI codegen pipeline.

### Added (v0.2.0)

- **Backend handler surfaces** for the v0.1.0 admin UI:
  - `/api/v1/panelcfg` (PR-F, #59) — sub-path rotation.
  - `/api/v1/users` (PR-G, #60) — admin user CRUD.
  - `/api/v1/hosts` (PR-H, #61) — host create/edit dialogs.
  - `/api/v1/nodes/{id}/inbounds` (PR-I, #62) — per-node
    inbounds CRUD with JSONB `params` editor.
  - `/api/v1/audits` + `/api/v1/auth/me/password` (PR-M, #66)
    — audit log read surface + operator change-password.
- **Argon2id operator CLI** (PR-J, #63) — `aegis admin add
  <user>`, `aegis admin passwd <user>`, `aegis admin list`.
  Production seed guard: `AEGIS_ENV=production` refuses to
  start with the dev seed user.
- **Per-sub_token rate limiting** (PR-K, #64) — in-memory
  token bucket with `Retry-After` header.
- **OpenAPI 3.0 codegen** (PR-L, #65) — `pnpm run codegen`
  regenerates `frontend/src/types/api.d.ts`;
  `pnpm run codegen:check` enforces byte-equality in CI.
- **Sub-token rotation + URL prefix rotation** (#47) —
  Panel-side helpers that let the operator rotate a user's
  sub-token or the panel-wide sub-path without code changes.
- **Placeholder `cmd/aegis-agent`** — `sleep infinity`
  systemd unit so the Apply path can be smoke-tested
  end-to-end without a real agent binary. Real Go binary
  ships in v0.3.0-c.

### Fixed

- **i18n coverage gap** between RU/EN locales (PR-E, #58).
- **KNOWN_LIMITATIONS.md** v0.1.0 gap list (PR-E, #58) —
  the per-scope list of what was open at v0.1.0 cut.
- **postcss 8.4 → 8.5** for GHSA-qx2v-qp2m-jg93 (#57).
- **`.gitattributes` LF policy** for Windows contributors
  (#56) — eliminates CRLF noise in CI.
- **go-chi 5.0 → 5.2.4** (#13) — security baseline.

## [0.1.0-mvp-render] - 2026-07-17

**Tag:** `v0.1.0-mvp-render` on `5840c13` (PR #50, last
v0.1.0 commit). v0.1.0 ships the renderable MVP: every
surface except the actual `Apply` call works through the
API + UI. The Apply call is a stub returning
`ErrApplyNotImplemented` — that is **OK for v0.1.0** per
the DoD in `ARCHITECTURE.md §21 / MVP-0.1`.

### Added (v0.1.0)

- **Subscription `PgStore`** (#50) — `internal/subscription/store_pg.go`
  and migration. Subscription URL endpoint works end-to-end
  against Postgres (MemoryStore still available for dev).
- **Panelcfg `PgStore`** (#50) — same package split; sub-path
  config persists in `panel_path_config` table.
- **Frontend stack** (ADR-0004, PR-B, #51) — TailwindCSS,
  shadcn-vue, Reka UI, `@tanstack/vue-table` (DataTable),
  `vee-validate`, `zod` (forms), `lucide-vue-next`.
- **DataTable + form primitives** (PR-C, #54) —
  `frontend/src/components/{Form,FormField,FormFieldError,DataTable}.vue`
  and `frontend/src/composables/useZodForm.ts` typed wrapper.
- **CRUD pages + auth flow** (PR-D, #55) — Dashboard, Nodes,
  Inbounds, Hosts, Subscription, Users, Settings, Login views
  with full create/edit/delete flows.
- **Smoke test** (`tools/scripts/smoke-frontend.sh`,
  PR-E, #58) — runs `vite preview` and validates the
  served HTML + asset graph.

### Architecture (v9 + v9.1, prereq to v0.1.0)

- **ADR-0003** (`docs/adr/0003-mvp-singbox-vertical-slice.md`)
  — sing-box is the only MVP core. Xray deferred to v2.0+.
  Batched Apply is the primary user-enforcement strategy.
- **ADR-0004** (`docs/adr/0004-frontend-ui-kit-shadcn-vue.md`)
  — shadcn-vue + Reka UI stack fix. Alternatives (NaiveUI,
  PrimeVue, Element Plus, Vuetify) considered and rejected
  with rationale.
- **ADR-0001** (`docs/adr/0001-xray-as-production-core.md`)
  marked **Superseded by ADR-0003**. Kept in-tree for history.
- **ARCHITECTURE.md v9** (`#49`) — full rewrite after the
  ADR-0001 cancellation. §21 unified roadmap is the single
  source of truth for phases. v8 (Phase 4 split roadmap +
  addendum) folded in.
- **ARCHITECTURE.md v9.1** (`#48` followup) — UI stack fix
  in §1 + §21 Phase 1 / MVP-0.1.

### Known gaps (closed in v0.2.0)

These are documented in detail in `KNOWN_LIMITATIONS.md` under
the "Closed in v0.2.0" section. Top items:

- Per-node inbounds editor (closed by PR-I, #62).
- Host create / edit dialogs (closed by PR-H, #61).
- User CRUD (closed by PR-G, #60).
- Settings UI / panelcfg HTTP (closed by PR-F, #59).
- OpenAPI codegen (closed by PR-L, #65).
- Per-sub_token rate limiting (closed by PR-K, #64).
- Argon2id operator CLI (closed by PR-J, #63).

## [0.0.1] - 2026-07-13

Pre-alpha skeleton. Architecture v7 is finalised; the code tree is in
place. Nothing is wired up to run end-to-end yet; that is Phase 0 →
Phase 1.

### Added (skeleton)
- Repository skeleton (monorepo: `backend/`, `frontend/`, `docs/`, `deploy/`).
- Backend: Go 1.22+ service skeleton (`chi`, env config, structured
  logging, healthcheck, metrics stub, initial SQL migration).
- Frontend: Vue 3 + TS + Vite admin UI skeleton (Pinia, vue-i18n
  ru/en, dashboard view).
- Docs: VuePress 2 site (local-only, not published yet).
- Dev environment: Docker Compose stack (PostgreSQL 16, Redis 7,
  NATS 2.10, ClickHouse 24, MinIO, Caddy 2).
- Deploy: Ansible roles, Caddyfile templates for panel and node
  (with decoy + masquerade ports), fail2ban jails, systemd units.
- GitHub: workflows (ci, release), dependabot, issue / PR templates,
  community health files (CONTRIBUTING, CODE_OF_CONDUCT, SECURITY).
- Tooling: `tools/scripts/{release,restore,backup,branch-start}.sh`.
- Conventional Commits template (`.gitmessage.txt`).
- Architecture document (`ARCHITECTURE.md`, 28 sections).
