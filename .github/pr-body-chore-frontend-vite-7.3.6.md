# chore(deps-dev): bump vite 7.3.0 → 7.3.6 (patch batch)

Closes the 6 dependabot security advisories
that dependabot opened against the just-merged
vite 7.3.0 (in #84):

- #30 — Vite: arbitrary file read via WebSocket
  (HIGH, dev-only).
- #31 — Vite: `server.fs.deny` bypassed via
  requests (HIGH, dev-only).
- #34 — Vite: `server.fs.deny` bypass via Windows
  alternative paths (HIGH, dev-only).
- #29 — js-yaml: YAML merge key chains cause
  quadratic CPU consumption (MODERATE, dev-only).
- #32 — Vite: path traversal in optimized
  dependencies `.map` handling (MODERATE, dev-only).
- #35 — launch-editor: NTLMv2 hash disclosure on
  Windows UNC path (MODERATE, dev-only).

All six are **Development-only** impacts — the
attacker needs access to the dev server
(`pnpm run dev` / `vite serve` on the operator's
machine, port 5173). The production build
(`vite build` → static `dist/` served by Caddy)
is not affected. The advisories were opened by
dependabot 36 minutes after the #84 merge that
bumped vite 6→7; they were not introduced by #87
(the footgun-fix PR), they exist in vite 7.3.0
itself.

## What changed

- `vite: ^7.3.0 → ^7.3.6` (patch). All four vite
  advisories above are fixed by one of the patches
  in the 7.3.1–7.3.6 range. `launch-editor` is a
  transitive dev-dep of vite's HMR machinery; its
  Windows UNC disclosure is fixed in the 7.3.x
  line. `js-yaml` is a transitive of the
  openapi-typescript generator and various lint
  tools; the upstream fix landed in 4.x and
  7.3.6 pulls the patched range through npm's
  resolution.

## What did NOT change

- No source code touched. The 7.3.0 → 7.3.6 delta
  is bug-fix-only per the vite changelog.
- No new transitive deps with peer consequences.
  Vue 3.5.40, vue-router 5.2.0, pinia 3.0.4,
  eslint 10.8.0, eslint-plugin-vue 10.10.0,
  vue-eslint-parser 10.4.1, vitejs/plugin-vue 6.0.8,
  typescript-eslint 8.65.0, vitest 4.1.10 — all
  unchanged.
- `package.json` dependencies section unchanged
  (vite lives in devDependencies, which is correct
  for a build-tool dep).

## Why this is a one-PR patch bump, not a followup
  to a later cycle

The user asked for option (A) — bump vite 7.3.0 →
7.3.6 — specifically because the advisories were
just opened and the bump is within the same major.
Bumping later would mean the advisories sit in the
Dependabot queue for another week (the next weekly
schedule), and any future PR that does touch vite
would have a much larger diff.

## Verified

- `pnpm run lint` — clean.
- `pnpm run type-check` (vue-tsc) — clean.
- `pnpm run build` (vite 7.3.6) — clean, 7.42s.
  Bundle sizes essentially identical to the 7.3.0
  build (the only deltas are internal library
  version strings, not user-facing imports).

## Files

- `frontend/package.json` (+1 / -1)
- `frontend/package-lock.json` (regenerated;
  ~40 lines of platform-binary version bumps
  within the 7.3.x patch range)

Total: 2 files. No app code modified.

## Followups

- None required. The 6 dependabot advisories
  should close automatically once this PR is
  merged and dependabot re-scans.
- (Out of scope) The `trivy (frontend)` container
  scan will still flag the caddy base-image CVEs
  (Go stdlib in the upstream binary); those are
  baseline-accepted per `.trivyignore`. Real CVE
  drift from future vite / rollup / esbuild updates
  will continue to surface as new advisories; the
  weekly `dependabot` schedule + the CI security
  jobs catch them.

Refs: dependabot advisories #29, #30, #31, #32,
#34, #35. ARCHITECTURE.md §21 followups.
