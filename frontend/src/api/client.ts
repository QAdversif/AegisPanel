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
//
// v0.8.13+ auth-cookie storage:
//   * The refresh token is NO LONGER read from JS —
//     the server reads it from the HttpOnly cookie.
//     The frontend never touches `refreshToken` at
//     all (no localStorage, no Pinia ref, no
//     request body).
//   * `withCredentials: true` on the axios instance
//     so the browser attaches the cookie to every
//     `/api/v1` request. Same-origin requests
//     already include credentials by default, but
//     the explicit flag also covers any future
//     cross-origin dev setup (e.g. Vite on a
//     different port with a CORS proxy).
//   * The access token is in-memory only (Pinia
//     ref) — the audit's "in-memory only" call.
//     Page refresh loses the access token; the
//     next API call hits 401, the response
//     interceptor fires the refresh path, and the
//     new access token lands back in the Pinia
//     store before the original request is retried.
//     The user does not see a re-login unless the
//     refresh cookie has also expired.

import axios, { AxiosError, type AxiosRequestConfig } from 'axios'

import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'
import { i18n } from '@/i18n'
import { router } from '@/router'
import type { ApiError } from '@/types'

// Recursively convert snake_case object keys to camelCase.
// Used in the response interceptor to bridge the panel's
// snake_case OpenAPI output to the UI's camelCase TS types.
// Memoised via a WeakMap keyed by the source object. The cache
// is GC'd automatically when the response object is no longer
// referenced, so there is no memory leak. The `!== undefined`
// check (rather than a truthy check) is intentional — a cached
// `undefined` value is impossible because the only branch that
// stores into the cache runs after `out` is fully populated.
const camelCache = new WeakMap<object, unknown>()
function camelizeKeys<T>(value: T): T {
  if (Array.isArray(value)) {
    return value.map((item) => camelizeKeys(item)) as unknown as T
  }
  if (value && typeof value === 'object') {
    const cached = camelCache.get(value as object)
    if (cached !== undefined) return cached as T
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(value as Record<string, unknown>)) {
      const camelKey = k.replace(/_([a-z0-9])/g, (_, c: string) => c.toUpperCase())
      out[camelKey] = camelizeKeys(v as unknown)
    }
    camelCache.set(value as object, out)
    return out as unknown as T
  }
  return value
}

export const api = axios.create({
  // Base URL is the directory part of the current URL.
  // In the Phase 1 sub-path deploy this resolves to
  // "/<panel-sub-path>/" so API calls go through top-level
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
  // v0.8.13+: withCredentials so the browser attaches
  // the HttpOnly refresh cookie to every /api/v1
  // request, and to log Set-Cookie from the
  // /auth/login + /auth/refresh responses. Same-origin
  // requests already include credentials by default;
  // the flag is explicit for clarity and covers any
  // future cross-origin dev setup.
  withCredentials: true,
  headers: {
    Accept: 'application/json',
    'Content-Type': 'application/json',
  },
})

// Attach bearer token on every request. v0.8.13+:
// the access token is in-memory (Pinia ref) so the
// page refresh + 401-refresh-retry dance is the
// recovery path (the refresh path lands the new
// access token in the store via the response
// interceptor before the original request is retried).
api.interceptors.request.use((config) => {
  const access = useAuthStore().accessToken
  if (access) {
    config.headers.set('Authorization', `Bearer ${access}`)
  }
  return config
})

// Endpoints that must NEVER be retried after 401
// (would loop forever).
const NON_RETRYABLE_PATHS = ['/api/v1/auth/login', '/api/v1/auth/refresh', '/api/v1/auth/logout']

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
    // v0.8.13+: the refresh token lives in the
    // HttpOnly cookie. We send NO body — the server
    // reads the cookie via http.Cookie. The body
    // backwards-compat path is a v0.8.0-v0.8.12
    // affordance, not the v0.8.13+ canonical path.
    //
    // v0.8.28.6: the response interceptor above
    // (`response.use` at line ~196) camelizes every
    // success body, including this one. Reading
    // `data.accessToken` (camelCase) — NOT
    // `data.access_token` — is load-bearing; the
    // pre-v0.8.28.6 snake_case destructure
    // (`data.access_token`) silently returned
    // `undefined` (the keys had already been
    // camelized), which set the auth store to
    // `{accessToken: undefined, expiresAt: undefined}`,
    // failed the `if (newToken)` truthy-check in the
    // 401 interceptor's error branch, cleared the
    // store, and bounced the user to /login on
    // every access-token expiry (15 min TTL) even
    // when the HttpOnly refresh cookie was still
    // valid. Symptom: any tab left open for >15 min
    // logged the user out on the next API call.
    const { data } = await api.post<{
      accessToken: string
      expiresAt: string
    }>('/api/v1/auth/refresh', null)
    // Push the new access token into the in-memory
    // store. The Pinia ref is the only authoritative
    // source of the access token — the localStorage
    // surface was deleted in v0.8.13+.
    useAuthStore().setAccessToken({
      accessToken: data.accessToken,
      expiresAt: data.expiresAt,
    })
    flushRefreshQueue(data.accessToken)
    return data.accessToken
  } catch {
    // Refresh failed: the cookie is gone or
    // revoked. Clear the store and surface a clear
    // "session expired" signal so the user knows
    // why the page is about to re-route.
    //
    // v0.8.26+: before this fix the interceptor
    // silently dropped the tokens; the user saw a
    // generic per-action "401 — http_error" toast
    // (from the view's catch block) and the page
    // itself didn't navigate, so the operator had
    // to refresh the tab to land on /login. With
    // the fix: the first stale-session 401 fires a
    // destructive toast, sets the
    // `sessionExpiredNotified` latch, and
    // programmatically pushes the user to /login
    // with the original URL in `?redirect=`. The
    // latch dedups the toast when 5 in-flight
    // requests all 401 in parallel.
    const auth = useAuthStore()
    auth.clear()
    if (!auth.sessionExpiredNotified) {
      auth.markSessionExpired()
      useToastStore().add({
        title: i18n.global.t('common.sessionExpired'),
        description: i18n.global.t('common.sessionExpiredDescription'),
        variant: 'destructive',
        duration: 6000,
      })
      const currentRoute = router.currentRoute.value
      if (currentRoute.name !== 'login') {
        void router.push({
          name: 'login',
          query: { redirect: currentRoute.fullPath },
        })
      }
    }
    flushRefreshQueue(null)
    return null
  } finally {
    isRefreshing = false
  }
}

