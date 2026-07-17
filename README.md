# Aegis — VPN Control Panel

> **Aegis** is a self-hosted control panel for multi-protocol VPN services
> (sing-box on MVP v1.0, Xray as second provider in v2.0+). Multi-core
> ready via CoreProvider abstraction, BYO Node, Cascade Topology (Xray-only,
> v2.2+), MCP-driven (v2.6+), full-client compatibility (Hiddify, v2rayNG/N,
> Streisand, Clash и др.), anti-censorship via Caddy + decoy sites + port
> masquerading. See [`ARCHITECTURE.md`](./ARCHITECTURE.md) §21 + v9 entry,
> and [`ADR-0003`](./docs/adr/0003-mvp-singbox-vertical-slice.md) for the
> MVP strategy.
>
> **Stack:** Go 1.22+ backend, Vue 3 + TypeScript frontend, Caddy, fail2ban,
> PostgreSQL, ClickHouse, Redis, NATS. **License:** AGPL-3.0.
> **Tenancy:** single-tenant (one panel = one operator, multiple admin accounts).
> See [`ARCHITECTURE.md`](./ARCHITECTURE.md) for full design.

## Project status

**Pre-alpha.** Architecture is finalised in `ARCHITECTURE.md`.
The skeleton in this repository is a starting point; the working MVP is
under construction.

## Repository layout (monorepo)

```
aegis/
├── ARCHITECTURE.md          # the design document
├── README.md                # this file
├── LICENSE                  # AGPL-3.0
├── Makefile                 # top-level orchestration
├── backend/                 # Go 1.22+ service (aegis panel + agent)
├── frontend/                # Vue 3 + TS admin UI
├── docs/                    # VuePress documentation (local-only for now)
└── deploy/                  # Ansible, Caddy, fail2ban, docker, systemd
```

See [`ARCHITECTURE.md` § 28](./ARCHITECTURE.md) for the full layout
specification.

## Quick start (development)

> **Status:** the dev environment is being bootstrapped. Until Phase 0
> lands, the commands below are aspirational and will be wired up in the
> next iteration.

```bash
# prerequisites: Go 1.22+, Node 20+, pnpm, docker + docker compose, make
make dev          # full local stack: panel + ui + postgres + redis + nats + caddy
make docs         # VuePress dev server at http://localhost:8080
make test         # unit + integration tests
make lint         # golangci-lint, eslint, prettier
```

## Development workflow

- **Branches:** `main` (stable) / `develop` (active) / `feature/*` / `fix/*`
- **Commits:** [Conventional Commits](https://www.conventionalcommits.org/)
- **Versions:** [SemVer](https://semver.org/) with `vMAJOR.MINOR.PATCH` tags
- **License:** every source file carries an SPDX header:
  `// SPDX-License-Identifier: AGPL-3.0-or-later`

## Repository hosting

This project is being developed **locally** at this stage. It will be
published to GitHub (target name `QAdversif/AegisPanel`) once the MVP skeleton
is ready. Documentation is **not** published until the first release —
the local `docs/` directory is the source of truth.

## Security

Please report security issues privately. Until the public repository is
created, contact the maintainer directly through the agreed channel.
See `SECURITY.md` (will be added before publication).

## Contributing

Contributions are welcome once the project is published. Until then the
codebase is being built by the initial author; pull requests are not
accepted yet. Feel free to open issues for design discussion against
`ARCHITECTURE.md`.

## License

Aegis is released under the **GNU Affero General Public License v3.0 or
later**. See [`LICENSE`](./LICENSE) for the full text.

> **Why AGPL-3.0?** It protects against SaaS-ploitation: anyone offering
> Aegis as a hosted service must publish their modifications. This is
> the same approach used by Remnawave, Marzneshin, and other panels in
> the space, and it keeps the open-source ecosystem healthy.
