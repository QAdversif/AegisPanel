## Problem

The Phase 1 sub-path deploy at
`https://domain.com/p-7k2mx9n4q8r3/` made four runtime
quirks visible. They were all invisible on dev
(Vite's `/api` proxy short-circuits them) and only
showed up once a real login + real `/me` + real list
endpoints were exercised.

## Fixes

### 1. `axios.create({ baseURL: '/' })` -> sub-path aware

Bare `'/'` + relative `/api/v1/auth/login` produced
`https://domain.com/api/...` on the sub-path deploy.
Top-level Caddy has no route for `/api/*` on the apex
— only `handle_path /p-7k2mx9n4q8r3/*`. So every API
call landed on the decoy HTML and axios hung forever
(opaque CORS response, no clean throw).

The fix is a baseURL IIFE that derives the directory
part of the current URL:

```ts
baseURL: (() => {
  const m = window.location.pathname.match(/^(\/[^/]+\/)/)
  return m ? m[1] : '/'
})()
```

In dev `pathname` is `/` so this is `/` and Vite's
`/api` proxy takes over; in the sub-path deploy it
resolves to `/p-7k2mx9n4q8r3/`.

### 2. snake_case -> camelCase in the response interceptor

Every TS interface under `src/api/services/*` and
`src/stores/*` is written in camelCase, but the
panel's OpenAPI spec uses snake_case. A plain `as` cast
on `api.post<LoginResponse>` only silences the
compiler; at runtime `result.accessToken` is
`undefined`, the token pair serialises to the empty
object `"{}"`, and the next request's interceptor
finds no token to attach — so the server answers
`401 missing bearer token`.

The fix is a recursive `camelizeKeys` walk installed
on the response interceptor. Every consumer stays in
camelCase; the wire-format mismatch is hidden in one
place.

### 3. `axios.post` -> `api.post` in `refreshTokens`

`refreshTokens` used the bare `axios` import for its
POST, which doesn't honour the dynamic `baseURL` from
this file. On the sub-path deploy the refresh call
went to the apex (decoy), JSON.parse threw, the
catch block wiped the tokens, and the original request
was retried without auth. Switching to the same
`api` instance fixes it.

### 4. `listNodes` returns `{ nodes: Node[] }`, not `Node[]`

`listHosts`, `listUsers`, and `listAudits` were
already updated in v0.2.0 to unwrap `{ hosts: Host[] }`
etc. `listNodes` was missed. Without this, every
caller that did `nodes.value = await listNodes()`
then `nodes.value.map(...)` blew up with
`TypeError: nodes.value.map is not a function`. The
first thing that exposes the bug is opening the host
create dialog — "Add new host" was silently dead.

```ts
const { data } = await api.get<{ nodes: Node[] }>('/api/v1/nodes/')
return data.nodes ?? []
```

## Verification

- `npm run build` clean: vue-tsc + vite build
  succeed.
- `npm run lint` clean: eslint + `check-raw-text.mjs` pass.
- End-to-end on the live Phase 1 deploy:
  - Login writes the full `accessToken` to localStorage.
  - `/api/v1/auth/me` returns 200 with the auth header.
  - All list endpoints return arrays.
  - "Add new host" button opens the create dialog.

## Not in scope

- `InboundsView` still references a non-existent
  `/api/v1/inbounds/` top-level list. v0.4.0 only has
  per-node inbounds (`/api/v1/nodes/{id}/inbounds`).
  The view code needs a small follow-up; not blocking.
- The backend list response shape (`{ items: ... }`
  wrapper) is inconsistent across endpoints. A
  backend PR to pick one shape and stick with it is
  the right next step. The `?? []` in every list
  service keeps the UI working either way.

## Deploy note

Same flow as #113 / #115: `gh workflow run release
-f tag=v0.4.0` on the merged commit, then
`docker pull && docker rm -f && docker run` on the
server, preserving the existing
`/tmp/ui-Caddyfile:/etc/caddy/Caddyfile:ro` volume mount
for the SPA fix.
