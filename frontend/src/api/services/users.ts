// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Users service. Wraps /api/v1/users (admin CRUD).
// v0.2.0 surface:
//
//   - GET  /              -> list every user
//   - GET  /{id}          -> get a single user
//   - POST /              -> create a user
//   - PATCH /{id}         -> partial update
//   - POST /{id}/rotate-token -> rotate the sub_token
//
// Soft-delete (status='deleted') is handled via PATCH
// for v0.2; a dedicated DELETE endpoint lands in
// v0.3 alongside the audit log.

import type { User, UUID } from '@/types'

import { api } from './../client'

export interface CreateUserRequest {
  username: string
  status?: User['status']
  planId?: UUID
  expireAt?: string
  trafficLimitBytes?: number
  deviceLimit?: number
  hostsAllowlist?: UUID[]
  hostsBlocklist?: UUID[]
}

export interface UpdateUserRequest {
  username?: string
  status?: User['status']
  planId?: UUID
  clearPlanId?: boolean
  expireAt?: string
  clearExpireAt?: boolean
  trafficLimitBytes?: number
  deviceLimit?: number
  hostsAllowlist?: UUID[]
  hostsBlocklist?: UUID[]
}

export interface RotateTokenRequest {
  /** Optional grace window (seconds) during which
   * the OLD sub_token still serves requests. The
   * server caps at 3600 (1h).
   */
  graceWindowSeconds?: number
}

export async function listUsers(): Promise<User[]> {
  const { data } = await api.get<{ users: User[] }>('/api/v1/users/')
  return data.users ?? []
}

export async function getUser(id: UUID): Promise<User> {
  const { data } = await api.get<User>(`/api/v1/users/${id}`)
  return data
}

export async function createUser(req: CreateUserRequest): Promise<User> {
  const { data } = await api.post<User>('/api/v1/users/', req)
  return data
}

export async function updateUser(id: UUID, req: UpdateUserRequest): Promise<User> {
  const { data } = await api.patch<User>(`/api/v1/users/${id}`, req)
  return data
}

export async function rotateUserToken(
  id: UUID,
  req: RotateTokenRequest = {},
): Promise<User> {
  const { data } = await api.post<User>(`/api/v1/users/${id}/rotate-token`, req)
  return data
}
