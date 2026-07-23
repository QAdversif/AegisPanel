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

/** TofuPolicy is the closed set of SSH host-key trust
 * policies the panel accepts (see
 * `backend/internal/bootstrap/state.go`).
 */
export const tofuPolicySchema = z.enum(['reject', 'accept-and-append'])

/** Fields the operator sets when provisioning a node
 * (v0.3.0 BYO Node flow). Mirrors the Go
 * `bootstrap.provisionRequest` struct.
 *
 * The `ssh_private_key` is the only required field;
 * the rest are per-call overrides. The form layer
 * also enforces the `expected_fingerprint` requirement
 * when `tofu_policy === 'reject'` (cross-field rule,
 * not a per-field rule).
 */
export const nodeProvisionSchema = z
  .object({
    ssh_port: z
      .number()
      .int()
      .min(1, 'ssh_port must be 1..65535')
      .max(65535, 'ssh_port must be 1..65535')
      .optional(),
    ssh_user: z.string().max(64).optional(),
    ssh_private_key: z
      .string()
      .min(1, 'ssh_private_key is required (PEM, no passphrase)'),
    tofu_policy: tofuPolicySchema.optional(),
    expected_fingerprint: z.string().max(200).optional(),
  })
  .superRefine((value, ctx) => {
    // The Go handler accepts empty / omitted `tofu_policy`
    // and treats it as `reject`. When the policy is
    // `reject`, the operator must paste the fingerprint
    // so the panel does not silently trust an unknown
    // host key. When the policy is `accept-and-append`,
    // the fingerprint is ignored (the panel records the
    // observed one instead).
    if (
      (value.tofu_policy === undefined || value.tofu_policy === 'reject') &&
      (!value.expected_fingerprint || value.expected_fingerprint.trim() === '')
    ) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        path: ['expected_fingerprint'],
        message:
          'expected_fingerprint is required when tofu_policy is "reject" (the safe default). Either paste the SHA256 fingerprint or switch tofu_policy to "accept-and-append".',
      })
    }
  })

export type NodeProvisionInput = z.infer<typeof nodeProvisionSchema>
