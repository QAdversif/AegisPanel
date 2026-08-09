// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Subscription service. Wraps the /api/v1/sub/* and
// the rotated top-level /<sub_path>/sub/* endpoints.
// v0.1.0 only renders a preview (the operator pastes
// a sub_token to see the rendered payload); the
// per-user CRUD lands with the user module in v0.2.
//
// v0.8.x: the per-user preview was previously
// represented by a `fetchSubscriptionForUser` helper
// that called a non-existent backend endpoint
// (`GET /api/v1/users/{id}/sub`). The endpoint
// was never implemented — the admin preview path
// uses the same `/api/v1/sub/{token}` route with
// the per-user `subToken`. The dead helper is
// removed; the UsersView now constructs the
// operator-facing URL from the panel's
// `window.location.origin` + the active `sub_path`
// (via `getActivePanelPath()`) + the user's
// `subToken` and passes that token to
// `fetchSubscription` for the in-dialog preview.

import { api } from '../client'

export interface RenderedSubscription {
  /** The wire format that was requested. */
  format: 'sing-box' | 'clash' | 'base64' | 'html'
  /** The rendered payload, as a UTF-8 string. */
  body: string
  /** A QR-encoded data URL, when the format is
   * suitable for QR (sing-box / clash / base64).
   */
  qrDataUrl?: string
}

export async function fetchSubscription(
  token: string,
  format: RenderedSubscription['format'] = 'sing-box',
): Promise<RenderedSubscription> {
  const { data } = await api.get<RenderedSubscription>(
    `/api/v1/sub/${encodeURIComponent(token)}`,
    { params: { format } },
  )
  return data
}

