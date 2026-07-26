# chore(repo): cleanup pass + roadmap (d-refactor.4)

## TL;DR

The fourth and final sub-step of the v0.4.0-d Path C
consolidation. Two cleanup moves + the project
roadmap. The v0.4.0 tag lands after this PR merges.

## What's in this PR

### `internal/users` — public `DefaultSubTokenRotationGrace` constant

- `users/service.go` — `DefaultSubTokenRotationGrace`
  is now a public package constant (was a
  magic-number literal `24 * time.Hour` inside
  `RotateSubToken`). The admin handler, future
  cabinet UI, and tests can reference the canonical
  duration without duplicating the literal. The
  constant has a doc comment explaining the 3X-UI
  convention and the grace <= 0 mapping.
- `users/rotation_test.go` — drops the test-internal
  `const DefaultSubTokenRotationGrace` re-export
  (the canonical constant now lives in the
  `users` package; the test references it through
  the package qualifier).

### `docs/ROADMAP.md` — the milestone ladder

New file. Documents the v0.4.0-d Path C status
(✅ shipped through d.r3; this PR is d.r4), the
v0.5.0 polish scope, the v0.6.0 (plans) and v0.7.0
(webhooks) follow-ups, the v1.0.0-mvp-soft-launch
GA scope, and the 9 open-gap packages (cabinet,
caddy, cascades, decoy, events, mcp, notifications,
stats, subscriptions-plural) with their post-v1.0
targeting.

The table at the top is the canonical "what
shipped, what's next" view. The body sections
explain the rationale for each milestone and the
non-obvious dependency chain (e.g. why v0.7.0
webhooks needs the events package even though the
events package itself ships in v1.1+).

### `.markdownlint.json` — disable `MD060` table-column-style

The default `MD060` ("aligned") style requires
vertical alignment of pipes in markdown tables,
which is fragile under PR review (any cell edit
re-flows the table). The existing project
markdownlint config already disables a long list
of stylistic rules (MD013/022/024/025/031/032/033
/034/036/040/041/058) on the same grounds: the
project's docs have mixed authorial voices and
the strict-default settings produce false-positives.
This PR adds `MD060: false` to the same block.

The PR's own `docs/ROADMAP.md` table is
markdownlint-clean under the new config.

## What this PR does NOT do

- **No `v0.4.0` tag.** Tagging is a separate
  operation (the user is the only maintainer, the
  convention is `git tag -a v0.4.0 -m "..."` on
  main after the d.r-series is complete). The PR
  includes the tag message template in
  `docs/ROADMAP.md`'s tagging-policy section.
- **No CHANGELOG update.** The CHANGELOG is
  updated in the same merge as the tag (the
  per-PR summary lands in CHANGELOG when v0.4.0
  is tagged). Keeping the CHANGELOG update and
  the tag in the same commit makes the "what
  shipped when" trace a single `git log` away.

## Why d-refactor.4 is the right cut

The d-r-series ends here. Beyond this PR, the
subscription package has zero user-CRUD surface
(the render handler does the `sub_token` lookup
through `users.Service` directly), zero
user-CRUD service methods, zero user-CRUD Store
methods, and zero user-CRUD admin handler. The
project can tag `v0.4.0` and move to v0.5.0
polish.

The `DefaultSubTokenRotationGrace` constant is
the only meaningful code change in this PR. It
was either a test-internal re-export or a
magic-number literal; the canonical-public-
constant form is the only sustainable shape
(every future cabinet-UI sub-token rotation
form would otherwise duplicate the `24h`
literal).

The `docs/ROADMAP.md` is the project's first
explicit roadmap. The user has been running
without one for the v0.x series; the
d-refactor series made the milestone boundaries
sharp enough that writing one down is worth
the bytes.

## Follow-ups (post-v0.4.0)

- `v0.5.0` polish (1-2 weeks): smoke test on
  fresh VM, backup/restore, JSON logs,
  quickstart doc, GPG-verify sing-box, GitHub
  API SHA-256 fetch. The `docs/ROADMAP.md` v0.5.0
  section has the per-bullet scope.
- `v0.6.0` (1-2 weeks): `internal/plans` package
  (table already in migration 0001).
- `v0.7.0` (1-2 weeks): `internal/webhooks` package
  (table already in migration 0001).
- `v1.0.0-mvp-soft-launch` (0.5 weeks): GA tag,
  plus the 9 open-gap packages deferred to v1.x.

## Verification

- `go test ./...` all green.
- `golangci-lint run --config backend/.golangci.yml ./...`
  with `GOFLAGS=-tags=integration`: 0 issues.
- `markdownlint-cli2 docs/ROADMAP.md
  CHANGELOG.md docs/developer/index.md`: 0
  issues.
