// SPDX-License-Identifier: AGPL-3.0-or-later
//
// ESLint flat config (ESLint v10).
//
// Migrated from the legacy `.eslintrc.cjs` (ESLint v8)
// as part of the v0.4.0 cleanup batch. The legacy
// config relied on three `extends` chains and one parser
// per language; the flat config imports the same rules
// and combines them in a single array of config
// objects.
//
// Notes:
//
// 1. The `typescript-eslint` umbrella package ships
//    the v8 parser + plugin + flat-config presets in
//    one. We use it instead of the bare
//    `@typescript-eslint/eslint-plugin` because the
//    plugin's `configs.recommended` (legacy-format)
//    is NOT iterable in flat-config land; the
//    `tseslint.configs.recommended` (flat-format)
//    is.
//
// 2. `vue-eslint-parser` runs in front of
//    `tseslint.parser` for `.vue` files. The chain is
//    configured via `languageOptions.parserOptions.parser`
//    in the per-file `files` block below.
//
// 3. `vue/multi-word-component-names` is OFF on
//    purpose: top-level page views (Home, Login,
//    Settings) are intentionally single-word. The
//    rule's rationale is for the user-defined
//    component namespace, not for route-level views.
//
// 4. `no-unused-vars` is OFF in favor of
//    `@typescript-eslint/no-unused-vars`, which
//    understands TS-only constructs and respects the
//    `_`-prefix convention.
//
// 5. `globals` is imported to set `browser` + `es2022`
//    + `node` globals in one go instead of the old
//    `env: { browser: true, es2022: true, node: true }`.

import js from '@eslint/js'
import globals from 'globals'
import tseslint from 'typescript-eslint'
import pluginVue from 'eslint-plugin-vue'

export default [
  {
    ignores: [
      'dist/**',
      'node_modules/**',
      'public/**',
    ],
  },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/recommended'],
  {
    languageOptions: {
      ecmaVersion: 'latest',
      sourceType: 'module',
      globals: {
        ...globals.browser,
        ...globals.node,
        ...globals.es2022,
      },
    },
  },
  {
    files: ['**/*.vue'],
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
        extraFileExtensions: ['.vue'],
        sourceType: 'module',
      },
    },
  },
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tseslint.parser,
    },
  },
  {
    rules: {
      'vue/multi-word-component-names': 'off',
      '@typescript-eslint/no-unused-vars': [
        'warn',
        {
          argsIgnorePattern: '^_',
          varsIgnorePattern: '^_',
        },
      ],
      'no-unused-vars': 'off',
    },
  },
]
