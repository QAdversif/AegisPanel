// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Nodes service. Wraps the /api/v1/nodes CRUD endpoints.
// v0.1.0 ships list/get/create/update/delete.
// v0.3.0 adds provision (the BYO Node bootstrap
// action; backed by `internal/bootstrap`).

import type { Node, NodeProvisionRequest, NodeProvisionResponse, UUID } from '@/types'

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

/**
 * Provision a node (v0.3.0 BYO Node flow). Wraps
 * `POST /api/v1/nodes/{id}/provision`. Synchronous
 * in v0.3.0 — the panel runs the install to
 * completion before returning. v0.5.0 will move to
 * kick-off+poll (returns 202 + a job id) for
 * large fleets; this signature stays stable.
 *
 * Throws on 400 / 404 / 409 / 502 — the UI
 * distinguishes by `toApiError(error).message`.
 */
export async function provisionNode(
  id: UUID,
  req: NodeProvisionRequest,
): Promise<NodeProvisionResponse> {
  const { data } = await api.post<NodeProvisionResponse>(
    `/api/v1/nodes/${id}/provision`,
    req,
  )
  return data
}

