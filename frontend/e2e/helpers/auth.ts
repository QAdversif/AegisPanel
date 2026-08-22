// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Auth helper for Playwright E2E tests. Logs in
// as the admin user ONCE per test worker via the
// UI login form (NOT direct API call), so the
// Pinia auth store's in-memory `accessToken` is
// populated and the SPA treats the test as
// authenticated on every subsequent navigation.
//
// Why UI login instead of direct API: the v0.8.14+
// auth flow stores the access token ONLY in Pinia
// (in-memory), NOT in localStorage. A direct API
// login sets the HttpOnly refresh cookie + returns
// the access token in the response body, but the
// SPA's auth store stays empty and the next page
// navigation redirects to /login. The UI form
// submission is what calls `authStore.setAccessToken`
// from the response.
//
// Usage in a spec file:
//
//   import { test, expect } from '@playwright/test'
//   import { loginAsAdmin } from './helpers/auth'
//
//   test('can navigate to InboundsView', async ({ page }) => {
//     await loginAsAdmin(page)
//     await page.goto('/inbounds')
//     expect(page).toHaveURL(/\/inbounds$/)
//   })
//
// The helper does NOT expose a storageState —
// each test calls `loginAsAdmin(page)` at the
// top. Caching across tests is done by Playwright's
// `test.use({ storageState })` if you need it
// (the storageState here would have the refresh
// cookie + whatever else, but the in-memory
// access token wouldn't survive anyway).

import { type Page, expect } from '@playwright/test'
import { E2E_CREDS } from '../../playwright.config'

const LOGIN_PATH = 'api/v1/auth/login'  // relative (no leading slash) — the baseURL has the path prefix; leading slash would replace the base path

export interface AuthFixture {
  storageState: string
}

/**
 * Logs in via the UI login form. Navigates to the
 * login page, fills username + password, submits,
 * waits for the redirect to the dashboard.
 *
 * Throws if E2E_ADMIN_PASSWORD is empty.
 */
export async function loginAsAdmin(
  page: Page,
  username: string = E2E_CREDS.username,
  password: string = E2E_CREDS.password,
): Promise<void> {
  if (!password) {
    throw new Error(
      'E2E_ADMIN_PASSWORD is not set. The Playwright ' +
        'E2E suite refuses to run with an empty admin ' +
        'password (would silently use a default that may ' +
        'not match the real admin). Set the env var.',
    )
  }
  // Navigate to the UI. We need the path-prefixed
  // baseURL to land on the UI (not the decoy
  // root). `page.goto('login')` is relative to the
  // configured baseURL.
  await page.goto('login')
  // The vee-validate <Form> + <Input> pattern
  // doesn't put `name=` on the underlying <input>.
  // Identify inputs by their autocomplete
  // attribute (set by the LoginView):
  //   - autocomplete="username" → username field
  //   - autocomplete="current-password" → password field
  // These are stable across en / ru translations.
  const usernameInput = page.locator('input[autocomplete="username"]').first()
  const passwordInput = page.locator('input[autocomplete="current-password"]').first()
  // The submit button is type=submit inside the
  // form. Use a Playwright role-based locator
  // (i18n-safe via the button's accessible name).
  const submitButton = page.getByRole('button', {
    name: /sign in|log in|войти|вход/i,
  }).first()
  await expect(usernameInput).toBeVisible({ timeout: 10_000 })
  await expect(passwordInput).toBeVisible()
  await expect(submitButton).toBeVisible()
  await usernameInput.fill(username)
  await passwordInput.fill(password)
  await submitButton.click()
  // Wait for navigation away from /login (to
  // /dashboard or /).
  try {
    await page.waitForURL((url) => !url.pathname.endsWith('/login'), {
      timeout: 15_000,
    })
  } catch (e) {
    process.stderr.write(`[auth] login did not redirect, current url: ${page.url()}\n`)
    throw e
  }
}
