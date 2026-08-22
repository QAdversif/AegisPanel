// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vitest configuration for the Aegis admin UI.
//
// Two test runners coexist in `frontend/`:
//
//   1. **Vitest** (this config) - jsdom, fast
//      unit tests for Vue components + pure
//      Node tests for static contracts. The
//      `npm run test` script runs this. Files
//      match `**/*.{test,spec}.ts` under `src/`.
//
//   2. **Playwright** (`playwright.config.ts`)
//      - chromium, real browser E2E tests for
//      the admin UI. The `npm run e2e` script
//      runs this. Files match `**/*.spec.ts`
//      under `e2e/`.
//
// The `exclude` below scopes vitest to `src/`
// and keeps the Playwright `e2e/` files out of
// the unit-test discovery (vitest's default glob
// matches `**/*.spec.ts` and would otherwise try
// to run Playwright tests through @vue/test-utils,
// which doesn't understand `page` / Playwright's
// `test()` API).
//
// We re-use the Vite plugins + alias from
// `vite.config.ts` so the .vue files compile and
// the `@/...` path alias resolves. Without this,
// every test that imports a component (or imports
// from `@/api/services` which uses `@/...`) would
// fail with "Failed to resolve import" errors.

import { fileURLToPath, URL } from 'node:url'
import { defineConfig, mergeConfig } from 'vitest/config'
import viteConfig from './vite.config'

export default mergeConfig(
  viteConfig,
  defineConfig({
    test: {
      include: ['src/**/*.{test,spec}.ts'],
      exclude: [
        'node_modules/**',
        'dist/**',
        // Playwright spec files. vitest would
        // otherwise try to run them through
        // @vue/test-utils, which doesn't
        // understand the @playwright/test API.
        'e2e/**',
        'playwright.config.ts',
        'playwright/**',
      ],
    },
  }),
)
