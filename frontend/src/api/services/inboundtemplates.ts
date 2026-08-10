// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Inbound templates service. v0.8.13 ships the
// admin CRUD surface for the inbound_templates
// table (PR #205 backend, this PR frontend).
//
// Endpoints (panel-wide, NOT per-node):
//
//   - GET    /api/v1/inbound-templates/         -> list
//   - GET    /api/v1/inbound-templates/{id}/   -> get one
//   - POST   /api/v1/inbound-templates/         -> create
//   - PATCH  /api/v1/inbound-templates/{id}/   -> partial update
//   - DELETE /api/v1/inbound-templates/{id}/   -> hard delete
//
// The `inbounds.template_id` column has
// `ON DELETE SET NULL` (migration 0021), so
// deleting a template that still has inbounds
// pointing at it drops the FK to NULL on those
// inbounds; the sing-box renderer falls back to
// each inbound's inline `params` (the
// v0.8.0-v0.8.12 default). The UI surfaces a
// confirm dialog that lists the affected
// inbound count before DELETE.
//
// # Why a separate service file (not folded
// into inbounds.ts)
//
// The inbounds package deals with
// per-node CRUD (`/nodes/{nodeId}/inbounds`).
// Templates are panel-level
// (`/inbound-templates`), so the URL prefix
// does not match. Folding them into
// inbounds.ts would have made the service
// file a two-headed beast; the per-resource
// split mirrors the Go-side
// `internal/inbounds/` vs
// `internal/inboundtemplates/` package
// structure (the s-suffix split is
// intentional; see the PR #205 design doc
// for the rationale).

import type { InboundTemplate, UUID } from '@/types'
import type {
  InboundTemplateCreateInput,
  InboundTemplateUpdateInput,
} from '@/schemas'

import { api } from '../client'

export async function listInboundTemplates(): Promise<InboundTemplate[]> {
  const { data } = await api.get<{ templates: InboundTemplate[] }>(
    '/api/v1/inbound-templates/',
  )
  return data.templates ?? []
}

export async function getInboundTemplate(id: UUID): Promise<InboundTemplate> {
  const { data } = await api.get<InboundTemplate>(
    `/api/v1/inbound-templates/${id}`,
  )
  return data
}

export async function createInboundTemplate(
  req: InboundTemplateCreateInput,
): Promise<InboundTemplate> {
  const { data } = await api.post<InboundTemplate>(
    '/api/v1/inbound-templates/',
    req,
  )
  return data
}

export async function updateInboundTemplate(
  id: UUID,
  req: InboundTemplateUpdateInput,
): Promise<InboundTemplate> {
  const { data } = await api.patch<InboundTemplate>(
    `/api/v1/inbound-templates/${id}`,
    req,
  )
  return data
}

export async function deleteInboundTemplate(id: UUID): Promise<void> {
  await api.delete(`/api/v1/inbound-templates/${id}`)
}
