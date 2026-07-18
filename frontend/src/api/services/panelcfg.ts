// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Panelcfg service. Wraps /api/v1/panelcfg.
// v0.2.0 surface:
//
//   - GET  /             -> current active sub_path
//   - POST /rotate       -> random rotation
//   - POST /rotate-to    -> explicit rotation
//   - POST /reset        -> back to default (empty)
//
// All endpoints require the `admin` scope on the
// caller's access token. The axios client adds the
// bearer header + 401-refresh transparently; this
// file only owns the wire shapes.

import type { PanelPathConfig } from '@/types'

import { api } from './../client'

export interface RotateRequest {
  /** Optional grace window (seconds) during which
   * the OLD sub_path still serves requests. The
   * 3X-UI convention is "no grace" (the old path
   * stops working immediately); 24h is the common
   * alternative. The server caps at 3600 (1h).
   */
  graceWindowSeconds?: number
}

export interface RotateToRequest extends RotateRequest {
  /** The new sub_path. 4-64 chars, [a-z0-9-] charset. */
  subPath: string
}

export async function getActivePanelPath(): Promise<PanelPathConfig> {
  const { data } = await api.get<PanelPathConfig>('/api/v1/panelcfg/')
  return data
}

export async function rotatePanelPath(req: RotateRequest = {}): Promise<PanelPathConfig> {
  const { data } = await api.post<PanelPathConfig>('/api/v1/panelcfg/rotate', req)
  return data
}

export async function rotatePanelPathTo(req: RotateToRequest): Promise<PanelPathConfig> {
  const { data } = await api.post<PanelPathConfig>('/api/v1/panelcfg/rotate-to', req)
  return data
}

export async function resetPanelPath(): Promise<PanelPathConfig> {
  const { data } = await api.post<PanelPathConfig>('/api/v1/panelcfg/reset', {})
  return data
}
