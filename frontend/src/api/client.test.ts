// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Regression test for the v0.8.28.6 fix to the
// `refreshTokens()` snake_case / camelCase mismatch
// in `frontend/src/api/client.ts:138-151` (issue
// #289 / C1 - the "dead session refresh" bug).
//
// Background: the response interceptor camelizes every
// success body (including `POST /api/v1/auth/refresh`)
// before the typed return reaches `refreshTokens()`.
// Pre-v0.8.28.6 the function destructured the
// wire-shape with snake_case keys
// (`data.access_token`, `data.expires_at`), got
// `undefined` for both, and called
// `useAuthStore().setAccessToken({ accessToken: undefined,
// expiresAt: undefined })`. The next request's
// interceptor found no bearer to attach -> 401 -> refresh
// -> same `undefined` -> store.clear() -> redirect to
// /login on every access-token expiry (15-min TTL) even
// when the HttpOnly refresh cookie was still valid.
// Symptom: any tab left open for >15 min logged the
// user out on the next API call.
//
// v0.8.32.2 (#301) added a fifth test: the response
// interceptor must NOT call `camelizeKeys()` on a
// `responseType: 'blob'` payload, otherwise the Blob
// is converted to a plain `{}` and the backup-download
// flow (which uses `URL.createObjectURL(data)`) throws
// "Invalid URL" and never starts the download. The fix
// is a `responseType` skip list at the top of the success
// branch; the same skip applies to `'arraybuffer'` and
// `'stream'` for the same reason.
//
// The test covers the five scenarios in the issue's
// "Test plan" section:
//   1. 401 + 200 from /auth/refresh -> original
//      retried with the new bearer, store populated;
//   2. parallel 401s -> single-flight (one refresh
//      call), queue flushed;
//   3. refresh 401 -> store cleared, latched
//      "session expired" toast (single), redirect
//      carries `?redirect=`.
//   4. snake_case server response is bridged to
//      camelCase via the response interceptor (the
//      bridge the v0.8.28.6 fix relies on).
//   5. v0.8.32.2 (#301): a `responseType: 'blob'`
//      payload is returned to the caller as a Blob,
//      NOT camelized into a plain `{}`.
//
// All five fail against the pre-fix code; they are the
// regression guard the file never had.
//
// The refresh flow's full contract - the auth store,
// the toast store, the router push, the session-expiry
// latch - is tested via Pinia + a vue-router mock
// (we don't need a real router; the interceptor's
// `router.push({ name: 'login', query: { redirect: ... }})`
// is recorded into a mock router we inject).
//
// The mock adapter pattern (per the vitest unit-test
// recipe in the v0.1.0 docs): monkey-patch the
// `axios.create` call inside `client.ts` to swap the
// transport layer for a per-test in-memory adapter
// that resolves with our fixture. This way we don't
// touch the file under test (no `axios.defaults.adapter`
// - that affects every axios instance, not just the
// one we want to test) and we don't need a real
// network.
//
// @vitest-environment jsdom
import axios from 'axios'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { AxiosError, type AxiosInstance, type AxiosRequestConfig, type InternalAxiosRequestConfig } from 'axios'
import { setActivePinia, createPinia } from 'pinia'

// NOTE: do NOT import the static `api` from
// `@/api/client` here. The mock for `axios.create`
// is installed in `beforeEach` (after the static
// imports have already evaluated the module). The
// static `api` would be bound to a real network
// transport; we need every test to use a fresh
// module instance with the mock adapter, which
// is what `freshClient()` returns.
//
// We DO import the static `toApiError` - it is a
// pure function (no module-level state, no transport
// dependency). Bare calls in the toApiError describe
// block resolve to the function via the named import.
import { toApiError } from './client'
import * as clientModule from './client'

// Stub the router module BEFORE importing `client`.
// The interceptor calls `router.push` on session
// expiry. We don't need a real router; we just need
// to record the push calls.
const routerPushMock = vi.fn()
vi.mock('@/router', () => ({
  router: {
    // `vi.fn` produces a variadic mock, so we use
    // a fixed-arity alias here (`unknown[]` is not
    // a tuple, which TypeScript rejects in a
    // rest-parameter position).
    push: (to: unknown) => routerPushMock(to),
    currentRoute: { value: { name: 'inbounds', fullPath: '/inbounds' } },
  },
}))

