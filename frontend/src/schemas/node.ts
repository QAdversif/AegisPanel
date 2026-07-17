// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Node CRUD schemas. The Go model lives in
// `backend/internal/nodes/`; the zod schemas here
// validate the create / update payloads the admin UI
// sends over the v1 API.
//
// State is a closed set (see node.go) — using zod's
// `z.enum` rather than `z.string` catches typos at
// the form layer.

import { z } from 'zod'

import { hostPortSchema, tagSchema } from './primitives'

export const nodeStateSchema = z.enum([
  'new',
  'online',
  'draining',
  'offline',
  'disabled',
])

/** Fields the operator sets when creating a node. The
 * `id`, `state`, and timestamps are server-side.
 */
export const nodeCreateSchema = z.object({
  name: z.string().min(1).max(64),
  region: z.string().min(1).max(32),
  capacityHint: z.string().max(64).optional(),
  address: hostPortSchema,
  tags: z.array(tagSchema).max(16).optional(),
})

export type NodeCreateInput = z.infer<typeof nodeCreateSchema>

/** Fields the operator may patch. The Go Service
 * rejects unknown fields, so we use `.strict()` to
 * surface typos early.
 */
export const nodeUpdateSchema = nodeCreateSchema.partial().strict()

export type NodeUpdateInput = z.infer<typeof nodeUpdateSchema>
