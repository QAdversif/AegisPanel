// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP client wrapping the Aegis panel API.
// v0.1.0 (PR-D):
//   * Bearer-token interceptor (reads from auth store)
//   * 401 -> refresh once, retry original request
//   * Structured ApiError surface for the toast store
//   * `/api/v1/auth/refresh` and `/api/v1/auth/login`
//     are excluded from the 401-retry loop to avoid
//     an infinite refresh cycle on a bad refresh
//     token.

import axios, { AxiosError, type AxiosRequestConfig } from 'axios'

import { useAuthStore } from '@/stores/auth'
import type { ApiError } from '@/types'

const STORAGE_KEY = 'aegis.tokens'

// Recursively convert snake_case object keys to camelCase.
// Used in the response interceptor to bridge the panel's
// snake_case OpenAPI output to the UI's camelCase TS types.
function camelizeKeys<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => camelizeKeys(item)) as unknown as T
  }
  if (value && typeof value === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      const camelKey = k.replace(/_([a-z0-9])/g, (_, c: string) => c.toUpperCase())
      out[camelKey] = camelizeKeys(v as unknown)
    }
    return out as unknown as T
  }
  return value
}

interface TokenPair {
  accessToken: string
  refreshToken: string
  expiresAt: string
}

function loadTokens(): TokenPair | null {
  if (typeof localStorage === 'undefined') return null
  const raw = localStorage.getItem(STORAGE_KEY)
  if (!raw) return null
  try {
    return JSON.parse(raw) as TokenPair
  } catch {
    return null
  }
}

function persistTokens(tokens: TokenPair | null): void {
  if (typeof localStorage === 'undefined') return
  if (tokens === null) {
    localStorage.removeItem(STORAGE_KEY)
  } else {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(tokens))
  }
}

export const api = axios.create({
  // Base URL is the directory part of the current URL.
  // In the Phase 1 sub-path deploy this resolves to
  // "/***REMOVED***/" so API calls go through top-level
  // Caddy's `handle_path` rule; in dev it resolves to "/"
  // and Vite's `/api` dev-proxy takes over. The previous
  // hard-coded `'/'` sent every request to the apex, which
  // top-level Caddy has no route for and answered with the
  // decoy HTML site (and CORS-blocked the real POST).
  baseURL: (() => {
    const m = window.location.pathname.match(/^(\/[^/]+\/)/)
    return m ? m[1] : '/'
  })(),
  timeout: 15_000,
  headers: {
    Accept: 'application/json',
    'Content-Type': 'application/json',
  },
})

// Attach bearer token on every request.
api.interceptors.request.use((config) => {
  const tokens = loadTokens()
  if (tokens?.accessToken) {
    config.headers.set('Authorization', `Bearer ${tokens.accessToken}`)
  }
  return config
})

// Endpoints that must NEVER be retried after 401
// (would loop forever).
const NON_RETRYABLE_PATHS = ['/api/v1/auth/login', '/api/v1/auth/refresh']

// 401 -> refresh + retry once.
let isRefreshing = false
let refreshQueue: Array<(token: string | null) => void> = []

function flushRefreshQueue(token: string | null): void {
  for (const cb of refreshQueue) cb(token)
  refreshQueue = []
}

async function refreshTokens(): Promise<string | null> {
  if (isRefreshing) {
    return new Promise((resolve) => refreshQueue.push(resolve))
  }
  isRefreshing = true
  try {
    const current = loadTokens()
    if (!current?.refreshToken) {
      flushRefreshQueue(null)
      return null
    }
    // Use the `api` instance, not the bare `axios` import —
    // the bare `axios.post` doesn't honour the dynamic
    // baseURL set in this file, so in the Phase 1 sub-path
    // deploy it hit the apex (decoy HTML 200) instead of the
    // panel's real /api/v1/auth/refresh endpoint, the JSON
    // parse threw, the catch block wiped the tokens, and
    // every subsequent /me call came back 401.
    const { data } = await api.post<{
      access_token: string
      refresh_token: string
      expires_at: string
    }>('/api/v1/auth/refresh', { refresh_token: current.refreshToken })
    const next: TokenPair = {
      accessToken: data.access_token,
      refreshToken: data.refresh_token,
      expiresAt: data.expires_at,
    }
    persistTokens(next)
    flushRefreshQueue(next.accessToken)
    return next.accessToken
  } catch {
    persistTokens(null)
    flushRefreshQueue(null)
    return null
  } finally {
    isRefreshing = false
  }
}

api.interceptors.response.use(
  (response) => {
    // The panel's OpenAPI spec uses snake_case (access_token,
    // refresh_token, expires_at) but every TS interface in
    // `services/*` and `stores/*` was written in camelCase.
    // A plain `as` cast on `api.post<LoginResponse>` only
    // silences the type system; at runtime the response
    // really is snake_case, so `result.accessToken` is
    // `undefined`, the token pair stored in localStorage is
    // `{accessToken: undefined, refreshToken: undefined,
    // expiresAt: undefined}`, JSON.stringify of which is the
    // empty object `"{}"` — and the next request's
    // interceptor finds no accessToken to attach, so the
    // server answers 401 "missing bearer token". Convert
    // once on the way in so every consumer can stay in
    // camelCase.
    if (response.data && typeof response.data === 'object') {
      response.data = camelizeKeys(response.data)
    }
    return response
  },
  async (error: AxiosError<ApiError>) => {
    const original = error.config as AxiosRequestConfig & { _retried?: boolean }
    const status = error.response?.status
    const path = original?.url ?? ''
    const isAuthEndpoint = NON_RETRYABLE_PATHS.some((p) => path.endsWith(p))

    if (status === 401 && !original._retried && !isAuthEndpoint) {
      original._retried = true
      const newToken = await refreshTokens()
      if (newToken) {
        original.headers = original.headers ?? {}
        ;(original.headers as Record<string, string>).Authorization = `Bearer ${newToken}`
        return api.request(original)
      }
      // Refresh failed: drop tokens, kick the auth
      // store so the UI re-routes to /login.
      useAuthStore().clear()
    }

    return Promise.reject(error)
  },
)

/** Convert an axios error into the panel's ApiError
 * shape. Falls back to a generic error when the
 * response body is not JSON.
 */
export function toApiError(error: unknown): ApiError {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as Partial<ApiError> | undefined
    if (data?.code && data?.message) return data as ApiError
    return {
      code: 'http_error',
      message: error.message,
      details: { status: String(error.response?.status ?? 0) },
    }
  }
  return { code: 'unknown_error', message: String(error) }
}
