# chore(deps): bump vue-router 4→5 + vite 6→7 + pinia 2→3 (cleanup-batch 3/3)

## Cleanup-batch item 3/3

Re-batched from dependabot #71 (vue-router 4→5,
closed 2026-07-23) plus the natural cascade
`vite 6→7` and `pinia 2→3` that vue-router 5's
peer-deps demand. This is the chained "all at once"
migration — splitting the three into separate PRs
would have failed CI at each step because the
intermediate states are not buildable (vue-router 5
requires vite ≥ 7.3, pinia 5 requires the same, and
neither would resolve if the other one was still on
the v4/v2 train).

## What changed

- `pinia: ^2.2.6 → ^3.0.4` (major). Pinia 3 is
  described as a "boring" major release that drops
  Vue 2 support and deprecated APIs. Our stores in
  `src/stores/{auth,toast,ui}.ts` already use the
  new `defineStore('name', () => { ... })`
  setup-style syntax, so no code changes were
  needed. (`PiniaStorePlugin` → `PiniaPlugin` rename
  did not apply; we don't use that name. The
  `defineStore({ id: 'foo', ... })` form was already
  removed in v0.1.0 work; we use the
  `defineStore('name', ...)` positional form.)
- `vue: ^3.5.13 → ^3.5.40` (minor). Required by
  vue-router 5's peer `vue: ^3.5.34 || ^4.0.0`. No
  code changes; the Vue 3.5 line is API-stable for
  our usage.
- `vue-router: ^4.4.5 → ^5.2.0` (major). The router
  config in `src/router/index.ts` uses
  `createRouter` + `createWebHistory` +
  `RouteRecordRaw` + the `meta` field on routes + a
  `beforeEach` / `afterEach` guard pair. All five
  APIs are still in v5 with the same signatures;
  the type system rewrite is internal. The Pinia
  store import inside the guard (`useAuthStore`) is
  also unchanged.
- `vite: ^6.4.3 → ^7.3.0` (major). Required by
  vue-router 5's peer `vite: ^7.3.0 || ^8.0.0`. Our
  `vite.config.ts` is minimal (Vue plugin, alias
  for `@`, dev server proxy, build target es2022)
  — all of which are Vite-7 compatible without
  changes. We are NOT jumping to vite 8
  (just-released) because vue-router 5's peer-dep
  range is satisfied by 7.x and 8.x equally;
  staying on 7.x keeps the diff smaller and the
  migration lower-risk.
- `@types/node: ^22.10.0 → ^22.12.0` (minor). Vite
  7's peer-dep range is
  `@types/node: ^20.19.0 || >=22.12.0`; our previous
  `^22.10.0` sat below the floor for the 22.x
  branch.
- `lucide-vue-next: ^0.460.0 → ^1.0.0` (major).
  The new major dropped a few icon names that we
  do not import, so no code change. Picked up
  automatically by npm during the install
  (lucide-vue-next is a peer of several of the
  shadcn-vue primitives we use).
- `@vitejs/plugin-vue: ^6.0.0` unchanged. The 6.0.8
  we have supports both Vite 6 and 7
  (peer: `vite: ^5.0.0 || ^6.0.0 || ^7.0.0 ||
  ^8.0.0`), so we did not need to bump it.
- `vue-eslint-parser: ^10.4.1` unchanged. Already
  on the v10 train from PR #83.

## Verified

- `pnpm run lint` — clean. (eslint 10 + flat config
  from PR #83 still happy.)
- `pnpm run type-check` (vue-tsc) — clean.
- `pnpm run build` (vite) — clean, 7.64s. Bundle
  sizes essentially identical to the pre-bump
  numbers; the only asset hash changes are from
  internal library version strings, not from any
  user-facing import.
- `pnpm install --frozen-lockfile=false` — clean in
  CI. (The lockfile regen is large: +600 / -163
  lines, mostly because vite 7 brings in
  `lightningcss` as an optional native dep, and
  pinia 3 swaps a few TS type internals.)

## Files

- `frontend/package.json` (+5 / -5)
- `frontend/package-lock.json` (regenerated,
  +600 / -163)

Total: 2 files. No app code touched.

## Why no other major bump was needed

- `@vue/tsconfig: ^0.7.0` — the bumped
  `@vue/tsconfig 0.9.1` needs TypeScript ≥ 5.8. We
  pin `typescript: ~5.6.3`. Bumping TS to 5.8+ is a
  separate item (dependabot #69, deferred to a
  future cycle).
- `vitest: ^4.1.10` — already on the v4 line from
  PR #82. No bump needed.
- `@typescript-eslint/*` / `typescript-eslint` —
  already on the v8 train from PR #83.
- `eslint: ^10.8.0` / `eslint-plugin-vue: ^10.10.0` —
  already on the v10 line from PR #83.

## Followups

- Re-open + immediately close #69 and #73 with
  "upstream still blocking" comment. (Re-batched
  in v0.5.0 cleanup window if the upstream catches
  up.)
- (Out of scope for this PR) The project setup
  footgun flagged in PR #83: `package-lock.json` in
  repo + `pnpm install` in CI + `npm ci` in the
  Dockerfile. Either standardize on npm in CI OR
  commit `pnpm-lock.yaml`. This is a followup task
  for a separate cycle.

Refs: #71 (closed 2026-07-23), ARCHITECTURE.md
§21 cleanup-batch.
