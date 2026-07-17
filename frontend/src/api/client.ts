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
  baseURL: '/',
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
    const { data } = await axios.post<{
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
  (response) => response,
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
