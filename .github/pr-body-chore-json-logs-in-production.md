# chore(ops): JSON logs in production, hardened (v0.8.6)

## Summary

The `AEGIS_ENV=production` switch that flips zerolog from the
colourised `ConsoleWriter` to JSON has been wired since
v0.5.0-era (see `obs.ConfigureLogger()` in
`backend/internal/obs/obs.go` + the call in
`cmd/aegis/main.go:69`). What's missing is the **defensive
guard** that converts the silent-misconfig failure mode
into a loud boot-time error: a pg-backed install that
forgot to set `AEGIS_ENV` runs happily with the colourised
writer and emits log lines that a downstream log shipper
cannot parse.

This PR adds the guard in `Config.validate()`: when
`AEGIS_ENV` is the `development` default AND any
`AEGIS_*_BACKEND` is `pg`, the panel refuses to boot
with a loud, actionable error. The pure-memory dev path
(`go run ./cmd/aegis` with no env setup) is unaffected —
the development writer is exactly what a memory-only dev
install wants.

## Changes

### Backend (the real ops guard)

- `backend/internal/config/config.go`:
  - **New `Config.usesAnyPgBackend()` helper** — hard-OR
    across the eleven `AEGIS_*_BACKEND` fields
    (`Auth` / `Hosts` / `Nodes` / `Inbounds` /
    `Subscription` / `Users` / `Plans` / `Webhooks` /
    `Panelcfg` / `Audits` / `Credentials`). A single pg
    surface is enough to classify the install as
    "production-shaped" for the log-format guard.
  - **New check in `Config.validate()`** — refuses to
    boot when `cfg.Env == "development" && cfg.usesAnyPgBackend()`.
    Error message names the env var + the two fix values
    (`AEGIS_ENV=production` or `AEGIS_ENV=staging`) +
    notes that a memory-only dev install does not need
    the flag.
- `backend/internal/config/config_test.go` (new file,
  11.4 KB / 8 test functions / 18 sub-tests):
  - `TestValidate_AllMemory_DevelopmentEnv_Passes` —
    the pure-dev happy path (matches the smoke
    `TestBuild_AllMemoryBackends` shape).
  - `TestValidate_DevelopmentEnv_WithAuthPg_Refused` —
    the headline refusal case (single pg backend +
    dev default).
  - `TestValidate_DevelopmentEnv_WithAuditsPg_Refused` —
    symmetric case for the audit log backend.
  - `TestValidate_StagingEnv_WithPg_Passes` —
    explicit `staging` env value bypasses the guard
    (pre-prod drills keep the colourised writer).
  - `TestValidate_ProductionEnv_WithPg_Passes` —
    the intended prod case (guard never fires on
    `Env=production`).
  - `TestValidate_InvalidEnv_StillRefused` — the
    pre-existing env-var switch keeps working
    (regression coverage for the order of checks in
    `validate()`).
  - `TestValidate_DevelopmentEnv_WithEveryPg_Refused` —
    all-pg + dev default is the loud refusal shape.
  - `TestUsesAnyPgBackend_ExhaustiveSweep` (with 12
    sub-tests) — flips each backend field to `pg` in
    turn and asserts the helper reports `true`.
    Catches a future regression where a new
    `*Backend` field is added to `Config` but the
    helper is forgotten.

### Docs

- `docs/operator-guide.md` Logs section — expanded with
  the v0.8.6 guard documentation, the boot-time error
  message verbatim, and the three-env-values matrix
  (`production` baked by the Dockerfile, `staging` for
  pre-prod drills, `development` for memory-only dev).
- `docs/ROADMAP.md` —
  - `v0.8.5` row: ⏳ → ✅ shipped (#186).
  - New `v0.8.6` row with the guard scope + tests.
  - `v0.8.x` row: removed the "JSON logs in production"
    item (closed in v0.8.6).
- `CHANGELOG.md` — new `[Unreleased]` section for
  v0.8.6 with the `Added` / `Tests` / `Security shape`
  subsections; existing v0.8.5 content moved to a proper
  `[0.8.5] - 2026-08-05` section.
- `KNOWN_LIMITATIONS.md` — removed the "JSON logs in
  production" entry from the `Operations polish`
  section; updated the duplicate entry under
  `Out of scope` to a `closed in v0.8.6` reference
  pointing at the new files.
- `docs/openapi.yaml` — version bump `0.8.1` → `0.8.6`
  (no API change; just the spec metadata).

## Test plan

- `go test -count=1 ./internal/config/` — 8 functions /
  18 sub-tests, all green.
- `go test -count=1 ./internal/obs/ ./internal/app/` —
  pre-existing suites unaffected (memory-backend dev path
  is exactly the path the guard explicitly does NOT
  cover).
- `go build ./...` and `go vet ./...` — clean.
- `golangci-lint run --config .golangci.yml` — 0 issues.
- `npm run type-check` (frontend) — clean.
- `npm run codegen` — `api.d.ts` unchanged (no API
  change, just the spec version).
- `markdownlint-cli2` on the four edited markdown files
  — 0 errors.

## Security shape

- The guard fails closed: a pg-backed install with the
  development default cannot boot. Previous behaviour
  was to boot successfully and emit colourised
  un-parseable log lines — a silent failure mode for
  any operator running a log shipper downstream.
- The pure-memory dev path is unchanged
  (no env-var setup needed).
- The error message is loud and actionable
  (names the env var + the two fix values), so an
  operator who hits it on a fresh deploy knows
  exactly what to set.

## Operator impact

The shipped panel image already bakes
`AEGIS_ENV=production` into the Dockerfile (the
default-value approach), so production installs are
unaffected. The guard fires only on a container that
overrides the env to `development` via an env-file
entry while ALSO setting a pg backend — exactly the
silent-misconfig shape the rule is meant to catch.

For the next live deploy (currently deferred per the
user's "пока что отложим деплой" decision), the
docker-compose env_file should keep `AEGIS_ENV=production`
explicit (or rely on the Dockerfile-baked default).
The guard makes the choice explicit.

## Diff

```
 CHANGELOG.md                      | 85 +++++++++++++++++++++
 KNOWN_LIMITATIONS.md              | 22 ++--
 backend/internal/config/config.go | 43 +++++++++
 backend/internal/config/config_test.go | 374 ++++++++++++ (new)
 docs/ROADMAP.md                   |  5 +-
 docs/openapi.yaml                 |  2 +-
 docs/operator-guide.md            | 36 +++++++
 6 files changed, 552 insertions(+), 15 deletions(-)
```

## Related

- Closes the "JSON logs in production" item that was
  tracked in `docs/ROADMAP.md` (v0.8.x bucket) and
  `KNOWN_LIMITATIONS.md` (Operations polish section).
- Sets up the next v0.8.x item: cosign re-signing on
  every release (still open).
