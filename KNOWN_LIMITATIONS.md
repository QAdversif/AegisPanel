# Known Limitations — AegisPanel v0.1.0

This document tracks the gaps between what
`v0.1.0-mvp-render` ships and the full design
in `ARCHITECTURE.md` §21. Every entry points
to the milestone that closes it.

## Frontend

### Per-node inbounds editor — v0.2

`/inbounds` lists every inbound across every
node with a per-node filter. The create /
update dialogs are not built — the `params`
JSONB payload is protocol-specific (VLESS-
Reality, Hysteria 2, Shadowsocks-2022, Trojan)
and the editor is non-trivial. Until v0.2
the operator registers inbounds via the Go
admin path or by editing the DB directly.

### Host create / edit dialogs — v0.2

`/hosts` lists + deletes. The create / edit
dialogs need a nested-endpoint editor (per
the v3 model in `ARCHITECTURE.md` §10) plus
the cross-field invariants (direct = 1
endpoint; balancer ≥ 2 + strategy required)
encoded in the zod schema. PR-C ships the
schema; PR-D ships the dialog. v0.2 finishes
the wiring.

### User CRUD — v0.2

`/users` is a placeholder. The full CRUD
surface (list / create / edit / delete +
per-user subscription URL + soft-delete via
`status='deleted'`) lands with the user
module in v0.2.

### Settings UI — v0.2

`/settings` is a placeholder. Panel sub-path
rotation, audit log, and operator profile
land in v0.2 (the backend services exist
for all three but the panelcfg HTTP handler
is not wired).

### Light theme polish — v1.5

The light + dark pair ships but the light
theme is unstyled beyond the CSS variable
swap. The Aegis long-term look is a slate
base, but the light variant of the same
base needs a design pass. v1.5.

### Tailwind v4 migration — v1.5

`tailwindcss@3.4` is the v0.1.0 baseline. v4
ships oxide engine + container queries; the
move is deferred until the rest of the
ecosystem (forms / typography / animate)
publishes v4-compatible releases.

### OpenAPI codegen for the TS types — v0.2

`src/types/aegis.ts` is a hand-maintained
mirror of the Go wire format. A codegen
step (generate from an OpenAPI schema) lands
in v0.2 once the API surface stabilises. The
mirror is a v0.1.0 shortcut.

## Backend

### Argon2id for the admin password — v0.2

`internal/auth` uses a legacy password hash
back-end. Argon2id (with the panel's resource
profile) lands in v0.2.

### Panelcfg HTTP handler — v0.2

`internal/panelcfg` ships a service plus a
MemoryStore and a PgStore, but no HTTP handler. The router
mounts the service for the rotated sub_path
prefix read at boot. The UI cannot rotate
the sub_path until v0.2.

### Batched apply (`cores/singbox.Apply`) — v0.2/v0.4

The sing-box CoreProvider is a no-op in dev
mode (per `ARCHITECTURE.md` §7.5). Real
`RenderConfig` lands with the Batched
Applier in v0.4. HY2 reconnect-under-reload
load-test is deferred to v0.4 (or v1.0 if we
ship before Batched Apply lands).

### Real subscription rate-limiting — v0.2

The subscription handler has no rate-limit
gate yet. v0.2 adds an in-memory token
bucket per sub_token (or per IP, behind a
config flag).

## Operations

### BYO Node flow + Ansible — v0.3

The node registration path is documented
(SSH + the agent's first-run handshake) but
not automated. v0.3 ships the Ansible
playbook that provisions a host, installs
the agent, and joins it to the panel.

### Backup / restore — v0.2

The DB schema is straightforward Postgres
but there is no automated backup / restore
flow yet. v0.2 ships `pg_dump`-based
backup with rotation + a smoke-tested
restore playbook.

### Smoke on a fresh VM — v1.0

The Definition of Done in `ADR-0003`
requires a clean-VM smoke. v1.0 is the
first milestone that lands it. The
intermediate v0.x milestones skip the
fresh-VM step because the deploy story is
not yet final.

## Cross-cutting

### CI parallel-package test flakes — v0.2

The `mustNewPool` advisory lock is supposed
to serialise Go integration-test packages
that share a Postgres, but flakes still
surface. v0.2 adds an explicit per-test
schema-namespace strategy that removes the
shared-state risk.

### Dependabot for the v0.x window

Several dependabot PRs were closed without
merging during the v0.1.0 window because the
bumps carried unrelated breaking changes
(see the `.gitattributes` PR's discussion of
`@vue/tsconfig`). v0.2 lands a dependabot
config that scopes to devDeps only and
ignores any package whose upstream doesn't
yet support the pinned TS / Go toolchain.

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
