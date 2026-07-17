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

