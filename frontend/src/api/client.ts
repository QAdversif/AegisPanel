// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP client wrapping the Aegis panel API.
// Phase 0: a thin axios instance with the panel base URL inferred from
// the current origin (the dev proxy maps /api → :8080).
// Phase 1 will add: bearer-token interceptor, refresh rotation,
// 401 -> refresh + retry, idempotency-key injection for POST/PUT.

import axios from 'axios'

export const api = axios.create({
  baseURL: '/',
  timeout: 15_000,
  headers: {
    Accept: 'application/json',
    'Content-Type': 'application/json',
  },
})

// In Phase 1, interceptors will be added here to:
//   * attach the access token from the auth store
//   * on 401, call /api/v1/auth/refresh, retry the original request once
//   * surface structured errors via a global snackbar
