---
title: Developer guide
---

# Developer guide

> The developer guide is filled in as modules land. See
> `ARCHITECTURE.md` §28 for the repository layout and the
> build/test/lint workflow.

## Conventions

- Go: `gofmt` + `goimports` + `golangci-lint` v2 (config in
  `backend/.golangci.yml`). The v2 linter is opinionated about
  shadowing built-in identifiers, `//nolint:` rationale
  comments, double `fmt.Errorf("%w", ...)` wrapping, etc. — see
  the inline comments in the existing code for the patterns.
- Vue / TS: ESLint + Prettier (configs land with the first
  component).
- Commits: [Conventional Commits](https://www.conventionalcommits.org/).
- Branches: `main` (stable) / `feat/<scope>/<name>` /
  `chore/<scope>/<name>` / `fix/<scope>/<name>` /
  `refactor/<scope>/<name>`. The `develop` branch is gone;
  `main` is the integration branch.
- License header in every source file:
  `SPDX-License-Identifier: AGPL-3.0-or-later`.

## Pre-PR local gate

```bash
make pre-pr-install   # installs .git/hooks/pre-push
```

The hook delegates to `tools/scripts/pre-pr.sh`, which runs
the CI-equivalent checks locally before every push. The
script has scope flags for partial runs:

```bash
tools/scripts/pre-pr.sh --backend   # gofmt, golangci-lint v2, go test -short
tools/scripts/pre-pr.sh --frontend  # vue-tsc, eslint, vite build
tools/scripts/pre-pr.sh --docs      # markdownlint-cli2 on **/*.md
tools/scripts/pre-pr.sh --quick     # lint-only (~30s)
```

The pre-PR gate catches ~80% of the issues that would
otherwise bounce in CI (markdownlint MD errors, shellcheck
SC2164, gofmt, golangci-lint). What it does NOT catch:
ansible-lint (not installed locally), Linux-only test
failures (Windows runners don't see `/bin/true` as a
non-existent binary). Both are on the v0.5.x follow-up.

## Module overview

| Package / binary | Purpose |
| --- | --- |
| `cmd/aegis` | The panel entry point. |
| `cmd/aegis-agent` | The per-node Go binary that polls the panel and writes sing-box configs. |
| `cmd/aegis-pg-backup` | Operator-side backup CLI (list / get / create / delete / download). |
| `cmd/aegis-pg-restore` | Operator-side restore CLI. Separate binary from `aegis-pg-backup` to enforce the safety boundary at the process level. |
| `internal/auth` | JWT middleware, scope-based RBAC. |
| `internal/backups` | `pg_dump` orchestration, sidecar SHA-256, the on-disk index. |
| `internal/nodes` | The BatchedApplier transport, node registry. |
| `internal/subscription` | The render orchestrator (sing-box JSON, Clash Meta YAML, base64 URI list). |
| `internal/users` | The user CRUD, the admin handler. |
| `deploy/ansible/` | Bootstrap, secrets, sing-box, agent, caddy, fail2ban, decoy, panel install roles. |
