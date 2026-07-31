# chore(deps): bump vue-i18n 10 to 11

Final major bump of the Go+frontend dependency batch. Touches 16
files (every view + i18n setup) but contains **zero code changes**
beyond the version pin.

## Bump

- `vue-i18n` `^10.0.5` -> `^11.4.8` (dependencies)

## Compatibility check (already verified locally)

- `npm install --legacy-peer-deps` clean (after mavis-trash
  node_modules to avoid the pnpm-store artifact conflict from
  PR #142)
- `npm run type-check` (vue-tsc 3.3.8 --noEmit) -> **clean on
  first try, no type errors**
- `npm run lint` -> 0 errors, 52 pre-existing warnings
- `npm run build` -> clean, `built in 15.99s`, index bundle
  +1.6 kB gzipped (415.50 kB vs 413.90 kB in #142, within
  expected range for vue-i18n 11)

## Why this worked without code changes

The codebase already used the Composition API idiom exclusively
(`legacy: false` set in `src/i18n/index.ts`, `useI18n()` in
every view). Per the vue-i18n v11.0.0 release notes, the only
breaking change is the deprecation of the Legacy API mode — which
we are not using. The v11 minor versions between 11.0.0 and
11.4.8 have been stability + bug fix releases.

## Files touched

- `frontend/package.json` (version pin)
- `frontend/package-lock.json` (npm install output, ~50 lines)
- `.github/pr-body-chore-deps-vue-i18n-11.md` (this file)

**Zero application code changes.** The 16 files that import
`vue-i18n` keep working as-is.

## Risk

Low. Confirmed by:
- type-check clean (vue-tsc 3 catches prop/type drift)
- lint clean
- build clean
- bundle size delta is small (+1.6 kB gzipped, expected)

## Part of dependency batch (all shipped)

- [x] PR #A (Go minors, #141) - merged `f529b35`
- [x] PR #B (pinia 4, #142) - merged `f1d6d39`
- [x] PR #C (vue-tsc 3, #143) - merged `28b93e7`
- [x] PR #D (this) - vue-i18n 11

Closes the 4-PR batch.
