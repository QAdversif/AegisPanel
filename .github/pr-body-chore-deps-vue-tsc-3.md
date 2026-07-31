# chore(deps-dev): bump vue-tsc 2 to 3

Dev-only major bump of the type-check tool. No application code
changes expected — vue-tsc 3 dropped Vue 2 support and stabilized
hybrid mode (Volar 2.x underlying engine), which we don't use
(we're on Vue 3.5).

## Bump

- `vue-tsc` `^2.1.10` -> `^3.3.8` (devDependencies only)

## Type-check fix: WebhooksView.vue prop name drift (vue-tsc 3 stricter)

vue-tsc 3 surfaced a pre-existing bug in PR #139 (WebhooksView):
the `DataTable` component was called with `:rows` and
`:empty-message` props, but the actual `DataTable` API uses
`:data` and `empty-key` / `emptyLabel`. vue-tsc 2 didn't enforce
the prop names strictly; vue-tsc 3 does.

### Fix

3 prop renames in `src/views/WebhooksView.vue`:

- `:rows` -> `:data` (matches the documented DataTable prop)
- `:empty-message="t('webhooks.empty')"` ->
  `empty-key="webhooks.empty"` (vue-i18n lookup, the idiomatic
  variant; eslint rule flags the raw-string override)
- Same for the deliveries and DLQ dialogs (`deliveriesTableRows` /
  `dlqTableRows` -> `:data`, plus `empty-key` for the DLQ dialog)

This is functionally a no-op for the user: `t('webhooks.empty')` and
`empty-key="webhooks.empty"` produce the same string. The
behavioral difference would have manifested only if DataTable's
fallback chain ever used a `rows` prop, which it never has.

## Local verification

- `npm install --legacy-peer-deps` clean (after mavis-trash
  node_modules to avoid the pnpm-store artifact conflict from
  PR #142)
- `npm run type-check` (vue-tsc 3.3.8 --noEmit) -> clean
- `npm run lint` -> 0 errors, 55 pre-existing warnings
- `npm run build` -> clean, `built in 16.57s`

## Risk

Low. vue-tsc is a type-checker (no runtime impact). The WebhooksView
fix is 3 prop renames in 1 file. No other views or stores touched.

## Part of dependency batch

- [x] PR #A (Go minors, #141)
- [x] PR #B (pinia 4, #142)
- [x] PR #C (this) - vue-tsc 3
- [ ] PR #D - vue-i18n 11
