// SPDX-License-Identifier: AGPL-3.0-or-later
//
// User CRUD schemas. Mirrors
// `backend/internal/subscription/subscription.go`.

import { z } from 'zod'

import { uuidSchema } from './primitives'

export const userLifecycleStatusSchema = z.enum([
  'active',
  'grace',
  'disabled',
  'expired',
  'deleted',
])

const bytesSchema = z
  .number()
  .int('Bytes must be a whole number')
  .min(0)
  .max(1024 ** 4) // 1 TiB cap — way past anything sane
  .default(0)

export const userCreateSchema = z.object({
  username: z
    .string()
    .min(3, 'Username is too short')
    .max(32, 'Username is too long')
    .regex(/^[a-z0-9_-]+$/, 'Username: lowercase letters, digits, _ or -'),
  status: userLifecycleStatusSchema.default('active'),
  planId: uuidSchema.optional(),
  expireAt: z.string().datetime().optional(),
  trafficLimitBytes: bytesSchema,
  deviceLimit: z.number().int().min(0).max(64).default(3),
  hostsAllowlist: z.array(uuidSchema).max(256).optional(),
  hostsBlocklist: z.array(uuidSchema).max(256).optional(),
})

export type UserCreateInput = z.infer<typeof userCreateSchema>

export const userUpdateSchema = userCreateSchema.partial().strict()

export type UserUpdateInput = z.infer<typeof userUpdateSchema>
