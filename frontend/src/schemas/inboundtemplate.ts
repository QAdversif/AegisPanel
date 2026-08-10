// SPDX-License-Identifier: AGPL-3.0-or-later
//
// InboundTemplate CRUD schemas. v0.8.13 ships the
// admin surface for the inbound_templates table
// (migration 0021_inbound_templates.sql, the
// `internal/inboundtemplates/` package from
// PR #205). The schemas here mirror
// `backend/internal/inboundtemplates/validate.go`
// and the request shapes in
// `backend/internal/inboundtemplates/handler.go`.
//
// # Why protocol + params are required on Create
//
// The panel's storage of `params` is opaque
// (`map[string]any`, JSONB) — the sing-box
// provider is the authoritative per-protocol
// schema validator. But protocol itself must
// be one of the closed set; the Go-side
// ValidationError rejects anything outside
// the set with field=protocol. The form-side
// zod schema enforces the same set so the
// operator never sends a value the panel
// will reject.
//
// # Update semantics
//
// Partial update (the same PATCH semantics the
// v0.2 CRUD surfaces use). Every field is
// optional; the absence of a key means "leave
// unchanged". `protocol` IS patchable on its
// own (rare, but the Go Service supports it);
// the inbound-tables' `validateProtocol` rule
// still applies.

import { z } from 'zod'

import { protocolSchema } from './inbound'

// Description is a free-form note. The Go side
// caps it at 256 chars (matches the table column
// width); we enforce the same on the form so the
// operator sees the limit before submit.
const descriptionSchema = z
  .string()
  .max(256, 'Description must be 256 characters or fewer')
  .optional()

// params is opaque JSON. The form surfaces a
// textarea; the panel's store accepts whatever
// JSON the user pastes. Per-protocol shape
// validation lives in the sing-box provider
// (downstream of the panel).
const paramsSchema = z.record(z.unknown()).default({})

export const inboundTemplateCreateSchema = z.object({
  name: z.string().min(1).max(64),
  protocol: protocolSchema,
  params: paramsSchema,
  description: descriptionSchema,
})

export type InboundTemplateCreateInput = z.infer<typeof inboundTemplateCreateSchema>

export const inboundTemplateUpdateSchema = z.object({
  name: z.string().min(1).max(64).optional(),
  protocol: protocolSchema.optional(),
  params: paramsSchema.optional(),
  description: descriptionSchema,
})

export type InboundTemplateUpdateInput = z.infer<typeof inboundTemplateUpdateSchema>

// InboundTemplate is the full row shape the
// GET /api/v1/inbound-templates/{id} response
// returns. The same shape is used for each item
// in the list response. Mirrors the Go-side
// `InboundTemplate` struct one-to-one
// (camelCase after the json-tag normalisation
// the v0.2 wire-format fix introduced).
export interface InboundTemplate {
  id: string
  name: string
  protocol: z.infer<typeof protocolSchema>
  params: Record<string, unknown>
  description?: string | null
  createdAt: string
  updatedAt: string
}

// listInboundTemplatesResponse is the unwrapped
// list shape (the HTTP envelope has a
// `{templates: [...]}` wrapper; services strip
// it before returning to callers).
export interface InboundTemplateListResponse {
  templates: InboundTemplate[]
}

// The Create response returns the full row
// (with server-assigned id + timestamps), so
// callers can immediately show the new template
// in the table without a second GET.
export type InboundTemplateCreateResponse = InboundTemplate
