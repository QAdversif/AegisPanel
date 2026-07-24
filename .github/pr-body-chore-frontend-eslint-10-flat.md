# chore(deps-dev): migrate eslint 8→10 + flat config (cleanup-batch 2/3)

## Cleanup-batch item 2/3

Re-batched from dependabot #72 (closed 2026-07-23 with the
promise of "v0.4.0 cleanup window"). This is a 2-major jump
(eslint 8 → 10) plus the flat-config migration; both were
deferred together because the legacy `.eslintrc.*` chain
(`extends: ['eslint:recommended', 'plugin:vue/...',
'plugin:@typescript-eslint/recommended']`) is incompatible
with eslint v9+ (which is a hard dependency of v10).

## What changed

- **`.eslintrc.cjs` → `eslint.config.js`** (flat config).
  The legacy config relied on three `extends` chains and a
  per-language parser override. The flat config imports
  the same rules and combines them in a single array of
  config objects (see file for the exact mapping and the
  per-`.vue` parser override).
- **`@typescript-eslint/parser` + `@typescript-eslint/eslint-plugin`
  → `typescript-eslint@8.65.0` umbrella.** The umbrella
  package ships the v8 parser + plugin + flat-config
  presets in one. We removed the separately-listed deps
  because the umbrella covers them and
  `tseslint.configs.recommended` is the flat-format preset
  (the bare plugin's `configs.recommended` is the legacy
  format and is NOT iterable in flat-config land).
- **`vue-eslint-parser` 9.4.3 → 10.4.1.** Required by
  `eslint-plugin-vue@10.10.0` peer-dep range (`^10.3.0`).
- **`eslint-plugin-vue` 9.32.0 → 10.10.0.** Required by
  eslint 10 (plugin 9's peer range is `<= 9`).
- **`@eslint/js` + `globals` added.** `@eslint/js` is the
  v10 canonical location for `js.configs.recommended`
  (replaces the legacy `eslint:recommended` extend);
  `globals` replaces the legacy
  `env: { browser: true, es2022: true, node: true }` with
  explicit `languageOptions.globals`.
- **`package.json` lint script: removed `--ext .ts,.vue`.**
  eslint 10 doesn't recognize `--ext` and emits a
  deprecation warning. Flat config matches file types by
  pattern in each config object, so the global flag is
  unnecessary.
- **`tools/scripts/check-raw-text.mjs` regex fix.** The
  legacy regex `/[()=<>{}\[\],;]/` worked because `\]` was
  load-bearing — `]` in the middle of a character class
  closes the class prematurely, which was a real bug we
  hit immediately when we ran check-raw-text on Form.vue
  (whose file header comment contains a literal
  `<template>` block that the script's extractor matches).
  eslint 10's `no-useless-escape` rule flagged `\]`
  because the escape "looks" unnecessary to the linter,
  even though it isn't. The fix is to use the standard
  `]`-at-the-start-of-class idiom (`/[\]()=<>{} ,;]/`),
  which is both escape-free AND unambiguous.

## Why the regex bug was hidden until now

The legacy `Form.vue` file was added in PR-C
(18832ab, 2026-07-17). The check-raw-text script was added
in PR-E (00dcc7e, also 2026-07-17) and ran clean against
Form.vue at the time. The reason: the script's
`stripNonText` does ONE pass of `<!--[\s\S]*?-->` AFTER
`extractTemplate`, but the template content extracted by
the buggy regex was the body of the comment's inner
`<template>` block, which doesn't have its own comment
markers — so the strip pass didn't see them.

With the now-correct regex, the strip pass catches the
JS-syntax runs in the leaked expression
(`setFieldValue('name',` etc.) and skips them via the
`[()=<>{} ,;]` check. So the script now returns "OK"
both for the new files and the old ones that were
silently broken.

## Verified

- `pnpm run lint` — clean (eslint 10 + check-raw-text).
- `pnpm run type-check` (vue-tsc) — clean.
- `pnpm run build` (vite) — clean, 6.35s.
- `pnpm run lint:fix` — clean (no fixable issues).
- `pnpm install --frozen-lockfile=false` — clean (CI
  re-resolves the lockfile from package.json; the committed
  `package-lock.json` is npm-format and is what gets
  updated locally; the CI pnpm install generates a fresh
  `pnpm-lock.yaml` on the runner and does not commit it
  back — same as #82).

## Files

- `frontend/eslint.config.js` (new, 80 lines)
- `frontend/.eslintrc.cjs` (deleted, 31 lines)
- `frontend/package.json` (+5 / -2)
- `frontend/package-lock.json` (regenerated, +291 / -654)
- `frontend/tools/scripts/check-raw-text.mjs` (+7 / -4 in
  the two affected regexes + comments)

Total: 5 files, +446 / -654.

## Followups

- `chore(deps): bump vue-router 4→5 + vite 6→7 + pinia 2→3`
  (cleanup-batch item 3/3) — chained migration; vue-router
  5's peerDeps require Vite 7.3+ and Pinia 3.0.4+.
- Re-open + immediately close #73 (zod 3→4) and #69
  (frontend minor+patch + TS major) with "upstream blockers
  still in place" comment.
- (Out of scope) The project should standardize on either
  `npm install` in CI OR commit the `pnpm-lock.yaml` the CI
  generates. Currently the repo carries a `package-lock.json`
  (npm) but the CI runs pnpm, which is a mild footgun for
  contributors who `npm install` locally and end up with a
  lockfile the CI doesn't see. Add a followup task for
  whichever cycle is next.

Refs: #72 (closed 2026-07-23), ARCHITECTURE.md §21
cleanup-batch.
