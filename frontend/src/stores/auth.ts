// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Pinia store: authentication + panel-reachability.
// PR-D adds the real auth flow on top of the Phase 0
// `ping()` stub:
//
//   * `login(username, password)` -> POST /api/v1/auth/login,
//     persist the access/refresh pair in localStorage
//   * `me()` -> GET /api/v1/auth/me, populates the
//     `me` ref so the topbar can show "Logged in as X"
//   * `logout()` -> drop the tokens
//   * `clear()` -> same as logout, used by the
//     axios interceptor on refresh failure
//
// The token pair is also persisted by
// `api/client.ts` (the bearer-token interceptor reads
// from localStorage so it works even before Pinia is
// initialised). The two writers agree on the storage
// key and the shape, defined in `@/api/client`.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, toApiError } from '@/api/client'
import { login as apiLogin, me as apiMe } from '@/api/services'

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

export const useAuthStore = defineStore('auth', () => {
  const status = ref<'unknown' | 'ok' | 'down'>('unknown')
  const lastCheckedAt = ref<Date | null>(null)

  const tokens = ref<TokenPair | null>(loadTokens())
  const me = ref<{ userId: string; username: string; scopes: string[] } | null>(null)

  const isAuthenticated = computed(() => Boolean(tokens.value?.accessToken))

  async function ping(): Promise<void> {
    try {
      await api.get('/api/v1/health', { timeout: 3000 })
      status.value = 'ok'
    } catch {
      status.value = 'down'
    } finally {
      lastCheckedAt.value = new Date()
    }
  }

  async function login(username: string, password: string): Promise<void> {
    const result = await apiLogin({ username, password })
    const next: TokenPair = {
      accessToken: result.accessToken,
      refreshToken: result.refreshToken,
      expiresAt: result.expiresAt,
    }
    tokens.value = next
    persistTokens(next)
    // Best-effort: cache identity. If it fails, we
    // still consider the user logged in — the next
    // page will retry.
    try {
      me.value = await apiMe()
    } catch {
      me.value = null
    }
  }

  async function refreshMe(): Promise<void> {
    if (!isAuthenticated.value) return
    try {
      me.value = await apiMe()
    } catch (error) {
      // 401 is handled by the interceptor (refresh +
      // retry). Anything else is a real failure.
      const apiErr = toApiError(error)
      if (apiErr.code !== 'http_error' || apiErr.details?.status !== '401') {
        throw error
      }
    }
  }

  function logout(): void {
    tokens.value = null
    me.value = null
    persistTokens(null)
  }

  /** Called by the axios interceptor when the
   * refresh path itself failed. Same as logout but
   * named differently to keep call sites honest.
   */
  function clear(): void {
    logout()
  }

  return {
    status,
    lastCheckedAt,
    tokens,
    me,
    isAuthenticated,
    ping,
    login,
    refreshMe,
    logout,
    clear,
  }
})
