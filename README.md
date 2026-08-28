# Aegis — VPN Control Panel

> **Aegis** is a self-hosted control panel for multi-protocol VPN
> services. **v0.8.32.1** (latest tagged release, 2026-08-27) is
> the test/CI hygiene baseline release — no image change over
> v0.8.32. Closes 6 P0/P1 issues that lived on main since v0.8.30
> (sqlfluff LT02 false-positive, testutil aegis_it race, e2e.yml
> stale-secrets silent Playwright 401, testutil `findBackendDir`
> hardcoded the old `backend/migrations` path, migration
> `strings.Index` docstring-shadowing the real `-- +migrate Up`
> marker in `0023_agentca.sql` / `0024_add_nodes_agent_transport.sql`,
> and the v0.8.32 image was built without those test/CI fixes).
> Plus the three `App.Close()` premature-`Close` bugs
> (`defer a.AgentCA.Close()` in `Build()`, `defer client.Close()`
> in the wiring helper, no `Wait()` on `BatchedApplier` goroutines)
> that the v0.8.31.1 hotfix made visible but did not fix. The
> image is `ghcr.io/qadversif/aegispanel:0.8.32.1`; the full
> release record (8 PRs) is in [`CHANGELOG.md`](./CHANGELOG.md).
> **v0.8.32** (the prior tagged release, 2026-08-25) is the
> image rebuild that ships the 3 small post-v0.8.31.1 cleanups
> (`aegis-agent` mTLS flag help text, migration files 0023+0024
> dual-section trap doc, `config.validate()` completeness) — see
> `CHANGELOG.md` §[0.8.32].
>
> **Stack:** Go 1.26+ backend, Vue 3 + TypeScript frontend
> (Vite + shadcn-vue), PostgreSQL, Caddy, fail2ban, sops+age
> secrets, cosign-signed OCI images. See
> [`ARCHITECTURE.md`](./ARCHITECTURE.md) (v9.5) for the full design
> and [`docs/adr/0003-mvp-singbox-vertical-slice.md`](./docs/adr/0003-mvp-singbox-vertical-slice.md)
> for the MVP strategy.
>
> **Gap analysis vs. roadmap:** see
> [`docs/gap-analysis-v0.8.24.md`](./docs/gap-analysis-v0.8.24.md)
> for the full Phase 0 / 0.1 / 0.2 / 0.3 / 0.4 / 1.0 status, what's
> blocking the v1.0.0-mvp-soft-launch tag, and the recommended
> Tier 1 / 2 / 3 / 4 plan to get there.

## Status

**v0.8.32.1 (shipped, 2026-08-27) — test/CI hygiene
baseline + 6 P0/P1 issues closed + `App.Close()`
shutdown order fix.** No image change over v0.8.32. PRs
\#318 (sqlfluff LT02 + testutil aegis_it race + e2e.yml
stale-secrets + testutil `findBackendDir` + migration
`strings.Index` → line-anchored regex; the v0.8.32 image
was built without these test/CI fixes), #320 (Blob
bodies preserved by the response interceptor — issue
#301), #321 (backups scheduler wired + `s.cron` read
race — issue #302), #322 (per-user credentials in
base64 sub + HostEditDialog non-editable endpoint
fields — issues #303 + #304), #323 (migrator journal
atomic with schema + 0013 markers — issues #306 + #307),
#324 (three `App.Close()` premature-`Close` bugs: defer
in `Build()`, defer in wiring helper, no `Wait()` on
`BatchedApplier` goroutines). All 22 CI checks green on
both runners. Memory: `v0.8.32.1 tag` anchor. The release
record is in [`CHANGELOG.md`](./CHANGELOG.md) §[0.8.32.1].

**v0.8.32 (shipped, 2026-08-25) — 3 small post-v0.8.31.1
cleanups.** Image rebuild. The 3 fixes are: `aegis-agent`
mTLS flag help text corrected, migration files
`0023_agentca.sql` + `0024_add_nodes_agent_transport.sql`
dual-section trap documented (with `sed` snippet in
`docs/operator-install.md`), and `config.validate()`
completeness (the v0.8.28 prod env shipped with two env
vars that should have been required but were silently
tolerated). No new endpoints, no new env vars, no
schema changes.

**v0.8.31.1 (shipped, 2026-08-25) — 3 v0.8.30/31
mTLS install-pipeline hotfixes baked in.** Image rebuild.
The hotfixes close the v0.8.30/31 mTLS chicken-and-egg
(panel boots with partial schema → first query against
new column crashes) without operator intervention.
The big one: `EnsureRoot` is now called in `app.Build`,
so the agentca root CA is minted at boot and the mTLS
bootstrap stops failing with "root CA not yet
provisioned". Plus: the panel binary `//go:embed`s the
24 migration files (no more scp before every upgrade),
and the migration runner fail-loud on a partial
host-mount override.

