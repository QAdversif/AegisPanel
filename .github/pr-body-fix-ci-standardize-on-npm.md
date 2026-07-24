# fix(ci): standardize on npm + commit package-lock.json (footgun)

Closes the long-standing footgun flagged in PR #83
and the v0.3.0 cleanup batch: the project historically
carried a `package-lock.json` (npm) in git, ran
`pnpm install` in CI, and used `npm ci` in the
Dockerfile. Three lockfile sources, one of which (the
Dockerfile's `npm ci`) was the only thing that would
actually catch a package.json ↔ lockfile desync — and
only because `pnpm install --frozen-lockfile=false`
silently regenerated the lock on the CI side and the
runner was on a state where the desync didn't trip the
lint+build job.

## What changed

### `frontend/package.json`

- `pnpm.overrides.esbuild` → top-level `overrides.esbuild`.
  npm 8.3+ supports the same semantics; pnpm 11+ ignores
  the `pnpm.overrides` field (warned at us during the #84
  install). With the override moved, `esbuild` actually
  resolves to `^0.25.0` for real (the old lockfile had
  it at 0.27.7 at the top level because pnpm was not
  honouring the override; this is a real bug fix, not
  just a rename).

### `.github/workflows/ci.yml` (frontend job)

- `pnpm/action-setup@v4` removed.
- `actions/setup-node@v4` now uses `cache: 'npm'` +
  `cache-dependency-path: frontend/package-lock.json` so
  the npm cache mirrors the lockfile.
- `pnpm install --frozen-lockfile=false` → `npm ci
  --no-audit --no-fund`. The `frozen-lockfile=false` flag
  was specifically papering over the lockfile-desync
  issue that bit us in PR #83. `npm ci` fails fast on a
  desync, which is what we want.
- All `pnpm run ...` → `npm run ...`.

### `.github/workflows/security.yml` (pnpm-audit job)

- Same `pnpm/action-setup` → `actions/setup-node` +
  `cache: 'npm'` swap.
- `pnpm install --no-frozen-lockfile` → `npm ci
  --no-audit --no-fund`.
- `osv-scanner --lockfile=pnpm-lock.yaml` →
  `osv-scanner --lockfile=package-lock.json`.
  `osv-scanner` natively reads both npm and pnpm lockfiles
  (OSV.dev format); the npm lockfile is the canonical one
  in this repo so we point at it. The previous
  `pnpm install --no-frozen-lockfile` was generating a
  `pnpm-lock.yaml` on the fly that didn't necessarily match
  the committed `package-lock.json` — the same class of
  bug, just in security.

### `frontend/Dockerfile`

- The conditional `if [ -f pnpm-lock.yaml ]; then ...; elif
  [ -f package-lock.json ]; then npm ci; else npm install;
  fi` is now `if [ -f package-lock.json ]; then npm ci; else
  npm install; fi`. The pnpm-lock.yaml branch is gone
  because we don't ship one (and won't until/unless someone
  explicitly switches the project to pnpm in a future
  cycle).
- The `COPY ... frontend/pnpm-lock.yaml* ./` line is gone
  for the same reason.

## What did NOT change

- The committed `package-lock.json` itself is now actually
  load-bearing: it is the source of truth for what the CI
  lint+build job installs, what the security audit scans,
  and what the production image builds. Three toolchains,
  one lockfile.
- `package.json` dependency declarations are unchanged
  (this PR does not move any version pins — that work was
  done in the v0.3.0 cleanup-batch #82/#83/#84).
- App code is unchanged (this is purely a toolchain/infra
  change).

## Verified

- `npm ci --no-audit --no-fund` against the committed
  `package-lock.json` — clean on this Windows machine
  (same install semantics CI will use).
- `npm run lint` — clean.
- `npm run type-check` (vue-tsc) — clean.
- `npm run build` (vite) — clean.
- `npm ls esbuild` — version 0.25.12, the override is now
  actually applied (was 0.27.7 in the old lockfile because
  pnpm was not honouring it).

## Files

- `frontend/package.json` (+3 / -3)
- `frontend/package-lock.json` (regenerated; +1329 / -1329.
  The big diff is npm re-laying-out the tree now that the
  `overrides` field is real: 23+ esbuild platform-specific
  binaries move from top-level `node_modules/@esbuild/*`
  (where they were forced by the override to coexist with
  the nested vite copy) to nested under
  `node_modules/vite/node_modules/@esbuild/*`, and similar
  for the rollup native binaries. No semantic change —
  the same packages, just better organised.)
- `frontend/Dockerfile` (-3 / -3, the pnpm branch and copy)
- `.github/workflows/ci.yml` (frontend job rewrite,
  +19 / -19)
- `.github/workflows/security.yml` (pnpm-audit job
  rewrite, +19 / -22)

Total: 5 files.

## Followups

- The `trivy (frontend)` and `pnpm-audit (frontend)` job
  failures on every PR are unchanged by this PR — they're
  real CVE findings in our pinned dep set, not toolchain
  mismatches. The followup work is a dependency-bump cycle
  (which we just did in #82/#83/#84) or a `.trivyignore`
  triage.
- The pnpm-audit job name is now a slight misnomer (we use
  npm + osv-scanner, not pnpm-audit). Renaming is a one-line
  followup if it bothers the reader; we left it for now to
  keep the diff small.

Refs: ARCHITECTURE.md §21 cleanup-batch followups, PR #83
pr-body footgun note, PR #84 pr-body followup note.
