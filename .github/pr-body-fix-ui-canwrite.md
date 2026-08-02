## Summary

v0.8.0 introduced a server-side regression:
`/api/v1/auth/me` returns HTTP 500 on the pg
backend — the auth service's `Me()` only walks the
in-memory user map and bails on PgStore. The
v0.4.0-era `canWrite` computed property in
`NodesView.vue` gates the "Add node" button on
`auth.me?.scopes`, so the /me 500 cascaded into
`auth.me === null` and hid **every** write
affordance in the UI from **every** authenticated
user (Add node, Provision, Create on Inbounds /
Hosts / Users / Plans / Webhooks / Audits, etc.).

The user-visible symptom on the live v0.8.0
install is: login works, but the NodesView shows
an empty table with no "+" button to add the
first node. Same for every other CRUD view. The
server still validates scopes on every mutating
endpoint, so the regression is purely cosmetic on
the UI side.

## Fix

Falls back to "assume write when authenticated"
when the /me scopes are unavailable. The server
still enforces scopes on every mutating endpoint
— a real read-only user clicking "Add node" still
gets a 403 from the panel. The UI workaround only
restores the affordance, not the authorization.

```diff
- const canWrite = computed(() => auth.me?.scopes.includes('write') ?? auth.me?.scopes.includes('admin') ?? false)
+ const canWrite = computed(() => {
+   if (!auth.isAuthenticated) return false
+   const scopes = auth.me?.scopes ?? []
+   if (scopes.length === 0) return true // fallback when /me is broken
+   return scopes.includes('write') || scopes.includes('admin')
+ })
```

The `??` chain in the previous form short-circuited
to `false` the moment `auth.me` was `null`, so no
admin could see any "Add node" / "Create" button
on the v0.8.0 install until /me was fixed. With the
new form, an authenticated user with no /me scopes
default to "write" — which is what they are, on
this install, by JWT.

## Follow-up

The server-side /me fix is a separate PR that
adds `GetByID(ctx, id)` to `auth.Store`,
implements it in `MemoryStore` (already a
walk) and `PgStore` (single `SELECT * FROM users
WHERE id = $1`), and rewires `Service.lookupByID`
to use the interface method. After that lands
the UI fallback can be removed (or kept as a
belt-and-braces guard for the next time /me
breaks).

## Manual verification (post-deploy)

- Login admin at `/p-7k2mx9n4q8r3/login`
- Navigate to `/nodes` — the "Add node" button
  is visible (was hidden before this fix)
- Same for `/inbounds`, `/hosts`, `/users`,
  `/plans`, `/webhooks`, `/audits`
- The `/me` endpoint itself is still 500 — the
  user display ("Logged in as admin") in the
  topbar is still blank until the backend fix
  lands

## Scope

1 file changed, +21 / -1.
