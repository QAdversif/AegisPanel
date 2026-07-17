// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Inbound CRUD schemas. Mirrors
// `backend/internal/inbounds/inbound.go`.

import { z } from 'zod'

import { tagSchema, uuidSchema } from './primitives'

export const protocolSchema = z.enum([
  'vless',
  'hysteria2',
  'shadowsocks',
  'trojan',
])

const portSchema = z
  .number()
  .int()
  .min(1)
  .max(65535, 'Port must be between 1 and 65535')

/** Listen port. Defaults to `::` (IPv6 wildcard) on
 * the Go side; the form pre-fills the same value.
 */
const listenSchema = z
  .string()
  .min(1)
  .max(64)
  .default('::')
  .refine(
    (v) => v === '::' || /^\d{1,3}(\.\d{1,3}){3}$/.test(v) || /^[0-9a-f:]+$/i.test(v),
    'Listen must be a wildcard (:: / 0.0.0.0) or an IP literal',
  )

export const inboundCreateSchema = z.object({
  nodeId: uuidSchema,
  name: z.string().min(1).max(64),
  protocol: protocolSchema,
  listen: listenSchema,
  listenPort: portSchema,
  listenPorts: z.array(portSchema).max(16).optional(),
  enabled: z.boolean().default(true),
  tags: z.array(tagSchema).max(16).optional(),
  params: z.record(z.unknown()).optional(),
})

export type InboundCreateInput = z.infer<typeof inboundCreateSchema>

export const inboundUpdateSchema = inboundCreateSchema.partial().strict()

export type InboundUpdateInput = z.infer<typeof inboundUpdateSchema>
