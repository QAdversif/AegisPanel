## feat(ui): PlansView + sidebar nav + i18n en/ru

Wires the v0.6.0 plans CRUD surface into the admin UI.
The Go CRUD layer (#131), HTTP handler + ScopePlans
(#132), and OpenAPI spec + hand-mirrored API client
(#133) are all on main; this PR adds the view, the
sidebar entry, and the i18n keys so the operator can
manage the tariff ladder.

### What ships in this PR

- `frontend/src/views/PlansView.vue` — new view. v0.6.0
  ships the full CRUD surface: list (DataTable with
  name, traffic limit, duration, device limit, reset
  period, price columns) + create dialog + edit dialog
  + delete confirm dialog + global search. Mirrors
  the UsersView / BackupsView pattern (vue-i18n
  `t('plans.*')` for every string, zod schema for the
  form, `useZodForm` `onSubmit` handler that maps to
  the `/api/v1/plans` service).
- `frontend/src/router/index.ts` — adds the `/plans`
  route with `titleKey: 'nav.plans'` and the lazy
  `() => import('@/views/PlansView.vue')` chunk.
- `frontend/src/layouts/AppLayout.vue` — imports the
  `Package` icon from `lucide-vue-next` and adds the
  `plans` entry to the `navItems` array, between
  `hosts` and `subscription` (the data-graph order).
- `frontend/src/i18n/locales/en.json` — adds
  `nav.plans: "Plans"` and the `plans.*` namespace
  (title, subtitle, create / edit / delete labels, all
  form field labels with hints, the four reset-period
  enum values as nested keys, the success / failure
  toast strings, the search and empty-state strings,
  the duration-format strings the table uses to
  render nanoseconds back to a human-readable form).
- `frontend/src/i18n/locales/ru.json` — same set of
  keys in Russian.

### Duration handling

The wire format is int64 nanoseconds (the Go side
stores it as Postgres INTERVAL via `pgtype.Interval`;
see `internal/plans/pg_store.go` for the encode path).
The form takes a human-readable "30 days" / "1 hour" /
"5 minutes" string and converts to ns at submit time
via a small `parseDurationInput` helper (matches
`<N><unit>` where unit is one of `{s, m, h, d, w}`);
the table formats ns back to a string at render time
via a `formatDurationNs` helper. The round-trip is
lossy for sub-second precision (Postgres INTERVAL is
microsecond-precision), which is documented in the Go
side too.

### DataTable generic workaround

The DataTable component is typed as
`T extends Record<string, unknown>`. The Plan
interface is a typed shape (not an index signature),
so passing it directly to the columns / data props
is a TS variance error. The cast is done via the
`tableColumns` / `tableRows` computeds, so the markup
section stays free of the awkward "unknown as" casts
(the `check-raw-text` script flags the word "unknown"
as user-facing text inside the markup section).
v0.6.x: relax the DataTable generic so the cast goes
away.

### Why no zod schema file

The `users`, `hosts`, `nodes` services have a zod
schema in `src/schemas/<entity>.ts` for the create /
update request shapes. Plans gets a zod schema in
v0.6.x when the UI matures — the schema file would
live at `src/schemas/plan.ts` and the request shapes
here would re-export it.

### Why no audit log writes / Status / sub-token

Plans are a simple catalog. There is no per-row state
machine, no per-user traffic counter, no sub_token to
rotate. The CRUD is exactly the wire format: name +
traffic limit + duration + device limit + reset
period + price. The DELETE is a hard delete (the Go
handler does `DELETE FROM plans`); the UI shows a
confirm dialog that warns about the dangling-users
effect.

### How to verify locally

```sh
cd frontend
npm run type-check   # vue-tsc, no errors
npm run lint         # eslint + check-raw-text, 0 errors
npm run codegen      # openapi.yaml → api.d.ts stays in sync
npm run codegen:check # "codegen up to date"
npm run build        # vite build, no errors
```

Then a manual smoke against the dev panel:

1. Start the panel (`AEGIS_AUTH_BACKEND=memory` etc.)
2. Open the UI in a browser
3. Click "Plans" in the sidebar — empty list
4. Click "New plan" — fill in name="starter",
   duration="30d", reset=monthly, price=500
5. Submit — toast "Plan created", row appears
6. Click the row's menu → Edit → change name →
   Save — toast "Plan updated", row reflects the
   change
7. Click the row's menu → Delete → confirm —
   toast "Plan deleted", row disappears

### Tag plan

This is the fourth of 5 PRs in the v0.6.0 batch:

1. #131 — internal/plans package (merged)
2. #132 — admin HTTP handler + ScopePlans (merged)
3. #133 — OpenAPI /plans endpoints (merged)
4. #134 — this PR — PlansView + sidebar + i18n
5. #135 — v0.6.0 CHANGELOG + ROADMAP + plans API
   reference docs

Tag `v0.6.0` after the docs PR lands.
