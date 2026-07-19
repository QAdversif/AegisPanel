feat(inbounds): create / edit dialog with JSONB params editor (PR-I)

Fourth sub-PR of v0.2.0-mvp-agent. Closes the
v0.1.0 "list-only" Inbounds surface: the operator
can now create, edit, and delete inbounds from
the admin UI. The form is the most permissive
of the v0.2.0 CRUD dialogs because the protocol-
specific `params` field is a free-form JSONB blob
whose schema is owned by the sing-box provider.

## Backend

* `backend/internal/inbounds/inbound.go` —
  switches every JSON tag on `Inbound` from
  snake_case to camelCase (`node_id` → `nodeId`,
  `listen_port` → `listenPort`, `listen_ports` →
  `listenPorts`, `created_at` → `createdAt`,
  `updated_at` → `updatedAt`). The Go struct
  field names (PascalCase) stay unchanged. The
  wire-format fix mirrors PR-H (Host/Endpoint)
  and PR-G (User/Plan/Pool): bring the Go wire
  format in line with the camelCase source-of-
  truth in `src/types/aegis.ts`.

* `backend/internal/inbounds/handler.go` — the
  two request shapes (`createRequest`,
  `updateRequest`) get the matching camelCase
  JSON tags. The Go field names are unchanged
  so the handler logic and the unit tests are
  untouched.

* `internal/router/router.go` — no change.
* `cmd/aegis/main.go` — no change.

The v0.1.0 list view was actually broken
(typos in the `params` field's JSONB shape
display) but the bug was silent because the
view never wrote the field. PR-I makes the
list correctly read `nodeId`, `listenPort`,
`listenPorts`, `createdAt`, `updatedAt`, and
`params` from the wire.

## Frontend

* `src/api/services/inbounds.ts` — adds
  `createInbound(nodeId, req)`,
  `updateInbound(nodeId, id, req)`, and
  `deleteInbound(nodeId, id)`. The list
  endpoint is updated to unwrap the
  `{"inbounds": [...]}` envelope the backend
  already returns (it was reading the raw
  array before; same envelope pattern as the
  Hosts fix in PR-H).

* `src/views/InboundsView.vue` — replaces the
  v0.1.0 list-only surface with a full CRUD.
  The dialogs use a "row" form shape
  (comma-separated tags, one-port-per-line
  listen_ports, JSON-text params) and convert
  to the wire shape on submit. The same
  row-schema-then-convert pattern as PR-H.

  The dialogs feature:

  * `nodeId` (Select, required) — picks the
      parent node. Defaults to the currently-
      selected node filter so the common
      "I filtered to one node, now add an
      inbound" flow is a single click.
  * `name` (required, 1-64 chars; unique
      per node, enforced by the DB UNIQUE
      constraint; the backend surfaces a 409)
  * `protocol` (vless | hysteria2 |
      shadowsocks | trojan, required)
  * `listen` (default `::`, with a regex
      validator for IP literals)
  * `listenPort` (1-65535, required)
  * `listenPorts` (Textarea, one port per
      line, parsed to `number[]` on submit;
      max 16)
  * `tags` (Input, comma-separated, parsed
      to `string[]` on submit; max 16)
  * `params` (Textarea, free-form JSON;
      validated by `JSON.parse` on submit;
      rejects non-object roots and trailing
      junk)

  The `params` field is the only one with a
  custom validator that runs **before** the
  backend submission: the helper `parseParams`
  rejects empty / non-object / syntactically
  invalid payloads and surfaces a toast
  (the form's zod schema treats the field as
  a free-form string and does not parse the
  JSON itself — that would lose the user's
  intent for things like trailing comments
  the editor is happy to keep around).

  Protocol-specific sub-forms (a separate
  "VLESS" / "Hysteria 2" / etc. tab with the
  real fields) are explicitly out of scope
  for v0.2.0: the per-protocol params schema
  is owned by the sing-box provider and the
  v0.2.0 work lands in a dedicated v0.3 PR.

* `src/i18n/locales/{en,ru}.json` — extends
  the `inbounds` block with the create /
  edit dialog keys (`create`, `createTitle`,
  `createDescription`, `editTitle`,
  `editDescription`, `nameHint`, `listenHint`,
  `listenPortHint`, `listenPorts`,
  `listenPortsHint`, `tags`, `tagsHint`,
  `params`, `paramsHint`, `paramsInvalidJson`,
  `paramsEmpty`, `created`, `createFailed`,
  `updated`, `updateFailed`, `deleted`,
  `deleteFailed`, `confirmDelete`).

## Quality

* `go test ./...` — clean (all 15 packages
  pass; inbounds has 8 handler tests + 6
  service tests, all green).
* `go build ./...` — clean.
* `gofmt -l` — clean (the changed files in
  `internal/inbounds/` are gofmt-clean on
  the LF view; the Windows CRLF noise on
  pre-existing files is unchanged — see
  KNOWN_LIMITATIONS.md).
* `go vet ./...` — clean.
* `staticcheck ./internal/inbounds/...` —
  clean.
* `npm run type-check` — clean.
* `npm run lint` — clean (eslint + raw-text
  check both pass; the 60 extra
  `vue/max-attributes-per-line` warnings on
  InboundsView are the same `vue-i18n`-style
  warnings every CRUD view carries; CI
  ignores warnings).
* `npm run build` — clean. InboundsView
  bundle is 17.8 KB / 4.0 KB gzip, the
  simplest of the v0.2.0 CRUD views (no
  nested arrays like Hosts).

## Out of scope (later PRs)

* Protocol-specific params editor — a
  per-protocol sub-form (one tab per
  protocol with the actual fields) lands
  in v0.3 alongside the sing-box schema
  refactor that introduces typed protocol
  structs. The v0.2.0 generic JSON textarea
  is good enough for power users; the
  sub-form is the "I don't know the JSON
  shape" ergonomics fix.
* The `enabled` form field — v0.2 keeps the
  column visible but does not render an
  input for it (no Switch component yet;
  same as Hosts PR-H). The field is in the
  form's zod schema so the wire payload
  stays correct, and the edit dialog can
  toggle the badge on the row.
* A real "Delete" button — v0.2 uses
  `window.confirm(...)` like every other
  CRUD view; a styled AlertDialog lands
  when shadcn-vue adds the `AlertDialog`
  primitive in v0.3.
* The pre-existing v0.1.0 wire-format
  mismatch on Nodes, Subscriptions, and
  Panelcfg (same pattern as Inbounds:
  Go emits snake_case, frontend types are
  camelCase). PRs J (Argon2id), K
  (rate-limiting), L (OpenAPI codegen) will
  sweep the rest in turn.

## Refs

* `ARCHITECTURE.md` v9 §10 (Host/Endpoint
  v3 model — the inbound is the referenced
  FK target on every Endpoint)
* `docs/adr/0003-singbox-only-mvp.md`
* `docs/adr/0004-frontend-ui-kit-shadcn-vue.md`
* `KNOWN_LIMITATIONS.md` — Inbounds entry
  updated (was "create / edit dialogs land
  in v0.2", now closed)

Co-authored-by: Aegis Dev <dev@aegis.local>
