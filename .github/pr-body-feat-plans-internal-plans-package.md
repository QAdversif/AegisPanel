## feat(plans): internal/plans package — core types, store, service

Adds a dedicated `internal/plans` Go package that owns the
CRUD surface for the `plans` table (migration 0001). The
table already existed in the schema and was read-only via
the `subscription` package; this PR promotes the writes
into a stand-alone package with a pgx-backed store, the
same shape as `internal/users` / `internal/nodes` / `internal/hosts`.

The `plan_pool` join table is intentionally NOT touched in
this PR — the subscription package keeps its read-only view
of it for the render path. v0.6.x will fold the plan_pool
CRUD into this package and have subscription delegate to it.

### What ships in this PR

- `Plan` model + `ResetPeriod` closed enum
  (daily/weekly/monthly/never)
- `Store` interface with
  `Create` / `GetByID` / `GetByName` / `List` / `Update` / `Delete`
- `MemoryStore` (in-process, used by unit tests + dev
  docker-compose)
- `PgStore` (pgx-backed, used when `AEGIS_PLANS_BACKEND=pg`)
  - day-precision `pgtype.Interval` <-> `time.Duration`
    conversion
  - 30-day-per-month decode policy (documented; v0.6.x may
    revisit)
- `Service` (input validation, ID/timestamp generation on
  Create)
  - validates Name (1..64 chars, trimmed), Duration
    (1 minute..10 years), ResetPeriod enum, non-negative
    numbers
  - returns rich per-field errors via `*ValidationError` so
    the future HTTP handler can return 400s with useful
    messages
- 23 unit tests (MemoryStore, Service, duration round-trip)
  plus 4 pg integration tests (gated on
  `INTEGRATION_DATABASE_URL`)

### What is NOT in this PR

- No HTTP handler, no router mount, no main.go wiring
  (planned for PR #132)
- No OpenAPI spec, no frontend, no audit log writes
  (planned for PRs #133 + #134)
- No plan_pool CRUD (planned for v0.6.x)

### How to verify locally

```sh
cd backend
go test -short ./internal/plans/...        # 23 unit tests
INTEGRATION_DATABASE_URL=postgres://... \
  go test -count=1 ./internal/plans/...    # + 4 pg integration tests
golangci-lint run ./internal/plans/...     # clean
```

### Why this is a separate package

Mirrors the d-refactor split: the `users` package owns
`internal/users`, `hosts` owns `internal/hosts`, etc. The
`plans` table is the next biggest CRUD surface the operator
touches (every user references a plan via `users.plan_id`),
so it gets its own package. The `subscription` package
keeps its read-only `Plan` struct + Store for the render
path; a v0.6.x follow-up will collapse the two and have
`subscription` consume `plans.Plan` directly.

### Tag plan

This is the first of 5 PRs in the v0.6.0 batch:

1. **#131 — this PR** — internal/plans package
2. #132 — admin HTTP handler + ScopePlans + router/main
   wiring + config
3. #133 — OpenAPI `/plans` endpoints + `Plan` schema
4. #134 — `PlansView.vue` + sidebar nav + i18n en/ru
5. #135 — v0.6.0 CHANGELOG + ROADMAP + plans API
   reference docs

Tag `v0.6.0` after the docs PR lands.
