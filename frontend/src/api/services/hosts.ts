// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Hosts service. Wraps the /api/v1/hosts CRUD endpoints.
// v0.2.0 surface:
//
//   - GET    /              -> list every host
//   - GET    /{id}          -> get a single host
//   - POST   /              -> create a host
//   - PUT    /{id}          -> update a host
//   - DELETE /{id}          -> delete a host
//
// The create / update payloads mirror the host
// schema in `src/schemas/host.ts` (with the
// camelCase wire format the Go side now emits after
// the v0.2.0 json-tag fix). The endpoint array is
// passed as-is; the superRefine cross-field rules
// (direct=1 endpoint, balancer>=2 + strategy)
// are enforced server-side and surface as a 400
// response with a descriptive error message.

import type { Host, UUID } from '@/types'
import type { HostCreateInput, HostUpdateInput } from '@/schemas'

import { api } from '../client'

export async function listHosts(): Promise<Host[]> {
  const { data } = await api.get<{ hosts: Host[] }>('/api/v1/hosts/')
  return data.hosts ?? []
}

export async function getHost(id: UUID): Promise<Host> {
  const { data } = await api.get<Host>(`/api/v1/hosts/${id}`)
  return data
}

export async function createHost(req: HostCreateInput): Promise<Host> {
  const { data } = await api.post<Host>('/api/v1/hosts/', req)
  return data
}

export async function updateHost(id: UUID, req: HostUpdateInput): Promise<Host> {
  const { data } = await api.put<Host>(`/api/v1/hosts/${id}`, req)
  return data
}

export async function deleteHost(id: UUID): Promise<void> {
  await api.delete(`/api/v1/hosts/${id}`)
}