api.interceptors.response.use(
  (response) => {
    // The panel's OpenAPI spec uses snake_case (access_token,
    // expires_at, scopes) but every TS interface in
    // `services/*` and `stores/*` was written in camelCase.
    // A plain `as` cast on `api.post<LoginResponse>` only
    // silences the type system; at runtime the response
    // really is snake_case, so `result.accessToken` is
    // `undefined`, the Pinia ref is `null`, and the next
    // request's interceptor finds no accessToken to attach,
    // so the server answers 401 "missing bearer token".
    // Convert once on the way in so every consumer can
    // stay in camelCase. v0.8.14+: the `refresh_token`
    // field is gone from the response body (it was the
    // v0.8.13 backwards-compat shim), so the camelized
    // LoginResponse no longer has a `refreshToken` key.
    if (response.data && typeof response.data === 'object') {
      // v0.8.32.2 (#301): bail out for non-JSON response
      // types. `responseType: 'blob'` (used by the backup
      // download flow in `api/services/backups.ts:115`)
      // is a `Blob` instance at runtime, not a plain
      // object; `typeof blob === 'object'` is true, so
      // the pre-fix code would happily call
      // `camelizeKeys(blob)` and return a plain `{}`
      // (Blob has no enumerable own properties). The
      // caller then did `URL.createObjectURL({})` and
      // the browser threw "Invalid URL" — the
      // download never started. The skip list also
      // covers `'arraybuffer'` and `'stream'` for the
      // same reason; neither is an enumerable-object
      // payload, and treating them as one would corrupt
      // the bytes before the call site sees them.
      const rtype = response.config?.responseType
      if (rtype !== 'blob' && rtype !== 'arraybuffer' && rtype !== 'stream') {
        response.data = camelizeKeys(response.data)
      }
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
        // Stamp the new bearer onto the original
        // request's headers and retry it. The
        // `original.headers` field is typed as
        // `RawAxiosRequestHeaders | AxiosHeaders | undefined`
        // in axios v1.x (the response interceptor sees
        // the original request config, not the
        // InternalAxiosRequestConfig that dispatch
        // normalises). The assignment + Record cast
        // works for both RawAxiosRequestHeaders
        // (plain object — direct field write) and
        // AxiosHeaders (the cast is a no-op since
        // AxiosHeaders indexer is also Record-shaped
        // for `Authorization`). v0.8.28.6: removed
        // the duplicate `auth.clear()` call in the
        // catch path that followed this `if` block —
        // the refresh-failure handler in
        // `refreshTokens()` already clears the store,
        // fires the latched "session expired" toast,
        // and pushes the router to /login.
        original.headers = original.headers ?? {}
        ;(original.headers as Record<string, string>).Authorization = `Bearer ${newToken}`
        return api.request(original)
      }
      // Refresh failed. `refreshTokens()` already
      // called `auth.clear()` + fired the latched
      // "session expired" toast + pushed the router
      // to /login (with the original URL in
      // `?redirect=`). Do NOT call `auth.clear()` a
      // second time here — the v0.8.26+ latch in
      // `sessionExpiredNotified` makes the second
      // clear a no-op, but the second
      // `router.push({ name: 'login' })` was
      // firing twice in the failure path and
      // double-pushing the router history. The
      // 401 just falls through to `Promise.reject`
      // and the per-call catch in the view handles
      // the toast (or silently swallows if the
      // `sessionExpiredNotified` toast is already on
      // screen).
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
