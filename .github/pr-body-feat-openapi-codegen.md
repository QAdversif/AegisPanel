feat(tooling): OpenAPI codegen + CI staleness check (PR-L)

Seventh sub-PR of v0.2.0-mvp-agent. The v0.1.0
hand-maintained `src/types/aegis.ts` (the
mirror of the Go wire types) is now
sidelined in favour of an auto-generated
`src/types/api.d.ts` produced from
`docs/openapi.yaml` via
[`openapi-typescript`](https://openapi-ts.dev).
The OpenAPI spec itself is expanded to cover
the v0.2.0 CRUD surface (auth, nodes, inbounds,
hosts, users, panelcfg, the public
subscription endpoint, plus the health /
cores meta endpoints).

The v0.3 follow-up will deprecate `aegis.ts`
and have the services + views consume the
generated types directly. v0.2.0 ships the
toolchain + a freshly-expanded spec so the
deprecation is a focused mechanical PR with
no infrastructure work.

## Tooling

* `frontend/package.json` — adds two
  scripts:
* `pnpm run codegen` — runs
    `openapi-typescript ../docs/openapi.yaml
    -o src/types/api.d.ts`.
* `pnpm run codegen:check` — runs the
    generator to a sibling `.check` file
    and compares it byte-for-byte to the
    committed one. Exits 1 with a clear
    error message on mismatch.

* `frontend/tools/scripts/check-codegen.mjs` —
  the cross-platform Node script behind
  `codegen:check`. Uses Node's `fs` and
  `child_process` only (no `diff` / `rm`
  shell-out, so Windows CI works without
  git-bash). Output is a single
  `::error::` line that GitHub Actions
  surfaces in the PR's "Files changed" tab
  + a unified-diff hint pointing at
  `pnpm run codegen && git diff`.

* `frontend/src/types/api.d.ts` — the
  generated file. ~2000 lines, 60+
  exported types. The export shape follows
  the v7 `openapi-typescript` convention:
  `paths`, `components`, and `operations`
  are namespaces; a consumer writes
  `paths['/nodes/{id}']['get']['responses']
  ['200']['content']['application/json']`
  or (more often) re-exports a friendly
  alias from a service-level wrapper. The
  flat `aegis.ts` interfaces (e.g. `Node`)
  are reachable as
  `components['schemas']['Node']`. A
  future PR adds the `openapi-fetch` /
  `openapi-react-query` codegen variants
  that would let services consume
  `paths['/nodes']['get']` directly.

* `frontend/package.json` (`devDependencies`)
  — adds `openapi-typescript@7.13.0`.

## OpenAPI spec

* `docs/openapi.yaml` — expanded from
  ~270 lines (auth-only) to ~970 lines
  covering the v0.2.0 CRUD surface:
* `paths` — every endpoint mounted by
    the v0.2.0 router: `/auth/*`, `/nodes`
    + `/nodes/{id}`, `/nodes/{nodeId}/
    inbounds` + `/nodes/{nodeId}/
    inbounds/{id}`, `/hosts` + `/hosts/
    {id}`, `/users` + `/users/{id}` +
    `/users/{id}/rotate-token`,
    `/panelcfg` + `/panelcfg/rotate` +
    `/panelcfg/rotate-to` + `/panelcfg/
    reset`, `/sub/{token}`, `/health`,
    `/cores`. The `429` response on
    `/sub/{token}` documents the
    `Retry-After` header that PR-K added.
* `components.schemas` — every Go
    struct the v0.2.0 handlers return,
    plus request bodies for create /
    update operations. Field names are
    camelCase to match the wire format
    the v0.2.0 Go side emits (the
    snake_case → camelCase work that
    PR-H, PR-I, PR-G already did for
    Host, Inbound, User, etc.).

  Out of scope for v0.2.0 (documented in
  the spec's `info.description`):
* `/admin/*` — the principal-management
    surface that PR-J added; lands in
    v0.3 alongside the audit log UI.
* `/audits/*` — the audit log endpoints
    that PR-M will add.
* `/cabinet/*` — the end-user cabinet
    (post-MVP per the v9 §3.5 roadmap).

## CI

* `.github/workflows/ci.yml` — adds a
  "Check codegen up to date" step right
  after the `pnpm install` step. The step
  runs `pnpm run codegen:check` and the
  build fails if `src/types/api.d.ts` is
  stale relative to `docs/openapi.yaml`.
  This is the canonical way to enforce
  "spec is the source of truth for the
  contract" — a PR that changes the
  backend's response shape without
  regenerating the frontend types will
  fail CI before it lands.

## Quality

* `pnpm run type-check` — clean.
* `pnpm run lint` — clean (0 errors; the
  171 pre-existing `vue/max-attributes-
  per-line` warnings on the CRUD views
  are unchanged).
* `pnpm run build` — clean (the generated
  file is `.d.ts` only, so it does not
  add to the bundle size).
* `pnpm run codegen:check` — passes on
  a clean tree; fails with a clear
  error message on a stale tree (verified
  by inserting a one-line marker and
  re-running).
* `git diff` on the staged files: only
  the new `api.d.ts` (2000 lines), the
  expanded `openapi.yaml` (~700 lines
  added), the four new lines in
  `package.json`, the new
  `check-codegen.mjs`, and the CI
  workflow step. No changes to existing
  view / service code.

## Out of scope (later PRs)

* The actual `aegis.ts` → `api.d.ts`
  deprecation. v0.2.0 ships the toolchain
  + the expanded spec; v0.3 will replace
  the import in every service / view
  and delete the hand-maintained file.
  Doing it in the same PR would multiply
  the surface area (every consumer file
  changes) and dilute the
  "toolchain-only" diff that this PR
  presents.
* `openapi-fetch` / `openapi-react-query`
  codegen variants. The current
  `openapi-typescript` output is the
  types-only baseline. The full client
  generators (which produce
  type-safe `fetch` / `useQuery`
  wrappers) are a v0.3+ concern once the
  v0.3 work confirms the wire shapes
  are stable.
* `swaggo` annotations on the Go side to
  generate `docs/openapi.yaml` from
  source comments. The current spec is
  hand-maintained; the v0.3 follow-up
  (alongside the v0.3 multi-replica
  work) will wire `swag generate` into
  the CI pipeline so the spec is
  generated from the Go code (one
  source of truth instead of two).
* Per-endpoint request / response
  examples. The current spec has
  examples for the auth endpoints and
  the public subscription render; the
  rest will follow as the v0.3 work
  expands the spec with the Go-side
  examples.

## Refs

* `ARCHITECTURE.md` v9 §3.3 (BYO Node flow
  and the audit log UI cross-references
  this work)
* `KNOWN_LIMITATIONS.md` (the
  "OpenAPI codegen for the TS types"
  entry — closed in v0.2 (PR-L))
* `docs/openapi.yaml` (the source of
  truth going forward)

Co-authored-by: Aegis Dev <dev@aegis.local>
