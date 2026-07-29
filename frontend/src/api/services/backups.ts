// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Backups service. Wraps /api/v1/backups (admin
// surface shipped in #120 backend, #121 frontend).
//
// v0.5.0 surface:
//
//   - GET    /          -> list rows (newest first)
//   - POST   /          -> create (202 + row body)
//   - GET    /{id}      -> single row
//   - GET    /{id}/download -> stream gzip bytes
//   - DELETE /{id}      -> delete (204)
//   - POST   /{id}/restore -> restore (202, gated by
//                            AEGIS_BACKUPS_ALLOW_UI_RESTORE)
//
// The `Restore` endpoint is intentionally NOT exposed
// in the v0.5.0 UI: a UI-driven restore is dangerous
// (it drops the panel DB) and the operator's safer
// path is the future `cmd/aegis-pg-restore` CLI
// binary. The endpoint is wired in this service so a
// follow-up PR can surface it behind a confirmation
// dialog without touching the wire format.
//
// All functions rely on the axios response
// interceptor in `api/client.ts` to camelCase the
// snake_case wire format (`created_at` -> `createdAt`,
// `size_bytes` -> `sizeBytes`, …).

import { api } from './../client'
import type { Backup, BackupTrigger } from '@/types'

/** List every backup row, newest first.
 *
 *  Returns an empty array (never null) on success
 *  so the table component can render a "no rows"
 *  state directly. 5xx and 4xx bubble up as
 *  rejections; the caller is expected to wrap the
 *  call in a try/catch and surface a toast. */
export async function listBackups(): Promise<Backup[]> {
  const { data } = await api.get<Backup[] | null>('/api/v1/backups/')
  return Array.isArray(data) ? data : []
}

/** Fetch a single backup row by id. The list path
 *  returns every field we need, so this is currently
 *  used only by the detail dialog if we ever split
 *  the columns. Reserved for v0.5.x. */
export async function getBackup(id: string): Promise<Backup | null> {
  try {
    const { data } = await api.get<Backup>(`/api/v1/backups/${encodeURIComponent(id)}`)
    return data
  } catch (error) {
    // 404 collapses to a soft null — the row may
    // have been pruned by retention between the
    // list call and the detail call, and the UI
    // should just show "entry gone" rather than
    // an error toast.
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

/** Trigger a new backup. The default trigger is
 *  `manual`; pass `scheduled` when the call is
 *  driven by the in-process scheduler (currently
 *  unused from the UI; reserved for the v0.5.x
 *  "schedule now" debug action).
 *
 *  On success the server returns the inserted row
 *  with status=`running`; the caller can poll the
 *  list endpoint to watch the transition to `ok`
 *  or `failed`. The current BackupsView polls every
 *  2s while at least one row is in `running`. */
export async function createBackup(trigger: BackupTrigger = 'manual'): Promise<Backup> {
  const { data } = await api.post<Backup>('/api/v1/backups/', { trigger })
  return data
}

/** Delete a backup row AND its dump file + sidecar.
 *  Idempotent: the panel returns 204 on success
 *  and on a missing-id (the row is already gone).
 *  5xx rejects and propagates to the caller. */
export async function deleteBackup(id: string): Promise<void> {
  await api.delete(`/api/v1/backups/${encodeURIComponent(id)}`)
}

/** Trigger a restore. Gated by the server-side
 *  `AEGIS_BACKUPS_ALLOW_UI_RESTORE` env var; the
 *  default (production) is `false`, in which case
 *  the panel answers 403 and this function
 *  surfaces the error. The CLI binary
 *  (`cmd/aegis-pg-restore`, future PR) bypasses
 *  the HTTP path entirely.
 *
 *  Not used by the v0.5.0 UI. */
export async function restoreBackup(id: string): Promise<void> {
  await api.post(`/api/v1/backups/${encodeURIComponent(id)}/restore`)
}

/** Fetch the dump bytes for a backup and trigger
 *  a browser download. Returns the suggested
 *  filename (`<id>.dump.gz`) so the caller can
 *  show a toast with it. The bearer token is
 *  attached by the axios request interceptor;
 *  we never expose it in the URL. */
export async function downloadBackup(id: string, filename: string): Promise<void> {
  const { data } = await api.get<Blob>(`/api/v1/backups/${encodeURIComponent(id)}/download`, {
    responseType: 'blob',
  })
  // Create an object URL, hand it to an <a>, click,
  // and revoke. This is the standard pattern for
  // "save this response as a file" with axios in
  // the browser — the URL.createObjectURL revoke
  // MUST happen after the click() microtask, or
  // Safari aborts the download.
  const url = URL.createObjectURL(data)
  try {
    const a = document.createElement('a')
    a.href = url
    a.download = filename
    a.style.display = 'none'
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
  } finally {
    // Defer the revoke by a tick so the browser
    // can pick the download up off the URL.
    setTimeout(() => URL.revokeObjectURL(url), 0)
  }
}