**v0.8.27 — anti-leak infra + `release.yml` smoke gate
plus oncall runbook + recreate gap-analysis — shipped.**
The big-ticket items: anti-leak infrastructure
(AGENTS.md + `tools/scripts/check-sensitive.sh` scanner +
pre-commit hook + CI gate, PR #241); `release.yml`
hard-gate smoke test that runs before cosign re-sign
(PR #247); recreated `docs/gap-analysis-v0.8.24.md`
closing 3 broken cross-links (PR #246); the
`docs/RUNBOOKS/oncall.md` incident-response playbook
(PR #251); the `docs/RUNBOOKS/deploy.md` production-
state sync (PR #252); the gitignore
`.local/` directory (PR #249); the Go 1.26.5 → 1.26.6
govulncheck bump (PR #248); the
`tools/scripts/branch-start.sh` / `release.sh`
`--dry-run` / `--snapshot` hardening (PR #250).
v0.8.27 is the first release where the anti-leak
infrastructure gates a merge end-to-end (pre-commit +
CI + the agent's banned-list). PR #240 was the
interim `chore(release): 0.8.26` cut that carried
v0.8.25's `UploadAndSwap` closure into the docs
tag.

**v0.8.25 — `UploadAndSwap` for ETXTBSY-safe re-provision — shipped.**
Closes the last silent production bug in the v0.8.17 → v0.8.24
chain (9 bugs total, all closed across the v0.8.15..v0.8.25
releases). New `bootstrap.Client.UploadAndSwap(ctx, src, dst, mode)`
method on the SSH client; the existing `Upload` is unchanged
and remains the right call for non-binary uploads. PR #235
(squash merge `0ce05992`). CHANGELOG surgery in v0.8.25 cut
(`890ded6`).

**v0.8.24 — `BootstrapNodeProvider.Update` propagates State — shipped.**
The final piece of the silent-bug chain: the `Update` method
mutated `current.State` locally then called
`a.Svc.Update(ctx, current.ID, UpdateInput{})` with an EMPTY
`UpdateInput`. `UpdateInput` is a pointer-field struct where
nil pointers mean "leave alone", so the underlying service
update wrote nothing. v0.8.24 passes the new state via
`UpdateInput{State: &newState}`. One real-line code change + a
comment block documenting the pre-PR-#234 bug. PR #234 (squash
merge `58efd97`).

**v0.8.13 — inbound-templates + audit-3.1 fix chain — shipped.**
Two big things in one release. (1) The inbound-templates
feature (per-tenant `Params` defaults) lands end-to-end as
a 5-PR planned sequence: foundation #205 (data model +
service + handler), docs sync #209, renderer #210 (sing-box
`BuildCoreConfigForNode` reads `template.params` when
`inbound.template_id` is set), validation #211 (rejects
mismatched protocols), frontend #212 (`InboundTemplatesView`
plus Template dropdown in `InboundsView`). The first release
where a single feature landed in 5 separate PRs in a planned
sequence (foundation → docs sync → renderer → validation →
frontend) — future complex features should follow the same
pattern. (2) The audit-3.1 fix chain (PRs #214, #215, #216)
ships: HttpOnly refresh cookie on the server side, frontend
`withCredentials` + dropped localStorage + in-memory access
token, strict CSP in the panel's Caddyfile. v0.8.13 is the
first release where a single feature + a security fix chain
both shipped together. Migration `0021_inbound_templates.sql`
is the only schema change. CHANGELOG-only release cut
(PR #213, commit `ed20d2a`).

**v0.8.0–v0.8.8 — multi-user + operator-recovery batch — shipped.**
The full v0.8.x line:

- **v0.8.0** — Phase 2 multi-user sing-box render end-to-end
  (data model + renderer + builder + BatchedApplier narrow +
  per-user subscription render). The migration landscape is
  `0001..0019` here; migration `0020` lands in v0.8.1.
- **v0.8.1** — auto-deploy bootstrap batch: shared
  `internal/crypto/envelope` package
  (X25519+ChaCha20-Poly1305, multi-recipient for key rotation),
  password-based first auth for BYO Node, persistent panel
  SSH key (ed25519 + envelope encrypt + `authorized_keys` push).
  Three-way auth radio (key / password / stored) in the UI.
  Migration `0020_node_ssh_private_key.sql`. OpenAPI spec
  bumped to `0.8.1`.
- **v0.8.2** — server-side `auth.me` fix on pg backend
  (`auth.Store.GetByID` + `MemoryStore`/`PgStore` impl, closes
  the v0.8.0 `auth.me === null` 500 on pg); HTTP admin surface
  for `user_inbound_credentials` (`/api/v1/credentials/`
  mount + `ScopeCredentials` + OpenAPI + Credentials tab).
- **v0.8.3** — operator-side CLI `aegis admin node
  rotate-panel-key <node-uuid> --key <path>` for v0.3.0..v0.7.x
  nodes (generates fresh ed25519 keypair, pushes public half
  to `authorized_keys`, seals private half with the operator's
  age envelope).
- **v0.8.4** — HTTP mirror of v0.8.3: `POST
  /api/v1/nodes/{id}/rotate-panel-key` + NodesView dropdown
  entry (visible for `online`/`offline`/`draining`/`disabled`,
  hidden for `new`).
- **v0.8.5** — "Show stored key" debug surface: `GET
  /api/v1/nodes/{id}/stored-key` returns the public-key line
  - SHA-256 fingerprint (private key never leaves the panel
  process). The read is audited as `node.stored-key.read`.
- **v0.8.6** — JSON logs in production, hardened: the
  `AEGIS_ENV=production` → `zerolog.JSON` writer switch gets a
  `Config.validate()` guard that refuses to boot when
  `AEGIS_ENV` is `development` AND any `AEGIS_*_BACKEND=pg`
  (silent misconfig → loud boot error). New
  `backend/internal/config/config_test.go` with 8 test
  functions / 18 sub-tests covering the guard.
- **v0.8.7** — refresh agent bearer: `nodes.Service.RefreshAgentBearer`
  decrypts the stored panel SSH key, SSHes into the node,
  reads `/etc/aegis/agent.env`, parses `AEGIS_AGENT_BEARER`,
  updates `nodes.agent_bearer`. The recovery path for
  "agent regenerated its bearer out-of-band". 30 + 11 unit
  tests.
- **v0.8.8** — BatchedApplier 401→auto-refresh: a 401 from
  `POST /v1/apply` triggers a refresh + retry without operator
  intervention. `singbox.NodeResolver` extended with
  `RefreshBearer(ctx, id) (string, error)`. One retry only,
  no loop. 500/404 do NOT trigger refresh.

The release ladder:

| Milestone | Status | Notes |
| --- | --- | --- |
| `v0.1.0-mvp-render` | **shipped** | Renderable MVP — admin UI + subscription endpoint + sing-box (no-op core in dev) |
| `v0.2.0-mvp-agent` | **shipped** | Per-`sub_token` rate limit, OpenAPI codegen, audit log, operator CLI, per-resource handler surfaces |
| `v0.3.0-mvp-byo-node` | **shipped** | BYO Node flow: SSH probe + agent install + state machine |
| `v0.4.0-mvp-batched` | **shipped** | `BatchedApplier` + real apply transport + `install_singbox` Ansible role + `aegis-agent` writes config to disk and reloads sing-box |
| `v0.4.0-d` | **shipped** | `internal/users` data layer (d.1) + Path C consolidation (d.r1–d.r4) |
| `v0.4.0` (tag) | **shipped** | Aggregate of d.1 / d.r1 / d.r2 / d.r3 / d.r4 |
| `v0.4.0-post` | **shipped** | Release workflow fixes (#102 / #103 / #104 / #111) — no application code change |
| `v0.5.0` | **shipped** | sops+age secrets, backup/restore (pkg + UI + CLI), pre-PR gate, GitHub-API sing-box SHA-256, container wiring for secrets, operator guide + SECURITY + quickstart |
| `v0.6.0` | **shipped** | `internal/plans` — plan catalog promoted from the v0.3.0 table stub to a full CRUD surface |
| `v0.7.0` | **shipped** | `internal/webhooks` — outgoing-webhook surface with HMAC signing, retry with exponential backoff, DLQ |
| `v0.7.1` | **shipped** | Webhook call-site wiring, sops+age envelope on `webhook_endpoints.secret`, background retry worker, events multi-select, shared zod schema, plus the post-v0.7.0 Go+frontend dependency batch (#141–#144) and the docs sync (#145) |
| `v0.7.2` | **shipped** | Audit batch closeout: God-object `main.go` extracted into `internal/app.Build` (#156); real BatchedApplier FlushFn + Enqueue from user/inbound services (#157); end-to-end integration test against a real Postgres (#158) |
| `v0.8.0` | **shipped** | Phase 2 multi-user sing-box render end-to-end (#167 data model, #168 renderer, #169 builder + BatchedApplier narrow, #170 subscription per-user render); audit-log call-site wiring into every mutating service (#166); frontend dependency batch — TS / CSS / axios / vue-tsconfig / postcss (#159, #161, #163, #165) |
| `v0.8.1` | **shipped** | Auto-deploy bootstrap batch: shared `internal/crypto/envelope` package (#177 refactor); `brace-expansion` 5.0.8 → 5.0.9 CVE (#178 chore); password-based first auth + persistent node SSH key (#179 feat); three-way radio in the provision UI (#180 feat). Migration 0020 (`nodes.ssh_private_key_ciphertext` BYTEA). OpenAPI spec bumped to 0.8.1. |
| `v0.8.2` | **shipped** | Server-side `auth.me` fix on pg backend (PR #182); HTTP admin surface for `user_inbound_credentials` (`/api/v1/credentials/` mount + `ScopeCredentials` + OpenAPI + Credentials tab in the user detail page) — PR #183 |
| `v0.8.3` | **shipped** | Operator-side CLI `aegis admin node rotate-panel-key <node-uuid> --key <path>` for v0.3.0..v0.7.x nodes (PR #184) |
| `v0.8.4` | **shipped** | HTTP mirror of the v0.8.3 rotate-panel-key CLI: `POST /api/v1/nodes/{id}/rotate-panel-key` + NodesView dropdown entry (PR #185) |
| `v0.8.5` | **shipped** | "Show stored key" debug surface in NodesView: `GET /api/v1/nodes/{id}/stored-key` returns the public-key line + SHA-256 fingerprint (PR #186) |
| `v0.8.6` | **shipped** | JSON logs in production, hardened with `Config.validate()` guard (PR #187) |
| `v0.8.7` | **shipped** | Refresh agent bearer: `nodes.Service.RefreshAgentBearer` (PR #188) |
| `v0.8.8` | **shipped** | BatchedApplier 401→auto-refresh integration (PR #189) |
| `v0.8.9` | **shipped** | Release workflow hardening: cosign re-sign + verify on every release (PR #190). Pure workflow change, no code touched. |
| `v0.8.10` | **shipped** | Per-user credential filter in the Builder (PR #198) — `internal/users.Service.AllowedUsersForNode` + `internal/cores/builder.ListUsersAllowedForNode` interface + per-inbound filter inside `BuildCoreConfigForNode` (one DB round-trip per flush). Closes the v0.7.x Phase 2 multi-user TODO and unblocks the v1.0.0 GA tag. Pure backend change. |
| `v0.8.11` | **shipped** | Consolidation release closing the 3-PR gap (frontend-deps #196: `@vueuse/core` 11→14, `vite` 7→8, `jsdom` 25→30; Tailwind v4 #197: CSS-first config via `@tailwindcss/vite` plugin; PR #198 per-user credential filter). 0 backend / 0 schema / 0 env changes. |
| `v0.8.12` | **shipped** | Consolidation release closing the 3-PR gap (lint cleanup #200: `eslint --fix` on 5 target files; merged "Add node + Provision" dialog #201: `nodeAddSchema` extends `nodeCreateSchema` with `provisionNow` discriminator; shadcn-vue `RadioGroup` primitive #202: `components/ui/RadioGroup.vue` + `RadioGroupItem.vue` thin wrappers over `radix-vue`; docs closure #203). 0 backend / 0 OpenAPI / 0 env / 0 schema changes. |
| `v0.8.13` | **shipped** | Feature release: inbound-templates end-to-end (per-tenant `Params` defaults, 5-PR planned sequence — foundation #205 + docs sync #209 + renderer #210 + validation #211 + frontend #212) plus the audit-3.1 fix chain (HttpOnly refresh cookie #214, frontend `withCredentials` #215, Caddy CSP #216). First release where a single feature + a security fix chain both shipped together. Migration `0021_inbound_templates.sql` is the only schema change. |
| `v0.8.14` | **shipped** | Consolidation + security tightening release: closes the v0.8.13 backwards-compat shim that kept the refresh token in the JSON body of `/auth/login` and `/auth/refresh` (PR #217). v0.8.14+ is cookie-only — drop the `RefreshToken` field from `loginResponse`; drop the `refreshRequest` struct + the body-fallback parse in `readRefreshToken`; document the previously-undocumented `POST /api/v1/auth/logout` endpoint; regenerate `frontend/src/types/api.d.ts`. v0.8.14 is a **drop-in replacement for v0.8.13** on the server side. |
| `v0.8.15` | **shipped** | Multi-stage Dockerfile for `pg_dump` + `aegis-agent` + bootstrap `writeError` logging (PR #222, squash merge `6a46881`). Closes two silent functional gaps from v0.8.14: backups (no `pg_dump` in image → 100% failed rows) and provision (no `aegis-agent` in image → 502 on first `os.Stat` step). Runtime base switched from `distroless/static` to `distroless/base`; `+~50 MB` image size (mostly the .so tree). No OpenAPI / env / migration / UI breaking changes. |
| `v0.8.16` | **shipped** | `postgresql-client-15` + `joinHostPort` host:port parse fix (PR #224). v0.8.15 still used a symlink to the distroless `pg_wrapper` shell script → no shell in the runtime image → `pg_dump` exited 1 silently. v0.8.16 installs the real client package + handles `host:22:22` (double-colon) parse path. No OpenAPI / env / migration changes. |
| `v0.8.17` | **shipped** | `rm /usr/bin/pg_dump && cp /usr/lib/postgresql/15/bin/pg_dump /usr/bin/pg_dump` in the tooling stage of the multi-stage Dockerfile (PR #226). v0.8.16's installed symlink was still pointing at the wrapper; this commit replaces the symlink with the real binary so the runtime image has a working `pg_dump` that postgres-16 server accepts. No OpenAPI / env / migration changes. |
| `v0.8.18` | **shipped** | `Dumper` / `Restorer` interfaces + `pgDumpArgs` / `pgRestoreArgs` pure functions (PR #228). Architectural refactor: replaces the single `dumpFn` callback with consumer-side interfaces; the service holds the full DSN in `Config` and delegates extract to injected `Dumper` / `Restorer`. Production wiring installs `pgBinaries`. Fixed 3 silent bugs: full-DSN was stripped to bare db name, `pg_dump` exit code was discarded by `closeQuiet(src)`, and `pgDumpReader.Close()` now returns the subprocess exit code (no more 23-byte empty-dump files masquerading as backups). `pg_dumpArgs` is a pure table-tested function; PGPASSWORD is set via env, NEVER argv. No OpenAPI / migration / UI changes. |
| `v0.8.19` | **shipped** | `pg_dump` 15 → 16 via PGDG apt repo (PR #229). v0.8.18 fixed the silent-fail mode but the binary was still pg_dump 15 against a postgres-16 server, which fails with "server version mismatch". v0.8.19 adds the PGDG GPG key + `apt.postgresql.org` repo in the tooling stage + installs `postgresql-client-16` + copies the real binary. PGDG trust anchor removed at end of `RUN` (no third-party repos in the runtime image long-term). Live smoke on the first deploy: backup row `status="ok"`, `size_bytes=21982` (real 21KB dump, not 23 bytes). No OpenAPI / migration / UI changes. |
| `v0.8.20` | **shipped** | `bootstrap.hostKeyCallback` TOFU-policy fix (PR #230). Pre-PR the callback early-returned the strict `knownhosts.New` whenever the `known_hosts` file existed (even an empty one), which short-circuited the TOFU policy entirely. v0.8.20 lifts the TOFU logic to be the single source of truth: the `known_hosts` file is consulted INSIDE the `TofuAcceptAndAppend` callback (and inside `TofuReject`), never as an early exit. 3 regression tests: empty-file TOFU accepts, known-key accepts silently, mismatch rejects with `ErrHostKeyMismatch`. No OpenAPI / migration / UI changes. |
| `v0.8.21` | **shipped** | SSH fingerprint from binary wire format (PR #231). Pre-PR the panel used Go's `ssh.FingerprintSHA256` which (in modern Go) actually hashes the *authorized_keys* line format (`"ssh-ed25519 AAAA…\n"`), not the binary wire format. The operator's OpenSSH-generated fingerprint pin therefore mismatched the actual key. v0.8.21 adds a new `sshFingerprintWire(key) string` helper that SHA-256s `key.Marshal()` + strips trailing `=` from the base64, matching `ssh-keygen -lf` byte-for-byte. Test fixture uses a real Demo-нода public-key blob. No OpenAPI / migration / UI changes. |
| `v0.8.22` | **shipped** | `HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}` (PR #232). Pre-PR the panel accepted any of `{rsa, ecdsa, ed25519}` during SSH kex; the server's `kexinit` preferred ECDSA, the operator's ed25519 pin was rejected as "mismatch". v0.8.22 pins the algorithm to ed25519. v0.9.0 candidate: parse the algorithm from the expected fingerprint (32-byte ed25519 vs 64-byte P-256 vs 256-byte RSA-2048) and pin the algorithm list accordingly. No OpenAPI / migration / UI changes. |
| `v0.8.23` | **shipped** | `stripFingerprintPrefix(fp)` + `fingerprintEqual(a, b)` (PR #233). Pre-PR the compare was literal: `pCnGi…` ≠ `SHA256:pCnGi…` (the actual key matched the un-prefixed side, the operator's pin had the `SHA256:` prefix). v0.8.23 strips `SHA256:` / `MD5:` (case-insensitive) from both sides. 5 table-driven cases: case-insensitive, different base64, mixed prefix, MD5 prefix, unknown prefix (passes through → surfaces as real mismatch). No OpenAPI / migration / UI changes. |
| `v0.8.24` | **shipped** | `BootstrapNodeProvider.Update` propagates `State` (PR #234). Pre-PR the method mutated `current.State` locally then called `a.Svc.Update(ctx, current.ID, UpdateInput{})` with an empty struct; `UpdateInput` is pointer-field, all-nil = "leave alone" = no SQL UPDATE. v0.8.24 passes the new state via `UpdateInput{State: &newState}`. One real-line change + a comment block. No OpenAPI / migration / UI changes. |
| `v0.8.25` | **shipped** | `Client.UploadAndSwap(ctx, src, dst, mode)` for ETXTBSY-safe binary replacement (PR #235). Pre-PR the SFTP step did direct overwrite of `/usr/local/bin/aegis-agent`, which Linux refused with `ETXTBSY` (text-file-busy) on a re-provision of a running node — the agent's mmap'd text region can't be unlinked by another process. v0.8.25 splits the upload into SFTP-to-temp (`.basename.swap.<8-hex>`) + `mv -f` over the target via SSH; `rename(2)` is always permitted, the running process keeps the unlinked inode alive until it exits, the systemd `Restart=always` loop picks up the new binary. Mock seam in `installer_test.go` records `uploadSwapPaths` separately from `uploadPaths`; `TestInstaller_SuccessPath` asserts the agent binary path uses `UploadAndSwap` (regression guard). No OpenAPI / migration / UI changes. |
| `v0.8.26` | **shipped** | CHANGELOG-only release cut (PR #240) that re-anchors the v0.8.25 `UploadAndSwap` fix in the docs tag. No application code change. |
| `v0.8.27` | **shipped** | Anti-leak infrastructure (PR #241: AGENTS.md + `tools/scripts/check-sensitive.sh` scanner + pre-commit + CI gate); `release.yml` hard-gate smoke test (PR #247); recreated `docs/gap-analysis-v0.8.24.md` (PR #246); `docs/RUNBOOKS/oncall.md` (PR #251); `docs/RUNBOOKS/deploy.md` production-state sync (PR #252); gitignore `.local/` (PR #249); Go 1.26.5 → 1.26.6 govulncheck bump (PR #248); `branch-start.sh` / `release.sh` `--dry-run` / `--snapshot` hardening (PR #250). First release where the anti-leak infrastructure gates a merge end-to-end. |
| `v0.8.32.1` | **shipped** | test/CI hygiene baseline + 6 P0/P1 issues closed + `App.Close()` shutdown order fix. No image change over v0.8.32. PRs #318 (sqlfluff LT02 + testutil aegis_it race + e2e.yml stale-secrets + testutil `findBackendDir` + migration `strings.Index` → line-anchored regex; the v0.8.32 image was built without these test/CI fixes), #320 (Blob bodies preserved by the response interceptor — issue #301), #321 (backups scheduler wired + `s.cron` read race — issue #302), #322 (per-user credentials in base64 sub + HostEditDialog non-editable endpoint fields — issues #303 + #304), #323 (migrator journal atomic with schema + 0013 markers — issues #306 + #307), #324 (three `App.Close()` premature-`Close` bugs: defer in `Build()`, defer in wiring helper, no `Wait()` on `BatchedApplier` goroutines). All 22 CI checks green on both runners. |
| `v0.8.32` | **shipped** | 3 small post-v0.8.31.1 cleanups. Image rebuild. The 3 fixes are: `aegis-agent` mTLS flag help text corrected, migration files `0023_agentca.sql` + `0024_add_nodes_agent_transport.sql` dual-section trap documented (with `sed` snippet in `docs/operator-install.md`), and `config.validate()` completeness (the v0.8.28 prod env shipped with two env vars that should have been required but were silently tolerated). No new endpoints, no new env vars, no schema changes. |
| `v0.8.31.1` | **shipped** | 3 v0.8.30/31 mTLS install-pipeline hotfixes baked in. Image rebuild. The hotfixes close the v0.8.30/31 mTLS chicken-and-egg (panel boots with partial schema → first query against new column crashes) without operator intervention. The big one: `EnsureRoot` is now called in `app.Build`, so the agentca root CA is minted at boot and the mTLS bootstrap stops failing with "root CA not yet provisioned". Plus: the panel binary `//go:embed`s the 24 migration files (no more scp before every upgrade), and the migration runner fail-loud on a partial host-mount override. |
| `v0.8.28.6` | **shipped** | No image change — pure ops + code-quality batch. The `ghcr.io/qadversif/aegispanel:0.8.28` image is the same as v0.8.28; this entry covers a migration applied to the live prod database and a series of code-quality / infra PRs. C1-C4 of issue #289 (cross-user credential cache leak, `Subs.WithCreds` wiring, agent response body leak, migration 0022 path_check relaxation) closed in PRs #291-#298; hand-rolled JSON escapers × 11 handler files (#290 D3) replaced with `internal/httpjson` in PR #300; compose install contract + idempotent wrapper + privacy rules (#297); `NodeProvisionResponse` type dedup (#299). |
| `v0.8.28` | **shipped** | Tier 3 dialog extraction closeout (PRs #254-#270) + Tier 1 #3 (backup cron) closeout (PRs #273-#275) + anti-leak infra hardening (PR #272). 5 dialog-extraction PRs (#265-#269) split HostsView and NodesView into 8 self-contained dialog components under `frontend/src/views/dialogs/`; adjacent refactors `ChangePasswordRequest` dedup (#254), `window.confirm` → `ConfirmDialog` (#256), typed `as Parameters<...>` casts (#263); two perf wins (`camelizeKeys` memoization #255, new `GET /api/v1/inbounds` batch endpoint #264); 52 new vitest tests across the 8 dialog test files (39 → 91 total, PR #270). Tier 1 #3: cron parser extended to the full Vixie `N-M` / `N-M/S` / `*/S` / `N,M,K` construct set (#273); 33 scheduler goroutine tests across 4 new test functions (#274); admin-UI surface — `Service.ReloadCron` + `GET /api/v1/backups/schedule` endpoint + `Backups → Schedule` section in `BackupsView.vue` + 10 i18n keys + OpenAPI schema bump to `0.8.28` (#275). PR #272 adds a `ghp_` / `github_pat_` regex to `BANNED_PATTERNS` in `check-sensitive.sh` (and the AGENTS.md mirror), closing the 2026-08-20 3-PAT incident loop. 7 v0.9.1 follow-up items parked (data race in `scheduler.maybeFire`, `GET /schedule` handler tests, `scheduleActive` semantic, `POST` endpoint for hot-reload, weekly orphan-file cron, `BackupsCron` field naming, doc syntax examples). Release cut at `4a3c31a`. |
| `v0.8.x` | done | All v0.8.x-bucket items shipped: host → node mapping (PR #192), subscription URL display (PR #193), per-user credential filter (PR #198, v0.8.10+), merged "Add node + Provision" dialog (PR #201, v0.8.12+), eslint cleanup (PR #200, v0.8.12+), shadcn-vue `RadioGroup` (PR #202, v0.8.12+), inbound-templates (PRs #205/#209/#210/#211/#212, v0.8.13+), audit-3.1 fix chain (PRs #214/#215/#216, v0.8.13+), v0.8.13 body-field shim closure (PR #217, v0.8.14), v0.8.14 dialog overflow + SelectItem empty value (PRs #220/#221), v0.8.15 multi-stage Dockerfile (PR #222), v0.8.16..v0.8.25 silent-bug chain (PRs #222/#224/#226/#228/#229/#230/#231/#232/#233/#234/#235), anti-leak infra + smoke gate + oncall + recreate gap-analysis (PRs #241/#246/#247/#249/#250/#251/#252, v0.8.27+), Tier 3 dialog extraction + perf + tests (PRs #254-#270, in v0.8.28). |
| `v0.9.0` | planned | Smoke test on fresh VM in CI (terraform + ansible + boot log artifact) + restore-drill on a clean VM (download backup → restore → first-boot → panel reachable) + `release.yml` hard-gate smoke (the single most-important infra change to prevent future silent bugs). The missing pieces for the v1.0.0-mvp-soft-launch tag. |
| `v1.0.0-mvp-soft-launch` | planned | GA tag — minimum surface for the public release. v0.8.15 unblocks the code path; v0.9.0 unblocks the operational confidence. |

See [`docs/ROADMAP.md`](./docs/ROADMAP.md) for the milestone ladder,
[`CHANGELOG.md`](./CHANGELOG.md) for the per-PR release notes, and
[`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md) for the current gap
list.

## Repository layout (monorepo)

```
aegis/
├── ARCHITECTURE.md         # the design document (v9.5)
├── CHANGELOG.md            # per-version release notes (Keep a Changelog)
├── KNOWN_LIMITATIONS.md    # current gap list (v0.8.9)
├── README.md               # this file
├── LICENSE                 # AGPL-3.0
├── Makefile                # top-level orchestration
├── .gitattributes          # LF / CRLF policy (LF in repo, CRLF on .bat/.cmd/.ps1)
├── .markdownlint.json      # docs lint config
├── .markdownlint-cli2.yaml # markdownlint-cli2 scratch-file exclusions
├── backend/                # Go 1.26+ service
│   ├── cmd/
│   │   ├── aegis/          # the `aegis` panel binary (server + admin subcommands)
│   │   ├── aegis-agent/    # the per-node Go agent (writes sing-box config + reloads)
│   │   ├── aegis-pg-backup/    # operator-side backup CLI
│   │   └── aegis-pg-restore/   # operator-side restore CLI (separate binary, safety boundary)
│   ├── internal/           # 21 active packages: app, audits, auth, backups, bootstrap, config, cores, credentials, crypto, db, hosts, inbounds, migrations, nodes, obs, panelcfg, plans, ratelimit, router, subscription, users, webhooks (+ 9 doc.go-only placeholders: cabinet, caddy, cascades, decoy, events, mcp, notifications, stats, subscriptions)
│   ├── migrations/         # 20 SQL files (0001..0020; 0020 adds nodes.ssh_private_key_ciphertext)
│   └── testutil/           # shared Postgres test fixtures
├── frontend/               # Vue 3 + TS admin UI (shadcn-vue)
│   ├── src/components/ui/  # 25 base shadcn-vue components
│   ├── src/components/     # Form / DataTable / FormField (typed wrapper around vee-validate + zod)
│   ├── src/api/services/   # typed API clients (auth / backups / nodes / inbounds / hosts / users / plans / panelcfg / subscription / audits / webhooks / credentials)
│   ├── src/schemas/        # zod schemas
│   ├── src/views/          # Dashboard / Nodes / Inbounds / Hosts / Plans / Subscription / Users / Webhooks / Credentials / Backups / Settings / Audits / Profile / Login
│   ├── src/i18n/           # vue-i18n (en + ru)
│   ├── src/types/          # aegis.ts (hand mirror) + api.d.ts (codegen from openapi.yaml)
│   └── tools/scripts/      # check-raw-text.mjs (i18n lint) + check-codegen.mjs (openapi-typescript freshness)
├── deploy/
│   ├── ansible/            # bootstrap_node / configure_secrets / install_agent / install_caddy / install_fail2ban / install_singbox / install_panel / setup_decoy roles + playbooks
│   ├── secrets/            # sops+age encrypted secrets (secrets.example.yml is committed encrypted; secrets.yml is gitignored)
│   ├── docker/             # docker-compose.prod.yml.j2 template
│   └── caddy/              # Caddyfile templates
├── docs/
│   ├── adr/                # Architecture Decision Records (0001–0004)
│   ├── api/                # API reference (rendered from openapi.yaml, currently 0.8.1)
│   ├── archive/            # superseded docs (e.g. ARCHITECTURE_ADDENDUM_1)
│   ├── comparison/         # honest architecture comparisons (e.g. remnawave.md)
│   ├── developer/          # developer guide (module overview, testing, contributing)
│   ├── guide/              # rendered ARCHITECTURE.md + quickstart + getting-started
│   ├── internal/           # internal architecture deep-dives
│   ├── user-guide/         # operator-facing admin user guide
│   ├── operator-guide.md   # the canonical "fresh VPS → panel" reference (v0.8.x)
│   ├── SECURITY.md         # threat model + disclosure flow (v0.8.9)
│   ├── ROADMAP.md          # the milestone ladder (v0.8.9)
│   ├── README.md           # docs index
│   ├── KNOWN_LIMITATIONS.md  # (root) gap list
│   ├── RUNBOOKS/           # operator runbooks (deploy, restore-drill)
│   └── openapi.yaml        # OpenAPI 3.0 spec (codegen source of truth; currently 0.8.1)
└── tools/scripts/          # pre-pr.sh, install-pre-push.sh, branch-start.sh, smoke-frontend.sh, release.sh, backup.sh, restore.sh
```

## Quick start

For the **operator path** (a single VPS behind a public domain,
secrets on disk, real users, real backups) the
[**operator guide**](./docs/operator-guide.md) is the canonical
entry. The five-minute version is the
[**quickstart**](./docs/guide/quickstart.md).

For the **development path** (Postgres + Redis + NATS + panel +
UI on a laptop) the
[**getting started**](./docs/guide/getting-started.md) page is the
right entry. The TL;DR:

```bash
# 1. Clone
git clone https://github.com/QAdversif/AegisPanel.git aegis
cd aegis

# 2. Install the pre-push gate (recommended)
make pre-pr-install   # installs .git/hooks/pre-push delegating to tools/scripts/pre-pr.sh

# 3. Bring up the dev stack
make dev              # Postgres + Redis + NATS + panel + UI on :5173 (UI) / :8080 (panel)

# 4. Smoke
./tools/scripts/smoke-frontend.sh
```

Prerequisites: **Go 1.26+**, **Node.js 20+**, **npm** (the project
is standardized on `npm ci` against the committed
`package-lock.json`; `pnpm` is no longer used), **Docker 24+** and
**Docker Compose v2**, **Make**.

## What's where

- **Architecture** — [`ARCHITECTURE.md`](./ARCHITECTURE.md) (v9.5).
  Source of truth for the design.
- **Roadmap** — [`docs/ROADMAP.md`](./docs/ROADMAP.md). Milestone ladder
  with per-PR status.
- **Operator guide** — [`docs/operator-guide.md`](./docs/operator-guide.md).
  The end-to-end "from a fresh VPS to a panel that serves real users"
  flow (v0.8.x: decrypt-on-operator + manual docker compose).
- **Quickstart** — [`docs/guide/quickstart.md`](./docs/guide/quickstart.md).
  The five-minute operator path.
- **Security policy** — [`docs/SECURITY.md`](./docs/SECURITY.md).
  Threat model (v0.8.9), supply-chain trust, disclosure flow.
- **API reference** — [`docs/api/`](./docs/api/index.md). Rendered from
  `docs/openapi.yaml` (currently 0.8.1; v0.8.2..v0.8.9 did not
  change the API surface).
- **CHANGELOG** — [`CHANGELOG.md`](./CHANGELOG.md). Per-version release
  notes (Keep a Changelog format).
- **Known limitations** — [`KNOWN_LIMITATIONS.md`](./KNOWN_LIMITATIONS.md).
  Open gaps (v0.8.25) and the milestone that closes each.
- **Runbooks** — [`docs/RUNBOOKS/`](./docs/RUNBOOKS/deploy.md).
  Operator runbooks (deploy, restore-drill).
- **Developer guide** — [`docs/developer/`](./docs/developer/index.md).
  Module overview, testing, contributing.

## Contributing

- **Branch naming:** `feat/<scope>/<name>`, `fix/<scope>/<name>`,
  `chore/<scope>/<name>`, `refactor/<scope>/<name>`, `docs/<scope>/<name>`.
  Branch off `main`; `main` is the integration branch (no `develop`).
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/).
  Avoid backticks in `-m` strings (PowerShell execution policy). Multi-line
  commits: write the message to a `.git-commit-*.txt` and `git commit
  --file <path>`. Throwaway drafts are gitignored.
- **PRs:** one PR per work unit. `gh pr create --body-file
  .github/pr-body-<name>.md`. Merges use
  `gh pr merge --admin --squash --delete-branch`.
- **Pre-PR gate:** run `tools/scripts/pre-pr.sh` (or the installed
  pre-push hook) before pushing. The gate runs gofmt, golangci-lint
  v2, vue-tsc, eslint, markdownlint-cli2, `go test -short`, and
  `npm run codegen:check`. It catches ~80% of the issues that would
  otherwise bounce in CI.
- **i18n:** every user-facing string goes through `t('key')`. Run
  `node frontend/tools/scripts/check-raw-text.mjs` locally; the CI gate
  runs the same script.
- **License header** in every source file:
  `// SPDX-License-Identifier: AGPL-3.0-or-later` (Go / shell / SQL) or
  `<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->` (Vue / TS).
- **Never publish deploy URLs, server IPs, admin credentials, JWT
  secrets, or SSH key paths** in PRs, issues, or commit messages.
  Operator-only state lives in `~/.aegis/deploy.local.md` (outside
  the repo) — see the privacy note in
  [`deploy/secrets/README.md`](./deploy/secrets/README.md).

## License

AGPL-3.0-or-later. See [LICENSE](./LICENSE).

The Aegis project is single-tenant and AGPL-licensed: any operator
who runs a modified version of the panel is required to publish the
modifications. The operator's modifications and the upstream Aegis
source are both governed by this license.
