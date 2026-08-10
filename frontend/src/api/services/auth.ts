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

// logout hits POST /api/v1/auth/logout. The refresh
// token is read by the server from the HttpOnly cookie
// (the v0.8.13+ authoritative channel); the body is
// also accepted as a backwards-compat path but the
// v0.8.13+ client does not send it. `withCredentials`
// is set so the browser attaches the cookie. The
// server replies 204 No Content and clears the
// cookie; the frontend then drops the access token
// from the Pinia store.
export async function logout(): Promise<void> {
  await api.post('/api/v1/auth/logout', null, {
    withCredentials: true,
  })
}

