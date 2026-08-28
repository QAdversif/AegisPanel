# Changelog

All notable changes to Aegis are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/),
and this project adheres to [Semantic Versioning](https://semver.org/).

## [0.8.32.1] - 2026-08-27

CI-hygiene baseline release. No image change — `ghcr.io/qadversif/aegispanel:0.8.32.1` is the same `0.8.32` image. This release marks the v0.8.32 test/CI surface as green and closes 6 P0/P1 issues that lived on main since v0.8.30. The next product release will be a separate tag.

### Fixed

- **CI: 4 pre-existing CI failures on main cleared (PR #318)** — sqlfluff LT02 false-positive on `0023_agentca.sql` (added to `exclude_rules`); testutil `aegis_it` race (`recreateDatabaseOnConn` retry budget 10×100ms → 30 attempts × exponential 200ms base 2s cap, plus new `probeNewDatabase` with `SELECT 1` after CREATE); e2e.yml stale-secrets silent Playwright 401 (top-of-job `${VAR:?message}` smoke check on `E2E_BASE_URL` / `E2E_ADMIN_PASSWORD` / `E2E_ADMIN_USERNAME`); testutil `findBackendDir` + `runMigrationsOnConn` hardcoded the old `backend/migrations` path (now `backend/internal/migrations/sql` per the v0.8.31.1 hotfix). Files: `.sqlfluff`, `backend/testutil/db.go`, `.github/workflows/e2e.yml`.
- **migration marker parser: docstring shadows no longer drop real SQL (PR #318)** — `internal/migrations/migrator.go:upDownBodyOf` used `strings.Index` for the literal `-- +migrate Up` substring, which silently shadowed a back-quoted reference in the docstring of `0023_agentca.sql` and `0024_add_nodes_agent_transport.sql`. The parser returned a slice of just the docstring comments and dropped the real `ALTER TABLE` on line 122. Symptom: every test against `nodes.agent_transport` failed with `column "agent_transport" does not exist`. Fix: line-anchored regex `(?m)^[ \t]*--[ \t]*\+migrate (Up|Down)[ \t]*$` (the `[ \t]*` instead of `\s*` is load-bearing — Go `\s` includes `\n`, and a greedy `\s*$` would eat the line's trailing newline and defeat the line-anchored check). Three regression tests in `migrator_test.go`: `TestUpBodyOf_IgnoresMarkerInDocstring`, `TestUpBodyOf_RealMarkerAfterDocstringShadow`, `TestDownBodyOf_IgnoresMarkerInDocstring`.
- **golangci-lint: 8 issues fixed in PR #318** — weakCond (the new regex matcher), indent-error-flow (the testutil retry/probe loop), deprecatedComment (the AgentTransport godoc), 3 gofmt, ST1005 (the AEGIS_AGENT_BINARY error string), errcheck (the dry-run fmt.Fprintf). `golangci-lint run --timeout 15m ./...` now reports 0 issues.
- **Blob bodies preserved by response interceptor (PR #320, issue #301)** — `frontend/src/api/client.ts:230-232` now skips `camelizeKeys()` for `responseType` in `{blob, arraybuffer, stream}`. The backup-download flow (`api/services/backups.ts:113-115`) uses `responseType: 'blob'`; the pre-fix code called `camelizeKeys(blob)` and returned a plain `{}` (Blob has no enumerable own properties), the caller's `URL.createObjectURL({})` threw "Invalid URL", and the download never started. Test 5 in `client.test.ts` queues a Blob through the mock adapter and asserts `response.data instanceof Blob` and that `Blob.text()` round-trips the original bytes.
- **Backups scheduler wired + `s.cron` read race closed (PR #321, issue #302)** — `internal/app/app.go:640-649` was building `backups.Config{...}` without the `BackupsCron` field, and `Service.Run(ctx, expr)` was never invoked from production code. Net effect: `AEGIS_BACKUPS_CRON` was silently ignored; no auto-backup fired in the 11 days between 2026-08-13 and 2026-08-24. Fix: pass `BackupsCron` into `backups.Config` + new step 16b in `app.Build` that calls `ParseCron` first (fail-loud on malformed schedule) and starts the scheduler goroutine (mirrors the webhook retry worker pattern in step 11: child context, cancel-func stored on `App`, cancel in `Close()`). Plus a data race in `internal/backups/schedule.go:334` — `scheduler.maybeFire` read `s.cron` for the `cron.matches(now)` call without the same mutex that `ReloadCron` and `Run` use to swap it. The race never tripped in the field because the goroutine never started, but starting it (which this PR also does) surfaces a `-race` data race. Fix: read `s.cron` under `s.mu` once at the top into a local `cron` variable.
- **Base64 subscription honours per-user credentials (PR #322, issue #303)** — `internal/subscription/render.go:RenderBase64` was the only subscription renderer that ignored the per-`{user, inbound}` credential map. The HTML subscription page (the one operators hand to users) renders URIs through `RenderBase64` and then base64-encodes the joined string for the QR code / sub URL. Pre-fix, every user's base64 sub carried the shared operator UUID/password from `inbounds.params`; a per-user revocation never cut off a user's base64 access. Fix: thread `precomputeUserCreds(ctx, u)` through `RenderBase64` → `renderEndpointURI` → `renderVLESS` / `renderHysteria2` / `renderTrojan` with the same "userCred if non-empty, else params" pattern that `RenderSingbox` / `RenderClash` already use. Two regression tests in `render_phase2_test.go`: `TestRenderBase64_Phase2_UsesUserCredential` (asserts the per-user uuid is in the URI), `TestRenderBase64_Phase2_OtherUserCredNotLeaked` (cross-user auth boundary). Side fix: `service.go:precomputeUserCreds` now logs the load-failure path with the user ID — the pre-fix comment promised "log + return empty" but the log call was missing, so every silent fallback was indistinguishable from "user has no per-inbound creds" in the operator log.
- **HostEditDialog preserves non-editable endpoint fields (PR #322, issue #304)** — `frontend/src/views/dialogs/HostEditDialog.vue:toUpdatePayload` was `v.endpoints.map(rowToEndpoint)`, and `rowToEndpoint` only emitted the 5 fields the dialog displays (nodeId, inboundId, weight, address, port). The backend's PUT semantic is "replace the endpoints bundle wholesale", so the next save silently stripped `sni[]`, `host[]`, `path`, `downloadHostId`, `protocol`, `id` from every endpoint. The dialog has no UI for those keys (they are advanced overrides set via the create dialog or a separate admin surface). Fix: `toUpdatePayload` now merges each row's editable fields with the corresponding endpoint from `current.endpoints` (the host prop) by `id` first (the stable key the backend mints) and falls back to the positional index for newly-added rows that have no server-side id yet. `endpointToRow` and `rowToEndpoint` are now narrow: row ↔ editable keys only. Regression test: `preserves non-editable endpoint fields (sni, host, path, downloadHostId, protocol, id) across save (#304)`.
- **Migrator journal atomic with schema (PR #323, issue #306)** — `internal/migrations/migrator.go:applyBody` no longer self-commits. It returns the open `pgx.Tx` to the caller so the journal update (record for Up, unrecord for Down) shares the same Tx. Pre-fix, `Up` split them across `applyBody` (which self-committed) and a separate `recordMigration(pool, ...)` `pool.Exec`; a crash between apply and record left the schema applied and the journal empty; re-boot then re-applied a non-idempotent migration. `Down` never deleted the journal row; a `down → up` sequence rolled the schema back, left the journal saying the migration was still applied, and permanently skipped the migration on the next `up`. New `recordMigrationInTx` and `unrecordMigrationInTx` helpers. Integration test `migrator_integration_test.go:TestUpDownUp_CycleClearsJournalRow_306` runs the `up → down → up` cycle against a live postgres; the load-bearing journal-empty check after `Down` is what catches the pre-fix bug. The test exercises `applyBody` + `recordMigrationInTx` + `unrecordMigrationInTx` directly (cannot go through `migrations.Up` because the override-completeness check from PR #318 fail-loud on a partial override).
- **Migration 0013 has `+migrate Up` / `+migrate Down` markers (PR #323, issue #307)** — `backend/internal/migrations/sql/0013_nodes_agent_bearer.sql` had no markers (the only such migration in the tree). Pre-fix, `Up` worked only via the migrator's whole-file fallback (the pre-fix `strings.Index` would have returned the whole file as the Up body, which happened to work because there was a single `ALTER TABLE`), and `Down` silently committed an empty transaction and reported success. Post-fix: the canonical `BEGIN; -- +migrate Up ... -- +migrate Down ... COMMIT;` markers, matching every other migration. The Down body is a defensive `ALTER TABLE nodes DROP COLUMN IF EXISTS agent_bearer;` (idempotent if the operator ever needs to re-run Down by hand).
- **`App.Close()` shutdown order fixed (PR #324)** — three production-breaking bugs in the composition root. (1) `defer a.AgentCA.Close()` in `Build()` was firing when `Build` returned, so any consumer that called `a.AgentCA.RootCertPEM()` after `Build` saw a closed receiver — the mTLS bootstrap in `bootstrap.ServiceConfig.MTLSCerts` saw `ErrNotFound`, the provisioner silently swallowed the error, the installer pushed no `/etc/aegis/agent.{crt,key,ca.pem}` to the node, and the v0.8.31+ agent refused to start. (2) `defer client.Close()` in the wiring helper in `cmd/aegis/main.go` was firing when the helper returned, so the `agentgrpc.Client` was closed before the first Apply. (3) `App.Close()` did not wait for `BatchedApplier` goroutines — cancel told them to stop, but the next line closed the pg pool, and a goroutine in the middle of an Apply (which does a DB write to `flush_log` + the Apply-side audit row INSERT) saw `pgxpool: closed pool`. Fix: `App.Close()` is reshaped into a deterministic 7-step shutdown — cancel webhook retry worker, cancel backups scheduler, cancel every BatchedApplier per-applier ctx, wait for the BatchedApplier goroutines to finish (10s ceiling, `sync.WaitGroup` + `time.After` race), close the agentgrpc.Client, close the agentca Service, close the pg pool. New `App.batchedApplierWg sync.WaitGroup` field, `App.agentClient agentgrpc.Client` field, `App.SetAgentClient(c)` setter. New regression guard: `TestBuild_EnsuresAgentCARoot` now calls `a.Close()` twice in a row and asserts the second call is a no-op (idempotency).

### Closed issues (no code change)

- **#287** closed via `gh issue close` — already fixed in v0.8.28.5 (PR #288) per the line-anchored `open: undefined` default in `Select.vue` `withDefaults`. The pre-fix UI bug was Vue 3 boolean-casting `open: false` onto the radix-vue `SelectRoot`; the post-fix `open: undefined` re-enables the controlled-vs-uncontrolled mode. Operator confirmed in real headed browser 2026-08-23 ~02:30 MSK ("Работает.").
- **#289** closed via `gh issue close` — all 4 sub-issues (C1 refresh wiring, C2 Subs.WithCreds nil, C3 cross-user credential cache leak, C4 Apply resp.Body fd leak) closed in PRs #291-#294 (the v0.8.28.6 batch). Memory anchor: `v0.8.28.6 prod deploy complete`.

### Migration / breaking changes

NONE. v0.8.32.1 is the test/CI surface of v0.8.32; the panel binary on `ghcr.io/qadversif/aegispanel:0.8.32.1` is identical to `:0.8.32`. The 8 PRs (PRs #318 + #320-#324) are pure internal/UI/test/CI/lint changes. The migration runner is now atomic with the schema (`#306`); the marker parser is line-anchored (`#307`); the response interceptor is non-JSON-aware (`#301`); the backups scheduler runs in-process (`#302`); the base64 sub honours per-user creds (`#303`); the HostEditDialog preserves non-editable endpoint fields (`#304`); the App.Close() shutdown order is deterministic (`#324`). No SQL migration delta, no env var change, no API change, no schema change.

### Operator action required

NONE for the panel binary. The next panel start picks up every change in this release automatically (the v0.8.32.1 image is bit-for-bit identical to v0.8.32). The release record makes the audit trail visible.

## [0.8.32.4] - 2026-08-28

Bugfix for the agentca eager-`EnsureRoot` regression that broke every v0.8.32.x deploy. The `ghcr.io/qadversif/aegispanel:0.8.32.4` image is functionally the same as `:0.8.32.2` plus a 2-line `pem.Decode` branch in `decodeKeyDER` path 2a that handles SEC1 PEM (the v0.8.25 prod shape) and PKCS#8 PEM (forward-compat) before falling through to the existing raw-DER branch. No SQL migration delta, no env var change, no API change, no schema change. The v0.8.25 prod row is the canonical fixture (a pre-existing-row test, not just a fresh-row test, would have caught the bug — see Lessons below).

### Fixed

- **agentca: `decodeKeyDER` path 2 missing `pem.Decode` of plaintext (PR #328, issue #329)** — the v0.8.25 prod row stored `key_ciphertext` as age-envelope ciphertext of SEC1 PEM bytes (`openssl genpkey -outform PEM | age -r <pubkey>`, the default format). The v0.8.32.3 `decodeKeyDER` path 2 fed the plaintext directly to `x509.ParseECPrivateKey`, which expects raw SEC1 DER; the panel crashed on boot with `envelope.Decrypt succeeded but the decrypted bytes are not a valid EC private key: asn1: structure error: tags don't match`. v0.8.31.0 was lazy (`EnsureRoot` only ran on first node provisioning) and never hit the path, so the prod row's PEM shape stayed hidden until the eager call in v0.8.32.x promoted it to boot. Fix: new path 2a runs `pem.Decode` first when the plaintext starts with `-----BEGIN`; tries SEC1 PEM (`x509.ParseECPrivateKey`) then PKCS#8 PEM (`x509.ParsePKCS8PrivateKey` + type assert to `*ecdsa.PrivateKey`) before the existing raw-DER branch. Files: `backend/internal/agentca/service.go` (+~50 lines), `backend/internal/agentca/pg_store_decode_test.go` (+1 test `TestService_EnsureRoot_AgeEncrypted_PreExistingRow_PEMPlaintext`).

### Prod data fix (out-of-band, not in any tag)

The `agentca.cert_pem` row on prod was a single 592-byte line with no LF separators — the 2026-08-25 hand-mint wrote the cert into the `text` column without PEM framing newlines (`-----BEGIN CERTIFICATE-----MIIBkD...=-----END CERTIFICATE-----`). Both Go's `pem.Decode` and openssl reject this format; the v0.8.32.2 + v0.8.32.3 panels crashed on boot with `parseRootCertPEM: no block`. Fixed by an out-of-band `UPDATE agentca SET cert_pem = <reformatted>` (LF after BEGIN, LF before END, base64 wrapped at 64 chars). The reformatted PEM is preserved across deploys (604 bytes, 12 LFs, openssl + Go `pem.Decode` both happy). Backup of the broken cert_pem is at `/var/lib/aegis/backups/agentca-cert-20260828-broken.sql` on the prod host. A follow-up should add a `aegis admin agentca repair-cert` admin CLI to fix this kind of PEM shape in place from the panel side (no psql round-trip required); tracked separately.

### Lessons (lifted from this incident; the rules apply to any future eager-load or cert-mint code)

1. **Eager load + pre-existing row = both encodings**. Any new `App.Build` step that touches a pre-existing row must run a regression test that exercises BOTH encodings the row can be in. PR #327's tests covered plaintext-DER AND envelope-of-DER; they did NOT cover envelope-of-PEM (the v0.8.25 hand-mint shape) because the v0.8.25 fixture used `openssl genpkey -outform PEM` (the default), not `-outform DER`. The mismatch was only visible at boot. Apply when: any future "eager load X at boot" change. Test the pre-existing row, not just a fresh INSERT.
2. **Cert/key mint tooling must round-trip-parse the bytes before INSERT/age-seal**. Any cert/key mint tooling that writes a row to the DB should run `x509.ParseCertificate(block.Bytes)` on the cert and `x509.ParseECPrivateKey(der)` (or `x509.ParsePKCS8PrivateKey(der)`) on the key. If the round-trip fails, the mint must fail loudly BEFORE the DB write. The 2026-08-25 hand-mint was a manual `openssl ... | psql -c INSERT` and skipped this verification; the v0.8.32+ `Service.SaveRoot` path does it correctly via the typed `Root` struct.
3. **Deploy slot swap must `docker stop` BEFORE `docker run`**. The pattern `docker rename aegis-panel aegis-panel-prev8` followed by `docker run -d --name aegis-panel --network aegis-net -p <prod-host>:8080:8080` (the IP-specific bind so the panel listens on the live server's public interface, not 127.0.0.1) failed silently on a held port: the new container ended up in `Created` state on the **default bridge** (not `aegis-net`), and DNS for `aegis-postgres` then failed via `127.0.0.53:53` (systemd-resolved stub) instead of `127.0.0.11:53` (Docker DNS). Recovery: `docker rm aegis-panel`, `docker stop aegis-panel-prevN`, re-run. The `tools/scripts/install-aegis-stack.sh` and `aegis-deploy-v0.8.X.Y.sh` templates need the stop-then-rename ordering.

### Closed issues

- **#326** closed by PR #327 (the bytea scan panic in `pgStore.GetRoot` — the parent bug that motivated the eager `EnsureRoot` call).
- **#329** closed by PR #328 (the PEM plaintext path — this PR).

### Operator action required

NONE for the panel binary. The next panel start picks up the fix automatically. If you operated the panel during the v0.8.32.2 / v0.8.32.3 outage window (2026-08-28 07:15-08:50 UTC), the cert_pem reformat was applied out-of-band; the v0.8.32.4 deploy picks it up without further action. mTLS handshake against demo-node:7001 verified with `Verification: OK` (TLS 1.3, ECDSA, server cert `aegis-agent@<demo-host>` issued by `CN = Aegis Panel Root CA`).

## [0.8.32.3] - 2026-08-28 (BROKEN, do not use)

This tag is preserved on origin for the audit trail but the `:0.8.32.3` image is broken on prod (panel restart-loops on `parseRootCertPEM: no block` then, after cert_pem reformat, on `envelope.Decrypt succeeded but the decrypted bytes are not a valid EC private key: tags don't match`). The image bit-for-bit equals the v0.8.32.2 image plus the bytea-scan fix from PR #327; the panic on boot is the upstream eager-`EnsureRoot` chain that v0.8.32.4 fixes. Do not deploy. v0.8.32.4 supersedes this tag.

## [0.8.32.2] - 2026-08-28 (BROKEN, do not use)

This tag is preserved on origin for the audit trail (the 5-PR silent-bug batch PRs #320-#324) but the `:0.8.32.2` image is broken on prod (panel restart-loops on `cannot scan bytea (OID 17) in binary format into **ecdsa.PrivateKey`). The image bit-for-bit equals the v0.8.32.1 image; the panic on boot is a regression in the eager `EnsureRoot` call path. PR #327 (closed via #326) fixed the scan path; v0.8.32.3 still broke on the cert/key shape; v0.8.32.4 fixes the cert/key shape. Do not deploy. v0.8.32.4 supersedes this tag.

## [0.8.32] - 2026-08-25

Three small post-v0.8.31.1 cleanups that close UX / observability gaps the v0.8.31.1 hotfixes exposed but did not fix. Image rebuild — the `ghcr.io/qadversif/aegispanel:0.8.32` image differs from `:0.8.31.1` in the 3 code patches documented below (no new endpoints, no new env vars, no schema changes). The migration warnings are doc-only; the agent help text + log hint + config.validate are code. Ship as a separate tag (no fold-into-0.8.31.2) so the release record makes the operator-surface cleanup visible; an operator who has already pulled v0.8.31.1 and is happy with the hotfixes can defer this to the next scheduled deploy.

### Fixed

- **`aegis-agent` mTLS flag help text lied** — the `-mtls-{cert,key,ca}` flags claimed "empty = plaintext fallback" (the v0.8.29 transitional posture, removed in v0.8.30+). Post-fix: every of the three flags documents "REQUIRED in v0.8.30+ — agent refuses to start the gRPC server if the file is missing" with the env-var override path (`AEGIS_AGENT_MTLS_CERT/KEY/CA`) for non-canonical layouts. The `loadMTLSConfig` error message wraps the read failure with a "hint:" prefix naming the bootstrap install endpoint AND the manual scp path. The `mTLS disabled` path (operator explicitly sets an env var to "") also gets a similar remediation hint. Files: `cmd/aegis-agent/main.go`, `cmd/aegis-agent/grpc.go`, `cmd/aegis-agent/mtls.go`. Tests: extended `TestLoadMTLSConfig_Disabled` + new `TestLoadMTLSConfig_MissingFile_HasInstallHint` in `cmd/aegis-agent/mtls_test.go`.
- **migration files 0023+0024 dual-section trap documented** — both files contain BOTH a `-- +migrate Up` section (the schema creation) AND a `-- +migrate Down` section (the rollback path). The panel's migrator reads only the Up section per file; running the file directly with `psql -f` executes BOTH sections, which creates the schema and then immediately drops it. Pre-fix: a 2026-08-25 prod session that required manual `psql -f` migration application hit this. Post-fix: a `-- v0.8.32 follow-up:` warning block at the top of each file + a new `### psql -f direct-execute warning` section in `docs/operator-install.md` with a copy-pasteable `sed` snippet to extract the Up section for the post-mortem case where the panel can't boot without the new schema already in place. Files: `backend/internal/migrations/sql/0023_agentca.sql`, `backend/internal/migrations/sql/0024_add_nodes_agent_transport.sql`, `docs/operator-install.md`.
- **`config.validate()` completeness** — the v0.8.28 prod env shipped with two env vars that should have been required but were silently tolerated: `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE` accidentally deleted (only `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` survived) and `AEGIS_AGENT_BINARY=/usr/local/bin/aegis-agent` (the node-side path). Both surfaced as 502s on the first use, not as a config error at boot. Post-fix: when `AEGIS_WEBHOOKS_BACKEND=pg`, both envelope env vars are now required; when `AEGIS_AGENT_BINARY` is the exact string `/usr/local/bin/aegis-agent`, the loader refuses to boot with an error explaining the node-side vs container-side path distinction. Files: `internal/config/config.go`, `internal/config/config_test.go`. Tests: 4 new `TestValidate_*_Reused` / `TestValidate_ContainerSideAgentBinary_Passes` functions, all `t.Setenv`-based (no process-global state).

## [0.8.31.1] - 2026-08-25

Image rebuild. The `ghcr.io/qadversif/aegispanel:0.8.31.1` image is functionally the same as `:0.8.31.0` (no new Go API, no new endpoint) but bakes in three v0.8.30/31 mTLS install-pipeline hotfixes that every fresh deploy needed and that the 2026-08-25 prod session had to work around by hand. The hotfixes close the v0.8.30/31 mTLS chicken-and-egg (panel boots with partial schema → first query against new column crashes) without operator intervention.

### Fixed

- **agentca root CA never minted in production wiring** — `internal/agentca/service.go:EnsureRoot` is the only path that mints the panel's root CA + populates the in-memory `cachedRoot`, but no production code called it. The `bootstrap.ServiceConfig.MTLSCerts` factory reads via `a.AgentCA.RootCertPEM()` which returns `ErrNotFound` until the cache is populated; the provisioner's `mintMTLSCerts` (internal/bootstrap/provisioner.go:226) silently swallowed the error; the installer's `writeMTLSCerts` skipped the cert push; the v0.8.31+ agent then hard-failed to start without `/etc/aegis/agent.{crt,key,ca.pem}`. Fix: call `a.AgentCA.EnsureRoot(ctx)` once in `app.Build` after the service is constructed and the `WithAgentCA` adapter is wired. Idempotent (read-or-create from store + populate cache). One-line wiring + comment block explaining the silent-failure chain. New `TestBuild_EnsuresAgentCARoot` regression guard (hermetic memory backends, asserts `RootCertPEM()` returns a non-empty PEM + 2nd call returns the same cached PEM).
- **migration runner: panel binary now `//go:embed`s the SQL files** — the 24 migration files were previously only in the host's `/var/lib/aegis/migrations/` mount. The install contract (PR #297) required the operator to scp them before every upgrade; missing the scp produced a silent fail-through (migrator saw an empty dir, applied nothing) + crash on first query against the new column. Fix: move SQL files to `backend/internal/migrations/sql/`, `//go:embed all:sql/*.sql` in `migrator.go`. The host mount is now an optional operator override (hot-fix path) rather than the only source of truth. CI lint path (`sqlfluff lint backend/migrations`) updated to `backend/internal/migrations/sql`. `docs/operator-install.md` §"Schema migrations (v0.8.31.1+)" rewritten to document the new contract.
- **migration runner: fail-loud on partial host-mount override** — when the host mount is non-empty but missing some embedded migrations, `migrations.Up` now returns an explicit error naming the missing files + the install-contract remediation ("either remove the host mount to use embedded, or copy the missing files into the mount") rather than silently falling back. The pre-fix silent-fallback was the immediate cause of the 2026-08-25 prod crash (host had 0001-0022, missing 0023-0024, panel booted with partial schema, singbox wiring crashed on `n.agent_transport`). New `TestResolveSource_FailsLoudOnPartialDir` regression guard.
- **installer: post-install `systemctl is-active` verify deadline 5s → 30s** — the v0.4.0 placeholder agent (`sleep infinity`) becomes `active` in <1s, hence the original 5s deadline. The v0.8.30+ agent loads mTLS certs + binds gRPC on :7001 during `systemctl start` and can take >5s on a fresh install. Pre-fix behaviour: the LAST probe at the 5s mark often succeeded but the deadline had expired, so the loop returned `ErrVerifyFailed` with state="active" in the error message; the provisioner transitioned the node to `offline` and the operator had to SQL-UPDATE the state back to `online` by hand. Bump to 30s (still well below operator patience + observed 5-10s cert+bind time on prod). `verifyDeadline` is now a package-level `var` (not const) so `TestInstaller_VerifyFailure` can override it to 50ms and keep the test's wall time at 0.2s. New `TestInstaller_VerifyAcceptsSlowAgent` exercises the multi-poll path (3 "activating" probes + 1 "active" probe, asserts `result.OK == true`).

### Changed

- **install contract: `backend/migrations/*.sql` → `backend/internal/migrations/sql/*.sql`** — see the Fixed entry for the embed.FS migration. Operators on a clean install no longer need to scp migration files before pulling a new image. Operators with a hot-fix in flight (a custom 9999_*.sql or tweaked 00XX) must keep the full set of embedded migrations in the host mount too (the fail-loud check refuses a partial override). See `docs/operator-install.md` §"Schema migrations (v0.8.31.1+)" for the full new contract.
- **CI: sqlfluff lint path** — `.github/workflows/ci.yml:202` `sqlfluff lint backend/migrations` → `sqlfluff lint backend/internal/migrations/sql` to match the new SQL file location. No functional change to the lint step itself.
- **`backend/internal/bootstrap/installer.go:verifyDeadline` exposed as a package-level `var`** — the seam is a deliberate test affordance. Production code reads the var (default `30 * time.Second`); tests override it via `verifyDeadline = 50 * time.Millisecond` + a `defer` to restore. Not a configuration knob for production.

## [0.8.28.6] - 2026-08-24

No image change — pure ops + code-quality batch. The `ghcr.io/qadversif/aegispanel:0.8.28` image is the same as v0.8.28; this entry covers a migration applied to the live prod database and a series of code-quality / infra PRs.

### Fixed

- **agent response body leak (issue #289 / C4)** — `nodes/handler_refresh_bearer.go` + `bootstrap/handler.go:postApply` were sending an SSH client response through a connection close without `defer resp.Body.Close()`. The leak manifested as `file descriptor exhaustion` on long-running agent-bearer / provisioner calls — silent for hours, then panel-side goroutines couldn't open new files / sockets. Fix: defer close in both call sites. PR #292 (squash `7fc57c3`).
- **cross-user subscription credential cache leak (issue #289 / C3)** — `subscription/service.go` cached a per-user `map[uuid.UUID]credentials.Credential` on the long-lived `Service` struct, populated by the first `/render` call, then served on every subsequent call across all users. A user who happened to be the first render after restart would silently receive their creds on a different user's next render. Fix: build the per-user `map` locally inside the render path (C3b added a 400-goroutine data-race patch in `precomputeUserCreds` to prove the equivalence before deleting the field). PR #293 (squash `211f126`).
- **Phase 2 dead wiring — `Subs.WithCreds` after `Credentials` is built (issue #289 / C2)** — `app.go` called `Subs.WithCreds(ctx, ...)` BEFORE `Credentials` had been built, so the subscriber always saw `credentials: nil` and silently emitted placeholder credentials. Fix: moved the call to step `14c` (after `Credentials.Build()` at step `13`). PR #294 (squash `a5b47fe`).
- **schema: `host_endpoints.path_check` self-contradiction** — migration `0004_hosts_v3.sql` declared `path TEXT NOT NULL DEFAULT ''` together with `CHECK (path <> '')`; every INSERT without an explicit path produced a row that violated its own DEFAULT. Only surfaced on the first host creation in prod (the v0.8.28.6 smoke). Fix migration `0022_relax_host_endpoints_path.sql` drops the DEFAULT, drops NOT NULL, drops the CHECK. Applied to prod out-of-band (`docker exec aegis-postgres psql`) and recorded in `schema_migrations` so the panel's own migration runner skips it. PR #298 (squash `65af85c`). **Net Go change: zero.** Pure DB-schema relaxation.
- **hand-rolled JSON escapers × 11 handler files (#290 D3)** — `jsonString` / `jsonEscape` in `audits` / `auth` / `bootstrap` / `backups` / `hosts` / `inbounds` / `inboundtemplates` / `nodes` / `panelcfg` / `users` all formatted non-BMP runes (emoji) as `"\u1F680"` 5-hex-digit, which is **not a valid JSON string escape** — strict parsers (`jq`, Rust `serde_json`, Go `encoding/json.Unmarshal` with `DisallowInvalidUTF8`) rejected the whole response, lenient parsers (JavaScript's `JSON.parse`) silently corrupted the character. `bootstrap/handler.go:501-523` was the worst offender — escaped *every* letter and truncated non-BMP runes to their low 16 bits. Fix: introduce `internal/httpjson` package (`WriteJSON` / `WriteError` / `String` on `encoding/json`) and migrate all 11 packages. 23 unit tests cover ASCII, BMP non-ASCII (Cyrillic), non-BMP runes (rocket, grinning face, regional indicators), HTML-unsafe chars, control chars, long messages, Content-Type, and the `{"error": msg}` envelope shape. PR #300 (squash `a86c878`).

### Changed

- **compose install contract (#297)** — `tools/scripts/aegis-stack.yml` (production compose: aegis-panel + aegis-ui, 4 canonical mounts, `0.0.0.0:8081:8080` for UI) + `tools/scripts/install-aegis-stack.sh` (idempotent wrapper) + `docs/operator-install.md` (TL;DR, fresh install, upgrade workflow, privacy rules). `cd /opt/aegis && docker compose up -d` is now the canonical install / upgrade entry point. The 2026-08-24 v0.8.28.6 prod-deploy lessons (502 on `<IP>:8081` because docker-proxy bound only on `<IP>` + missing `/var/lib/aegis/backups` mount) are encoded in the canonical mounts and the publish flags — neither can be re-broken by a future hand-written `docker run`. PR #297 (squash `7b946d4`).
- **typescript: drop duplicate `NodeProvisionResponse` (#290 D2-immediate)** — `frontend/src/types/aegis.ts` declared `NodeProvisionResponse` twice (L127 with `node_id: UUID`, L546 with `node_id: string` + JSDoc). Because `type UUID = string`, TypeScript's interface-merging was silently accepting both declarations as a single nominal type — the exact "linter doesn't catch it, type-check doesn't catch it, IDE doesn't catch it" trap. The second declaration (the one with the JSDoc) is the canonical one; the first is removed. PR #299 (squash `f9dc9fe`).

### Backlog

- **#290 D1 (MemoryStore/PgStore divergence)** — moved to **backlog** per operator (2026-08-24). 12 packages (`auth`, `audits`, `credentials`, `hosts`, `inbounds`, `inboundtemplates`, `nodes`, `panelcfg`, `plans`, `subscription`, `users`, `webhooks`) each have a `MemoryStore` shortcut that disagrees with the prod `PgStore` semantics. Subscription is the documented case (`every pool with member is attached to every plan` vs the real `plan_pool` join). Plan: per-package `RunStoreContract(t, newStore)` called twice (Memory + Pg, "Pg wins"), start with `subscription` → `users/credentials` (security) → 9 remaining. **Out of scope for v0.8.28.6.**

## [0.8.28] - 2026-08-21

Tier 3 dialog extraction closeout + Tier 1 #3 (backup
cron) closeout + Tier 2 dependency batch + tests +
anti-leak infra hardening. The 17-PR Tier 3 batch
(#254-#270) lands the 8-dialog split of HostsView and
NodesView; the 3-PR Tier 1 #3 batch (#273-#275) ships
the cron-parser step + range + list syntax, the
scheduler goroutine test coverage, and the
`GET /api/v1/backups/schedule` endpoint + BackupsView
"Schedule" section. PR #272 closes the 2026-08-20
incident loop by adding a `ghp_` / `github_pat_` regex
to the secret scanner. Dialogs split: HostsView and
NodesView each shed their per-action dialogs into 8
self-contained Vue components under
`frontend/src/views/dialogs/`; the view files keep
only the trigger refs + per-row pointers, the
dialogs own the form state + wire-payload builder +
success card surface. Backup cron: the parser now
supports the full Vixie `*`, `N`, `N-M`, `N-M/S`,
`*/S`, `N,M,K` construct set; the scheduler goroutine
is covered by 33 tests (4 new test functions
including the `IdempotentWithinMinute`,
`AdvancesLastEvenOnNonMatch`,
`RespectsCancelledContext`, `TriggersAtScheduledTime`
coverage); the new read-only `GET
/api/v1/backups/schedule` endpoint surfaces the
active schedule (admin-scoped, `backups` scope); the
`Backups → Schedule` section in the admin UI renders
the active cron + retention + `scheduleActive` flag
so the operator can audit at a glance.

### Added

- feat(backups): extend cron parser with step + range + list syntax — `N-M`, `N-M/S`, `*/S`, `N,M,K` now supported in `parseCronField`; gocritic `unnamedResult` compliance (named returns + 2 body `:=` → `=`) (#273)
- feat(backups): add hot-reload + admin UI for schedule + retention — `Service.ReloadCron` + `GET /api/v1/backups/schedule` endpoint (admin-scoped, `backups` scope) + `BackupsView.vue` "Schedule" section + 10 i18n keys (`backups.schedule.*` in en.json + ru.json) + OpenAPI schema bump to `0.8.28` + auto-regenerated `api.d.ts` (#275)

### Changed

- refactor(frontend): dedup `ChangePasswordRequest` type (#254)
- refactor(frontend): replace `window.confirm` with `ConfirmDialog` component (HostsView + InboundsView) (#256)
- refactor(frontend): replace `as never` with typed `as Parameters<...>` casts in HostsView + NodesView (#263)
- refactor(frontend): extract `HostCreateDialog` + `HostEditDialog` from HostsView (#265)
- refactor(frontend): extract `NodeCreateDialog` + `NodeEditDialog` from NodesView (#266)
- refactor(frontend): extract `NodeProvisionDialog` from NodesView (#267)
- refactor(frontend): extract `NodeRotateDialog` from NodesView (#268)
- refactor(frontend): extract `NodeRefreshDialog` + `NodeInspectDialog` from NodesView (#269)

### Performance

- perf(frontend): memoize `camelizeKeys` for large response bodies (#255)
- perf(frontend): batch inbounds preload via new `GET /api/v1/inbounds` endpoint (one round-trip replaces the per-row `GetByNode` fan-out) (#264)

### Tests

- test(frontend): add vitest coverage for 8 extracted dialogs (52 new tests, 39 existing → 91 total) (#270)
- test(backups): add scheduler goroutine test coverage — 33 tests across 4 new test functions (`IdempotentWithinMinute`, `AdvancesLastEvenOnNonMatch`, `RespectsCancelledContext`, `TriggersAtScheduledTime`) in `scheduler_test.go` (#274)

### Security

- chore(ci): add `ghp_` / `github_pat_` regex to `BANNED_PATTERNS` in `tools/scripts/check-sensitive.sh` (and AGENTS.md mirror) — closes the 2026-08-20 3-PAT incident loop; the scanner now catches classic `ghp_`/fine-grained `github_pat_`/OAuth `gho_`/`ghu_`/`ghs_`/`ghr_` tokens. All three leaked tokens from the 2026-08-20 session have been rotated by the operator (#272)

### Chore

- chore(ci): bump node 20 → 24.19.0 (jsdom30/undici8 engines requirement) (#257)
- chore(deps): bump the minor-and-patch group in `/frontend` with 6 updates (#259)
- chore(deps): bump `golang.org/x/mod` 0.37.0 → 0.40.0 (CVE-2026-56864, CVE-2026-56865) (#261)
- chore(deps): bump the minor-and-patch group across 1 directory with 2 updates (`/backend`) (#262)

## [0.8.27] - 2026-08-16

### Added

- feat(scripts): add --dry-run to branch-start.sh, harden release.sh --snapshot (#250)
- feat(repo): anti-leak infrastructure (AGENTS.md + scanner + pre-commit + CI) (#241)
- feat(frontend): withCredentials + drop refresh from localStorage + access in-memory (#215)
- feat(auth): store refresh token in HttpOnly cookie, not the JSON body (#214)
- feat(frontend): InboundTemplatesView + Template dropdown in InboundsView (#212)
- feat(inbounds): reject inbound with TemplateID pointing at template of different protocol (#211)
- feat(renderer): sing-box BuildCoreConfigForNode reads template.params when inbound.template_id is set (#210)
- feat(inbound-templates): per-tenant Params defaults — foundation (v0.8.x) (#205)
- feat(ui): shadcn-vue RadioGroup primitive + migrate NodesView auth-method radios (#202)
- feat(ui): merge "Add node + Provision" into a single dialog (#201)
- feat(builder): per-user credential filter (closes v0.7.x Phase 2 multi-user TODO) (#198)
- feat(styles): migrate to tailwindcss v4 (CSS-first config + @tailwindcss/vite) (#197)
- feat(ui): subscription URL display in UsersView (v0.8.x) (#193)
- feat(builder): host->node mapping in Builder + user fan-out (v0.8.x) (#192)
- feat(cores): BatchedApplier 401 auto-refresh integration (v0.8.8) (#189)
- feat(nodes): refresh-agent-bearer (v0.8.7) (#188)
- feat(ui): NodesView Rotate panel key button (v0.8.3 item 2, HTTP mirror of #184) (#185)
- feat(cli): `aegis admin node rotate-panel-key` for v0.3.0..v0.7.x nodes (#184)
- feat(credentials): HTTP admin surface + UI for user_inbound_credentials (#183)
- feat(ui): password / stored-key radio for the node provision form (#180)
- feat(bootstrap): password-based first auth + persistent node SSH key (#179)
- feat(subscription): per-user credential render (Phase 2 step 4, multi-user sub URL) (#170)
- feat(credentials+cores): wire credentials through builder and narrow BatchedApplier fan-out (Phase 2 step 3) (#169)
- feat(cores): multi-user sing-box renderer (Phase 2 step 2) (#168)
- feat(credentials): Phase 2 multi-user sing-box render data model (#167)
- feat(audits): wire audit_log call-sites into every mutating service (#166)
- feat(cores): real BatchedApplier FlushFn + Enqueue from user/inbound services (#157)
- feat(ci): local docker-compose end-to-end smoke script (#152)
- feat(ui-webhooks): events multi-select in create + edit dialogs (#150)
- feat(webhooks): wire Service.Dispatch to all mutating handlers (#148)
- feat(webhooks): age envelope on endpoint secret (#147)
- feat(webhooks): background retry worker (#146)
- feat(ui-webhooks): WebhooksView + sidebar nav + i18n en/ru + Webhook icon (#139)
- feat(webhooks): admin HTTP handler + ScopeWebhooks + AEGIS_WEBHOOKS_BACKEND + wiring (#137)
- feat(webhooks): internal/webhooks package — Endpoint + Delivery + DLQ + HMAC + retry + Service (#136)
- feat(ui): PlansView + sidebar nav + i18n en/ru (#134)
- feat(plans): admin HTTP handler + ScopePlans + router/main wiring (#132)
- feat(plans): internal/plans package — core types, store, service (#131)
- feat(cli): aegis-pg-backup + aegis-pg-restore operator CLI (#125)
- feat(backups): BackupsView.vue + API client (#121)
- feat(backups): internal/backups package + admin router (#120)

### Changed

- refactor(crypto): extract internal/crypto/envelope from webhooks/secret (#177)
- refactor(backend): extract internal/app.Build from main.go (#156)
- refactor(ui-webhooks): extract shared zod schema (#149)

### Fixed

- fix(ui): inject VITE_VERSION at build time (was hardcoded v0.0.0-dev) (#253)
- fix(ui): SVG width=icon leak + SelectItem empty value (#239)
- fix(ui): surface session-expired toast + auto-redirect to /login (#238)
- fix(bootstrap): UploadAndSwap (SFTP temp+rename) for ETXTBSY-safe binary replacement (v0.8.25) (#235)
- fix(nodes): BootstrapNodeProvider.Update propagates State field (v0.8.24) (#234)
- fix(bootstrap): strip SHA256:/MD5: prefix in fingerprint compare (v0.8.23) (#233)
- fix(bootstrap): pin HostKeyAlgorithms to ed25519 so fingerprint compare is unambiguous (#232)
- fix(bootstrap): compute SSH fingerprint from binary wire format, not authorized_keys line (#231)
- fix(bootstrap): TOFU policy was unreachable when known_hosts file exists (#230)
- fix(backups): upgrade pg_dump to 16 via PGDG repo (v0.8.19) (#229)
- fix(backups): Dumper/Restorer interfaces, full DSN, propagated Close errors (#228)
- fix(backups): replace /usr/bin/pg_dump with symlink to real binary (#226)
- fix(backups,provision): copy real pg_dump binary + joinHostPort handles host:port (#224)
- fix(backups,provision): bundle pg_dump + aegis-agent in panel image (multi-stage distroless/base) (#222)
- fix(backups,inbounds): admin missing backups scope + SelectItem empty value (#221)
- fix(ui): dialog content overflow on content-heavy dialogs (v0.8.x) (#220)
- fix(docs): merge duplicate [Unreleased] in CHANGELOG (markdownlint MD024)
- fix(auth): add GetByID to Store interface, wire /me through it (#182)
- fix(ui): show write affordances when /me is broken (#172)
- fix(cli): suppress echo on aegis admin password prompts (#154)
- fix(nodes): pin State enum to migration 0006 with a regression guard (#153)
- fix(ci): verify-images handles workflow_dispatch re-runs (#130)
- fix(ci): emit `latest` tag on tag-push for non-prerelease versions (#127)
- fix(ui): four runtime quirks the Phase 1 deploy surfaced (#117)

### Documentation

- docs(runbooks): sync deploy.md §3.3/§5 to match production state (#252)
- docs(runbooks): add oncall.md (incident response playbook) (#251)
- docs: recreate docs/gap-analysis-v0.8.24.md (3 broken links) (#246)
- docs(deploy): sops+age runbook fixes + distroless UID 65532 gotcha (#191)
- docs: sync to v0.8.0 (Phase 2 multi-user sing-box render milestone) (#171)
- docs: sync to v0.7.2 (audit batch closeout)
- docs: sync to v0.7.1 (#151)
- docs: sync to v0.7.0 and the post-v0.7.0 4-PR dependency batch (#145)
- docs(v0.7.0): CHANGELOG + ROADMAP + api reference for /webhooks (#140)
- docs(webhooks): OpenAPI /webhooks endpoints + hand-mirrored services/webhooks.ts (#138)
- docs: v0.6.0 CHANGELOG + ROADMAP + plans API reference (#135)
- docs(openapi): add /plans endpoints + Plan schema + PlanResetPeriod (#133)
- docs: operator guide + security policy + quickstart (v0.5.0) (#126)

### CI

- ci(release): add smoke-test gate before cosign re-sign (#247)

### Tests

- test(cores): end-to-end integration test for BatchedApplier + FlushFn (#158)
- test(ui): add vitest suite for zod schemas (#155)

### Chore

- chore(repo): gitignore .local/ directory (gh CLI state etc.) (#249)
- chore(deps): bump go 1.26.5 → 1.26.6 (govulncheck 6 stdlib advisories) (#248)
- chore(docs): scrub historical banned-value leaks in RUNBOOKS/deploy.md + fix scanner regex anchor (#245)
- chore(repo): scrub historical banned-value leaks (14 docs/code files) (#244)
- chore(test): replace real banned value in ssh_test.go with synthetic fixture (#243)
- chore(repo): gitignore release-notes drafts + sync AGENTS.md (#242)
- chore(release): cut v0.8.26 - CHANGELOG surgery (#240)
- chore(repo): drop tracked .git-pr-title.txt scratch + broaden .gitignore (#237)
- chore(docs): sync to v0.8.25 (the silent-bug chain closeout) (#236)
- chore(release): cut v0.8.25 - CHANGELOG surgery
- chore(release): cut v0.8.24 - CHANGELOG surgery
- chore(release): cut v0.8.23 - CHANGELOG surgery
- chore(release): cut v0.8.22 - CHANGELOG surgery
- chore(release): cut v0.8.21 - CHANGELOG surgery
- chore(release): cut v0.8.20 - CHANGELOG surgery
- chore(release): cut v0.8.19 - CHANGELOG surgery
- chore(release): cut v0.8.18 - CHANGELOG surgery
- chore(release): cut v0.8.17 - CHANGELOG surgery (#227)
- chore(release): cut v0.8.16 - CHANGELOG surgery (#225)
- chore(release): cut v0.8.15 - CHANGELOG surgery (#223)
- chore(docs): sync to v0.8.14 (release cut + body-field shim closure) (#219)
- chore(release): cut v0.8.14 — CHANGELOG surgery, closes 4-PR audit-3.1 fix + body-drop gap (#214, #215, #216, #217) (#218)
- chore(auth): drop refresh_token from login/refresh JSON bodies (v0.8.14) (#217)
- chore(caddy): add Content-Security-Policy header for the admin path (#216)
- chore(release): cut v0.8.13 — CHANGELOG surgery, closes 4-PR inbound-templates gap (#205, #209, #210, #211, #212) (#213)
- chore(docs): sync to v0.8.12 + inbound-templates foundation (PR #205) (#209)
- chore(release): cut v0.8.12 — CHANGELOG surgery, closes 3-PR gap (#201, #202, #203) (#204)
- chore(docs): close shadcn-vue RadioGroup primitive entry (PR #202 follow-up) (#203)
- chore(frontend): lint cleanup  auto-fix 105 vue warnings in 5 files (#200)
- chore(release): cut v0.8.11  CHANGELOG surgery, closes 3-PR gap (#196, #197, #198) (#199)
- chore(frontend-deps): bump @vueuse/core 11>14, vite 7>8, jsdom 25>30 (#196)
- chore(release): cut v0.8.10  CHANGELOG surgery, closes 3-PR gap (#192, #193, #194) (#195)
- chore(docs): sync to v0.8.9 (README, operator-guide, SECURITY, quickstart, deploy/secrets) (#194)
- chore(release): cosign re-sign + verify on every release (v0.8.9) (#190)
- chore(ops): JSON logs in production, hardened (v0.8.6) (#187)
- # feat(ui): NodesView "Show stored key" debug surface (v0.8.5, read-side mirror of the v0.8.1 persistent key) (#186)
- chore(docs): sync to v0.8.1 (#181)
- chore(frontend-deps): bump brace-expansion 5.0.8 → 5.0.9 (CVE GHSA-rgw5-rvv9-x895) (#178)
- chore(frontend-deps): bump @vue/tsconfig 0.8.1 → 0.9.1 + postcss 8.5.24 → 8.5.25 (#165)
- chore(frontend-deps): bump axios 1.18.1 → 1.19.0 (CVE-2026 GHSA-hmw2-7cc7-3qxx) (#163)
- chore(frontend-deps): bump css/sass toolchain (3 packages) (#161)
- chore(frontend-deps): bump ts/types toolchain (5 packages) (#159)
- chore(deps): bump vue-i18n to 11.4.8 (#144)
- chore(deps-dev): bump vue-tsc to 3.3.8 and fix WebhooksView DataTable props (#143)
- chore(deps): bump pinia to 4.0.2 and add @vue/devtools-api (#142)
- chore(deps): bump Go minors (prometheus 1.24.1, env 11.4.1, zerolog 1.35.1) (#141)
- chore(release): cosign sign + verify for panel and UI images (#129)
- chore(obs): JSON logs in production via AEGIS_ENV (#128)
- chore(ops): install_panel role + prod compose + secrets.env mount (#124)
- chore(ops): install_singbox — runtime SHA-256 via GitHub Releases API (#123)
- chore(ops): tools/scripts/pre-pr.sh + pre-push hook (#122)
- chore(ops): secrets via sops+age (#119) (#119)
- chore(repo): gitignore operator deploy scripts under tools/scripts/ (#118)

[0.8.28]: https://github.com/QAdversif/AegisPanel/releases/tag/v0.8.28
[0.8.27]: https://github.com/QAdversif/AegisPanel/releases/tag/v0.8.27
