# feat(ui-webhooks): WebhooksView + sidebar nav + i18n en/ru + Webhook icon

v0.7.0 PR #4/5.

## What

The admin UI surface for the outgoing-webhook
package introduced in PR #1/5 (#136) and wired in
PR #2/5 (#137) and #3/5 (#138):

- `frontend/src/views/WebhooksView.vue` (new, 850
  lines) — list every operator-configured endpoint,
  create a new one, edit an existing one, delete it,
  send a synthetic test event, inspect the per-
  endpoint delivery history, and replay / drop
  entries in the cross-endpoint DLQ. The one-time
  HMAC-secret display widget is rendered as a
  prominent amber card above the table right after
  Create so the operator copies the secret to their
  receiver's HMAC config before dismissing.
- `frontend/src/router/index.ts` — `/webhooks`
  route mounted at the same layout as the rest of
  the admin surface.
- `frontend/src/layouts/AppLayout.vue` —
  `Webhooks` nav entry with the `Webhook` lucide
  icon, between `Backups` and `Profile`.
- `frontend/src/i18n/locales/en.json` +
  `ru.json` — `nav.webhooks` + a full
  `webhooks.*` namespace (title, subtitle, table
  columns, dialog labels, toasts, field labels,
  status badges). Russian translations mirror
  the English structure (formal "вы" -> informal
  "ты" not used; "Вебхуки" / "Исходящие вебхуки" /
  "Подписки на события" / etc.).
- Hand-rolls the standard shadcn-vue Dialog +
  DataTable pattern used by the other CRUD views
  (PlansView, UsersView, BackupsView). The
  per-endpoint deliveries dialog and the cross-
  endpoint DLQ dialog are two independent modal
  tables so the operator can switch between them
  without losing the endpoint list scroll state.
- Events field is omitted from the create/edit
  dialog for v0.7.0 (every endpoint defaults to
  the "all" wildcard); a v0.7.x follow-up adds
  the per-event multi-select.

## Verification

- `npm run type-check` — clean
- `npm run lint` — pre-existing PlansView warnings
  only (no new warnings on the new file)
- `npm run build` — clean (vite build, 2026 modules
  transformed)
- The view does NOT yet wire `Service.Dispatch` from
  every mutating handler (the production event
  flow). v0.7.0 ships the package + the UI; the
  v0.7.x follow-up batch lands the call-site wiring
  (same TODO the v0.6.x audit-log call-site batch
  carries).

## Files

- `frontend/src/views/WebhooksView.vue` (new, 850
  lines)
- `frontend/src/router/index.ts` (1 route added)
- `frontend/src/layouts/AppLayout.vue` (1 import,
  1 nav entry)
- `frontend/src/i18n/locales/en.json` (1 nav key,
  1 full namespace)
- `frontend/src/i18n/locales/ru.json` (1 nav key,
  1 full namespace)

No backend changes; no OpenAPI spec changes; no
test infrastructure changes. The PR is UI-only.
