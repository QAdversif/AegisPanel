# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [Unreleased]

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
