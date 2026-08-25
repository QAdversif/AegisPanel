# AegisPanel — Roadmap

> **Living document.** Updated each milestone as the d-refactor / v0.x / v1.0
> ladder progresses. The next unreleased tag is at the top; completed tags
> are listed in `CHANGELOG.md` (per-release log) and `docs/adr/`
> (architecturally significant decisions).

## Status (2026-08-25)

> **Recent activity (post-v0.8.28):**
> - **v0.8.31.1** (image rebuild) — mTLS install-pipeline
>   hotfixes. Three fixes that every fresh v0.8.30+ install
>   needed, all closed in a single PR:
>   - `app.Build` now calls `a.AgentCA.EnsureRoot(ctx)` so the
>     `cachedRoot` is populated before the bootstrap
>     `MTLSCerts` factory's first call (otherwise the
>     provisioner's `mintMTLSCerts` silently swallows the
>     `ErrNotFound` and the agent installs without mTLS
>     certs).
>   - Migration SQL files moved to
>     `backend/internal/migrations/sql/` + `//go:embed`'d in
>     the panel binary. The host mount at
>     `/var/lib/aegis/migrations` is now an optional operator
>     override (hot-fix path); partial overrides fail loud
>     with the missing filenames instead of silently
>     falling back.
>   - Installer `verifyDeadline` bumped 5s → 30s (the
>     v0.8.30+ agent takes >5s to load mTLS certs + bind
>     gRPC on a fresh install).
>   - `docs/operator-install.md` updated to the new contract
>     (host mount is optional; fail-loud behaviour; "Why
>     this changed" block pointing at the 2026-08-25 prod
>     incident).
>   See CHANGELOG `[0.8.31.1]` for the full list. 4
>   PRs/commits land against `main`; the new image is
>   `ghcr.io/qadversif/aegispanel:0.8.31.1`. Tag pushed
>   via the v0.8.31.0 release workflow (re-run on
>   v0.8.31.1).
> - **v0.8.31.0** (the 7-PR mTLS+gRPC chain) — released
>   2026-08-25 morning but the CHANGELOG entry was never
>   written (operator-side task that fell through the
>   cracks). The release notes (PR #315 + PR #314 + PR
>   #312 + PR #311 + PR #309 + PR #308) describe the
>   per-PR content. The mTLS pipeline it ships is
>   half-wired (the bug fixed by v0.8.31.1 above) — every
>   fresh install hits the silent mint failure until
>   v0.8.31.1. **Out of scope for the v0.8.31.0 entry
>   itself**, the v0.8.31.1 hotfix batch is the documented
>   remediation. Tracker: this ROADMAP entry.
> - **v0.8.28.6** (no image change) — issue #289 (4-bug chain C1–C4:
>   agents fd-leak, host_endpoints path-check, subscription cross-user
>   leak + render-local cache, Subs.WithCreds wiring order) +
>   migration `0022_relax_host_endpoints_path.sql` (DB-schema fix
>   applied out-of-band) + #297 docker-compose install contract +
>   #298 + #299 + #300 (D2 immediate + D3 `internal/httpjson`
>   package). 5 PRs landed against `main`; existing
>   `ghcr.io/qadversif/aegispanel:0.8.28` image unchanged.
> - **#290 D1** (MemoryStore/PgStore divergence) — moved to
>   **backlog** per operator (2026-08-24). 12 mini-PRs per package,
>   start with `subscription` then `users/credentials` (security)
>   then the remaining nine.

