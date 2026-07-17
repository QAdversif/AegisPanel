// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Aegis API wire types. These mirror the JSON shape
// the Go backend emits and accepts over the v1 HTTP
// API. The intent is for the rest of the frontend
// code to import from here rather than redefining the
// shapes inline.
//
// Per ADR-0004 + PR-D plan, the types are the source
// of truth for the v0.1.0 contract. The zod schemas
// in `src/schemas/*` validate inputs against the same
// shapes. The Go model in
// `backend/internal/{nodes,hosts,inbounds,subscription,panelcfg}`
// is the system-of-record; the TypeScript here is a
// hand-maintained mirror (a generator is out of scope
// for v0.1.0 — would need an OpenAPI schema first).
//
// Every field is camelCase. Optional fields use the
// `?` operator. UUIDs are bare strings (we do not
// bother with a `Uuid` brand type until v0.2+ when
// the volume of UUID traffic justifies the type-
// safety cost).

// ---------------------------------------------------------------------------
// Shared primitives
// ---------------------------------------------------------------------------

/** ISO-8601 timestamp string. */
export type ISODateTime = string

/**
 * A bare UUID v4 string. We do not brand the type
 * because v0.1.0 does not have a single hand-off
 * point that benefits from a stronger guarantee.
 */
export type UUID = string

// ---------------------------------------------------------------------------
// Nodes
// ---------------------------------------------------------------------------

/** Lifecycle state of a Node. The set is closed
 * (see `backend/internal/nodes/node.go`).
 */
export type NodeState = 'new' | 'online' | 'draining' | 'offline' | 'disabled'

export interface Node {
  id: UUID
  name: string
  region: string
  state: NodeState
  capacityHint?: string
  address: string
  tags?: string[]
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

// ---------------------------------------------------------------------------
// Inbounds
// ---------------------------------------------------------------------------

/** Protocol family of an Inbound. The set is closed
 * (see `backend/internal/inbounds/inbound.go`).
 */
export type Protocol = 'vless' | 'hysteria2' | 'shadowsocks' | 'trojan'

export interface Inbound {
  id: UUID
  nodeId: UUID
  name: string
  protocol: Protocol
  listen: string
  listenPort: number
  listenPorts?: number[]
  enabled: boolean
  tags?: string[]
  params?: Record<string, unknown>
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

// ---------------------------------------------------------------------------
// Hosts (v3 model: bundle of Endpoints)
// ---------------------------------------------------------------------------

export type HostType = 'direct' | 'balancer'

export type BalancerStrategy =
  | 'round_robin'
  | 'least_loaded'
  | 'random'
  | 'least_ping'
  | 'urltest'

export type UserStatus =
  | 'active'
  | 'on_hold'
  | 'expired'
  | 'limited'
  | 'disabled'

export interface Endpoint {
  id?: UUID
  nodeId: UUID
  inboundId: UUID
  protocol: Protocol
  weight: number
  address?: string[]
  port?: number
  sni?: string[]
  host?: string[]
  path?: string
  downloadHostId?: UUID
}

export interface Balancer {
  strategy: BalancerStrategy
  healthcheckUrl?: string
  healthcheckIntervalSec?: number
  failoverEndpointIds?: UUID[]
}

export interface Host {
  id: UUID
  remark: string
  displayName?: string
  type: HostType
  enabled: boolean
  priority: number
  statusFilter?: UserStatus[]
  country?: string
  city?: string
  tags?: string[]
  endpoints: Endpoint[]
  balancer?: Balancer
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

// ---------------------------------------------------------------------------
// Users, Plans, Pools
// ---------------------------------------------------------------------------

/** Lifecycle state of a User. */
export type UserLifecycleStatus =
  | 'active'
  | 'grace'
  | 'disabled'
  | 'expired'
  | 'deleted'

export type ResetPeriod = 'daily' | 'weekly' | 'monthly' | 'never'

export type PoolStrategy = 'all' | 'round_robin' | 'least_loaded' | 'geo_aware'

export interface User {
  id: UUID
  username: string
  status: UserLifecycleStatus
  planId?: UUID
  expireAt?: ISODateTime
  trafficLimitBytes: number
  trafficUsedBytes: number
  deviceLimit: number
  hostsAllowlist?: UUID[]
  hostsBlocklist?: UUID[]
  subToken: string
  subTokenRotatedAt?: ISODateTime
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

export interface Plan {
  id: UUID
  name: string
  trafficLimitBytes: number
  durationDays: number
  deviceLimit: number
  resetPeriod: ResetPeriod
  priceCents: number
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

export interface Pool {
  id: UUID
  name: string
  strategy: PoolStrategy
  antiaffinity: boolean
  createdAt: ISODateTime
  updatedAt: ISODateTime
}

// ---------------------------------------------------------------------------
// Panel config (sub-token URL prefix)
// ---------------------------------------------------------------------------

export interface PanelPathConfig {
  id: UUID
  subPath: string
  rotatedAt: ISODateTime
  createdAt: ISODateTime
}

// ---------------------------------------------------------------------------
// API envelope
// ---------------------------------------------------------------------------

/** Standard error shape the Go panel returns. */
export interface ApiError {
  code: string
  message: string
  details?: Record<string, string>
}

/** Standard list envelope. */
export interface ListResponse<T> {
  items: T[]
  total: number
}
