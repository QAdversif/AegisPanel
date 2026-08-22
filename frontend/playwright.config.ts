// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Playwright E2E test configuration for the Aegis
// admin UI. Runs against either:
//   - the local vite dev server (default, baseURL
//     http://localhost:5173) — for fast iteration
//     with a docker-compose dev stack + the Go
//     panel running outside docker
//   - the prod panel (BASE_URL=https://<prod-host>/
//     <sub-path>/) — for the v0.8.28 dropdowns
//     regression check on the live deploy
//
// Test artefacts (HTML reports, traces, screenshots)
// are written to `playwright-report/` and
// `test-results/` at the repo root. Both are
// .gitignored.
//
// Chromium is the only browser configured. The
// CI workflow installs the chromium browser
// binary via `npx playwright install --with-deps
// chromium` before running. WebKit / Firefox are
// not enabled because the UI is intended for
// Chromium-class browsers only (it's an admin
// panel, the operator runs it on whatever
// browser they happen to have installed).
//
// All test files live in `frontend/e2e/` and run
// in serial mode (single browser context) to keep
// the test data setup deterministic. The auth
// helper in `e2e/helpers/auth.ts` saves a
// per-worker `storageState` so each test starts
// logged in as `admin` without re-doing the
// login flow.

import { defineConfig, devices } from '@playwright/test'

const BASE_URL = process.env.E2E_BASE_URL ?? 'http://localhost:5173'
const ADMIN_USERNAME = process.env.E2E_ADMIN_USERNAME ?? 'admin'
// No default for the password. Tests that need it
// MUST set E2E_ADMIN_PASSWORD. The `test:e2e`
// npm script will refuse to run if the env var is
// empty (the auth helper throws on empty).
//
// For the prod panel, set this to the 28-char
// secrets.choice password from the operator's
// 1Password entry. For the dev stack, the
// `make docker-dev` seed creates an admin with
// the dev-mode fixture password — set the env
// var to that.

export default defineConfig({
  testDir: './e2e',
  testMatch: '**/*.spec.ts',
  // One test at a time per worker. The test
  // suite is small enough that parallelism would
  // just add flake. If we grow to 100+ tests we
  // can revisit (each worker would get its own
  // login session via the per-worker storageState
  // caching in e2e/helpers/auth.ts).
  fullyParallel: false,
  workers: 1,
  retries: 0,
  reporter: process.env.CI ? 'github' : 'list',
  use: {
    baseURL: BASE_URL,
    trace: 'on-first-retry',
    screenshot: 'only-on-failure',
    video: 'retain-on-failure',
    // The cookie-based refresh-token flow (v0.8.14+)
    // means a logged-in browser context persists
    // across page reloads. We still use the
    // per-test storageState pattern (see auth.ts)
    // so the test file can declare its own
    // dependency on being-logged-in.
  },
  projects: [
    {
      name: 'chromium',
      use: { ...devices['Desktop Chrome'] },
    },
  ],
  // Don't fail the suite on `webServer` startup
  // timeout if the dev server is already running
  // outside the test (e.g. `pnpm dev` in another
  // terminal). The reuseExistingServer flag
  // handles that.
  webServer: process.env.E2E_NO_WEBSERVER
    ? undefined
    : {
        // Re-use an already-running dev server if
        // one is listening on the BASE_URL host:port.
        // The dev workflow is:
        //   1. `make docker-dev` in one terminal
        //      (starts postgres/redis/nats/caddy/minio)
        //   2. `pnpm backend` in another terminal
        //      (starts the Go panel on :8080)
        //   3. `pnpm e2e` in a third terminal
        //      (vite auto-starts on :5173 if not
        //      already running, otherwise reuses)
        command: 'pnpm dev',
        url: BASE_URL,
        reuseExistingServer: true,
        timeout: 120_000,
        stdout: 'pipe',
        stderr: 'pipe',
      },
})

// Export the resolved creds so the auth helper
// can import them without re-parsing env vars.
export const E2E_CREDS = {
  baseURL: BASE_URL,
  username: ADMIN_USERNAME,
  password: process.env.E2E_ADMIN_PASSWORD ?? '',
} as const
