// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Credential CRUD schemas. Mirrors the wire shapes
// in `frontend/src/api/services/credentials.ts`
// (`CreateCredentialRequest` / `RotateCredentialRequest`)
// and the validation in
// `backend/internal/credentials/service.go` (the
// `ValidationError` rules).
//
// v0.8.2 inlines the rules in the create / rotate
// dialogs; the schema file extracts them so the
// dialogs share a single source of truth.

import { z } from 'zod'

/**
 * Opaque per-protocol credential value. 1..512 chars
 * to bound the request body size; the sing-box
 * renderer is the authoritative validator for
 * per-protocol shape (UUID format for VLESS,
 * password length for HY2 / Trojan, etc.). Empty
 * strings are rejected — the form requires the
 * operator to paste a value before submit.
 */
export const credentialValueSchema = z
  .string()
  .min(1)
  .max(512)

/**
 * UUID string. `frontend/src/schemas/primitives.ts`
 * carries the canonical regex + length check; we
 * re-export it for the credential-specific schemas.
 */
import { uuidSchema } from './primitives'

/**
 * Base object shape. The create and update forms
 * share the `userId`, `inboundId`, and
 * `credentialValue` fields; the rotate form drops
 * the (userId, inboundId) pair (the existing row
 * is fixed).
 */
export const credentialCreateSchema = z.object({
  userId: uuidSchema,
  inboundId: uuidSchema,
  credentialValue: credentialValueSchema,
})

export type CredentialCreateInput = z.infer<typeof credentialCreateSchema>

/**
 * Rotate form. The (userId, inboundId) pair is fixed
 * by the existing row; PATCH only takes the new
 * credentialValue. The Go handler enforces the same
 * shape (`backend/internal/credentials/admin_handler.go`).
 */
export const credentialRotateSchema = z.object({
  credentialValue: credentialValueSchema,
})

export type CredentialRotateInput = z.infer<typeof credentialRotateSchema>
