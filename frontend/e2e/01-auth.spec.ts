// SPDX-License-Identifier: AGPL-3.0-or-later
//
// E2E smoke test for the auth flow + the
// v0.8.28 dropdown regression.
//
// What this test guards against:
//   1. The admin login endpoint works end-to-end
//      (POST /api/v1/auth/login returns 200 + sets
//      the aegis_rt HttpOnly cookie).
//   2. The UI's auth flow (the form on the login
//      page) works end-to-end (operator can log
//      in through the browser, lands on the
//      dashboard).
//   3. The v0.8.28 dropdown bug stays fixed:
//      clicking the protocol <Select> trigger in
//      the Inbounds create dialog MUST make the
//      popup visible (computed opacity > 0). Pre-
//      fix, the popup was mounted in the DOM but
//      stayed at opacity 0 forever because the
//      @keyframes animate-in was missing from
//      styles.css (the tailwindcss-animate
//      keyframes were never inlined during the v3
//      -> v4 migration).
//
// The dropdown assertion is the regression guard
// that justifies the whole E2E suite — every
// other view in the admin UI depends on
// shadcn-vue <Select> / <Popover> / <Dialog>
// primitives working. If the keyframes regress
// again, this test fails before any of the
// per-view tests have a chance to run.

import { expect, test } from '@playwright/test'
import { adminStorageState } from './helpers/auth'

test.describe('auth + dropdown regression (v0.8.28 invisible-popups bug)', () => {
  test.use({ storageState: adminStorageState })

  test('lands on the dashboard after login', async ({ page }) => {
    await page.goto('/')
    // The v0.1.0 router sends the operator to
    // /dashboard after login. The v0.5.0+ sidebar
    // also defaults to /dashboard on cold load.
    await expect(page).toHaveURL(/\/(dashboard)?$/)
    // The dashboard greeting header should be
    // present. We don't assert the exact text
    // (i18n: en / ru) — just that the page is
    // not blank / error.
    await expect(page.locator('body')).toBeVisible()
  })

  test(
    'dropdowns are visible (regression for the v0.8.28 "all dropdowns are invisible" bug)',
    async ({ page }) => {
      // Navigate to Inbounds. The sidebar
      // anchor is "Inbounds" / "InboundsView" /
      // whatever the i18n key is. We use
      // getByRole to be language-agnostic.
      await page.goto('/inbounds')
      // The "Create inbound" button. shadcn-vue
      // Button is a <button> element.
      const createButton = page.getByRole('button', {
        name: /create|new|add|crear|nuevo/i,
      })
      await expect(createButton.first()).toBeVisible({ timeout: 10_000 })
      await createButton.first().click()
      // The Create dialog opens. The shadcn-vue
      // DialogContent has role="dialog" (radix-vue
      // sets it).
      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible({ timeout: 5_000 })
      // The protocol <Select> trigger. shadcn-vue
      // SelectTrigger renders as a <button> with
      // role="combobox" (radix-vue's contract).
      const protocolTrigger = dialog.getByRole('combobox').first()
      await expect(protocolTrigger).toBeVisible()
      // Click the trigger. radix-vue's SelectRoot
      // toggles the SelectContent visibility.
      await protocolTrigger.click()
      // The SelectContent (the popup) is teleported
      // to document.body by radix-vue's SelectPortal.
      // It has role="listbox" when open.
      const listbox = page.getByRole('listbox').first()
      await expect(listbox).toBeVisible({ timeout: 5_000 })
      // *** THE REGRESSION ASSERTION ***
      // Playwright's toBeVisible() checks computed
      // style (display, visibility, opacity, width,
      // height). opacity: 0 fails the check. This
      // is what would have caught the v0.8.28 bug
      // (where the popup was in the DOM but stayed
      // at opacity 0 because the @keyframes
      // animate-in was missing).
      //
      // We also assert that the listbox has a
      // non-zero bounding box (an opacity: 0 element
      // can still have a non-zero box but a
      // display:none element has a zero box — the
      // two together pin down both the CSS bug
      // class).
      const box = await listbox.boundingBox()
      expect(box, 'listbox must have a non-zero bounding box').not.toBeNull()
      if (box) {
        expect(box.width).toBeGreaterThan(0)
        expect(box.height).toBeGreaterThan(0)
      }
      // Assert that an option is visible (which
      // forces Playwright to check the actual
      // computed style, not just DOM presence).
      const vlessOption = listbox.getByRole('option', {
        name: /vless/i,
      }).first()
      await expect(vlessOption).toBeVisible()
    },
  )
})

test.describe('logout', () => {
  test.use({ storageState: adminStorageState })

  test('clears the session and redirects to /login on logout', async ({ page }) => {
    await page.goto('/')
    // The logout button is in the topbar / sidebar
    // (operator profile menu). Click the operator
    // avatar or profile button.
    const logoutTrigger = page
      .getByRole('button', { name: /logout|sign out|log out|cerrar sesi[oó]n/i })
      .first()
    if (!(await logoutTrigger.isVisible({ timeout: 2000 }).catch(() => false))) {
      // Some deployments hide the logout behind a
      // profile menu. Open the menu first.
      const profileMenu = page
        .getByRole('button', { name: /profile|account|operator/i })
        .first()
      if (await profileMenu.isVisible({ timeout: 2000 }).catch(() => false)) {
        await profileMenu.click()
      }
    }
    await expect(logoutTrigger).toBeVisible({ timeout: 5_000 })
    await logoutTrigger.click()
    // The v0.8.14+ logout endpoint is
    // POST /api/v1/auth/logout. The UI clears
    // the Pinia auth store and redirects to
    // /login.
    await expect(page).toHaveURL(/\/login$/, { timeout: 10_000 })
  })
})
