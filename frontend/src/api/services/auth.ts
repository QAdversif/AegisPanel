// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Auth service. Wraps the /api/v1/auth/* endpoints.

import { api } from '../client'

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  accessToken: string
  refreshToken: string
  tokenType: string
  expiresAt: string
  scopes: string[]
}

export interface MeResponse {
  userId: string
  username: string
  scopes: string[]
}

export async function login(req: LoginRequest): Promise<LoginResponse> {
  const { data } = await api.post<LoginResponse>('/api/v1/auth/login', {
    username: req.username,
    password: req.password,
  })
  return data
}

export async function me(): Promise<MeResponse> {
  const { data } = await api.get<MeResponse>('/api/v1/auth/me')
  return data
}

export interface ChangePasswordRequest {
  /** The operator's CURRENT password. Verified to defend
   * against a stolen access token. */
  current_password: string
  /** The NEW password. Must differ from the current one
   * and be at least 8 chars. */
  new_password: string
}

export async function changePassword(req: ChangePasswordRequest): Promise<MeResponse> {
  const { data } = await api.post<MeResponse>('/api/v1/auth/me/password', {
    current_password: req.current_password,
    new_password: req.new_password,
  })
  return data
}

