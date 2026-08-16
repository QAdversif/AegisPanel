// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Auth service. Wraps the /api/v1/auth/* endpoints.

import type { ChangePasswordRequest } from '@/types/aegis'
import { api } from '../client'

export interface LoginRequest {
  username: string
  password: string
}

export interface LoginResponse {
  // v0.8.14+: the body only carries the access token.
  // The refresh token is set as a `Set-Cookie: aegis_rt=...`
  // HttpOnly cookie on the same response (the only
  // authoritative channel). The v0.8.13 `refreshToken`
  // body-field shim is closed.
  accessToken: string
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

export async function changePassword(req: ChangePasswordRequest): Promise<MeResponse> {
  const { data } = await api.post<MeResponse>('/api/v1/auth/me/password', {
    current_password: req.current_password,
    new_password: req.new_password,
  })
  return data
}

// logout hits POST /api/v1/auth/logout. v0.8.14+: the
// refresh token is read by the server from the HttpOnly
// cookie (the only authoritative channel — the v0.8.13
// body-fallback is closed). The body is ignored. The
// `withCredentials` flag is set so the browser attaches
// the cookie; same-origin requests already include
// credentials by default, the flag is explicit for
// clarity. The server replies 204 No Content and clears
// the cookie; the frontend then drops the access token
// from the Pinia store.
export async function logout(): Promise<void> {
  await api.post('/api/v1/auth/logout', null, {
    withCredentials: true,
  })
}

