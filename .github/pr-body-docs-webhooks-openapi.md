# docs(webhooks): OpenAPI /webhooks endpoints + hand-mirrored services/webhooks.ts

v0.7.0 PR #3/5.

## What

The OpenAPI surface for the outgoing-webhook
package introduced in PR #1/5 (#136) and the HTTP
layer added in PR #2/5 (#137):

- `docs/openapi.yaml` — version bump 0.6.0 → 0.7.0.
  New paths under `/api/v1/webhooks/*`:
  - `GET    /` (list)
  - `GET    /{id}` (get)
  - `POST   /` (create — returns the secret verbatim
    in the 201 response so the operator can copy it
    to their receiver; subsequent reads redact the
    secret to `***`)
  - `PATCH  /{id}` (partial update)
  - `DELETE /{id}` (hard delete, cascades delivery
    history)
  - `GET    /{id}/deliveries` (per-endpoint delivery
    history)
  - `POST   /{id}/test` (synthetic webhook.test event)
  - `GET    /dlq` (cross-endpoint DLQ list)
  - `GET    /dlq/{did}` (single DLQ entry)
  - `POST   /dlq/{did}/replay` (take the entry off
    the queue and dispatch a fresh attempt)
  - `DELETE /dlq/{did}` (drop a DLQ entry)
- New schemas:
  - `WebhookEventType` (the closed set of 18 event
    types the dispatcher can fan out)
  - `WebhookDeliveryStatus` (success / retry /
    failed)
  - `WebhookEndpoint`, `WebhookEndpointCreateRequest`,
    `WebhookEndpointUpdateRequest`,
    `WebhookEndpointListResponse`
  - `WebhookDelivery`, `WebhookDeliveryListResponse`
  - `WebhookDLQEntry`, `WebhookDLQListResponse`
  - `WebhookDispatchResult` (the response shape of
    the test + replay endpoints)
- `frontend/src/api/services/webhooks.ts` (new) —
  hand-mirrored from the OpenAPI spec. Re-exports the
  wire-format types from `@/types/api`. 12 functions
  (listWebhooks, getWebhook, createWebhook,
  updateWebhook, deleteWebhook, listDeliveries,
  sendTestEvent, listDLQ, getDLQ, deleteDLQ,
  replayDLQ) + 2 request DTOs (CreateWebhookRequest,
  UpdateWebhookRequest). Follows the same pattern
  as `services/plans.ts`.
- `frontend/src/api/services/index.ts` — re-export
  the new service.
- `frontend/src/types/api.d.ts` — regenerated via
  `npm run codegen` (openapi-typescript 7.13.0).

## Verification

- `npm run codegen:check` — clean (api.d.ts is
  up-to-date with openapi.yaml).
- `npm run type-check` — clean (vue-tsc --noEmit).
- `npm run lint` — pre-existing `PlansView.vue`
  warnings (not in this PR's diff); no new warnings
  on the webhooks.ts file.
- `npm run build` — clean (vite build, 2024 modules
  transformed).

## Files

- `docs/openapi.yaml` (version + 11 paths + 12
  schemas)
- `frontend/src/api/services/webhooks.ts` (new,
  168 lines)
- `frontend/src/api/services/index.ts` (1-line
  re-export)
- `frontend/src/types/api.d.ts` (regenerated)

No changes to the backend / docs surface outside
`openapi.yaml`. The PR #4/5 WebhooksView is the
only consumer; it lands in the next PR.
