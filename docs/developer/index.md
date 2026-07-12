---
title: Developer guide
---

# Developer guide

> 🚧 Under construction. The developer guide is filled in as modules
> land. See `ARCHITECTURE.md` §28 for the repository layout and the
> build/test/lint workflow.

## Conventions

- Go: `gofmt` + `goimports` + `golangci-lint` (config in
  `backend/.golangci.yml`).
- Vue / TS: ESLint + Prettier (configs land with the first component).
- Commits: [Conventional Commits](https://www.conventionalcommits.org/).
- Branches: `main` (stable) / `develop` (active) / `feature/*` /
  `fix/*` / `release/*` / `hotfix/*`.
- License header in every source file:
  `SPDX-License-Identifier: AGPL-3.0-or-later`.