// Same for i18n - `i18n.global.t(...)` is called when
// the session-expiry toast is fired. Stub it to return
// the key.
const i18nTMock = vi.fn((key: string) => key)
vi.mock('@/i18n', () => ({
  i18n: {
    global: {
      // `vi.fn` produces a variadic mock, so we use
      // a fixed-arity alias here (`unknown[]` is not
      // a tuple, which TypeScript rejects in a
      // rest-parameter position).
      t: (key: string) => i18nTMock(key),
    },
  },
}))

import { useAuthStore } from '@/stores/auth'
import { useToastStore } from '@/stores/toast'

// Per-test adapter swap. The `client.ts` module
// calls `axios.create(...)` at import time to build
// its `api` instance. We patch `axios.create`
// BEFORE the first call to install a controllable
// adapter that resolves with the fixture the test
// puts in. The interceptor's refresh path then
// re-issues the original request through this same
// `api` instance - the second request's adapter
// call gets the next fixture in the queue, so
// tests script the request -> refresh -> retry
// sequence as a stack of `AdapterResponse`
// values.
type AdapterResponse = { status: number; data: unknown; responseType?: string }

const adapterQueue: AdapterResponse[] = []
// The built-in http/xhr/fetch adapters run
// `settle(resolve, reject, response)` themselves
// (axios/lib/core/settle.js) - that's where the
// `validateStatus` check lives, not in
// `dispatchRequest`. Our mock adapter has to do
// the same check, otherwise every non-2xx fixture
// resolves as a success and the 401-retry path
// never runs. The first iteration of this file
// forgot the check; all 4 main tests failed with
// "expected 401 to be 200" because the 401 was
// passed straight through to the caller. Use the
// real `validateStatus` from the test config
// (defaults to `status >= 200 && status < 300`)
// so a future change to the default in axios
// propagates to the test automatically.
const adapterSpy = vi.fn((config: AxiosRequestConfig) => {
  const next = adapterQueue.shift()
  if (!next) {
    return Promise.reject(
      new Error(
        `test adapter: no fixture queued for ${String(config.method ?? 'GET')} ${String(config.url ?? '')}`,
      ),
    )
  }
  // The real adapters (`http`, `xhr`, `fetch`) all
  // run `settle()` on the response before
  // returning; the response object's `config`
  // is then the `InternalAxiosRequestConfig`
  // (axios normalises the headers to
  // `AxiosRequestHeaders` before the adapter
  // sees it). Cast the local `config` to that
  // shape so the response satisfies the
  // `AxiosResponse<..., ..., { headers: AxiosRequestHeaders }, ...>`
  // type AxiosError expects in its
  // `response?: AxiosResponse<...>` parameter.
  //
  // v0.8.32.2 (#301): the test adapter honours a
  // per-fixture `responseType` override so the
  // success-path interceptor branch can be exercised
  // for non-JSON response types. We merge the
  // override into `config.responseType` so the
  // response interceptor's skip-list check sees the
  // value the call site set; the spread keeps the
  // cast narrow (only `responseType` is added; the
  // rest of `config` stays as `InternalAxiosRequestConfig`
  // so the type system is happy).
  const cfgWithType: InternalAxiosRequestConfig = {
    ...(config as InternalAxiosRequestConfig),
    responseType:
      next.responseType ??
      (config as { responseType?: string }).responseType,
  }
  const response = {
    status: next.status,
    data: next.data,
    statusText: '',
    headers: {},
    config: cfgWithType,
  }
  const validateStatus = config.validateStatus ?? ((s: number) => s >= 200 && s < 300)
  if (next.status === 0 || !validateStatus(next.status)) {
    return Promise.reject(
      new AxiosError(
        `Request failed with status code ${next.status}`,
        next.status >= 400 && next.status < 500 ? AxiosError.ERR_BAD_REQUEST : AxiosError.ERR_BAD_RESPONSE,
        // The adapter signature receives
        // `InternalAxiosRequestConfig` (axios
        // normalises the headers to `AxiosHeaders`
        // before invoking the adapter), but the
        // response interceptor sees the original
        // `AxiosRequestConfig` (where `headers` is
        // `RawAxiosRequestHeaders | AxiosHeaders | undefined`).
        // For the test adapter's purposes the
        // distinction doesn't matter - we just need
        // to satisfy the constructor's
        // `InternalAxiosRequestConfig` parameter
        // type.
        config as InternalAxiosRequestConfig,
        undefined,
        response,
      ),
    )
  }
  return Promise.resolve(response)
})

