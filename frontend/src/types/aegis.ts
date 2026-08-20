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
export type ISODateTime = string;

/**
 * A bare UUID v4 string. We do not brand the type
 * because v0.1.0 does not have a single hand-off
 * point that benefits from a stronger guarantee.
 */
export type UUID = string;

// ---------------------------------------------------------------------------
// Nodes
// ---------------------------------------------------------------------------

/** Lifecycle state of a Node. The set is closed
 * (see `backend/internal/nodes/node.go`).
 */
export type NodeState = "new" | "online" | "draining" | "offline" | "disabled";

export interface Node {
  id: UUID;
  name: string;
  region: string;
  state: NodeState;
  capacityHint?: string;
  address: string;
  tags?: string[];
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// NodeCreateRequest is the v0.1.0 wire shape
// for the create endpoint. PATCH /api/v1/nodes/{id}
// takes the same fields, all optional
// (a no-op PATCH is a legal call).
export interface NodeCreateRequest {
  name: string;
  region: string;
  address: string;
  capacityHint?: string;
  tags?: string[];
}

// ---------------------------------------------------------------------------
// Node v0.8.x wire shapes (provision + rotate-panel-key)
// ---------------------------------------------------------------------------
//
// The v0.3.0 provision flow has its own request /
// response shapes (the v0.8.1 wire format
// collapsed the auth fields into a two-field
// XOR ssh_private_key / ssh_password). The
// v0.8.4 rotate-panel-key flow is the HTTP
// mirror of the v0.8.3 `aegis admin node
// rotate-panel-key` CLI (PR #184) and uses a
// single-endpoint shape: the operator's
// existing private key is the only required
// field, the response carries the new public
// key line + SHA256 fingerprint so the UI
// can surface them in a "rotation result" card.
//
// All four types live in `@/types` (this file)
// rather than in `api/services/nodes.ts` so the
// rest of the frontend (NodesView, schema
// validators, future "key fingerprint viewer"
// debug surface) can import the wire shape
// without pulling in axios. The services file
// re-exports the request types for callers
// that want "service + type" in one import.

// NodeProvisionRequest is the v0.8.1 wire
// shape. The auth fields are optional at the
// type level; the Go handler enforces the
// XOR (see `internal/bootstrap/handler.go`,
// `TestHandleProvision`). The UI's
// authMethod radio is a local-only
// discriminator; the wire format collapses
// to the two-field XOR. v0.8.4 adds the
// "stored" path: both auth fields are empty
// when the operator selects the panel's own
// stored key (the Go side falls back to the
// encrypted panel key on that path).
//
// The v0.8.4 second copy of this interface
// (the original v0.3.0 definition with
// `ssh_private_key: string` required) is
// below at the v0.3.0 backwards-compat
// section; the v0.8.1+ canonical version is
// this one. The duplicate is kept for the
// codegen stability contract; future
// refactors will collapse the two.
export interface NodeProvisionRequest {
  ssh_private_key?: string;
  ssh_password?: string;
  ssh_port?: number;
  ssh_user?: string;
  tofu_policy?: "reject" | "accept-and-append";
  expected_fingerprint?: string;
}

export interface NodeProvisionResponse {
  node_id: UUID;
  new_state: NodeState;
  install_stage?: string;
  install_error?: string;
  verify_latency?: string;
}

// NodeRotatePanelKeyRequest is the v0.8.4 wire
// shape. Only the operator's existing private
// key is required; ssh_port / ssh_user are
// optional overrides that fall back to the
// panel's service-wide defaults.
export interface NodeRotatePanelKeyRequest {
  ssh_private_key: string;
  ssh_port?: number;
  ssh_user?: string;
}

// NodeRotatePanelKeyResponse is the v0.8.4 200
// body. The UI surfaces the public_key_line
// and fingerprint in a "rotation result" card
// so the operator can verify what is now in
// the node's authorized_keys. node_id is the
// same {id} the URL already had; the field is
// included so the response is self-describing
// for any future bulk-call path.
export interface NodeRotatePanelKeyResponse {
  node_id: UUID;
  public_key_line: string;
  fingerprint: string;
}

// NodeRefreshAgentBearerRequest is the v0.8.7
// request body for
// `POST /api/v1/nodes/{id}/refresh-agent-bearer`.
// The body is optional; the field defaults are
// the service-wide `AEGIS_AGENT_SSH_USER` for
// `ssh_user`, the node's stored `Address` for
// the host + port, and 30s for `timeout`. The
// Go-side handler accepts an empty body; the
// UI's dialog sends `{}` (no fields set) by
// default. A future PR can wire the dialog
// to a real form for the override case.
export interface NodeRefreshAgentBearerRequest {
  ssh_port?: number;
  ssh_user?: string;
}

// NodeRefreshAgentBearerResponse is the v0.8.7
// 200 body. The `bearer` field is the new
// agent bearer (the value the panel will use
// for subsequent `POST /v1/apply` calls to
// the agent); the operator can verify it on
// the node by `cat`-ing /etc/aegis/agent.env.
// The `key_fingerprint` is the SHA-256 of the
// public key derived from the stored private
// key (same string `ssh-keygen -lf` reports),
// so the operator can verify the refresh used
// the key they expect.
export interface NodeRefreshAgentBearerResponse {
  node_id: UUID;
  bearer: string;
  key_fingerprint: string;
}

// NodeStoredKey is the v0.8.5 read-side mirror
// of the v0.8.1 persistent panel SSH key
// feature. The panel decrypts
// `nodes.ssh_private_key_ciphertext` via the
// age envelope, derives the public-key line
// + SHA-256 fingerprint, and returns the
// public surface. The private key never
// leaves the panel process.
//
// `has_stored_key` is false for `new` nodes
// that have never been installed via the
// v0.8.1+ path; the UI surfaces a "no stored
// key yet" hint. The `key_updated_at` field
// is the row's `updated_at` — the ciphertext
// column has no independent timestamp, so
// the row-level `updated_at` is the operator's
// "is this the key I think it is" sanity
// check.
//
// The OpenSSH key comment
// (`aegis-panel@node-<nodeName>`) is NOT a
// separate field — it is the third
// whitespace-separated token of
// `public_key_line` (the OpenSSH
// authorized_keys format).
export interface NodeStoredKey {
  has_stored_key: boolean;
  public_key_line?: string;
  fingerprint?: string;
  algorithm?: string;
  key_updated_at?: ISODateTime;
}

// ---------------------------------------------------------------------------
// Inbounds
// ---------------------------------------------------------------------------

/** Protocol family of an Inbound. The set is closed
 * (see `backend/internal/inbounds/inbound.go`).
 */
export type Protocol = "vless" | "hysteria2" | "shadowsocks" | "trojan";

export interface Inbound {
  id: UUID;
  nodeId: UUID;
  name: string;
  protocol: Protocol;
  listen: string;
  listenPort: number;
  listenPorts?: number[];
  enabled: boolean;
  tags?: string[];
  params?: Record<string, unknown>;
  // v0.8.13+: optional FK to an
  // `inbound_templates` row. When non-null,
  // the sing-box renderer reads
  // `template.params` instead of
  // `inbound.params`. The CRUD layer (PR #211)
  // enforces template existence and protocol
  // match; a protocol mismatch returns 400
  // with `field=templateId`. Pre-v0.8.13
  // inbounds have `templateId = undefined`
  // (the v0.8.0-v0.8.12 default; every
  // inbound uses its inline `params`).
  templateId?: UUID | null;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// Inbound templates (v0.8.13+)
// ---------------------------------------------------------------------------
//
// A named, reusable protocol configuration that
// any number of `inbounds` rows on any node can
// reference via the `Inbound.templateId` FK
// (migration 0021_inbound_templates.sql).
// Templates are global (not per-node); the
// same template can be assigned to inbounds
// across multiple nodes.
//
// The wire shape mirrors the Go-side
// `InboundTemplate` struct (PR #205) one-to-one
// after the camelCase normalisation. The
// zod schema in
// `src/schemas/inboundtemplate.ts` is the
// source of truth for the request validation;
// this type is the read-side view.
export interface InboundTemplate {
  id: UUID;
  name: string;
  protocol: Protocol;
  params: Record<string, unknown>;
  description?: string | null;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// Hosts (v3 model: bundle of Endpoints)
// ---------------------------------------------------------------------------

export type HostType = "direct" | "balancer";

export type BalancerStrategy =
  "round_robin" | "least_loaded" | "random" | "least_ping" | "urltest";

export type UserStatus =
  "active" | "on_hold" | "expired" | "limited" | "disabled";

export interface Endpoint {
  id?: UUID;
  nodeId: UUID;
  inboundId: UUID;
  protocol: Protocol;
  weight: number;
  address?: string[];
  port?: number;
  sni?: string[];
  host?: string[];
  path?: string;
  downloadHostId?: UUID;
}

export interface Balancer {
  strategy: BalancerStrategy;
  healthcheckUrl?: string;
  healthcheckIntervalSec?: number;
  failoverEndpointIds?: UUID[];
}

export interface Host {
  id: UUID;
  remark: string;
  displayName?: string;
  type: HostType;
  enabled: boolean;
  priority: number;
  statusFilter?: UserStatus[];
  country?: string;
  city?: string;
  tags?: string[];
  endpoints: Endpoint[];
  balancer?: Balancer;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// Users, Plans, Pools
// ---------------------------------------------------------------------------

/** Lifecycle state of a User. */
export type UserLifecycleStatus =
  "active" | "grace" | "disabled" | "expired" | "deleted";

export type ResetPeriod = "daily" | "weekly" | "monthly" | "never";

export type PoolStrategy = "all" | "round_robin" | "least_loaded" | "geo_aware";

export interface User {
  id: UUID;
  username: string;
  status: UserLifecycleStatus;
  planId?: UUID;
  expireAt?: ISODateTime;
  trafficLimitBytes: number;
  trafficUsedBytes: number;
  deviceLimit: number;
  hostsAllowlist?: UUID[];
  hostsBlocklist?: UUID[];
  subToken: string;
  subTokenRotatedAt?: ISODateTime;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

export interface Plan {
  id: UUID;
  name: string;
  trafficLimitBytes: number;
  // Validity period of a subscription issued on
  // this plan, in nanoseconds. The Go side stores
  // it as a Postgres INTERVAL; the API exposes it
  // as int64 nanoseconds so the UI can render
  // "30 days" / "1 year" / "1 hour" without losing
  // precision. Convert to a human-readable unit
  // at the rendering layer (the PlansView in #134
  // uses Intl.DurationFormat or a tiny helper).
  durationNs: number;
  deviceLimit: number;
  resetPeriod: ResetPeriod;
  priceCents: number;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

export interface Pool {
  id: UUID;
  name: string;
  strategy: PoolStrategy;
  antiaffinity: boolean;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// Panel config (sub-token URL prefix)
// ---------------------------------------------------------------------------

export interface PanelPathConfig {
  id: UUID;
  subPath: string;
  rotatedAt: ISODateTime;
  createdAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// API envelope
// ---------------------------------------------------------------------------

/** Standard error shape the Go panel returns. */
export interface ApiError {
  code: string;
  message: string;
  details?: Record<string, string>;
}

/** Standard list envelope. */
export interface ListResponse<T> {
  items: T[];
  total: number;
}

// ---------------------------------------------------------------------------
// Audit log
// ---------------------------------------------------------------------------

/**
 * One row of the v0.2.0 audit log. The shape mirrors
 * the Go `AuditEntry` struct (camelCase json tags per
 * the v0.2.0 wire-format normalisation). The `before` /
 * `after` fields are elided on the list path and only
 * returned in full on the `/{id}` path; consumers
 * that want the diff should call `getAudit(id)`.
 */
export interface AuditEntry {
  id: string;
  actorId?: string;
  actorUsername?: string;
  action: string;
  resourceType: string;
  resourceId?: string;
  before?: unknown;
  after?: unknown;
  ip?: string;
  userAgent?: string;
  createdAt: ISODateTime;
}

/**
 * Shape of the change-password form. Snake-case
 * field names match the Go json tags so the request
 * body round-trips through the auth handler.
 */
export interface ChangePasswordRequest {
  /** The operator's CURRENT password. Verified to defend
   * against a stolen access token. */
  current_password: string;
  /** The new password to set. Operator-side validation
   * applies (length, complexity). */
  new_password: string;
}

// ---------------------------------------------------------------------------
// Credentials (v0.8.2 admin surface; data layer from v0.8.0)
// ---------------------------------------------------------------------------

/**
 * One row of the `user_inbound_credentials` table.
 * The (user_id, inbound_id) pair is unique. The
 * `credentialValue` is an operator secret (VLESS UUID,
 * Shadowsocks 2022-blake3 password, etc.) — the panel
 * stores it as opaque TEXT; the sing-box renderer is
 * authoritative for per-protocol shape validation.
 *
 * v0.8.2 wires the HTTP surface at /api/v1/credentials/.
 * The data model is in `internal/credentials` + migration
 * 0019 from v0.8.0 (PR #167).
 */
export interface Credential {
  id: UUID;
  userId: UUID;
  inboundId: UUID;
  credentialValue: string;
  createdAt: ISODateTime;
  updatedAt: ISODateTime;
}

// ---------------------------------------------------------------------------
// Node provision (v0.3.0 BYO Node flow)
// ---------------------------------------------------------------------------

/**
 * Trust-on-first-use policy for the SSH host key on
 * first contact with a fresh node. `reject` is the
 * safe default (operator must paste the fingerprint
 * first); `accept-and-append` is the v0.3.0 "first
 * contact" UX where the panel pins the key on
 * connect and reports the fingerprint back.
 */
export type TofuPolicy = "reject" | "accept-and-append";

/**
 * Body of `POST /api/v1/nodes/{id}/provision`. Snake-
 * case field names match the Go json tags so the
 * request body round-trips through the bootstrap
 * handler.
 *
 * v0.8.4: the v0.3.0 canonical "required key" shape
 * is no longer the only one — the v0.8.1 wire format
 * made `ssh_private_key` and `ssh_password` both
 * optional (the `authMethod: 'stored'` path sends
 * an empty auth object so the Go provisioner falls
 * back to the encrypted panel key). The canonical
 * `NodeProvisionRequest` lives earlier in this file
 * (the v0.8.1+ shape). This entry is kept as a
 * `LegacyV030NodeProvisionRequest` alias so the
 * v0.3.0 codegen consumers still type-check; new
 * code should import the canonical interface.
 *
 * @deprecated since v0.8.4 — use NodeProvisionRequest
 * (the v0.8.1+ canonical shape, both auth fields
 * optional). Kept for the codegen stability contract.
 */
export type LegacyV030NodeProvisionRequest = {
  /** Per-call override. Zero/omitted = service-wide default (22). */
  ssh_port?: number;
  /** Per-call override. Empty/omitted = service-wide default (root). */
  ssh_user?: string;
  /** Operator-pasted private key (PEM, no passphrase). Required. */
  ssh_private_key: string;
  tofu_policy?: TofuPolicy;
  /** Required when `tofu_policy === 'reject'`. `SHA256:base64`. */
  expected_fingerprint?: string;
};

/**
 * Response of `POST /api/v1/nodes/{id}/provision`.
 * The UI re-renders the node's state badge from
 * `new_state`; `install_stage` + `install_error` are
 * surfaced for the "retry" button's tooltip.
 */
export interface NodeProvisionResponse {
  node_id: string;
  new_state: NodeState;
  /** Best-effort stage tag from the provisioner. */
  install_stage?: string;
  /** Set when `new_state === 'offline'`. Empty string on success. */
  install_error?: string;
  /** ISO-8601 duration for the systemd is-active poll (e.g. `PT2.5S`). */
  verify_latency?: string;
}

// --- v0.5.0 backups (#120 backend, #121 frontend) ---

/**
 * Who or what initiated the backup.
 *  - `manual`    — operator clicked Create / POSTed with `{"trigger":"manual"}`
 *  - `scheduled` — the in-process scheduler fired on cron match
 */
export type BackupTrigger = "manual" | "scheduled";

/**
 * Lifecycle of a single backup. The state machine is
 * `running -> ok` (success) or `running -> failed`
 * (error; the row is retained for forensics with
 * `error` populated and the partial file deleted).
 */
export type BackupStatus = "running" | "ok" | "failed";

/**
 * One row of the v0.5.0 backup table. Mirrors the
 * Go `backups.Backup` struct (snake_case on the wire
 * is auto-camelCased by the axios response interceptor
 * in `api/client.ts`, so this interface is camelCase
 * even though the API emits snake_case).
 *
 * `sizeBytes` is 0 while the backup is `running`; the
 * row is updated to the real value before the Service
 * marks it `ok`. `error` is empty on success; the
 * `running` state has it empty too.
 */
export interface Backup {
  id: string;
  createdAt: string;
  sizeBytes: number;
  trigger: BackupTrigger;
  status: BackupStatus;
  error?: string;
  schemaVersion: number;
  nodeCount: number;
  userCount: number;
  hostCount: number;
  checksumSha256: string;
  /** Server-side path (relative to the backups root).
   *  Surfaced so the UI can show `<id>.dump.gz` as
   *  the download filename. Empty for in-flight rows. */
  path?: string;
}

/**
 * v0.9.x surface for the /backups/schedule
 * endpoint. Read-only; the operator edits the
 * env var + restarts the panel to apply changes
 * (a POST endpoint for hot-reload is deferred to
 * v0.9.1 per the Tier 1 #3 plan).
 *
 * `cron` is the live 5-field Vixie expression the
 * scheduler matches against (empty string =
 * manual-only mode). `retentionDays` and `maxCount`
 * are the retention policy applied after every
 * Create (and on explicit `Service.Cleanup` calls);
 * 0 on either means "unlimited". `scheduleActive`
 * is true when the scheduler goroutine is running.
 */
export interface BackupSchedule {
  cron: string;
  retentionDays: number;
  maxCount: number;
  scheduleActive: boolean;
}
