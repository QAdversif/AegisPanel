// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Primitive zod schemas reused across the entity
// schemas. Importing from here (rather than redefining
// inline) keeps the validation in lock-step with the
// wire types in `@/types`.

import { z } from 'zod'

/**
 * UUID v4 string. The v0.1.0 contract accepts any
 * RFC 4122-shaped UUID; we use a permissive regex
 * rather than the strict v4 bit-pattern check so the
 * panel can hand out v7 ids (already on the roadmap)
 * without a frontend change.
 */
export const uuidSchema = z
  .string()
  .regex(
    /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
    'Invalid UUID',
  )

/** ISO-8601 timestamp. Accepts both `Z` and offset
 * suffixes; the panel always emits UTC `Z`.
 */
export const isoDateTimeSchema = z
  .string()
  .regex(
    /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:?\d{2})$/,
    'Invalid ISO-8601 timestamp',
  )

/**
 * Free-form operator tag. 1-64 chars, lowercase
 * letters / digits / dashes only. Matches the Go
 * validator's `^[a-z0-9-]{1,64}$` rule.
 */
export const tagSchema = z
  .string()
  .min(1)
  .max(64)
  .regex(/^[a-z0-9-]+$/, 'Tag must be lowercase letters, digits, or dashes')

/** "host:port" pair. The Go side does not parse; we
 * keep the check client-side so the form rejects
 * obvious typos before round-tripping.
 */
export const hostPortSchema = z
  .string()
  .regex(
    /^[a-zA-Z0-9._-]+:\d{1,5}$/,
    'Expected "host:port" (e.g. node1.example.com:22)',
  )
  .refine((v) => {
    const port = Number.parseInt(v.split(':')[1] ?? '', 10)
    return port >= 1 && port <= 65535
  }, 'Port must be between 1 and 65535')
