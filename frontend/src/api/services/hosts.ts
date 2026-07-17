// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Hosts service. Wraps the /api/v1/hosts CRUD endpoints.

import type { Host, UUID } from '@/types'

import { api } from '../client'

export async function listHosts(): Promise<Host[]> {
  const { data } = await api.get<Host[]>('/api/v1/hosts/')
  return data
}

export async function getHost(id: UUID): Promise<Host> {
  const { data } = await api.get<Host>(`/api/v1/hosts/${id}`)
  return data
}

export async function deleteHost(id: UUID): Promise<void> {
  await api.delete(`/api/v1/hosts/${id}`)
}

