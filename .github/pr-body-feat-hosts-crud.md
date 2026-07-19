feat(hosts): create / edit dialog with nested endpoint editor (PR-H)

Third sub-PR of v0.2.0-mvp-agent. Closes the
last big CRUD gap on the Hosts surface: the
v0.1.0 list + delete view is now a full CRUD
backed by the existing /api/v1/hosts backend.
This is the most complex form in the panel
because each Host is a bundle of (potentially
many) Endpoints, and the cross-field
"direct=1 endpoint, balancer≥2 + strategy"
rules land in the same form via a zod
superRefine.

## Backend

The backend was already complete (PR #33 +
PR #34): `POST /`, `PUT /{id}`, `DELETE /{id}`,
`GET /`, `GET /{id}`. v0.1.0 left the wire
format in snake_case even though the
`src/types/aegis.ts` mirror was camelCase, so
the existing v0.1.0 list view was actually
displaying `undefined` for every field except
`remark` and `type`. PR-H fixes the wire format
to camelCase end-to-end.

* `backend/internal/hosts/host.go` —
  switches every JSON tag on `Host`,
  `Endpoint`, and `Balancer` from snake_case
  to camelCase (`display_name` →
  `displayName`, `node_id` → `nodeId`,
  `inbound_id` → `inboundId`,
  `download_host_id` → `downloadHostId`,
  `status_filter` → `statusFilter`,
  `healthcheck_url` → `healthcheckUrl`,
  `healthcheck_interval_sec` →
  `healthcheckIntervalSec`,
  `failover_endpoint_ids` →
  `failoverEndpointIds`). The `created_at` /
  `updated_at` fields follow the same pattern
  → `createdAt` / `updatedAt`. The Go struct
  field names (PascalCase) stay unchanged.

* `backend/internal/hosts/handler.go` — the
  four request shapes (`createRequest`,
  `createEndpoint`, `updateRequest`,
  `updateEndpoint`) get the matching camelCase
  JSON tags. The Go field names are unchanged
  so the handler logic and the unit tests are
  untouched (tests construct `createRequest`
  literals with Go field names).

* `internal/router/router.go` — no change.
* `cmd/aegis/main.go` — no change.

The fix mirrors PR-G's User/Plan/Pool json-tag
patch: bring the Go wire format in line with
the camelCase source-of-truth in
`src/types/aegis.ts`. The other entities
(Nodes, Inbounds, Subscriptions, Panelcfg)
will get the same treatment in their own
sub-PRs (PR-I for Inbounds, etc.). The
cumulative effect is that PR-H removes one
piece of the v0.1.0 "rendered-as-undefined"
bug; the rest follows.

## Frontend

* `src/api/services/hosts.ts` — adds
  `createHost(req: HostCreateInput)` and
  `updateHost(id, req: HostUpdateInput)`. The
  list endpoint is updated to read the
  `{"hosts": [...]}` envelope the backend
  already returns (it was unwrapping the raw
  array before; the v0.1.0 view never noticed
  the bug because nothing actually displayed).
  `deleteHost` and `getHost` are unchanged.

* `src/views/HostsView.vue` — replaces the
  v0.1.0 list + delete surface with a full
  CRUD. The create / edit dialogs are inline
  (the v0.2.0 convention: NodesView and
  UsersView do the same) and share a common
  body shape — same row schema, same
  FormField layout, just different submit
  handlers.

  The dialogs feature:

  * `remark` (required, 1-64 chars)
  * `displayName` (optional, max 128)
  * `type` (direct | balancer, required)
  * `enabled` (default true, currently
      always-on because v0.2 has no Switch
      component — the value is in the form
      state but not rendered; the column
      badge reads it from the backend)
  * `priority` (0-1000, default 50)
  * `country` (ISO-2)
  * `city` (max 64)
  * Nested endpoints editor: add / remove
      buttons, per-endpoint row with
      `nodeId` (Select of known nodes),
      `inboundId` (Select filtered to the
      selected node's inbounds — fetched
      lazily on dialog open), `weight`,
      `addressText` (Textarea, one address per
      line, split on submit), `portText`
      (string, parsed to number on submit).
  * Conditional Balancer section: shown
      when `type === 'balancer'`, with a
      `balancerStrategy` Select (5 options:
      round_robin / least_loaded / random /
      least_ping / urltest). Hidden when
      `type === 'direct'`.

  The form's zod schema (`createFormSchema` /
  `editFormSchema`) is declared inline in the
  view with the UI's row shape (`addressText`
  as a Textarea-friendly string, `portText`
  as a string-typed number input). On submit
  the row shape is converted to the wire
  format (the `protocol` field is read off
  the referenced Inbound on the server side,
  so the form does not ask for it). The
  cross-field superRefine from
  `src/schemas/host.ts` is replicated in
  `createFormSchema` because the wire schema
  is a ZodEffects that has no `.partial()`.

  The edit form is `createFormBaseSchema.partial().extend(...)`
  — same row shape, no superRefine (PUT
  semantics are "send only what changed", so
  the cross-field rules don't apply to
  partial updates). The submit handler
  computes a delta and only sends the keys
  the operator actually changed, so a
  read-after-edit round-trip is a no-op on
  the server.

