# Known Limitations — AegisPanel v0.3.0

This document tracks the gaps between what
`v0.3.0-mvp-byo-node` ships (sub-slice `v0.3.0-a`
merged) and the full design in `ARCHITECTURE.md`
§21. Every open entry points to the milestone
that closes it. **Closed** items are kept for
context — the PR that closed each one is named
so future readers can find the diff.

## v0.3.0 — currently open

### Frontend

#### "Add node" dialog (v0.3.0-b)

The backend provisioner is wired (PR #67): the
panel can `POST /api/v1/nodes/{id}/provision` to
run the install + state-transition flow. The
UI for it does not exist yet. v0.3.0-b adds:

- A modal in `NodesView` for "Add node" (IP,
  port, username, SSH-key paste).
- A status badge per node showing the current
  state (`new` / `online` / `offline`).
- i18n strings (en + ru) + OpenAPI spec
  extension + `pnpm run codegen:check` pass.

#### Real `aegis-agent` binary (v0.3.0-c)

The bootstrap install workflow writes a
`sleep infinity` systemd unit today. The real
Go agent binary (`cmd/aegis-agent/`) and the
Ansible `install_agent` role land in v0.3.0-c.
Without these two, the panel can "install"
the placeholder but the placeholder is not a
running agent — Apply end-to-end is a stub
until c lands.

### Backend

#### `/admin` HTTP surface

`internal/auth` exposes the operator CLI
(`aegis admin add` / `passwd` / `list`) but
no HTTP handler — the panel UI cannot
manage principals without DB access. v0.3+
(per the `v0.2.0` "What's next" note in
`KNOWN_LIMITATIONS-v0.1.0`).

#### Batched apply (`cores/singbox.Apply`) — v0.4

The sing-box CoreProvider is a no-op in dev
mode (per `ARCHITECTURE.md` §7.5). The
generic BatchedApplier lands in v0.4.
HY2 reconnect-under-reload load-test is
deferred to v0.4 (or v1.0 if we ship
Batched Apply later than the load-test).

### Operations

#### Backup / restore — v0.4

The DB schema is straightforward Postgres
but there is no automated backup / restore
flow yet. v0.4 ships `pg_dump`-based backup
with rotation + a smoke-tested restore
playbook. Originally planned for v0.2, but
`tools/scripts/backup.sh` was de-prioritised
in favour of the v0.2 handler surfaces.

#### Smoke on a fresh VM — v1.0

The Definition of Done in `ADR-0003` requires
a clean-VM smoke. v1.0 is the first milestone
that lands it. The intermediate v0.x milestones
skip the fresh-VM step because the deploy story
is not yet final.

### Cross-cutting

#### Dependabot majors for the v0.x window

Dependabot PRs #70 (vitest 3→4), #71
(vue-router 4→5), #72 (eslint 8→10), #73
(zod 3→4) are open and deferred to the
v0.4.0 cleanup window. They were opened on
2026-07-20 and held back because each carries
a breaking change in downstream packages
(typescript 5.8+ for #73, vue-router 5 API
rename, vitest 4 internal restructure, eslint
10 flat-config migration). PR #68 (chi bump
with the `RealIP` deprecation fix) and PR #69
(frontend minor+patch) were handled
separately — #68 superseded by #75, #69
deferred to v0.4.0 because the `@vue/tsconfig
0.9.1` minor transitively requires a
TypeScript 5.8+ major.

#### Light theme polish — v1.5

The light + dark pair ships but the light
theme is unstyled beyond the CSS variable
swap. The Aegis long-term look is a slate
base, but the light variant of the same
base needs a design pass. v1.5.

#### Tailwind v4 migration — v1.5

`tailwindcss@3.4` is the v0.1.0 baseline. v4
ships oxide engine + container queries; the
move is deferred until the rest of the
ecosystem (forms / typography / animate)
publishes v4-compatible releases.

## Closed in v0.2.0

These items are kept here so a reader of
`ARCHITECTURE.md §21 / MVP-0.2` can see what
was actually delivered, and so the diff
between v0.1.0 and v0.2.0 is auditable.

| Item | Closed by |
| --- | --- |
| Per-node inbounds editor | PR #62 (PR-I) |
| Host create / edit dialogs | PR #61 (PR-H) |
| User CRUD | PR #60 (PR-G) |
| Settings UI (panelcfg HTTP) | PR #59 (PR-F) |
| OpenAPI codegen for the TS types | PR #65 (PR-L) |
| Real subscription rate-limiting | PR #64 (PR-K) |
| Argon2id for the admin password (operational gap closed by `aegis admin` CLI; production seed guard) | PR #63 (PR-J) |
| Audit log + operator profile (read surface) | PR #66 (PR-M) |
| Sub-token rotation + URL-prefix rotation | #47 |

## What's NOT a limitation

These are sometimes mistaken for gaps; they
are intentional.

- The default admin password is documented
  in `deploy/ansible/group_vars/all.yml` —
  not a backdoor, just an operator onboarding
  aid.
- The default dark theme is intentional
  (dev-tool aesthetic per `ADR-0004`).
  Light theme is a token swap away.
- Subscriptions render the sing-box format
  by default; Clash / base64 / HTML are
  available via the `?format=` query
  parameter and the `/subscription` view.
