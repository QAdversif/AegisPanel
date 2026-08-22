// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Auth helper for Playwright E2E tests. Logs in
// as the admin user once per test worker and
// persists the storageState (cookies + localStorage)
// so subsequent tests start authenticated.
//
// Usage in a spec file:
//
//   import { test, expect } from '@playwright/test'
//   import { adminStorageState } from './helpers/auth'
//
//   test.use({ storageState: adminStorageState })
//
//   test('can navigate to InboundsView', async ({ page }) => {
//     await page.goto('/inbounds')
//     expect(page).toHaveURL(/\/inbounds$/)
//   })
//
// The auth flow follows the v0.8.14 cookie-only
// contract: POST /api/v1/auth/login with
// `{ username, password }` returns a 200 with
// the access-token response body AND sets the
// `aegis_rt` HttpOnly cookie (refresh token).
// Subsequent /api/v1/* requests carry the cookie
// automatically. The access token is also stored
// in the Pinia auth store's localStorage so the
// UI's in-memory auth state hydrates on reload.

import { request, type APIRequestContext, type BrowserContext } from '@playwright/test'
import { E2E_CREDS } from '../playwright.config'

const LOGIN_PATH = '/api/v1/auth/login'

export interface AuthFixture {
  storageState: string
}

/**
 * Logs in as admin via the public API and returns
 * the cookie-bearing storageState JSON. Caches
 * the result in a module-level promise so the
 * login runs once per test worker, not once per
 * test.
 *
 * Throws if E2E_ADMIN_PASSWORD is empty (the
 * auth helper refuses to silently use a default
 * password).
 */
let cachedStorageState: Promise<string> | null = null

export async function getAdminStorageState(
  baseURL = E2E_CREDS.baseURL,
  username = E2E_CREDS.username,
  password = E2E_CREDS.password,
): Promise<string> {
  if (cachedStorageState) return cachedStorageState
  if (!password) {
    throw new Error(
      'E2E_ADMIN_PASSWORD is not set. The Playwright ' +
        'E2E suite refuses to run with an empty admin ' +
        'password (would silently use a default that may ' +
        'not match the real admin). Set the env var to ' +
        'the prod 1Password password or the dev stack ' +
        'fixture password.',
    )
  }
  cachedStorageState = (async (): Promise<string> => {
    const api: APIRequestContext = await request.newContext({
      baseURL,
      storageState: { cookies: [], origins: [] },
    })
    const response = await api.post(LOGIN_PATH, {
      data: { username, password },
      headers: { 'Content-Type': 'application/json' },
    })
    if (!response.ok()) {
      const body = await response.text().catch(() => '<unreadable>')
      throw new Error(
        `admin login failed: ${response.status()} ${response.statusText()}\n` +
          `body: ${body.slice(0, 500)}\n` +
          `baseURL: ${baseURL}`,
      )
    }
    // The aegis_rt HttpOnly cookie + any non-HttpOnly
    // cookies are now on the api.context. Save the
    // whole storage state so the UI's in-memory
    // auth (which reads from localStorage on
    // bootstrap) also hydrates. For the dev stack
    // there's no localStorage entry to save (the
    // UI hydrates from the /me probe which uses
    // the cookie).
    const state = await api.storageState()
    await api.dispose()
    return JSON.stringify(state)
  })()
  return cachedStorageState
}

/**
 * Convenience: a synchronous-ish wrapper that
 * returns the JSON string. Playwright's
 * `test.use({ storageState })` accepts a string
 * (the JSON-serialised state) so this can be
 * called from the module top-level.
 */
export const adminStorageState = getAdminStorageState()

/**
 * Asserts the admin session is alive by hitting
 * /api/v1/auth/me. Throws if the cookie is gone
 * or the user was deleted between login and the
 * test run.
 */
export async function assertAdminLoggedIn(
  context: BrowserContext,
  baseURL = E2E_CREDS.baseURL,
): Promise<void> {
  const response = await context.request.get(`${baseURL}/api/v1/auth/me`)
  if (!response.ok()) {
    throw new Error(
      `admin session check failed: ${response.status()}\n` +
        'cookie may have expired or the admin was deleted',
    )
  }
}
