// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Plans service. Wraps /api/v1/plans (admin CRUD).
// v0.6.0 surface:
//
//   - GET    /          -> list every plan
//   - GET    /{id}      -> get a single plan
//   - POST   /          -> create a plan
//   - PATCH  /{id}      -> partial update
//   - DELETE /{id}      -> hard delete
//
// The shape mirrors the OpenAPI spec
// (`docs/openapi.yaml` -> `Plan` schema) one-to-one
// after the camelizeKeys transform. The wire format
// is the camelCase `Plan` type from `@/types/aegis`
// (the legacy `durationDays` field was renamed to
// `durationNs` in v0.6.0; convert to a human-readable
// "30 days" string at the rendering layer in
// `PlansView.vue`).
//
// The `reset_period` enum is the closed set
// (daily / weekly / monthly / never); the Service
// rejects anything outside the set with 400.
//
// # Why no zod schema (yet)
//
// The `users`, `hosts`, `nodes` services have a
// zod schema in `src/schemas/<entity>.ts` for the
// create / update request shapes. Plans gets a
// zod schema in v0.6.x when the PlansView
// (PR #134) lands — the schema file will live
// alongside `src/schemas/plan.ts` and the
// request shapes here will re-export it.

import type { Plan, UUID } from '@/types'

import { api } from './../client'

// CreatePlanRequest is the POST / body. The id
// and timestamps are NOT accepted from the
// caller — the server generates them.
//
// `durationNs` is in nanoseconds. The UI converts
// a human-readable "30 days" string to ns before
// sending (Intl.DurationFormat or a tiny helper;
// the conversion is in the PlansView).
export interface CreatePlanRequest {
  name: string
  trafficLimitBytes?: number
  durationNs: number
  deviceLimit?: number
  resetPeriod?: Plan['resetPeriod']
  priceCents?: number
}

// UpdatePlanRequest is the PATCH /{id} body.
// Every field is optional; the absence of a key
// means "leave unchanged".
export interface UpdatePlanRequest {
  name?: string
  trafficLimitBytes?: number
  durationNs?: number
  deviceLimit?: number
  resetPeriod?: Plan['resetPeriod']
  priceCents?: number
}

export async function listPlans(): Promise<Plan[]> {
  const { data } = await api.get<{ plans: Plan[] }>('/api/v1/plans/')
  return data.plans ?? []
}

export async function getPlan(id: UUID): Promise<Plan> {
  const { data } = await api.get<Plan>(`/api/v1/plans/${id}`)
  return data
}

export async function createPlan(req: CreatePlanRequest): Promise<Plan> {
  const { data } = await api.post<Plan>('/api/v1/plans/', req)
  return data
}

export async function updatePlan(
  id: UUID,
  req: UpdatePlanRequest,
): Promise<Plan> {
  const { data } = await api.patch<Plan>(`/api/v1/plans/${id}`, req)
  return data
}

export async function deletePlan(id: UUID): Promise<void> {
  await api.delete(`/api/v1/plans/${id}`)
}