* `src/i18n/locales/{en,ru}.json` — extends
  the `hosts` block with the create / edit
  dialog keys (`create`, `createTitle`,
  `createDescription`, `editTitle`,
  `editDescription`, `displayName`,
  `displayNameHint`, `country`, `countryHint`,
  `city`, `cityHint`, `remarkHint`,
  `priorityHint`, `endpointsHint`, `endpoint`,
  `addEndpoint`, `removeEndpoint`, `node`,
  `inbound`, `weight`, `weightHint`,
  `address`, `addressHint`, `port`, `portHint`,
  `selectNode`, `selectInbound`, `balancer`,
  `balancerStrategy`, `balancerStrategies` map
  for the 5 strategies, `errors` map for the
  three cross-field error messages,
  `created`, `createFailed`, `updated`,
  `updateFailed`). Adds two `common` keys:
  `required` and `invalidUuid`, shared with
  future forms.

## Quality

* `go test ./...` — clean (all 15 packages
  pass; hosts has 12 handler tests + 8
  service tests, all green).
* `go build ./...` — clean.
* `gofmt -l` — clean (the changed files in
  `internal/hosts/` were the only ones I
  touched, and the format is consistent).
* `go vet ./...` — clean.
* `staticcheck ./internal/hosts/...` —
  clean.
* `npm run type-check` — clean.
* `npm run lint` — clean (eslint + raw-text
  check both pass; the 70 extra
  `vue/max-attributes-per-line` warnings on
  HostsView are the same `vue-i18n`-style
  warnings that the other CRUD views already
  carry; CI ignores warnings).
* `npm run build` — clean. HostsView bundle
  is 23.45 KB / 4.78 KB gzip, the biggest
  view in the panel by a wide margin (the
  nested endpoint editor is what costs the
  bytes — v0.3 will split it into a
  sub-component if it grows further).

## Out of scope (later PRs)

* Inbounds create / edit dialog (PR-I) with
  the protocol-specific params editor (the
  `params` JSONB payload is per-protocol and
  needs a dedicated sub-form).
* The `enabled` form field — v0.2 keeps the
  column visible but does not render an input
  for it because shadcn-vue's Switch is not
  in the v0.2.0 component set. The field is
  in the form's zod schema so the wire
  payload stays correct, and the edit dialog
  can toggle the badge on the row. The Switch
  primitive lands in v0.3 alongside the
  per-entity CRUD work.
* The full override layer on Endpoint —
  `sni`, `host`, `path`, `downloadHostId`
  are in the wire schema but the v0.2 dialog
  only surfaces the four common fields
  (nodeId, inboundId, weight, address,
  port). A future PR adds the rest of the
  override knobs as a "Show advanced" tab.
* Per-endpoint live healthcheck —
  `healthcheck_url` / `healthcheck_interval_sec`
  on the Balancer are not surfaced; the
  agent-side probe lands with the host
  health PR per ARCHITECTURE.md §10.
* The pre-existing v0.1.0 wire-format
  mismatch on Nodes and Inbounds (same
  pattern as Host: Go emits snake_case,
  frontend types are camelCase). Each gets
  its own sub-PR in v0.2 — the next one is
  PR-I for Inbounds.

## Refs

* `ARCHITECTURE.md` v9 §10 (v3 Host model)
* `docs/adr/0003-singbox-only-mvp.md`
* `docs/adr/0004-frontend-ui-kit-shadcn-vue.md`
* `KNOWN_LIMITATIONS.md` — Hosts entry
  updated (was "create / edit dialogs land
  in v0.2", now closed)
* `src/schemas/host.ts` (PR-C) — the wire
  schema with the superRefine that PR-H
  mirrors in the form schema

Co-authored-by: Aegis Dev <dev@aegis.local>
