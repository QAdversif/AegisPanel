// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Nodes service. Wraps the /api/v1/nodes CRUD endpoints.
// v0.1.0 ships list/get/create/update/delete; a
// dedicated state-transition endpoint is deferred to
// v0.2 (the operator edits `state` via the regular
// update form for now).

import type { Node, UUID } from '@/types'

import { api } from '../client'

export interface NodeCreateRequest {
  name: string
  region: string
  address: string
  capacityHint?: string
  tags?: string[]
}

export type NodeUpdateRequest = Partial<NodeCreateRequest>

export async function listNodes(): Promise<Node[]> {
  const { data } = await api.get<Node[]>('/api/v1/nodes/')
  return data
}

export async function getNode(id: UUID): Promise<Node> {
  const { data } = await api.get<Node>(`/api/v1/nodes/${id}`)
  return data
}

export async function createNode(req: NodeCreateRequest): Promise<Node> {
  const { data } = await api.post<Node>('/api/v1/nodes/', req)
  return data
}

export async function updateNode(id: UUID, req: NodeUpdateRequest): Promise<Node> {
  const { data } = await api.put<Node>(`/api/v1/nodes/${id}`, req)
  return data
}

export async function deleteNode(id: UUID): Promise<void> {
  await api.delete(`/api/v1/nodes/${id}`)
}

