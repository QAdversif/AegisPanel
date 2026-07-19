// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Inbounds service. Inbounds are nested under
// /api/v1/nodes/{nodeId}/inbounds in the Go router
// (see backend/internal/router/router.go). v0.2.0
// surface:
//
//   - GET    /api/v1/nodes/{nodeId}/inbounds/         -> list
//   - GET    /api/v1/nodes/{nodeId}/inbounds/{id}/   -> get one
//   - POST   /api/v1/nodes/{nodeId}/inbounds/         -> create
//   - PUT    /api/v1/nodes/{nodeId}/inbounds/{id}/   -> update
//   - DELETE /api/v1/nodes/{nodeId}/inbounds/{id}/   -> delete
//
// The nodeId is a URL parameter; the create / update
// payloads mirror `src/schemas/inbound.ts` (with the
// camelCase wire format the Go side now emits after
// the v0.2.0 json-tag fix). PUT semantics are "send
// only what changed" (absent keys = "leave alone").
import type { Inbound, UUID } from '@/types'
import type { InboundCreateInput, InboundUpdateInput } from '@/schemas'

import { api } from '../client'

export async function listInboundsForNode(nodeId: UUID): Promise<Inbound[]> {
  const { data } = await api.get<{ inbounds: Inbound[] }>(`/api/v1/nodes/${nodeId}/inbounds/`)
  return data.inbounds ?? []
}

export async function getInbound(nodeId: UUID, id: UUID): Promise<Inbound> {
  const { data } = await api.get<Inbound>(`/api/v1/nodes/${nodeId}/inbounds/${id}`)
  return data
}

export async function createInbound(
  nodeId: UUID,
  req: Omit<InboundCreateInput, 'nodeId'>,
): Promise<Inbound> {
  const { data } = await api.post<Inbound>(`/api/v1/nodes/${nodeId}/inbounds/`, req)
  return data
}

export async function updateInbound(
  nodeId: UUID,
  id: UUID,
  req: InboundUpdateInput,
): Promise<Inbound> {
  const { data } = await api.put<Inbound>(`/api/v1/nodes/${nodeId}/inbounds/${id}`, req)
  return data
}

export async function deleteInbound(nodeId: UUID, id: UUID): Promise<void> {
  await api.delete(`/api/v1/nodes/${nodeId}/inbounds/${id}`)
}
