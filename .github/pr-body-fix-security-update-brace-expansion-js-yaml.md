# fix(security): override brace-expansion@^5.0.8 + js-yaml@^4.3.0

Closes the 3 osv-scanner HIGH findings that
the `pnpm-audit` job (now `osv-scanner` per the
#87 footgun-fix) has been flagging on every PR
since the v0.3.0 cycle. The findings are:

- GHSA-mh99-v99m-4gvg — `brace-expansion` ReDoS
  via crafted brace patterns (CVSS 7.5).
  Affects: 2.1.2 and 5.0.7. Fixed in 5.0.8.
- GHSA-52cp-r559-cp3m — `js-yaml` unbounded
  resource consumption (CVSS 7.5).
  Affects: 4.2.0. Fixed in 4.3.0.

All three are **Development-only** impacts —
the affected paths are inside the `glob`,
`@redocly/openapi-core`, `@vue/language-core`,
and `editorconfig` dev toolchains, never in
the production bundle (the `vite build`
output was byte-identical pre/post the
override, verified locally). The runtime
impact is on dev-server / lint / codegen
throughput, not on user-facing traffic.

## Why an override, not a top-level bump

`brace-expansion` 2.1.2 is pinned transitively
by `@redocly/openapi-core@1.34.17` (range
`^2.0.1`), `@vue/language-core@2.2.12` (range
`^2.0.1`), `editorconfig@^1.0.4` (range
`^2.0.1`), and `glob@^10.4.5` (range
`^2.0.1`).
`js-yaml` 4.2.0 is pinned by `openapi-typescript`
and a few lint tools.

`npm update` alone only bumps the top-level
copy of `brace-expansion` (5.0.7 → 5.0.8);
the transitive 2.1.2 copies stay at 2.1.2
because their parents' semver ranges don't
allow ^5. The cleanest way to force all of
them onto a fixed version without bumping
the parents is a top-level `overrides` entry
— same mechanism we used for `esbuild` in
the #87 footgun fix. npm 8.3+ honours
`overrides` for nested deps and re-lays-out
the tree accordingly; the diff in
`package-lock.json` is +30 / -96 (the
negative is the old 2.1.2 platform-binary
entries being removed because npm hoists
the single 5.0.8 copy).

## What changed

- `frontend/package.json`: `overrides` block
  extended with two new entries:
  ```
  "brace-expansion": "^5.0.8",
  "js-yaml": "^4.3.0"
  ```
  The existing `esbuild: "^0.25.0"` entry is
  preserved (it's load-bearing for the
  vite-7 esbuild-native-binary pinning).
- `frontend/package-lock.json`: regenerated
  (+30 / -96). The shape of the diff is:
  - 1 old `node_modules/brace-expansion@5.0.7`
    removed (top-level, replaced by 5.0.8).
  - 4 old `node_modules/<parent>/node_modules/brace-expansion@2.1.2`
    copies removed (`@redocly/openapi-core`,
    `@vue/language-core`, `editorconfig`,
    `glob`).
  - 1 new `node_modules/brace-expansion@5.0.8`
    added (hoisted, top-level).
  - 1 new `node_modules/<parent>/node_modules/brace-expansion@5.0.8`
    copy added (per-parent) to keep
    resolution local.
  - 1 old `node_modules/js-yaml@4.2.0` removed,
    1 new `node_modules/js-yaml@4.3.0` added.

## What did NOT change

- No source code touched.
- No production deps bumped. Both packages
  live only in devDependencies (transitively).
- `vue 3.5.40`, `vue-router 5.2.0`, `pinia 3.0.4`,
  `vite 7.3.6` (from #89), `eslint 10.8.0`,
  `eslint-plugin-vue 10.10.0`, `vue-eslint-parser 10.4.1`,
  `vitest 4.1.10`, `typescript-eslint 8.65.0`,
  `vue-tsc 2.2.12` — all unchanged.
- Production build output is byte-identical
  (verified: `dist/assets/index-*.js` and
  `dist/assets/useZodForm-*.js` have the same
  hashes as the pre-override build).

## Verified

- `npm ci` (against the regenerated lock) —
  clean.
- `pnpm run lint` — clean.
- `pnpm run type-check` (vue-tsc) — clean.
- `pnpm run build` (vite) — clean, 21.48s.
  Bundle sizes and asset hashes are
  identical to the pre-override build.
- `osv-scanner --lockfile=package-lock.json`
  on the regenerated lock — **0
  vulnerabilities remaining** (was 3 HIGH
  before).

## Files

- `frontend/package.json` (+2 / -1)
- `frontend/package-lock.json` (regenerated,
  +30 / -96)

Total: 2 files. 0 source code changes.

## Followups

- The `trivy (frontend)` and `pnpm-audit
  (frontend)` jobs will now both report
  0 vulnerabilities from our pinned dep
  set. (Trivy-frontend still has the
  Caddy base-image CVEs — those are
  baseline-accepted per `.trivyignore` and
  unrelated to this PR.)
- The weekly dependabot schedule + the
  osv-scanner security job will continue
  to surface new advisories as upstream
  patches land; this PR closes the three
  current findings.

Refs: GHSA-mh99-v99m-4gvg (brace-expansion),
GHSA-52cp-r559-cp3m (js-yaml). PR #87
(footgun fix that switched pnpm-audit to
osv-scanner on package-lock.json).
