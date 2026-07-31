# docs(v0.7.0): CHANGELOG + ROADMAP + api reference for /webhooks

v0.7.0 PR #5/5.

## What

The final docs pass for the v0.7.0 cut:

- `CHANGELOG.md` — new `[0.7.0] - 2026-07-31`
  section with five sub-sections (Added / Fixed /
  Changed / Security / Deferred) covering the
  package (PR 136), the HTTP handler (PR 137),
  the OpenAPI and hand-mirrored service (PR 138),
  and the UI view (PR 139). Pulled the v0.7.0
  entries out of `[Unreleased]`; the remaining
  `[Unreleased]` block keeps the operator-guide
  entry from PR #126 that landed in v0.5.0 but is
  still unversioned in `CHANGELOG.md`.
- `docs/ROADMAP.md` — v0.7.0 row marked shipped.
  Added the v0.7.x follow-up row (call-site
  wiring, sops envelope on the secret column,
  background retry worker, shared zod schema)
  and the next two release rows (v0.8.0
  notifications + v0.9.0 fresh-VM smoke test).
  Status date `2026-07-30` → `2026-07-31`.
- `docs/api/index.md` — version note `0.6.0` →
  `0.7.0`. New `webhooks` row in the headline
  groups table. The "shipped in a later version"
  list trimmed (the operator-side
  `POST /api/v1/webhooks/test` and the outbound
  delivery side are both shipped in v0.7.0).
  Remaining unversioned items: the inbound
  payment-confirmation webhook (lands with the
  Cabinet in v1.2+) and the notifications surface
  (lands in v0.8.0).

## Files

- `CHANGELOG.md` (~140 lines added)
- `docs/ROADMAP.md` (5 rows updated, 2 added)
- `docs/api/index.md` (3 sections touched)

No code, no OpenAPI spec, no migrations, no
backend changes. The docs are the final piece of
the v0.7.0 cut; once this lands, the tag can be
cut and the release workflow fires automatically.
