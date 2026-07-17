// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Panel-path rotation schemas. Mirrors
// `backend/internal/panelcfg/`.

import { z } from 'zod'

/** Operator-supplied rotation request. The Go side
 * generates the row id and the timestamp.
 *
 * `subPath` is the only operator-controlled input.
 * The Go validator enforces `^[a-zA-Z0-9_-]{8,32}$`
 * (must look like a secret, no slashes). We mirror
 * the same shape on the client so the form refuses
 * obviously-broken inputs before they round-trip.
 */
export const panelPathRotateSchema = z.object({
  subPath: z
    .string()
    .min(8, 'Sub-path is too short')
    .max(32, 'Sub-path is too long')
    .regex(
      /^[a-zA-Z0-9_-]+$/,
      'Sub-path may contain only letters, digits, underscores, dashes',
    ),
})

export type PanelPathRotateInput = z.infer<typeof panelPathRotateSchema>