| Tag                  | Scope                                                                                                  | Status               |
| ---                  | ---                                                                                                    | ---                  |
| `v0.1.0-mvp-render`  | Public-facing subscription endpoint, render orchestrator, base64 + singbox + Clash + HTML            | ✅ shipped (#50–58)  |
| `v0.2.0-mvp-agent`   | `aegis-agent` (Go binary) on every node, real `apply` writes, reload                                       | ✅ shipped (#59–66)  |
| `v0.3.0-mvp-byo-node` | BYO-node bootstrap (SSH + TOFU + provisioning)                                                          | ✅ shipped (#67, #74–77, #82–84, #87) |
| `v0.4.0-mvp-batched` | `BatchedApplier` + real apply transport + `install_singbox` Ansible role                                  | ✅ shipped (#92, #93, #94) |
| `v0.4.0-d.1`         | `internal/users` data layer (post-d.0 split; precedes the Path C consolidation)                          | ✅ shipped (#95)      |
| `v0.4.0-d.r1`        | `users.User` wire-format compat with `subscription.User`                                                  | ✅ shipped (#96)      |
| `v0.4.0-d.r2`        | Drop subscription-side Store / MemoryStore / PgStore user-CRUD (Path C step 2)                          | ✅ shipped (#97)      |
| `v0.4.0-d.r3`        | Move `admin_handler.go` to `users`; drop Service-level thin wrappers (Path C step 3)                   | ✅ shipped (#99)      |
| `v0.4.0-d.r4`        | Cleanup pass + roadmap — Path C step 4                                                                  | ✅ shipped (#100)     |
| `v0.4.0-post`        | Release workflow fixes: GHCR image tag lowercase, `workflow_dispatch` push, UI image tag input, explicit semver tags — no application code change | ✅ shipped (#102, #103, #104, #111) |
| `v0.4.0`             | Tag for the d-r-series; aggregate of #95–#100, on commit `3beff0f` → `db151f2` (rewritten post history-edit) | ✅ shipped |
| `v0.5.0`             | sops+age secrets, backup/restore (pkg + UI + CLI), pre-PR gate, GitHub-API sing-box SHA-256, container wiring for secrets, operator guide + SECURITY + quickstart | ✅ shipped (#119, #120, #121, #122, #123, #124, #125, #126) |
| `v0.6.0`             | `internal/plans` (table already exists in migration 0001)                                                | ✅ shipped (#131, #132, #133, #134) |
| `v0.7.0`             | `internal/webhooks` (table already exists in migration 0001)                                             | ✅ shipped (#136, #137, #138, #139) |
| `v0.7.1`             | Webhook call-site wiring, sops+age envelope on `webhook_endpoints.secret`, background worker for retry, shared zod schema at `frontend/src/schemas/webhook.ts`, events multi-select in the WebhooksView; plus the post-v0.7.0 Go+frontend dependency batch (#141, #142, #143, #144) and the docs sync (#145) | ✅ shipped (#146, #147, #148, #149, #150) |
| `v0.7.2`             | Audit batch closeout (closes the 2026-08-01 colleague review): God-object main.go extracted into `internal/app.Build`; real BatchedApplier FlushFn + Enqueue from user/inbound services; end-to-end integration test against a real Postgres; post-v0.7.1 docs sync | ✅ shipped (#156, #157, #158) |
| `v0.8.0`             | Phase 2 multi-user sing-box render end-to-end (data model #167, renderer #168, builder + BatchedApplier narrow #169, subscription per-user render #170); audit-log call-site wiring into every mutating service (#166); frontend dependency batch — TS / CSS / axios / vue-tsconfig / postcss (#159, #161, #163, #165); docs sync to v0.8.0 (#171) | ✅ shipped (#166, #167, #168, #169, #170, #171) |
| `v0.8.1`             | Auto-deploy batch: `fix(ui)` write affordances when `/me` is broken on pg backend (#172); `refactor(crypto)` extracting `internal/crypto/envelope` shared by webhooks + bootstrap (#177); `chore(frontend-deps)` `brace-expansion` 5.0.8 → 5.0.9 CVE (#178); `feat(bootstrap)` password-XOR + persistent panel key (ed25519 + envelope encrypt + `authorized_keys` push, migration 0020) (#179); `feat(ui)` three-way auth-method radio (key / password / stored) on the node provision form (#180); docs sync to v0.8.1 (#181) | ✅ shipped (#172, #177, #178, #179, #180, #181) |
| `v0.8.2`             | Server-side `auth.me` fix on pg backend (add `GetByID` to `auth.Store` interface, implement in `MemoryStore` and `PgStore`, rewire `Service.lookupByID`); HTTP admin surface for `user_inbound_credentials` (`/api/v1/credentials/` mount + `ScopeCredentials` + OpenAPI + Credentials tab in the user detail page) | ✅ shipped (#182, #183) |
| `v0.8.3`             | Operator-side CLI `aegis admin node rotate-panel-key <node-uuid> --key <path>` for v0.3.0..v0.7.x nodes (generates a fresh ed25519 keypair, pushes the public half to `authorized_keys`, seals the private half with the operator's age envelope) | ✅ shipped (#184) |
| `v0.8.4`             | HTTP mirror of the v0.8.3 rotate-panel-key CLI: `POST /api/v1/nodes/{id}/rotate-panel-key` endpoint + "Rotate panel key" dropdown entry on the NodesView (visible for `online` / `offline` / `draining` / `disabled`; hidden for `new`); 200 body carries the new public key line + SHA256 fingerprint so the operator can verify the rotation in the UI | ✅ shipped (#185) |
| `v0.8.5`             | "Show stored key" debug surface in NodesView: `GET /api/v1/nodes/{id}/stored-key` endpoint + "Show stored key" dropdown entry (visible for any state); the panel decrypts `nodes.ssh_private_key_ciphertext` via the age envelope, derives the public-key line + SHA-256 fingerprint, and returns the public surface. The private key never leaves the panel process. The read is recorded in the audit log as `node.stored-key.read` | ✅ shipped (#186) |
| `v0.8.6`             | JSON logs in production, hardened: the `AEGIS_ENV=production` → `zerolog.JSON` writer switch (already wired in v0.5.0-era) gets a config-level guard in `Config.validate()` that refuses to boot when `AEGIS_ENV` is the `development` default AND any `AEGIS_*_BACKEND` is set to `pg` (silent-misconfig → loud boot-time error: "set AEGIS_ENV=production or AEGIS_ENV=staging to confirm logging intent"). Pure-memory dev path is unaffected. Operator guide + KNOWN_LIMITATIONS + ROADMAP updated to reflect the wiring. New `backend/internal/config/config_test.go` with 8 test functions / 18 sub-tests covering the guard + the `usesAnyPgBackend` helper's exhaustive sweep across all eleven `*Backend` fields | ✅ shipped (#187) |
| `v0.8.7`             | Refresh agent bearer (Service + HTTP + UI): `nodes.Service.RefreshAgentBearer` decrypts the stored panel SSH key, SSHes into the node via `bootstrap.NewClient` (TofuPolicy=Reject), reads `/etc/aegis/agent.env`, parses `AEGIS_AGENT_BEARER`, and updates `nodes.agent_bearer` (the recovery path for "agent regenerated its bearer out-of-band"). `POST /api/v1/nodes/{id}/refresh-agent-bearer` handler + NodesView "Refresh agent bearer" dropdown entry + `node.agent-bearer.refresh` audit row. New `backend/internal/nodes/refresh_bearer.go` (Service) + `handler_refresh_bearer.go` (HTTP) + 30 + 11 unit tests covering the full SSH-fail / parse-fail / no-stored-key error mapping. App.go wires `WithSSHClientFactory(bootstrap.NewClient).WithKnownHosts(cfg.AgentKnownHosts).WithSSHUser(cfg.AgentSSHUser)`. BatchedApplier integration (401 → auto-refresh) is a v0.8.x follow-up | ✅ shipped (#188) |
| `v0.8.8`             | BatchedApplier 401→auto-refresh integration: the v0.8.7 `RefreshAgentBearer` recovery loop wired into the sing-box `Apply` path so a 401 from `POST /v1/apply` triggers a refresh + retry without operator intervention. `singbox.NodeResolver` interface extended with `RefreshBearer(ctx, id) (string, error)`; main.go `singboxNodeResolver` adapter implements it via `nodes.Service.RefreshAgentBearer`. One retry only — no loop. 500/404 do NOT trigger refresh (server-side, not stale-bearer). Audit row `ActorID` empty for auto-refresh (no `auth.Claims` in BatchedApplier context) vs non-empty for the v0.8.7 operator-initiated path — distinguishes "the panel did this" from "the operator did this" in the audit UI. Race is benign (two goroutines refreshing same node both read same `agent.env` value, DB write is idempotent). 6 new Apply-level tests + updated `flushfn_smoke_test` `stubResolver` | ✅ shipped (#189) |
| `v0.8.9`             | Release workflow hardening: cosign re-sign + verify on every release. After the existing single sign step, `release.yml` waits 30s (let GHCR settle) and then re-signs each image + runs `cosign verify` with the same OIDC flags a consumer would use (`--certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*"`, `--certificate-oidc-issuer https://token.actions.githubusercontent.com`). Three concrete failure modes covered (v0.8.8 evidence): (1) tag-mutation drift on `latest` between sign and pull; (2) sign-step OIDC flake recovery without full workflow_dispatch + rebuild; (3) explicit `cosign verify` audit trail in workflow log so a successful `sign` exit 0 is no longer a "we hope this works" claim. Adds ~50s to release budget (negligible vs ~2m total). Pure workflow change, no code touched | ✅ shipped (#190) |
| `v0.8.10`            | Per-user credential filter in Builder — closes the second half of the v0.7.x Phase 2 multi-user TODO. `internal/users.Service.AllowedUsersForNode` (reverse-direction read of v0.8.x `enqueueUserDelta` fan-out, reuses `expandHostsToNodes` + `StatusActive` filter, blocklist-wins-over-allowlist, fail-closed on nil `s.hosts` + non-empty user filter); `internal/cores/builder.ListUsersAllowedForNode` interface + per-inbound filter inside `BuildCoreConfigForNode` (one DB round-trip per flush, not per inbound); new `usersSrc` arg on `NewFlushFn` (between `credSrc` and `renderer`); sentinel via `make(map[uuid.UUID]struct{})` (empty non-nil = fail-closed "no users" → drop every cred; nil = default-allow legacy semantics). Fail-soft on lookup error. 8 existing tests updated for the 6-arg signature + 3 new `AllowedUsersForNode` unit tests + 5 new `BuildCoreConfigForNode_PerUserFilter_*` tests. Closes the v0.8.x `per-user credential filter` row + unblocks the v1.0.0 GA tag. The first of the v0.8.10/v0.8.11/v0.8.12/v0.8.13/v0.8.14 "audit-3.1 + UX + templates" cluster | ✅ shipped (#198) |
| `v0.8.11`            | Consolidation release closing the 3-PR gap (frontend-deps #196: `@vueuse/core` 11→14, `vite` 7→8, `jsdom` 25→30 — `@vueuse/core` is declared in `package.json` but never imported in `src/`, three majors went by without side effects; Tailwind v4 #197: CSS-first config via `@tailwindcss/vite` plugin, `tailwindcss-animate` rewrite in CSS, `@plugin "@tailwindcss/forms"` + `@plugin "@tailwindcss/typography"`; PR #198 per-user credential filter). 0 backend / 0 schema / 0 env changes. Documents the v0.8.10 per-user filter closure | ✅ shipped (#199) |
| `v0.8.12`            | Consolidation release closing the 3-PR gap (lint cleanup #200: `eslint --fix` on 5 target files only — `vue/max-attributes-per-line` (37), `vue/multiline-html-element-content-newline` (10), `vue/singleline-html-element-content-newline` (8), `vue/html-self-closing` (4), `vue/html-indent` (3); merged "Add node + Provision" dialog #201: `nodeAddSchema` extends `nodeCreateSchema` with `provisionNow` discriminator + superRefine XOR + conditional-required; the form submit handler does `createNode` then optionally `provisionNode`; partial-success non-fatal toast; the `stored` auth method is rejected at the schema level for first-time installs; shadcn-vue `RadioGroup` primitive #202: `components/ui/RadioGroup.vue` + `RadioGroupItem.vue` thin wrappers over `radix-vue`; both auth-method pickers in `NodesView.vue` migrated; hand-rolled `.nodes__auth-radios*` CSS deleted; visual parity preserved 1-в-1 + arrow-key + Space keyboard navigation inherited from `radix-vue`; docs closure #203: KNOWN_LIMITATIONS + ROADMAP + README + docs/README + CHANGELOG updated to v0.8.12 state). 0 backend / 0 OpenAPI / 0 env / 0 schema changes | ✅ shipped (#200, #201, #202, #203, #204) |
| `v0.8.13`            | Feature release: inbound-templates (per-tenant `Params` defaults), 5-PR planned sequence. Foundation #205: migration `0021_inbound_templates.sql` + `internal/inboundtemplates/` package (model + validate + store + service + handler + 8 unit tests + 4 pg integration tests) + `inbounds.template_id` nullable FK + 3 new webhook event types + `a.Inbounds.WithTemplates(a.InboundTemplates)` wiring. Renderer #210: `LookupTemplatesByID` interface in `internal/cores/builder/builder.go` + `GetManyByID` on `inboundtemplates.Store` (single `WHERE id = ANY($1)` batch query) + per-inbound `template_id` lookup in `BuildCoreConfigForNode` (one DB round-trip per flush, not per inbound) + per-inbound fallback to inline `params` on stale `TemplateID` or lookup error + `nil templateSrc` keeps the v0.8.0-v0.8.12 default. Validation #211: `templates *inboundtemplates.Service` field on `inbounds.Service` + `WithTemplates` setter (nil-safe, mirrors `WithWebhooks` / `WithAudits`) + `validateTemplateID` helper (rejects `templateID == uuid.Nil`, missing template, protocol mismatch with both protocols in the message). Frontend #212: `InboundTemplatesView.vue` (~600 lines, DataTable + search + Create/Edit/Delete dialogs mirroring `PlansView.vue`) + `inboundtemplates.ts` API service + `inboundtemplate.ts` zod schema + `templateId` in `Inbound` / `InboundCreateRequest` / `InboundUpdateRequest` (openapi.yaml + regenerated `api.d.ts`) + Template Select dropdown in both Create and Edit forms of `InboundsView` with protocol-filtered `templatesForProtocol(p)` helper + `/inbound-templates` route + `LayoutTemplate` nav icon + `nav.inboundTemplates` + 22-key `inboundTemplates` i18n section in both en.json + ru.json. **This is the first release where a single feature landed in 5 separate PRs in a planned sequence (foundation → docs sync → renderer → validation → frontend) — future complex features should follow the same pattern**. Migration 0021 is the only schema change. Also closes the audit-3.1 fix chain server side: PR #214 (HttpOnly refresh cookie, `setRefreshCookie` + `clearRefreshCookie` + `readRefreshToken` helpers; `Service.cookieSecure bool` field plumbed by main.go from `cfg.Env == "production"`; new `Store.RevokeOne` idempotent method; new public `POST /api/v1/auth/logout` route; `Path=/` because the panel is mounted under a rotated sub-path; 8 new unit tests). Plus frontend PR #215 (`withCredentials: true` on axios; access token in Pinia `ref` only; refresh token NEVER in JS; new `auth.boot()` page-load rehydration; new `auth.logout()` async calling `POST /api/v1/auth/logout`). Caddy CSP PR #216 (strict CSP in `deploy/caddy/Caddyfile.panel` for `/s3cr3t-p4n3l-*/*` admin path: `default-src 'self'`, `script-src 'self'`, `style-src 'self' 'unsafe-inline'`, `img-src 'self' data:`, `font-src 'self' data:`, `connect-src 'self'`, `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`, `object-src 'none'`). v0.8.13 is the first release where a single feature + a security fix chain both shipped together | ✅ shipped (#205, #209, #210, #211, #212, #214, #215, #216, #213) |
| `v0.8.14`            | Consolidation + security tightening release: closes the v0.8.13 backwards-compat shim that kept the refresh token in the JSON body of `/auth/login` and `/auth/refresh` for one release. v0.8.14+ is cookie-only: drop the `RefreshToken` field from the `loginResponse` struct (login + refresh); drop the `refreshRequest` struct (only used in the body-fallback parse); simplify `readRefreshToken` to cookie-only (no body parse, no `json.NewDecoder(r.Body).Decode` call); 400-on-missing-cookie is now `refresh token cookie is required` (was `cookie or body`); remove the `RefreshRequest` openapi schema; drop the `refresh_token` from the `LoginResponse` schema's `required` list; document the previously-undocumented `POST /api/v1/auth/logout` endpoint (the `204 No Content` + clear-cookie contract); regenerate `frontend/src/types/api.d.ts`; drop `refreshToken` from the `LoginResponse` TS interface. `doLogin` test reads refresh from the cookie; the body-fallback test is inverted to `TestHandleRefresh_BodyIsNotRead` (a body-only request gets 400, MUST NOT set a `aegis_rt` cookie — regression guard against reintroducing a body-derived cookie). v0.8.14 is a **drop-in replacement for v0.8.13** on the server side; the rolling-upgrade pattern is the standard "server before client". Closes the audit-3.1 finding end-to-end (HttpOnly cookie + frontend in-memory only + Caddy CSP — the body-field drop removes the last exfiltration path for an XSS payload targeting a pre-v0.8.14 client). The on-disk prod is unchanged from the v0.8.9 deploy; v0.8.14 is the canonical reference for the next hotfix branch and the cleanest snapshot for the v0.9.0 fresh-VM smoke test | ✅ shipped (#217, #218) |
| `v0.8.15`            | Multi-stage Dockerfile for `pg_dump` + `aegis-agent` + bootstrap `writeError` logging (PR #222, squash merge `6a46881`). Adds a `debian:12-slim` tooling stage that runs `apt-get install postgresql-client`, switches the runtime base from `distroless/static` to `distroless/base`, copies `pg_dump` + the whole `/usr/lib/x86_64-linux-gnu` tree into the runtime, adds a second `go build` for `./cmd/aegis-agent` in the same build stage, copies the resulting binary to `/app/bin/aegis-agent`, sets `AEGIS_AGENT_BINARY=/app/bin/aegis-agent` as the runtime default. Also adds a `log.Error().Int("status",...).Str("error",...).Msg(...)` line to `internal/bootstrap/handler.go`'s `writeError` so every 4xx/5xx lands in the structured log stream. v0.8.15 is a **drop-in replacement for v0.8.14** on the server side. Image size grows by ~50 MB (mostly the .so tree). | ✅ shipped (#222, #223) |
| `v0.8.16`            | `postgresql-client-15` + `joinHostPort` host:port parse fix (PR #224). v0.8.15 still used a symlink to the distroless `pg_wrapper` shell script → no shell in the runtime image → `pg_dump` exited 1 silently. v0.8.16 installs the real client package + handles `host:22:22` (double-colon) parse path. | ✅ shipped (#224, #225) |
| `v0.8.17`            | `rm /usr/bin/pg_dump && cp /usr/lib/postgresql/15/bin/pg_dump /usr/bin/pg_dump` in the tooling stage of the multi-stage Dockerfile (PR #226). v0.8.16's installed symlink was still pointing at the wrapper; this commit replaces the symlink with the real binary. | ✅ shipped (#226, #227) |
| `v0.8.18`            | `Dumper` / `Restorer` interfaces + `pgDumpArgs` / `pgRestoreArgs` pure functions (PR #228). Architectural refactor: replaces the single `dumpFn` callback with consumer-side interfaces; the service holds the full DSN in `Config` and delegates extract to injected `Dumper` / `Restorer`. Fixed 3 silent bugs: full-DSN was stripped to bare db name, `pg_dump` exit code was discarded, `pgDumpReader.Close()` now returns the subprocess exit code. `pgDumpArgs` is a pure table-tested function; PGPASSWORD is set via env, NEVER argv. | ✅ shipped (#228) |
| `v0.8.19`            | `pg_dump` 15 → 16 via PGDG apt repo (PR #229). v0.8.18 fixed the silent-fail mode but the binary was still pg_dump 15 against a postgres-16 server, which fails with "server version mismatch". v0.8.19 adds the PGDG GPG key + `apt.postgresql.org` repo in the tooling stage + installs `postgresql-client-16` + copies the real binary. Live smoke on the first v0.8.19 deploy: backup row `status="ok"`, `size_bytes=21982` (real 21KB dump, not 23 bytes). | ✅ shipped (#229) |
| `v0.8.20`            | `bootstrap.hostKeyCallback` TOFU-policy fix (PR #230). Pre-PR the callback early-returned the strict `knownhosts.New` whenever the `known_hosts` file existed (even an empty one), which short-circuited the TOFU policy entirely. v0.8.20 lifts the TOFU logic to be the single source of truth. | ✅ shipped (#230) |
| `v0.8.21`            | SSH fingerprint from binary wire format (PR #231). Pre-PR the panel used Go's `ssh.FingerprintSHA256` which hashes the *authorized_keys* line format, not the binary wire format. v0.8.21 adds `sshFingerprintWire` helper that SHA-256s `key.Marshal()` + strips trailing `=`. | ✅ shipped (#231) |
| `v0.8.22`            | `HostKeyAlgorithms: []string{ssh.KeyAlgoED25519}` (PR #232). Pre-PR the panel accepted any of `{rsa, ecdsa, ed25519}`; the server's `kexinit` preferred ECDSA, the operator's ed25519 pin was rejected as "mismatch". | ✅ shipped (#232) |
| `v0.8.23`            | `stripFingerprintPrefix(fp)` + `fingerprintEqual(a, b)` (PR #233). Pre-PR the compare was literal: `pCnGi…` ≠ `SHA256:pCnGi…`. v0.8.23 strips `SHA256:` / `MD5:` (case-insensitive) from both sides. | ✅ shipped (#233) |
| `v0.8.24`            | `BootstrapNodeProvider.Update` propagates `State` (PR #234). Pre-PR the method mutated `current.State` locally then called `a.Svc.Update(ctx, current.ID, UpdateInput{})` with an empty struct; `UpdateInput` is pointer-field, all-nil = "leave alone" = no SQL UPDATE. v0.8.24 passes the new state via `UpdateInput{State: &newState}`. | ✅ shipped (#234) |
| `v0.8.25`            | `Client.UploadAndSwap(ctx, src, dst, mode)` for ETXTBSY-safe binary replacement (PR #235). Pre-PR the SFTP step did direct overwrite of `/usr/local/bin/aegis-agent`, which Linux refused with `ETXTBSY` on a re-provision of a running node — the agent's mmap'd text region can't be unlinked by another process. v0.8.25 splits the upload into SFTP-to-temp (`.aegis-agent.swap.<8-hex>`) + `mv -f` over the target via SSH; `rename(2)` is always permitted, the running process keeps the unlinked inode alive until it exits. Mock seam in `installer_test.go` records `uploadSwapPaths` separately from `uploadPaths`; the `TestInstaller_SuccessPath` assertion specifically checks that the agent-binary path uses `UploadAndSwap` (regression guard). End-to-end verified on the live server: v0.8.25 deployed, Demo-нода re-provisioned without the `systemctl stop` workaround that v0.8.24 needed. | ✅ shipped (#235) |
| `v0.8.26`            | `chore(release)` CHANGELOG surgery cut (PR #240). No application code change. Re-anchors the v0.8.25 `UploadAndSwap` closure in the docs tag. | ✅ shipped (#240) |
| `v0.8.27`            | Anti-leak infrastructure end-to-end: `AGENTS.md` + `tools/scripts/check-sensitive.sh` scanner + pre-commit hook + CI gate (PR #241); `release.yml` hard-gate smoke test that runs before cosign re-sign and refuses to advance a release where `pg_dump --version` doesn't return a real binary (PR #247); recreated `docs/gap-analysis-v0.8.24.md` closing 3 broken cross-links (PR #246); `docs/RUNBOOKS/oncall.md` incident-response playbook (PR #251); `docs/RUNBOOKS/deploy.md` §3.3/§5 production-state sync (PR #252); gitignore `.local/` (PR #249); Go 1.26.5 → 1.26.6 govulncheck bump (PR #248); `tools/scripts/branch-start.sh` / `release.sh` `--dry-run` / `--snapshot` hardening (PR #250). v0.8.27 is the first release where the anti-leak infrastructure gates a merge end-to-end (the `docs/sync-*` PRs after this release go through the pre-commit + CI + agent banned-list all three). Release tag at `6b48879`. | ✅ shipped (#240, #241, #246-#252) |
| `v0.8.28`            | Tier 3 dialog extraction closeout (PRs #254-#270) + Tier 1 #3 (backup cron) closeout (PRs #273-#275) + anti-leak infra hardening (PR #272). 5 dialog-extraction PRs split HostsView and NodesView into 8 self-contained dialog components under `frontend/src/views/dialogs/`: HostCreateDialog + HostEditDialog (#265), NodeCreateDialog + NodeEditDialog (#266), NodeProvisionDialog (#267), NodeRotateDialog (#268), NodeRefreshDialog + NodeInspectDialog (#269). Adjacent refactors: `ChangePasswordRequest` dedup (#254), `window.confirm` → `ConfirmDialog` migration (#256), typed `as Parameters<...>` casts replacing `as never` (#263). Two perf wins: `camelizeKeys` memoization for large response bodies (#255) and a new `GET /api/v1/inbounds` batch endpoint that replaces the per-row `GetByNode` fan-out during HostsView + NodesView open (#264). 52 new vitest tests across 8 dialog test files; the project total goes from 39 → 91 (#270). Tier 1 #3 backup-cron hardening: cron parser extended to the full Vixie `N-M` / `N-M/S` / `*/S` / `N,M,K` construct set (#273); 33 scheduler goroutine tests across 4 new test functions in `scheduler_test.go` (#274); admin-UI surface — `Service.ReloadCron` + `GET /api/v1/backups/schedule` endpoint + `Backups → Schedule` section in `BackupsView.vue` + 10 i18n keys + OpenAPI schema bump to `0.8.28` (#275). PR #272 adds a `ghp_` / `github_pat_` regex to `BANNED_PATTERNS` in `check-sensitive.sh` (and the AGENTS.md mirror), closing the 2026-08-20 3-PAT incident loop. 7 v0.9.1 follow-up items parked (data race in `scheduler.maybeFire`, `GET /schedule` handler tests, `scheduleActive` semantic, `POST` endpoint for hot-reload, weekly orphan-file cron, `BackupsCron` field naming, doc syntax examples). Release cut at `4a3c31a`. | ✅ shipped (#254-#270, #272-#275) |
| `v0.8.28.6`          | No image change — pure ops + code-quality batch landed against `main` (2026-08-24). **Issue #289 (4-bug chain C1–C4, blocked all of #289's work)**: close agent response body in `postApply` to stop the FD leak (PR #292, C4); drop `host_endpoints_path_check` (DB-schema self-contradiction: `path TEXT NOT NULL DEFAULT ''` + `CHECK (path <> '')` — only surfaced on the first-ever host creation in prod, which was the v0.8.28.6 smoke; migration `0022_relax_host_endpoints_path.sql` applied out-of-band before PR #298 landed so the smoke could continue); make the subscription credential cache render-local (cross-user leak fix, PR #293, C3 with the C3b 400-goroutine data-race patch); wire `Subs.WithCreds` AFTER `Credentials` is built (Phase 2 dead-wiring fix, PR #294, C2). **Compose install contract (#297)**: `tools/scripts/aegis-stack.yml` + `install-aegis-stack.sh` + `docs/operator-install.md` — `cd /opt/aegis && docker compose up -d` is now the canonical install/upgrade entry point. **D2-immediate (#299)**: delete the duplicate `NodeProvisionResponse` declaration in `frontend/src/types/aegis.ts` (TypeScript interface-merging was silently hiding the drift). **D3 (#300)**: introduce `internal/httpjson` package (WriteJSON / WriteError / String on `encoding/json`) and migrate 12 handler files / 11 packages — the `jsonString` / `writeError` / `writeJSON` / `jsonEscape` copy-paste escapers were producing invalid JSON for non-BMP runes (emoji in user/host names, e.g. `"\u1F680"` 5-hex-digit instead of `"\uD83D\uDE80"` surrogate pair). **#290 D1 (MemoryStore/PgStore divergence) → BACKLOG** per operator (2026-08-24): 12 mini-PRs per package, start with `subscription`, then `users/credentials` (security), then the remaining nine. | ✅ shipped (#292, #293, #294, #297, #298, #299, #300) |
| `v0.8.31.1`          | Image rebuild — three v0.8.30/31 mTLS install-pipeline hotfixes that every fresh deploy needed, all closed in a single PR (4 commits + 5 new unit tests, ~150 LOC). **Fix 1 (`a.AgentCA.EnsureRoot` in `app.Build`)** — `internal/agentca/service.go:EnsureRoot` was defined but no production code called it; the bootstrap `MTLSCerts` factory returned `ErrNotFound` on every fresh install and the provisioner's `mintMTLSCerts` silently swallowed the error; the installer's `writeMTLSCerts` skipped the cert push; the v0.8.31+ agent then hard-failed to start without `/etc/aegis/agent.{crt,key,ca.pem}`. One-line wiring + `TestBuild_EnsuresAgentCARoot` regression guard. **Fix 2 (`//go:embed` migration source + fail-loud override check)** — moved 24 SQL files from `backend/migrations/` to `backend/internal/migrations/sql/`; `//go:embed all:sql/*.sql` in `migrator.go`; the host mount at `/var/lib/aegis/migrations` is now an optional operator override (hot-fix path) rather than the only source of truth. `resolveSource` returns a loud error naming the missing files when the host mount is non-empty but partial — the exact failure mode that hit prod 2026-08-25 (host had 0001-0022, missing 0023-0024, panel booted with partial schema, singbox wiring crashed on `n.agent_transport`). 5 new `TestResolveSource_*` regression guards. **Fix 3 (verify deadline 5s → 30s)** — v0.8.30+ agent takes >5s to load mTLS certs + bind gRPC on a fresh install; the 5s deadline was tuned for the v0.4.0 placeholder (`sleep infinity`, instant `active`); pre-fix behaviour: the LAST probe at the 5s mark often succeeded but the deadline had expired, so the loop returned `ErrVerifyFailed` with state="active" in the error; the provisioner transitioned the node to `offline` and the operator had to SQL-UPDATE the state back to `online` by hand. `verifyDeadline` is now a package-level `var` (not const) for test seam. `TestInstaller_VerifyAcceptsSlowAgent` exercises the multi-poll path. **Docs** — `docs/operator-install.md` §"Schema migrations (v0.8.31.1+)" rewritten to document the new contract (host mount is optional; fail-loud behaviour; "Why this changed" block pointing at the 2026-08-25 prod incident). CI lint path updated. Release tag at `v0.8.31.1`; image `ghcr.io/qadversif/aegispanel:0.8.31.1`; operator-side: `cd /opt/aegis && docker compose pull && docker compose up -d aegis-panel` (the install script + compose handle the rest). | ✅ shipped (PR #316) |
| `v0.8.32`            | Image rebuild — three post-v0.8.31.1 UX/observability cleanups that close gaps the hotfixes exposed but did not fix (3 commits + 5 new unit tests, ~330 LOC). **Cleanup 1 (agent help text + log hint)** — the `-mtls-{cert,key,ca}` flags claimed "empty = plaintext fallback" (the v0.8.29 transitional posture, removed in v0.8.30+); the operator saw a bare `read cert /etc/aegis/agent.crt: no such file or directory` with no remediation hint. Post-fix: the flag docs say "REQUIRED in v0.8.30+ — agent refuses to start the gRPC server if the file is missing"; `loadMTLSConfig` wraps the read error with a `hint:` prefix naming the bootstrap install endpoint AND the manual scp path. New `TestLoadMTLSConfig_MissingFile_HasInstallHint` regression guard. **Cleanup 2 (migration Down-section warning)** — both `0023_agentca.sql` and `0024_add_nodes_agent_transport.sql` contain BOTH a `-- +migrate Up` and a `-- +migrate Down` section; running the file directly via `psql -f` executes BOTH sections, which creates the schema and immediately drops it. The 2026-08-25 prod session that required manual `psql -f` migration application hit this. Post-fix: a `-- v0.8.32 follow-up:` warning block at the top of each file + a new `### psql -f direct-execute warning` section in `docs/operator-install.md` with a copy-pasteable `sed` snippet to extract the Up section. **Cleanup 3 (`config.validate()` completeness)** — two env vars that should have been required but were silently tolerated in the v0.8.28 prod env: `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE` accidentally deleted (only `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS` survived; first webhook fire returned 502) and `AEGIS_AGENT_BINARY=/usr/local/bin/aegis-agent` (the node-side path; first provision returned 502 with `stat in.AgentSource: no such file or directory`). Post-fix: `validate()` fails loud at boot when `AEGIS_WEBHOOKS_BACKEND=pg` and either envelope env is missing, OR when `AEGIS_AGENT_BINARY` is the exact string `/usr/local/bin/aegis-agent`. 4 new `TestValidate_*_Reused` / `TestValidate_ContainerSideAgentBinary_Passes` regression guards. Ship as a separate tag (no fold-into-0.8.31.2) so the release record makes the operator-surface cleanup visible; an operator who has already pulled v0.8.31.1 and is happy with the hotfixes can defer this to the next scheduled deploy. | ⏳ planned (this PR) |
| `v0.9.1`            | Close the 7 v0.8.28-deferred Tier 1 #3 follow-up items. (1) Resolve the load-bearing data race in `scheduler.maybeFire` (concurrent `loadSchedule` + `setNextFire` on the same `Scheduler` struct). (2) Add handler tests for `GET /api/v1/backups/schedule` covering the `cron` + `retentionDays` + `maxCount` + `scheduleActive` response shape, the `backups`-scope guard, and the empty-AEGIS_BACKUPS_CRON manual-only `scheduleActive: false` case. (3) Document the `scheduleActive` semantic — `true` means the scheduler goroutine is running AND a `cron` expression is set AND it's parseable; `false` is manual-only mode (either `AEGIS_BACKUPS_CRON` is empty OR the expression failed to parse at boot). (4) Wire a `POST /api/v1/backups/schedule` endpoint that calls `Service.ReloadCron` and returns the refreshed schedule; add a matching form in the `Backups → Schedule` UI section. (5) Add a weekly cron that sweeps orphan on-disk dump files (rows already deleted from the DB but the file still in `/var/lib/aegis/backups/`). (6) Field-naming consistency for `BackupsCron` — pick a single convention (env var, OpenAPI schema, Service.ReloadCron signature, UI form field) and propagate. (7) Add syntax examples for the new Vixie constructs in `docs/operator-guide.md` §"Cron expression syntax" and `KNOWN_LIMITATIONS.md` (e.g. `0 9-17/2 * * *` for every 2 hours from 09:00 to 17:00, `30 0 * * 1-5` for weekdays at 00:30, `0 0 1,15 * *` for the 1st and 15th of every month at midnight). Pure Tier 1 #3 batch; no schema changes, no new env vars beyond the optional POST endpoint payload shape. | ⏳ planned (post-v0.8.28) |
| `v0.8.x`             | host → node mapping in Builder filter (PR #192); subscription URL display in UsersView (PR #193); per-user credential filter in Builder (PR #198, **shipped in v0.8.10+** — closes the v0.7.x Phase 2 multi-user TODO); merged "Add node + Provision" dialog (PR #201, **shipped in v0.8.12+**); operations polish (pre-existing eslint warnings cleanup, PR #200, **shipped in v0.8.12+**); shadcn-vue `RadioGroup` primitive (PR #202, **shipped in v0.8.12+**); inbound-templates work (per-tenant `Params` defaults, **PR #205..#212 shipped in v0.8.13+** — 5-PR planned sequence: foundation → docs sync → renderer → validation → frontend); audit-3.1 fix chain (HttpOnly refresh cookie + frontend `withCredentials` + Caddy CSP, **PRs #214, #215, #216 shipped in v0.8.13+**); v0.8.13 body-field shim closure (**PR #217 shipped in v0.8.14**); v0.8.15..v0.8.25 silent-bug chain (multi-stage Dockerfile + pg_dump real binary + PGDG pg_dump 16 + Dumper/Restorer + TOFU policy reachable + sshFingerprintWire + ed25519 HostKeyAlgorithms + stripFingerprintPrefix + BootstrapNodeProvider.Update state propagation + Client.UploadAndSwap, **PRs #222..#235 shipped in v0.8.15..v0.8.25**); anti-leak infra + `release.yml` smoke gate + oncall runbook + recreate gap-analysis (**PRs #241 / #246 / #247 / #249 / #250 / #251 / #252 shipped in v0.8.27+**); Tier 3 dialog extraction + `window.confirm` → `ConfirmDialog` + typed casts + `camelizeKeys` memoization + `GET /api/v1/inbounds` batch endpoint + 52 new dialog vitest tests (**PRs #254-#270 in v0.8.28**) | ✅ all shipped |
| `v0.9.0`             | Smoke test on fresh VM in CI (terraform + ansible + boot log artifact) + `release.yml` hard-gate smoke test (the single most-important infra change to prevent future silent bugs — would have caught every one of the v0.8.15..v0.8.25 bugs before publish). See [`docs/gap-analysis-v0.8.24.md`](./gap-analysis-v0.8.24.md) §6 Tier 1. | ⏳                   |
| `v1.0.0-mvp-soft-launch` | GA tag — minimum surface for the public release. v0.8.25 unblocks the code path; v0.9.0 unblocks the operational confidence. | ⏳                   |

## Path C: v0.4.0-d consolidation

The d.1 PR (#95) shipped a `users` package that duplicated
`subscription`'s user-CRUD. Path C is the consolidation:

1. **d.1 (#95)**: `internal/users` data layer — landed.
2. **d.r1 (#96)**: `users.User` wire-format compat with
   `subscription.User` (snake_case JSON, `[]uuid.UUID` for
   hosts allow/block lists). Made the move possible without
   render-code churn. — landed.
3. **d.r2 (#97)**: Drop subscription-side Store / MemoryStore /
   PgStore user-CRUD. Type-alias `User = users.User` makes
   the render code (`render.go` / `render_singbox.go` /
   `render_clash.go` / `render_vars.go`) a no-op compile
   change. Service keeps 4 thin wrappers for the admin +
   render paths. — landed.
4. **d.r3 (#99)**: Move `admin_handler.go` to
   `internal/users/admin_handler.go`. Drop the 4 thin
   wrappers from `subscription.Service`. Render handler
   consults `*users.Service` directly for the
   `sub_token`-→-user lookup. — landed.
5. **d.r4 (#100)**: Cleanup pass + this roadmap.
   `DefaultSubTokenRotationGrace` is now a public constant
   on `users.Service` (was a test-internal re-export).
   Subscription package doc trimmed of the d.r2
   "AEGIS_USERS_BACKEND" reference. — landed.

**Net Path C diff:** `internal/subscription` shed ~600 lines
(Store / MemoryStore / PgStore user-CRUD + Service thin
wrappers + admin handler); `internal/users` gained ~900
lines (d.1 + d.r1 + d.r2 + d.r3 + d.r4 in this PR). The
subscription package is now a pure render orchestrator.

## v0.4.0 release workflow contract

The `release.yml` workflow supports two event
paths that now produce **identical** GHCR tag
lists for both images:

- **Tag-push** (`git push origin vX.Y.Z`) —
  `github.event_name = 'push'`,
  `github.ref_name = 'vX.Y.Z'`. Login + push +
  Create GitHub release all run. Panel
  `metadata-action` emits `[X.Y.Z, X.Y, latest]`;
  UI tagged `ghcr.io/qadversif/aegispanel-ui:vX.Y.Z`.
- **workflow_dispatch re-run**
  (`gh workflow run release -f tag=vX.Y.Z`) —
  `github.event_name = 'workflow_dispatch'`,
  `github.ref_name = 'main'`. Login + push run;
  Create GitHub release is SKIPPED. Panel
  `[X.Y.Z, X.Y, latest]` (via the
  `Compute release version` step + raw tags);
  UI `:vX.Y.Z` (via `env.release_tag`).

This contract was fixed across four PRs
(#102, #103, #104, #111) that landed on
`main` *after* the `v0.4.0` git tag (which
points to `39d4d9e`). The fixes are
infrastructure-only (no application code
change) and are documented under `[Unreleased]`
in `CHANGELOG.md` to be picked up by the next
`v0.4.1` / `v0.5.0` release. The previous
behaviour — push silently disabled on
`workflow_dispatch`, UI image tagged with the
branch name, panel `X.Y.Z` / `X.Y` tags left
on the original tag-push digest — is gone.

The `latest` tag follows correctly in both
cases: skipped for prerelease (`-rc` / `-beta`
/ `-alpha`) on tag-push via the
`flavor: latest=auto`; emitted on
`workflow_dispatch` from the default branch
via the raw `enable={{is_default_branch}}`.

## v0.5.0 — polish before v0.6.0+

Scope is the "operations-grade" feature set the panel
needs to be deployable for the soft launch. **All eight
items landed in #119–#126.**

- **sops+age secrets (`configure_secrets` role)** — #119.
  The panel host decrypts `secrets.yml.enc` to
  `secrets.env` (mode 0600, owner `aegis-deploy`).
  The plaintext never leaves the host; CI never decrypts.
- **`internal/backups` package** — #120. `pg_dump -Fc | gzip`
  with SHA-256 sidecar; per-node queue; 20s window;
  `inflight sync.Mutex` for single-flight. HTTP
  endpoints at `/api/v1/backups/` gated by `ScopeBackups`
  plus `AEGIS_BACKUPS_ALLOW_UI_RESTORE` (the latter is a
  sanity check, not a security boundary — the DSN is).
- **Backups UI (`BackupsView.vue`)** — #121. List, trigger,
  download, delete. Wire format: `Backup{ID, CreatedAt,
  SizeBytes, Trigger, Status, Error, SchemaVersion,
  NodeCount, UserCount, HostCount, ChecksumSHA256, Path}`.
  Trigger is `manual | scheduled`. Status is
  `running | ok | failed`.
- **Pre-PR local gate (`tools/scripts/pre-pr.sh`)** — #122.
  gofmt + golangci-lint v2 + vue-tsc + eslint +
  markdownlint-cli2 + go test -short + npm run codegen:check
  locally. Scope flags: `--backend`, `--frontend`, `--docs`,
  `--quick`. Makefile targets: `pre-pr`, `pre-pr-install`.
  Pre-push hook installer: `tools/scripts/install-pre-push.sh`.
- **GitHub API SHA-256 fetch for sing-box
  (`install_singbox` role)** — #123. Replaces the
  v0.4.0-c hardcoded digest with a runtime fetch from
  `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v{{ version }}`.
  **GPG-verify was the original scope; dropped** —
  SagerNet does not publish detached GPG / minisign
  signatures or a `SHA256SUMS` file. The trust model
  is therefore the GitHub API response (TLS + GitHub's
  signing keys), not a stronger guarantee than
  "trust GitHub". Cosign sign + verify for **our** Docker
  images is the v0.5.x equivalent for the panel/agent
  supply chain and is a separate, future PR.
- **Container wiring for #119 secrets (`install_panel` role
  plus `docker-compose.prod.yml.j2`)** — #124. The
  `secrets.env` file is bind-mounted read-only into the
  panel container via `env_file:` (with `required: true`).
  Loopback-only port 8080. Data services (Postgres /
  Redis / NATS) are operator-managed; the role refuses
  to start the panel without the secrets file present.
  The `aegis-agent.service` unit gains a secondary
  `EnvironmentFile=-/etc/aegis/secrets.env` (the
  systemd `-` prefix tells systemd to silently skip
  the file if missing; the per-node `agent.env` still
  wins on key collision).
- **Operator-side backup CLI (`aegis-pg-backup` +
  `aegis-pg-restore`)** — #125. Two separate binaries:
  `aegis-pg-backup` is the safe default (list / get /
  create / delete / download), `aegis-pg-restore` is the
  intentional destructive path. The split enforces the
  safety boundary at the process level: an operator who
  types `aegis-pg-backup restore <id>` gets an
  `unknown subcommand` error, not a silent data wipe.
  Both binaries are JSON-to-stdout cron-friendly; the
  restore CLI does a two-step id confirmation before
  the destructive op and supports `--dry-run` for
  eyeball checks via `pg_restore --list`.
- **Operator guide + security policy + quickstart docs**
  — #126 (this PR). `docs/operator-guide.md` (the
  canonical install + daily-ops reference),
  `docs/SECURITY.md` (the threat model + disclosure
  flow + supply-chain trust), `docs/guide/quickstart.md`
  (the 5-minute "fresh VPS to panel running" path).
  The `deploy/secrets/README.md` is the field-by-field
  sops+age workflow; the operator guide links to it.

**Deferred from the original v0.5.0 scope (the
"ничего резать не будем" decision does NOT apply to
items that were not in the original scope):**

- **JSON logs** — the zerolog ConsoleWriter / JSON
  switch was on the v0.5.0 list. The implementation
  is a one-line `AEGIS_ENV=production` toggle in
  `cmd/aegis/main.go` and a test in
  `internal/config/config_test.go`; the
  `internal/log` package's `New(env string)` already
  returns the right writer. **A v0.5.x follow-up;**
  the work is small but the CI round-trip on a one-line
  code change was not worth the cycle time. Operators
  who need JSON logs today can run `docker logs aegis-panel
  | jq` on the ConsoleWriter output (the format is
  already `key=value` with timestamps, not pure freeform).
- **Cosign sign + verify for our Docker images** — the
  v0.5.x follow-up. The release workflow has the
  `metadata-action` step, which is the natural
  integration point for `cosign sign` post-push. Until
  then, the trust model is the same as the OCI
  registry's authentication (TLS + GitHub's OIDC
  token).
- **Smoke test on fresh VM in CI** — out of v0.5.0
  scope. The `bootstrap_node + configure_secrets +
  install_panel` playbooks are tested in
  ansible-lint + the role defaults dry-run; a full
  VM bootstrap test is a v0.5.x follow-up.
- **GPG-verify sing-box** — the v0.5.0 plan called for
  a `gpg --verify` step on the sing-box tarball. SagerNet
  does not publish detached signatures, so the work
  was de-scoped in #123. The GitHub API digest is the
  trust model.

## v0.6.0 — `internal/plans` ✅ shipped

The `plans` table is in migration 0001; the package
exists as a `doc.go` stub (#77). v0.6.0 shipped the
full CRUD surface across the Go backend, the
HTTP layer, the OpenAPI spec, and the admin UI.

Closed by: PR #131 (Go package: Plan model +
ResetPeriod closed enum + Store interface +
MemoryStore + PgStore + Service with input
validation + 23 unit tests + 4 pg integration
tests), PR #132 (admin HTTP handler + ScopePlans
auth + AEGIS_PLANS_BACKEND config + router/main
wiring + 11 e2e handler tests), PR #133 (OpenAPI
spec + hand-mirrored API client + regenerated
types), PR #134 (PlansView.vue + sidebar nav +
i18n en/ru + zod form schema). Tag `v0.6.0`
after the docs PR lands.

What landed:

- `plans.Store` interface (MemoryStore + PgStore)
  backed by the `plans` table from migration 0001.
- `plans.Service` with input validation
  (Name 1..64 chars, Duration [1 minute, 10 years],
  ResetPeriod enum, non-negative numbers) and the
  ID / timestamp generation on Create.
- Admin handler at `plans.AdminRouter(plansSvc, auth)`
  behind `auth.RequireScope(auth.ScopePlans)`.
- Route mount: `r.Mount("/plans", plans.AdminRouter(...))`.
- Wire format: `plans.Plan` JSON DTO. Duration is
  int64 nanoseconds on the wire (the Go side
  stores it as a Postgres INTERVAL via
  `pgtype.Interval` with `Valid: true`; the UI
  formats ns back to a "30d" / "1h" string at the
  rendering layer).
- Frontend: `PlansView.vue` with the list +
  create + edit + delete dialogs + global search.
- Frontend: `plans.*` i18n namespace in en/ru,
  sidebar nav entry, OpenAPI codegen.
- 23 unit tests + 4 pg integration tests in the
  Go package; 11 e2e tests in the handler.

Deferred to v0.6.x:

- `plan_pool` writes (the join table linking
  plans to host pools). v0.6.0 keeps the
  read-only view in `internal/subscription`.
- `plan_pool` UI (no HostPool picker in the plan
  dialog yet).
- Audit log writes from the mutating handler
  (the call-site wiring is a separate batch
  across all admin handlers).
- Zod schema at `frontend/src/schemas/plan.ts`
  (the v0.6.0 view uses inline zod via
  `useZodForm`; a shared schema file lands when
  the UI matures).

## v0.7.0 — `internal/webhooks`

Same shape as plans: the `webhooks` table is in
migration 0001, the `doc.go` stub is in #77. The
v0.7.0 work:

- `webhooks.Store` (MemoryStore + PgStore).
- `webhooks.Service` with delivery (HTTP POST to the
  configured URL) + retry (exponential backoff,
  max 5 retries).
- Admin handler at `webhooks.AdminRouter(webhooksSvc, auth)`.
- Event-emission hook: when an event package
  (post-v0.6.0 / v0.8.0 `events` package) fires, the
  webhooks service delivers to all enabled subscriptions
  matching the event type.
- HMAC signature on the payload (operator-configured
  secret on the webhook row; SHA-256 in the
  `X-Aegis-Signature` header) so the receiver can
  verify the source.

## v1.0.0-mvp-soft-launch

The minimum surface for the public release:

- The v0.4.0-mvp-batched end-to-end flow (panel →
  aegis-agent → sing-box config write → reload) on at
  least one node with at least one user.
- The v0.4.0-d Path C consolidation (this PR plus
  #99, #97, #96, and #95).
- A `docs/operator-guide.md` for the soft launch
  operators.
- A `SECURITY.md` with the disclosure policy and the
  GPG-verify path for sing-box.
- The four empty packages that are NOT in the v1.0
  cut: `cabinet`, `caddy`, `cascades`, `decoy`,
  `events`, `mcp`, `notifications`, `plans` (v0.6.0),
  `stats`, `subscriptions-plural`, `webhooks` (v0.7.0).
  v1.0 ships without them; they land post-v1.0 in
  named v1.x releases.

## Open gaps (post-v0.4.0 audit)

11 packages are `doc.go`-only and un-wired (per the
v0.4.0-d audit). Of these, `plans` and `webhooks` are
on the v0.6.0 / v0.7.0 path; the remaining 9 are
post-v1.0:

- `cabinet` — end-user self-service UI (sub-token
  rotation, traffic stats, plan view). v1.2+ target.
- `caddy` — Caddy reverse-proxy admin API integration.
  Not a v1.0 dependency (Caddy runs out-of-band).
- `cascades` — the existing `BatchedApplier` already
  has cancel/replace semantics; cascades is the
  multi-user delta. v1.1+ target.
- `decoy` — decoy site content storage. v1.0 ships with
  the decoy-site static content as a single tarball
  (no admin UI); the v1.1+ work is a per-decoy CRUD
  surface.
- `events` — internal event bus. v0.7.0 (webhooks)
  has the minimum event-emission hook; the full
  events package lands v1.1+ with the audit log
  shape.
- `mcp` — Model Context Protocol server. v1.2+ target.
  Out of scope for the soft launch.
- `notifications` — outbound notification channels
  (Telegram, email, Discord). v1.2+ target.
- `stats` — per-user traffic stats (the
  `traffic_used_bytes` column is updated by the
  agent on every Apply). v1.1+ target.
- `subscriptions-plural` — the external Squads UI
  (the multi-tenant surface). v1.3+ target.

## Tagging policy

- v0.x.y tags land in `git tag -a v0.x.y -m "..."`
  on `main` after the milestone PR merges.
- The tag message includes the per-PR list (e.g.
  for v0.4.0-d: #95, #96, #97, #99, this PR).
- `CHANGELOG.md` is updated with the per-PR summary
  in the same merge as the tag.
- v1.0.0-mvp-soft-launch is the only tag in the v1.0
  range; v1.0.x patch releases are bugfixes only,
  no new features.
