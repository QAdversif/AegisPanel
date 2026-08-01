// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Webhook endpoint CRUD schemas. Mirrors
// `backend/internal/webhooks/endpoint.go` (the
// closed `AllowedEventTypes` set and the receiver
// URL / HMAC secret constraints) and the wire
// shapes in
// `frontend/src/api/services/webhooks.ts`
// (`CreateWebhookRequest` / `UpdateWebhookRequest`).
//
// v0.7.0 inlined these zod definitions inside
// WebhooksView.vue. v0.7.x #149 factored them out
// so the create and edit dialogs share a single
// source of truth and so v0.7.x #150 can introduce
// the events multi-select without re-touching the
// view's script block.

import { z } from 'zod'

/**
 * Closed set of event types the panel publishes.
 * 1:1 with `webhooks.AllowedEventTypes` in
 * `backend/internal/webhooks/endpoint.go`. An empty
 * `events` slice on the wire is the "all" wildcard;
 * the schema keeps the field optional so the
 * default flows through `.default([])`.
 */
export const webhookEventTypeSchema = z.enum([
  // User lifecycle.
  'user.created',
  'user.updated',
  'user.deleted',
  // Plan lifecycle.
  'plan.created',
  'plan.updated',
  'plan.deleted',
  // Node lifecycle.
  'node.created',
  'node.updated',
  'node.deleted',
  // Host lifecycle.
  'host.created',
  'host.updated',
  'host.deleted',
  // Backup lifecycle.
  'backup.created',
  'backup.completed',
  'backup.failed',
  // Inbound lifecycle.
  'inbound.created',
  'inbound.updated',
  'inbound.deleted',
])

/**
 * Receiver URL. http or https only, 10..2048 chars.
 * The Go handler enforces the same constraint; we
 * keep it client-side so the form rejects obvious
 * typos before round-tripping.
 */
export const webhookUrlSchema = z
  .string()
  .min(10)
  .max(2048)
  .refine(
    (v) => v.startsWith('http://') || v.startsWith('https://'),
    { message: 'url must be http or https' },
  )

/**
 * HMAC shared secret. 16..256 chars. The receiver
 * uses it to verify the `X-Aegis-Signature` header;
 * 16 chars gives ~96 bits of entropy which is well
 * past the receiver's brute-force threshold.
 */
export const webhookSecretSchema = z.string().min(16).max(256)

/**
 * Secret field in the EDIT form. Empty string means
 * "leave unchanged" — the backend's UpdateInput
 * treats an absent `secret` key as "no rotation",
 * and the view's submit handler skips the field
 * when it's empty. Otherwise the value must
 * satisfy `webhookSecretSchema`.
 */
const webhookSecretOptionalSchema = z
  .union([z.literal(''), webhookSecretSchema])
  .optional()

/**
 * Base object shape. We keep it as a separate schema
 * so `webhookUpdateSchema` can use `.partial()` /
 * `.strict()` without going through a `ZodEffects`
 * (which `.superRefine` returns) — `ZodEffects` has
 * no `.partial()` method. Same pattern as
 * `baseHostObjectSchema` in `host.ts`.
 */
const baseWebhookObjectSchema = z.object({
  url: webhookUrlSchema,
  enabled: z.boolean().default(true),
  events: z.array(webhookEventTypeSchema).default([]),
})

export const webhookCreateSchema = baseWebhookObjectSchema.extend({
  secret: webhookSecretSchema,
})

export type WebhookCreateInput = z.infer<typeof webhookCreateSchema>

export const webhookUpdateSchema = z
  .object({
    url: webhookUrlSchema.optional(),
    secret: webhookSecretOptionalSchema,
    enabled: z.boolean().optional(),
    events: z.array(webhookEventTypeSchema).optional(),
  })
  .strict()

export type WebhookUpdateInput = z.infer<typeof webhookUpdateSchema>
