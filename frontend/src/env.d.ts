// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Vite client types — gives `import.meta.env.VITE_*` proper
// typing in `.ts` and `.vue` files across the project. This
// is the standard `create-vue` scaffold file; without it,
// `import.meta.env` is `unknown` and `vue-tsc --noEmit` errors
// on every `VITE_*` access.
/// <reference types="vite/client" />
