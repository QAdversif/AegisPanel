// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Inbounds service. Inbounds are nested under
// /api/v1/nodes/{nodeId}/inbounds in the Go router
// (see backend/internal/router/router.go). v0.2.0
// surface:
//
//   - GET    /api/v1/nodes/{nodeId}/inbounds/         -> list (per-node)
//   - GET    /api/v1/nodes/{nodeId}/inbounds/{id}/   -> get one
//   - POST   /api/v1/nodes/{nodeId}/inbounds/         -> create
//   - PUT    /api/v1/nodes/{nodeId}/inbounds/{id}/   -> update
//   - DELETE /api/v1/nodes/{nodeId}/inbounds/{id}/   -> delete
//
// v0.8.x: a panel-wide /api/v1/inbounds/ endpoint
// was added so the admin UI can preload every inbound
// across every node in a single round-trip (instead
// of N per-node requests). Each record carries its
// `nodeId` so the UI can group client-side. The
// per-node list above is still used for the
// single-node refetch path (after Create/Update).
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

// listInbounds returns every inbound across every node
// in a single round-trip. Use this for panel-wide
// preloads (e.g. opening a create / edit dialog that
// needs to show port / name occupancy on all nodes).
// For single-node refreshes after a write, prefer
// listInboundsForNode so the change is local to the
// affected node.
export async function listInbounds(): Promise<Inbound[]> {
  const { data } = await api.get<{ inbounds: Inbound[] }>('/api/v1/inbounds/')
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
