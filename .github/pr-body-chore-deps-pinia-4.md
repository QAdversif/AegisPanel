# chore(deps): bump pinia 3 to 4 + add @vue/devtools-api

Pinia 4 minor: "only technically breaking changes" per the
release notes. The only required user-side change is installing
`@vue/devtools-api` alongside pinia (it was previously a transitive
of pinia; pinia 4 promotes it to peer).

## Changes

- `pinia` `^3.0.4` -> `^4.0.2`
- `@vue/devtools-api` `^8.2.1` (new direct dep, required peer for
  pinia 4; `npm view pinia peerDependencies` -> `@vue/devtools-api: ^8.1.5`)

## Compatibility check (already verified locally)

- `npm install --legacy-peer-deps` clean: 1 package changed (pinia),
  84 transitive deps removed (pinia 4 trimmed the dep tree)
- `npm run type-check` (vue-tsc --noEmit) -> clean
- `npm run lint` -> 0 errors, 55 pre-existing warnings
  (vue/singleline-html-element-content-newline,
  vue/max-attributes-per-line -- all pre-#134 baselines, not
  introduced by this PR)
- `npm run build` -> clean, `built in 23.46s`

## Pinia 4 breaking changes review (from v4.0.0 release notes)

- **ESM only** -- we already have `"type": "module"` in
  frontend/package.json (the v0.4.0 cleanup-batch PR #84 set this).
  No action needed.
- **`@vue/devtools-api` peer dep** -- added as direct dep.
- Errors / dev warns refactor -- our 3 stores (`auth`, `toast`,
  `ui`) use `defineStore` Options API which is unchanged in pinia 4.
  No code changes required.

## Risk

Low. 4 files changed in package.json + package-lock.json only.
No app code touched. The 3 existing stores work without
modification per the pinia 4 release notes ("technically breaking
only").

## Part of dependency batch

This is PR #B in the 4-PR Go+frontend batch:
- [x] PR #A (Go minors, #141) - merged
- [x] PR #B (this) - pinia 4
- [ ] PR #C - vue-tsc 2->3
- [ ] PR #D - vue-i18n 10->11
