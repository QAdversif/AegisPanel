## feat(plans): admin HTTP handler + ScopePlans + router/main wiring

Wires the v0.6.0 `internal/plans` package (#131) into
the panel's HTTP surface. The Go CRUD layer is already
on main; this PR adds the auth-scope, HTTP handler, and
the boot-time wiring so the endpoints are reachable
from the admin UI (the UI itself ships in #134).

### What ships in this PR

- `internal/auth/scopes.go` — new `ScopePlans`
  constant ("plans"). Granted to every role
  (super-admin, operator, viewer) because every
  operator-facing surface that lists users
  (UsersView, future Cabinet) needs to resolve
  a `plan_id` to a name. The fail-closed argument
  is the same as the existing `ScopeAudits` ("a
  viewer who cannot see the catalog cannot resolve
  a plan id").
- `internal/auth/pg_store.go` — `scopesForRole` adds
  `ScopePlans` to all three role branches. Default
  branch unchanged (read-only).
- `internal/config/config.go` — new `PlansBackend`
  field + `AEGIS_PLANS_BACKEND` env var, default
  `memory`. Same pattern as every other service's
  backend flag.
- `internal/plans/admin_handler.go` — new file.
  `AdminRouter(svc, authMW)` mounts
  `GET /`, `GET /{id}`, `POST /`, `PATCH /{id}`,
  `DELETE /{id}` behind `RequireScope(ScopePlans)`.
  Same shape as `users.AdminRouter` plus a DELETE
  (plans have no state machine, so a hard delete
  is the natural operation; the dangling
  `users.plan_id` is handled by the subscription
  package's `ListPoolsForUser`).
- `internal/plans/admin_handler_test.go` — 11
  end-to-end tests (auth required, scope required,
  list/create/get/update/delete, duplicate-name 409,
  not-found 404, bad-id 400, validation 400, every
  error path).
- `internal/router/router.go` — `Build(...)` signature
  gains `plansSvc *plans.Service`.
  `r.Mount("/plans", plans.AdminRouter(...))` sits
  next to `/users` (the natural pair: plan CRUD,
  then user CRUD that references it).
- `internal/router/router_test.go` — updated
  `buildRouterForTest` to pass a fresh plans
  service. All existing router tests still pass.
- `cmd/aegis/main.go` — `cfg.PlansBackend` plugged
  into `needsPg`; new `plansSvc` construction block
  between `usersSvc` (8) and the subscription service
  (renumbered to 10); passed to `router.Build`.
  Numbered comments kept consistent.

### What is NOT in this PR

- No OpenAPI spec update (planned for #133)
- No frontend (planned for #134)
- No audit log writes (the call-site wiring is a
  separate batch across all admin handlers; v0.2.0
  shipped the helper, the call-sites are a v0.3+ TODO)
- No `plan_pool` write surface (planned for v0.6.x;
  subscription package keeps its read-only view of
  plan_pool)

### How to verify locally

```sh
cd backend
go test -short ./internal/plans/...   # 34 unit tests
go test -short ./...                  # 20/20 packages green
golangci-lint run ./...               # 0 issues
```

Then a smoke against the dev binary:

```sh
AEGIS_AUTH_BACKEND=memory \
AEGIS_PLANS_BACKEND=memory \
./bin/aegis &
TOKEN=$(curl -sX POST localhost:8080/api/v1/auth/login \
  -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"aegis-dev-password"}' | jq -r .access_token)
curl -sX GET localhost:8080/api/v1/plans -H "Authorization: Bearer $TOKEN"
# {"plans":[]}
curl -sX POST localhost:8080/api/v1/plans -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"name":"starter","duration_ns":2592000000000000,"reset_period":"monthly"}'
# 201 + plan row
```

### Tag plan

This is the second of 5 PRs in the v0.6.0 batch:

1. **#131** — internal/plans package (merged)
2. **#132 — this PR** — admin HTTP handler + ScopePlans,
   plus router/main wiring and config
3. #133 — OpenAPI `/plans` endpoints + `Plan` schema
4. #134 — `PlansView.vue` + sidebar nav + i18n en/ru
5. #135 — v0.6.0 CHANGELOG + ROADMAP + plans API
   reference docs

Tag `v0.6.0` after the docs PR lands.