// Patch `axios.create` so it returns a fresh
// instance whose `adapter` is our `adapterSpy`. The
// original `axios.create` would otherwise build a
// real network transport; we want every test to run
// in-memory.
//
// IMPORTANT: do NOT call `axios.create` recursively
// inside the mock implementation - that causes
// "Maximum call stack size exceeded" because the
// mock is a spy on the same function. Instead, we
// hand-build an Axios instance with the same
// constructor the real `axios.create` would use,
// then have the spy return that. The instance is
// FRESH per test (not cached): reusing one across
// tests would let the response interceptors
// accumulate (each `freshClient()` re-installs
// them on top of the previous set), and the
// earlier sets would race the latest test for
// adapter fixtures.
//
// The instance is built with `{ ...axios.defaults,
// adapter: adapterSpy }` so it carries the full
// set of axios defaults - most importantly
// `validateStatus`, which controls whether non-2xx
// adapter responses are rejected as AxiosErrors.
// Without `validateStatus` in the instance's
// defaults, `lib/core/settle.js` falls through to
// `if (!response.status || !validateStatus || ...)`
// and resolves the response as a 2xx - so the
// 401 from the adapter never reaches the error
// path of the response interceptor, and the
// refresh + retry never runs. (The first version
// of this file used `new AxiosCtor({ adapter })`
// without the defaults spread; all 4 main tests
// failed with "expected 401 to be 200" - i.e. the
// 401 was passed straight through to the caller
// instead of being caught by the error path.)
let axiosCreateSpy: ReturnType<typeof vi.spyOn> | null = null

beforeEach(() => {
  adapterQueue.length = 0
  adapterSpy.mockClear()
  routerPushMock.mockClear()
  i18nTMock.mockClear()
  // Build a fresh Axios instance with the full
  // set of axios defaults (so `validateStatus` is
  // present) + the test adapter.
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const AxiosCtor = (axios as unknown as { Axios: new (cfg?: unknown) => AxiosInstance }).Axios
  const freshAxiosInstance = new AxiosCtor({
    ...axios.defaults,
    adapter: adapterSpy,
  }) as AxiosInstance
  axiosCreateSpy = vi
    .spyOn(axios, 'create')
    .mockImplementation(() => {
      // The dynamic-import `client.ts` evaluation
      // in `freshClient()` calls `axios.create(...)`
      // which now hits the spy and returns this
      // fresh instance. The interceptors are then
      // installed on THIS instance (only).
      return freshAxiosInstance as ReturnType<typeof axios.create>
    })
})

afterEach(() => {
  axiosCreateSpy?.mockRestore()
  axiosCreateSpy = null
})

// We re-import the module under test per test so the
// stateful `isRefreshing` + `refreshQueue` module
// variables are reset. The `vi.resetModules` +
// dynamic import pattern.
//
// IMPORTANT: the static `clientModule` import at
// the top of the file evaluated `axios.create` BEFORE
// the mock was installed. The `api` it bound to
// is therefore a real network transport. We never
// use that instance - every test uses a fresh
// module instance returned by `freshClient()`.
async function freshClient(): Promise<typeof clientModule> {
  // `vi.resetModules()` clears the module cache so
  // the next `import('./client')` rebuilds the
  // graph. The `vi.mock(...)` calls above are
  // hoisted to the top of this file by vitest, so
  // they re-apply to the new module instance
  // automatically. The new `axios.create` call
  // (inside the re-imported `./client`) hits the
  // mock (installed in `beforeEach`) and returns
  // a fresh instance with our adapter. The
  // interceptors are then installed on that fresh
  // instance, so the test sees the full
  // request -> 401 -> refresh -> retry -> 200 chain.
  vi.resetModules()
  return import('./client') as Promise<typeof clientModule>
}

