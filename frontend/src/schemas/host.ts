// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Host (bundle of Endpoints) CRUD schemas. Mirrors
// `backend/internal/hosts/host.go` and the v3 model in
// ARCHITECTURE.md §10.
//
// The two structural rules (direct = 1 endpoint,
// balancer = ≥2 + Balancer) are enforced in
// `hostCreateSchema.superRefine` because they are
// cross-field invariants; zod's per-field validators
// cannot express them.

import { z } from 'zod'

import { tagSchema, uuidSchema } from './primitives'

export const hostTypeSchema = z.enum(['direct', 'balancer'])

export const balancerStrategySchema = z.enum([
  'round_robin',
  'least_loaded',
  'random',
  'least_ping',
  'urltest',
])

export const userStatusSchema = z.enum([
  'active',
  'on_hold',
  'expired',
  'limited',
  'disabled',
])

export const endpointSchema = z.object({
  id: uuidSchema.optional(),
  nodeId: uuidSchema,
  inboundId: uuidSchema,
  protocol: z.enum(['vless', 'hysteria2', 'shadowsocks', 'trojan']),
  weight: z.number().int().min(1).max(1000).default(1),
  address: z.array(z.string().min(1)).max(8).optional(),
  port: z.number().int().min(1).max(65535).optional(),
  sni: z.array(z.string().min(1)).max(8).optional(),
  host: z.array(z.string().min(1)).max(8).optional(),
  path: z.string().max(256).optional(),
  downloadHostId: uuidSchema.optional(),
})

export type EndpointInput = z.infer<typeof endpointSchema>

export const balancerSchema = z.object({
  strategy: balancerStrategySchema,
  healthcheckUrl: z.string().url().optional(),
  healthcheckIntervalSec: z.number().int().min(1).max(3600).optional(),
  failoverEndpointIds: z.array(uuidSchema).max(16).optional(),
})

export type BalancerInput = z.infer<typeof balancerSchema>

/**
 * Base object shape. We keep it as a separate schema
 * so `hostUpdateSchema` can use `.partial().strict()`
 * without going through a `ZodEffects` (which
 * `.superRefine` returns) — ZodEffects has no
 * `.partial()` method.
 */
const baseHostObjectSchema = z.object({
  remark: z.string().min(1).max(64),
  displayName: z.string().max(128).optional(),
  enabled: z.boolean().default(true),
  priority: z.number().int().min(0).max(1000).default(50),
  statusFilter: z.array(userStatusSchema).max(5).optional(),
  country: z.string().length(2).optional(),
  city: z.string().max(64).optional(),
  tags: z.array(tagSchema).max(16).optional(),
  type: hostTypeSchema,
  endpoints: z.array(endpointSchema).min(1).max(32),
  balancer: balancerSchema.optional(),
})

export const hostCreateSchema = baseHostObjectSchema.superRefine((data, ctx) => {
  if (data.type === 'direct' && data.endpoints.length !== 1) {
    ctx.addIssue({
      code: z.ZodIssueCode.custom,
      message: 'Direct hosts must have exactly one endpoint',
      path: ['endpoints'],
    })
  }
  if (data.type === 'balancer') {
    if (data.endpoints.length < 2) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: 'Balancer hosts must have at least two endpoints',
        path: ['endpoints'],
      })
    }
    if (!data.balancer) {
      ctx.addIssue({
        code: z.ZodIssueCode.custom,
        message: 'Balancer hosts require a balancer strategy',
        path: ['balancer'],
      })
    }
  }
})

export type HostCreateInput = z.infer<typeof hostCreateSchema>

export const hostUpdateSchema = baseHostObjectSchema.partial().strict()

export type HostUpdateInput = z.infer<typeof hostUpdateSchema>
