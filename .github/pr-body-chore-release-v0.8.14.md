CHANGELOG-only release cut. Moves the `[Unreleased]`
content (the audit-3.1 fix chain + the v0.8.14
body-drop) to a new `## [0.8.14] - 2026-08-10`
section, adds a new empty `[Unreleased]` section
above. v0.8.14 closes the v0.8.13 backwards-compat
shim that kept the refresh token in the JSON body of
`/auth/login` and `/auth/refresh`. After the cut,
the refresh token is **only** in the
`Set-Cookie: aegis_rt=...` header.

The 4-PR gap closed by this cut:

- **PR #214** (`feat(auth): store refresh token in
  HttpOnly cookie, not the JSON body`) — the
  server-side half of the audit-3.1 fix. Adds the
  `setRefreshCookie` / `clearRefreshCookie` /
  `readRefreshToken` helpers, the
  `Service.cookieSecure bool` field, the
  `Store.RevokeOne` idempotent method, and the
  public `POST /api/v1/auth/logout` route.

- **PR #215** (`feat(frontend): withCredentials +
  drop refresh from localStorage + access
  in-memory`) — the client half of the audit-3.1
  fix. The `withCredentials: true` axios flag, the
  Pinia `ref` access-token storage, the new
  `auth.boot()` page-load rehydration.

- **PR #216** (`chore(caddy): add
  Content-Security-Policy header for the admin
  path`) — the third leg of the audit-3.1 fix
  chain. Strict CSP in the panel's Caddyfile
  (`default-src 'self'`, `script-src 'self'`,
  `style-src 'self' 'unsafe-inline'`,
  `img-src 'self' data:`, etc.) closes the
  injected-script surface.

- **PR #217** (`chore(auth): drop refresh_token
  from login/refresh JSON bodies (v0.8.14)`) — the
  v0.8.13 backwards-compat shim closure. Drops
  the body's `refresh_token` field, drops the
  `refreshRequest` struct + the body-fallback
  parse in `readRefreshToken`, removes the
  `RefreshRequest` openapi schema, and
  documents the previously-undocumented
  `POST /api/v1/auth/logout` endpoint.

No new backend API surface beyond what #214 + #217
already shipped; the cut is CHANGELOG-only. v0.8.14
is a **consolidation + security tightening release**
(closes the v0.8.13 body-field shim; no wire-format
break for a v0.8.13 frontend).

After this PR merges, the v0.8.14 tag is pushed
(annotation only) and the release workflow
(`.github/workflows/release.yml`) builds + pushes
the panel + UI images, cosign-signs them,
re-verifies the cosign signatures, settles GHCR for
30 s, then creates the GH release via
`softprops/action-gh-release@v2` (the canonical
release step in this repo, line 227 of release.yml).
