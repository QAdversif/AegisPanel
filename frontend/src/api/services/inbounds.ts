// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Inbounds service. Inbounds are nested under
// /api/v1/nodes/{nodeId}/inbounds in the Go router
// (see backend/internal/router/router.go).

import type { Inbound, UUID } from '@/types'

import { api } from '../client'

export async function listInboundsForNode(nodeId: UUID): Promise<Inbound[]> {
  const { data } = await api.get<Inbound[]>(`/api/v1/nodes/${nodeId}/inbounds/`)
  return data
}

export async function getInbound(nodeId: UUID, id: UUID): Promise<Inbound> {
  const { data } = await api.get<Inbound>(`/api/v1/nodes/${nodeId}/inbounds/${id}`)
  return data
}