afterEach(() => {
  vi.useRealTimers()
  setActivePinia(createPinia())
})

// Helper: queue the responses the interceptor will see
// in order, plus the post-call assertions the test
// wants. The first fixture is the response the
// ORIGINAL request receives. If it's a 401, the
// interceptor will fire the refresh path; subsequent
// fixtures are the responses to /auth/refresh and
// the retry.
function queue(...responses: AdapterResponse[]): void {
  for (const r of responses) adapterQueue.push(r)
}

// ----- Test 1: 401 + 200 from /auth/refresh -> retry succeeds -----

describe('client.ts / axios response interceptor / 401 refresh', () => {
  it('Test 1: 401 on original request + 200 from /auth/refresh -> original retried with the new bearer, store populated', async () => {
    setActivePinia(createPinia())
    const { api: freshApi } = await freshClient()

    // 1. original request -> 401
    // 2. POST /auth/refresh -> 200 with the camelCase
    //    accessToken + expiresAt (per the v0.8.28.6 fix)
    // 3. retry of the original request -> 200
    queue(
      { status: 401, data: { code: 'unauthorized', message: 'token expired' } },
      {
        status: 200,
        data: {
          // Critical: these are camelCase. The
          // pre-v0.8.28.6 code read
          // `data.access_token` (snake_case) and got
          // undefined, which set the auth store to
          // `{accessToken: undefined}` and triggered
          // the `if (newToken)` falsy branch.
          accessToken: 'new-access-token',
          expiresAt: '2026-08-23T03:00:00Z',
        },
      },
      { status: 200, data: { result: 'ok' } },
    )

    const auth = useAuthStore()
    // Pre-state: the user is "logged in" with a
    // (now-expired) access token.
    auth.token = {
      accessToken: 'old-access-token',
      expiresAt: '2026-08-22T00:00:00Z',
    }

    // Trigger the original request. The interceptor
    // sees the 401, fires the refresh, gets the new
    // token, retries the original. The retry's
    // response is what the caller sees.
    const response = await freshApi.get('/api/v1/nodes/')
    expect(response.status).toBe(200)
    expect(response.data).toEqual({ result: 'ok' })

    // The auth store now has the new access token.
    expect(auth.accessToken).toBe('new-access-token')
    expect(auth.token?.accessToken).toBe('new-access-token')
    expect(auth.token?.expiresAt).toBe('2026-08-23T03:00:00Z')
    // The latch was NOT flipped (this is a successful
    // refresh, not a real session expiry).
    expect(auth.sessionExpiredNotified).toBe(false)
    // The original request was retried (not bounced
    // to /login) - no router push happened.
    expect(routerPushMock).not.toHaveBeenCalled()
  })

  // ----- Test 2: parallel 401s -> single-flight -----

  it('Test 2: parallel 401s -> single /auth/refresh call (single-flight), queue flushed with the new token', async () => {
    setActivePinia(createPinia())
    const { api: freshApi } = await freshClient()

    // Two concurrent original requests both 401.
    // Then ONE /auth/refresh 200. Then both retries 200.
    queue(
      { status: 401, data: { code: 'unauthorized', message: 'a' } },
      { status: 401, data: { code: 'unauthorized', message: 'b' } },
      {
        status: 200,
        data: { accessToken: 'single-flight-token', expiresAt: '2026-08-23T03:00:00Z' },
      },
      { status: 200, data: { result: 'ok-1' } },
      { status: 200, data: { result: 'ok-2' } },
    )

    const auth = useAuthStore()
    auth.token = { accessToken: 'old', expiresAt: '2026-08-22T00:00:00Z' }

    // Fire two original requests in parallel. The
    // interceptor's `isRefreshing` flag should ensure
    // the second one waits for the first's refresh.
    const [r1, r2] = await Promise.all([
      freshApi.get('/api/v1/nodes/'),
      freshApi.get('/api/v1/hosts/'),
    ])

    expect(r1.status).toBe(200)
    expect(r1.data).toEqual({ result: 'ok-1' })
    expect(r2.status).toBe(200)
    expect(r2.data).toEqual({ result: 'ok-2' })

    // The auth store has the new access token.
    expect(auth.accessToken).toBe('single-flight-token')

    // The refresh adapter was called EXACTLY ONCE
    // (the second 401 waited for the in-flight refresh
    // rather than starting a second one).
    const refreshCalls = adapterSpy.mock.calls.filter(
      (call) => typeof call[0]?.url === 'string' && call[0]!.url!.endsWith('/api/v1/auth/refresh'),
    )
    expect(refreshCalls.length).toBe(1)
  })

  // ----- Test 3: refresh 401 -> store cleared, single toast, redirect with ?redirect= -----

  it('Test 3: refresh 401 -> store cleared, latched single toast, redirect carries ?redirect=', async () => {
    setActivePinia(createPinia())
    const { api: freshApi } = await freshClient()

    // 1. original request -> 401
    // 2. /auth/refresh -> 401 (the refresh cookie is
    //    gone or revoked)
    queue(
      { status: 401, data: { code: 'unauthorized', message: 'token expired' } },
      { status: 401, data: { code: 'unauthorized', message: 'refresh failed' } },
    )

    const auth = useAuthStore()
    auth.token = { accessToken: 'old', expiresAt: '2026-08-22T00:00:00Z' }
    const toast = useToastStore()
    // Pre-state: latch unset, no toasts.
    expect(auth.sessionExpiredNotified).toBe(false)
    expect(toast.toasts).toHaveLength(0)

    // The original request fails. The interceptor's
    // catch path returns Promise.reject (so the call
    // site gets an AxiosError); the call site would
    // have a per-action .catch that surfaces a toast,
    // but for the refresh-failure path the interceptor
    // itself fires the latched toast + router push.
    await expect(freshApi.get('/api/v1/nodes/')).rejects.toBeInstanceOf(AxiosError)

    // The auth store is cleared.
    expect(auth.accessToken).toBeNull()
    expect(auth.token).toBeNull()
    // The session-expiry latch is flipped (so a 2nd
    // 401 from a parallel in-flight request does not
    // spawn a duplicate toast).
    expect(auth.sessionExpiredNotified).toBe(true)
    // Exactly ONE toast was fired (and it carries the
    // "session expired" i18n key).
    expect(toast.toasts).toHaveLength(1)
    expect(toast.toasts[0]?.variant).toBe('destructive')
    // The router was pushed to /login with the
    // original URL in `?redirect=`.
    expect(routerPushMock).toHaveBeenCalledTimes(1)
    const pushArg = routerPushMock.mock.calls[0]?.[0] as
      | { name: string; query: { redirect?: string } }
      | undefined
    expect(pushArg?.name).toBe('login')
    expect(pushArg?.query?.redirect).toBe('/inbounds')
  })

  // ----- Bonus: snake_case server response is bridged via the response interceptor's camelize step -----

  it('Test 4 (regression guard): a snake_case refresh body (legacy server wire shape) is correctly bridged to camelCase via the response interceptor - the v0.8.28.6 fix relies on this bridge', async () => {
    setActivePinia(createPinia())
    const { api: freshApi } = await freshClient()

    // The panel's OpenAPI output is snake_case
    // (`access_token`, `expires_at`). The response
    // interceptor's success handler runs
    // `camelizeKeys()` on every 2xx body, including
    // this one, so the keys become `accessToken` /
    // `expiresAt` before `refreshTokens()` reads
    // them. The v0.8.28.6 fix relies on this
    // bridge: pre-fix, the code read
    // `data.access_token` (snake_case) from the
    // already-camelized body and got `undefined`.
    // This test queues a snake_case body to prove
    // the bridge is intact end-to-end. If a future
    // refactor ever drops the camelize step, the
    // refresh path will silently start returning
    // `undefined` again and this test will catch
    // it.
    queue(
      { status: 401, data: { code: 'unauthorized', message: 'token expired' } },
      {
        status: 200,
        // snake_case - the legacy / future-regression
        // server wire shape.
        data: {
          access_token: 'snake-case-bridged-token',
          expires_at: '2026-08-23T03:00:00Z',
        },
      },
      { status: 200, data: { result: 'ok' } },
    )

    const auth = useAuthStore()
    auth.token = { accessToken: 'old', expiresAt: '2026-08-22T00:00:00Z' }

    const response = await freshApi.get('/api/v1/nodes/')
    expect(response.status).toBe(200)
    // The store was populated with the bridged
    // value (camelized from snake_case). The v0.8.28.6
    // fix's `data.accessToken` read sees the
    // camelized key, not the snake_case one.
    expect(auth.accessToken).toBe('snake-case-bridged-token')
    expect(auth.token?.accessToken).toBe('snake-case-bridged-token')
    expect(auth.token?.expiresAt).toBe('2026-08-23T03:00:00Z')
    // The session-expiry latch was NOT flipped
    // (this is a successful refresh, not a real
    // session expiry).
    expect(auth.sessionExpiredNotified).toBe(false)
    // No router push - the user is not bounced to
    // /login when the refresh succeeds.
    expect(routerPushMock).not.toHaveBeenCalled()
  })

  // ----- Test 5 (v0.8.32.2 / #301): blob response is NOT camelized -----

  it('Test 5 (#301): a responseType: "blob" payload is returned to the caller as a Blob - NOT camelized into a plain {}', async () => {
    setActivePinia(createPinia())
    const { api: freshApi } = await freshClient()

    // The backup-download flow
    // (`api/services/backups.ts:113-115`,
    // `downloadBackup(id, filename)`) uses
    // `responseType: 'blob'` because the response is a
    // binary pg_dump artifact. The response
    // interceptor's success branch used to call
    // `camelizeKeys(blob)` which silently returned a
    // plain `{}` (Blob has no enumerable own
    // properties), so the caller's
    // `URL.createObjectURL({})` threw "Invalid URL"
    // and the download never started. The v0.8.32.2
    // fix added a `responseType` skip list at the
    // top of the success branch.
    const blobBody = new Blob(['hello world'], { type: 'application/octet-stream' })
    queue({
      status: 200,
      data: blobBody,
      responseType: 'blob',
    })

    // The fake adapter returns the Blob verbatim. The
    // response interceptor must NOT touch it (it
    // would convert to `{}` and the Blob would be
    // lost). The call site then does
    // `URL.createObjectURL(data)` and would throw
    // "Invalid URL" pre-fix.
    const response = await freshApi.get('/api/v1/backups/abc/download', {
      responseType: 'blob',
    })
    expect(response.status).toBe(200)
    // The data is still a Blob (not a plain object
    // or a string). Pre-fix, the response
    // interceptor's `camelizeKeys(blob)` returned
    // a plain `{}` and `data` arrived at the call
    // site as `{}` - which then failed at
    // `URL.createObjectURL({})` with "Invalid URL".
    expect(response.data).toBeInstanceOf(Blob)
    // The bytes survived the round-trip. We use
    // `Blob.text()` directly (jsdom's `Response` does
    // not consume Blob bodies in this setup, so the
    // cross-check is at the Blob interface level).
    const body = response.data as Blob
    expect(await body.text()).toBe('hello world')
  })
})

// ----- toApiError coverage (smoke) -----

describe('toApiError', () => {
  it('returns the structured ApiError when the response body has code+message', () => {
    const err = new AxiosError('boom', 'ERR_BAD_REQUEST', undefined, undefined, {
      status: 400,
      data: { code: 'invalid_input', message: 'bad' },
      statusText: 'Bad Request',
      headers: {},
      config: {} as never,
    } as never)
    const apiErr = toApiError(err)
    expect(apiErr.code).toBe('invalid_input')
    expect(apiErr.message).toBe('bad')
  })

  it('falls back to a generic http_error when the body is not structured', () => {
    const err = new AxiosError('boom', 'ERR_BAD_REQUEST', undefined, undefined, {
      status: 500,
      data: 'plain text',
      statusText: 'Internal Server Error',
      headers: {},
      config: {} as never,
    } as never)
    const apiErr = toApiError(err)
    expect(apiErr.code).toBe('http_error')
    expect(apiErr.message).toBe('boom')
  })
})
