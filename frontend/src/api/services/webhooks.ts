// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Webhooks service. Wraps /api/v1/webhooks (admin CRUD +
// delivery history + DLQ + test event). v0.7.0 surface:
//
//   - GET    /                    -> list every endpoint
//   - GET    /{id}                -> get a single endpoint
//   - POST   /                    -> create an endpoint
//   - PATCH  /{id}                -> partial update
//   - DELETE /{id}                -> hard delete (cascades delivery history)
//   - GET    /{id}/deliveries     -> per-endpoint delivery history
//   - POST   /{id}/test           -> send a synthetic webhook.test event
//   - GET    /dlq                 -> cross-endpoint DLQ list
//   - GET    /dlq/{did}           -> single DLQ entry
//   - POST   /dlq/{did}/replay    -> take the entry off the queue and
//                                     dispatch a fresh attempt
//   - DELETE /dlq/{did}           -> drop a DLQ entry
//
// The shape mirrors the OpenAPI spec
// (`docs/openapi.yaml` -> `WebhookEndpoint` / `WebhookDelivery`
// / `WebhookDLQEntry` schemas) one-to-one after the
// camelizeKeys transform.
//
// # Secret redaction
//
// The wire format renders the `secret` field as
// `***` on every read EXCEPT the immediate POST /
// response. The TS type does not distinguish the two
// cases — the `secret` field is just a string and the
// caller is expected to read it from the create
// response only. The WebhooksView copies the secret
// from the Create response into a "click to copy"
// clipboard widget and never reads it again.

import type { components } from '@/types/api'

import { api } from './../client'

// Re-export the wire-format types so the rest of the
// front-end can import them from one place. The
// `WebhookEndpoint` / `WebhookDelivery` /
// `WebhookDLQEntry` shapes are 1:1 with the
// `components['schemas']` entries; the camelCase is
// the `camelizeKeys` interceptor's output (the Go
// server emits snake_case, the axios interceptor
// converts on read).
export type WebhookEndpoint = components['schemas']['WebhookEndpoint']
export type WebhookDelivery = components['schemas']['WebhookDelivery']
export type WebhookDLQEntry = components['schemas']['WebhookDLQEntry']
export type WebhookEventType = components['schemas']['WebhookEventType']
export type WebhookDeliveryStatus =
  components['schemas']['WebhookDeliveryStatus']
export type WebhookDispatchResult =
  components['schemas']['WebhookDispatchResult']

export type UUID = string

// CreateWebhookRequest is the POST / body. The id
// and timestamps are NOT accepted from the caller —
// the server generates them.
export interface CreateWebhookRequest {
  url: string
  secret: string
  events?: WebhookEventType[]
  enabled?: boolean
}

// UpdateWebhookRequest is the PATCH /{id} body.
// Every field is optional; the absence of a key means
// "leave unchanged".
export interface UpdateWebhookRequest {
  url?: string
  secret?: string
  events?: WebhookEventType[]
  enabled?: boolean
}

export async function listWebhooks(): Promise<WebhookEndpoint[]> {
  const { data } = await api.get<{ endpoints: WebhookEndpoint[] }>(
    '/api/v1/webhooks/',
  )
  return data.endpoints ?? []
}

export async function getWebhook(id: UUID): Promise<WebhookEndpoint> {
  const { data } = await api.get<WebhookEndpoint>(`/api/v1/webhooks/${id}`)
  return data
}

export async function createWebhook(
  req: CreateWebhookRequest,
): Promise<WebhookEndpoint> {
  const { data } = await api.post<WebhookEndpoint>('/api/v1/webhooks/', req)
  return data
}

export async function updateWebhook(
  id: UUID,
  req: UpdateWebhookRequest,
): Promise<WebhookEndpoint> {
  const { data } = await api.patch<WebhookEndpoint>(
    `/api/v1/webhooks/${id}`,
    req,
  )
  return data
}

export async function deleteWebhook(id: UUID): Promise<void> {
  await api.delete(`/api/v1/webhooks/${id}`)
}

export async function listDeliveries(
  endpointId: UUID,
  limit = 100,
): Promise<WebhookDelivery[]> {
  const { data } = await api.get<{ deliveries: WebhookDelivery[] }>(
    `/api/v1/webhooks/${endpointId}/deliveries`,
    { params: { limit } },
  )
  return data.deliveries ?? []
}

export async function sendTestEvent(
  endpointId: UUID,
): Promise<WebhookDispatchResult> {
  const { data } = await api.post<WebhookDispatchResult>(
    `/api/v1/webhooks/${endpointId}/test`,
  )
  return data
}

export async function listDLQ(limit = 100): Promise<WebhookDLQEntry[]> {
  const { data } = await api.get<{ dlq: WebhookDLQEntry[] }>(
    '/api/v1/webhooks/dlq',
    { params: { limit } },
  )
  return data.dlq ?? []
}

export async function getDLQ(did: UUID): Promise<WebhookDLQEntry> {
  const { data } = await api.get<WebhookDLQEntry>(
    `/api/v1/webhooks/dlq/${did}`,
  )
  return data
}

export async function deleteDLQ(did: UUID): Promise<void> {
  await api.delete(`/api/v1/webhooks/dlq/${did}`)
}

export async function replayDLQ(
  did: UUID,
): Promise<WebhookDispatchResult> {
  const { data } = await api.post<WebhookDispatchResult>(
    `/api/v1/webhooks/dlq/${did}/replay`,
  )
  return data
}
