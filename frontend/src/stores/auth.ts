// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Pinia store: authentication + panel-reachability.
//
// v0.8.13+ auth-cookie storage:
//   * `accessToken` is in-memory only (Pinia ref).
//     Page refresh drops it; the next API call hits
//     401, the response interceptor fires the refresh
//     path (which reads the HttpOnly cookie), and
//     the new access token lands back in the store
//     before the original request is retried.
//   * The refresh token is NEVER held in JS — it
//     lives in the HttpOnly cookie that the server
//     set on /auth/login. The frontend only sees
//     the access token (15-min lifetime) and the
//     cookie's presence/absence is communicated by
//     200/401 on protected endpoints.
//   * The v0.8.0-v0.8.12 localStorage 'aegis.tokens'
//     surface is gone. Migrating from v0.8.12 is
//     automatic: the user re-logs in once, the new
//     cookie is set, and from that point on no JS
//     code touches the refresh token.
//
// The token pair shape here is now just `{ accessToken,
// expiresAt }` — the previous `refreshToken` field is
// removed. The auth client (`@/api/services`) decodes
// the snake_case wire format and writes the accessToken
// via `setAccessToken`; the refresh path does the same.

import { defineStore } from 'pinia'
import { computed, ref } from 'vue'

import { api, toApiError } from '@/api/client'
import { login as apiLogin, logout as apiLogout, me as apiMe } from '@/api/services'

// AccessToken is the only auth state held in JS.
// The refreshToken is unreachable from JS (HttpOnly cookie).
interface AccessToken {
  accessToken: string
  expiresAt: string
}

export const useAuthStore = defineStore('auth', () => {
  const status = ref<'unknown' | 'ok' | 'down'>('unknown')
  const lastCheckedAt = ref<Date | null>(null)

  // v0.8.13+: in-memory only. No localStorage. No
  // sessionStorage. No IndexedDB. A page refresh
  // drops the value; the next API call rebuilds it
  // via the 401-refresh-retry path (which reads
  // the HttpOnly cookie on the server).
  const token = ref<AccessToken | null>(null)
  const me = ref<{ userId: string; username: string; scopes: string[] } | null>(null)

  // accessToken is the computed the axios request
  // interceptor reads. Exposed as a top-level
  // computed (rather than reading `token.value
  // ?.accessToken` at every call site) so the
  // interceptor can write `useAuthStore().accessToken`
  // and the field is reactive for any future
  // template that wants to show "session
  // expiring soon" UI.
  const accessToken = computed(() => token.value?.accessToken ?? null)
  const isAuthenticated = computed(() => Boolean(token.value?.accessToken))

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
    // v0.8.13+: the server returned `refresh_token`
    // in the body for one release as a backwards-
    // compat shim. The v0.8.13+ client does NOT
    // use it — the cookie set by the same response
    // is the authoritative channel. We just take
    // the accessToken and forget the rest.
    token.value = {
      accessToken: result.accessToken,
      expiresAt: result.expiresAt,
    }
    // Best-effort: cache identity. If it fails, we
    // still consider the user logged in — the next
    // page will retry.
    try {
      me.value = await apiMe()
    } catch {
      me.value = null
    }
  }

  // boot is the v0.8.13+ page-load rehydration
  // hook. The Pinia store is fresh after a page
  // refresh, so token.value is null; the user
  // *appears* logged out until the first /auth/me
  // call. We fire that call from App.vue's onMounted
  // so the user is re-established transparently
  // (the 401-refresh-retry path handles the "no
  // access token" case automatically).
  async function boot(): Promise<void> {
    if (token.value?.accessToken) return
    try {
      me.value = await apiMe()
    } catch (error) {
      // 401 -> refresh -> 200: handled by the
      // interceptor. Anything else is a real
      // failure (no cookie, expired cookie, etc.)
      // and the user stays logged out.
      const apiErr = toApiError(error)
      if (apiErr.code !== 'http_error' || apiErr.details?.status !== '401') {
        throw error
      }
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

  // logout hits POST /api/v1/auth/logout (which
  // clears the cookie on the server) and then drops
  // the in-memory access token. The cookie is the
  // authoritative cleanup signal; the access-token
  // drop is the local cleanup.
  async function logout(): Promise<void> {
    try {
      await apiLogout()
    } catch {
      // Best-effort: even if the server call fails
      // (network blip, server down), the local
      // access token is dropped. The cookie may
      // linger on the browser, but it expires in
      // 30 days and the server-side row is
      // already marked used.
    }
    token.value = null
    me.value = null
  }

  // setAccessToken is the v0.8.13+ Pinia write
  // entry-point for the access token. The response
  // interceptor (api/client.ts) calls it from the
  // refresh path; the auth.login() call also calls
  // it. No other call site should mutate
  // token.value directly.
  function setAccessToken(t: AccessToken): void {
    token.value = t
  }

  /** Called by the axios interceptor when the
   * refresh path itself failed. Same as logout but
   * named differently to keep call sites honest.
   * The server-side row is already marked used (the
   * failed refresh call consumed it via the
   * ConsumeRefresh UPDATE), so no second
   * apiLogout() call is needed.
   */
  function clear(): void {
    token.value = null
    me.value = null
  }

  return {
    status,
    lastCheckedAt,
    token,
    accessToken,
    me,
    isAuthenticated,
    ping,
    boot,
    login,
    refreshMe,
    logout,
    setAccessToken,
    clear,
  }
})
