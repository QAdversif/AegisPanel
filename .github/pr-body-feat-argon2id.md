feat(auth): argon2id operator CLI + production seed guard (PR-J)

Fifth sub-PR of v0.2.0-mvp-agent. The
`internal/auth` package already shipped with
Argon2id (`alexedwards/argon2id`,
`DefaultParams`: m=64 MiB, t=1, p=4) and the
`admins.password_hash` migration column was
always declared as argon2id-encoded. PR-J
closes the operational gap: the operator can
now seed the first admin and rotate passwords
without writing SQL by hand, and the dev
seed is gated behind `AEGIS_ENV != production`
so a real install cannot boot with a
known-public credential.

## Backend

* `internal/auth/scopes.go` — adds the
  `ErrConflict` sentinel (UNIQUE-constraint
  surface) and extends the `Store` interface
  with `CreateUser`, `UpdatePassword`,
  `ListUsers`, and `LookupByUsername`. The
  new methods are the canonical surface for
  the admin CLI and the future `/admin`
  HTTP handler.

* `internal/auth/users.go` (MemoryStore) —
  implements all four new methods. The
  `CreateUser` path enforces username +
  email uniqueness (mirroring the migration
  UNIQUE indexes) and rejects empty / nil
  inputs with descriptive errors. The
  `UpdatePassword` path looks up the user
  by ID and replaces the hash, returning
  `ErrUnauthorised` on a missing user. The
  `ListUsers` path returns a sorted copy
  (deterministic for the CLI output).
  Also extends the `User` struct with
  `Email`, `Role`, `Enabled`, `UpdatedAt`
  fields (the new Store methods read /
  write them).

* `internal/auth/pg_store.go` — implements
  all four new methods on the pgx store.
  `CreateUser` catches the 23505 SQLSTATE
  from the migration's UNIQUE indexes and
  maps it onto `ErrConflict` (constraint
  name in the error message so the
  operator can debug). `UpdatePassword` is
  a single-statement UPDATE that returns
  `ErrUnauthorised` on zero rows. `ListUsers`
  is a `SELECT … ORDER BY username` round-trip.
  Also extends `LookupUser` to read the
  new `email`, `created_at`, `updated_at`
  columns so the in-memory and pgx views
  are byte-compatible.

* `internal/auth/service.go` — adds
  `Service.CreateAdmin(ctx, CreateAdminInput)`
  (the canonical entry point: hashes the
  plaintext with `HashPassword`, fills the
  default `Role = "operator"` if unset,
  delegates to `Store.CreateUser`) and
  `Service.ChangePassword(ctx, userID, plaintext)`
  (hashes the new plaintext, delegates to
  `Store.UpdatePassword`). Also adds
  `Service.LookupByUsername` and
  `Service.ListUsers` thin wrappers over
  the Store methods.

* `internal/auth/auth_test.go` — six new
  tests:

* `TestArgon2id_HashAndVerify` — pins
    the PHC string format (`$argon2id$v=19$m=...$`)
    and exercises both correct- and wrong-
    password branches. The Login integration
    test already covered the happy path; this
    test pins the format and the negative case
    directly.
* `TestArgon2id_VerifyEmptyHash` — confirms
    the verifier rejects an empty hash (the
    "first admin race" — the row exists but
    has no hash).
* `TestCreateUser_PasswordIsHashed` —
    confirms the plaintext never appears in
    the stored PHC string.
* `TestCreateUser_DuplicateUsername` —
    `ErrConflict` on a username collision.
* `TestCreateUser_DuplicateEmail` —
    `ErrConflict` on an email collision.
* `TestChangePassword` — `ChangePassword`
    hashes the new plaintext, persists it, and
    the OLD password no longer verifies.

* `cmd/aegis/main.go` — adds a new
  `aegis admin …` maintenance subcommand
  (sibling to `aegis migrate …`):

      aegis admin add    <username> --email <email> [--role <role>]
      aegis admin passwd <username>
      aegis admin list

  `add` and `passwd` prompt for the new
  password on stdin (twice for confirmation;
  min 8 chars; mismatch aborts). The CLI
  uses the same `Store` the runtime uses
  (`AEGIS_AUTH_BACKEND=memory|pg`) so the
  pg path persists via the production
  `admins` table. `list` dumps every
  user as a structured log line for
  scripting. v0.3 will add `/dev/tty` echo
  suppression for a true `passwd(1)` UX;
  v0.2 is good enough for scripted operators
  and the dev seed.

  Also gates the dev seed (`MemoryStore`
  with the `admin / aegis-dev-password`
  default user) behind `AEGIS_ENV != "production"`.
  A real install that boots in production
  with the dev seed now fails fast with a
  clear log line; non-production logs a loud
  warning. The dev seed is still useful for
  the dev workflow and CI; production must
  use the pg backend with a real operator
  minted via `aegis admin add`.

* `KNOWN_LIMITATIONS.md` — the v0.2 Argon2id
  entry now reads "closed in v0.2 (PR-J)"
  with a paragraph describing what actually
  shipped (the algorithm was already in
  place; the gap was operational tooling +
  the production seed guard).

## Quality

* `go test ./...` — clean (all 15 packages
  pass; auth has the 6 new tests, all
  green; the original 10 auth tests are
  unchanged).
* `go build ./...` — clean.
* `gofmt -l` — clean (the changed files
  were gofmt-clean on the LF view; Windows
  CRLF noise on pre-existing files is
  unchanged — see KNOWN_LIMITATIONS.md).
* `go vet ./...` — clean.
* `staticcheck ./internal/auth/...
  ./cmd/aegis/...` — clean.
* `gocritic check -enableAll` on the
  changed files — clean.
* `go build -o /tmp/aegis-test.exe ./cmd/aegis`
  followed by a manual `aegis admin` help run
  — clean (the help output matches the
  `adminUsage` text below).

## Out of scope (later PRs)

* `/admin` HTTP surface — the panel's
  UI-facing CRUD for principals. v0.3
  (lands with the audit log UI in PR-M).
  The CLI in this PR is the operator-only
  path; the UI surface reuses the same
  Service methods.
* `/dev/tty` echo suppression on the
  CLI's password prompts. v0.3 — the
  current `bufio.NewReader(os.Stdin)`
  approach works for scripted operators
  and the dev seed; a true `passwd(1)`
  UX hides the password on the terminal.
* Password rotation policy (max age,
  reuse, complexity rules). v1.0+ per
  the v9 architecture; out of scope for
  v0.2.
* Argon2id parameter upgrade path — the
  current `DefaultParams` are baked into
  every PHC string. Re-hashing on
  successful login (the standard
  approach) lands in v0.3 when the
  audit log UI is in place.
* Email verification flow — admins
  are created with a real email address
  (the column is NOT NULL) but the
  address is not verified. v0.3+.

## Refs

* `ARCHITECTURE.md` v9 §3.1 (auth
  surface — JWT + Argon2id + scope ACL)
* `docs/adr/0003-singbox-only-mvp.md`
* `KNOWN_LIMITATIONS.md` (Argon2id entry
  rewritten to "closed in v0.2 (PR-J)")
* `migrations/0001_initial.sql` (the
  `admins.password_hash TEXT NOT NULL --
  argon2id encoded` column that this PR
  fills through the new operator path)

Co-authored-by: Aegis Dev <dev@aegis.local>
