// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Audits service. Wraps /api/v1/audits (admin read API).
// v0.2.0 surface:
//
//   - GET /       -> list entries (with optional filters)
//   - GET /{id}   -> single entry (with full before/after)
//
// The write path is internal — the in-handler
// `audits.Record(...)` call lands in v0.3+ for the
// nodes / hosts / inbounds / users / panelcfg
// mutating handlers. v0.2.0 ships the read surface
// + the change-password trigger.

import { api } from './../client'

export interface AuditListFilters {
  actorId?: string
  action?: string
  resourceType?: string
  resourceId?: string
  /** ISO-8601 timestamp. Inclusive lower bound on `createdAt`. */
  since?: string
  /** ISO-8601 timestamp. Inclusive upper bound on `createdAt`. */
  until?: string
  /** Max entries to return. Server clamps to 1..1000. */
  limit?: number
}

export async function listAudits(filters: AuditListFilters = {}): Promise<
  Array<{
    id: string
    actorId?: string
    actorUsername?: string
    action: string
    resourceType: string
    resourceId?: string
    ip?: string
    userAgent?: string
    createdAt: string
  }>
> {
  // Build the query string. Only include keys with
  // a value — empty / null filters would be a
  // confusing no-op for the backend.
  const params = new URLSearchParams()
  if (filters.actorId) params.set('actor_id', filters.actorId)
  if (filters.action) params.set('action', filters.action)
  if (filters.resourceType) params.set('resource_type', filters.resourceType)
  if (filters.resourceId) params.set('resource_id', filters.resourceId)
  if (filters.since) params.set('since', filters.since)
  if (filters.until) params.set('until', filters.until)
  if (filters.limit) params.set('limit', String(filters.limit))
  const qs = params.toString()
  const path = qs ? `/api/v1/audits/?${qs}` : '/api/v1/audits/'
  const { data } = await api.get<{ audits: Array<{
    id: string
    actorId?: string
    actorUsername?: string
    action: string
    resourceType: string
    resourceId?: string
    ip?: string
    userAgent?: string
    createdAt: string
  }> }>(path)
  return data.audits ?? []
}

export async function getAudit(id: string): Promise<{
  id: string
  actorId?: string
  actorUsername?: string
  action: string
  resourceType: string
  resourceId?: string
  before?: unknown
  after?: unknown
  ip?: string
  userAgent?: string
  createdAt: string
} | null> {
  try {
    const { data } = await api.get<{
      id: string
      actorId?: string
      actorUsername?: string
      action: string
      resourceType: string
      resourceId?: string
      before?: unknown
      after?: unknown
      ip?: string
      userAgent?: string
      createdAt: string
    }>(`/api/v1/audits/${encodeURIComponent(id)}`)
    return data
  } catch (error) {
    // 404 collapses to a soft null — the audit
    // table may have been pruned by retention
    // between the list call and the detail call,
    // and the UI should just show "entry gone"
    // rather than an error toast.
    if (
      typeof error === 'object' &&
      error !== null &&
      'response' in error &&
      (error as { response?: { status?: number } }).response?.status === 404
    ) {
      return null
    }
    throw error
  }
}
