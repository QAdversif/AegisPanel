# Contributing to Aegis

Thanks for your interest in Aegis. This document covers the practical
side of contributing: how to set up a development environment, run the
tests, write a commit, and open a pull request.

> If you only want to use Aegis and not contribute code, you can skip
> this file.

## Quick start

```bash
# 1. Fork and clone
git clone git@github.com:<your-username>/aegis.git
cd aegis

# 2. Install toolchain (Go 1.22+, Node 20+, pnpm 9+, Docker 24+, Make)
make --version   # GNU make is required

# 3. Start the dev stack
make dev

# 4. Open the admin UI
#    http://localhost:5173

# 5. Browse the docs site (VuePress)
make docs
#    http://localhost:8080
```

## Development workflow

Aegis follows a `main` / `develop` / `feature/*` / `fix/*` / `release/*`
/ `hotfix/*` model. Branch protection (when the public repo is up) will
require PRs into `main` and `develop` to pass CI and a code-owner
review.

```bash
tools/scripts/branch-start.sh feat backend/nodes-bootstrap
# → creates feat/backend/nodes-bootstrap off develop, checks it out
```

## Coding style

- **Go:** `gofmt` + `goimports` + `golangci-lint` (`backend/.golangci.yml`).
  Every source file carries `// SPDX-License-Identifier: AGPL-3.0-or-later`.
- **Vue 3 / TypeScript:** ESLint + Prettier + `vue-tsc --noEmit`. Every
  source file carries `<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->`.
- **Shell:** `shellcheck` with `--severity=warning` (see CI).
- **SQL:** `sqlfluff` (see CI).
- **YAML / Markdown:** `yamllint` / `markdownlint-cli2` (see CI).
- **Ansible:** `ansible-lint` (see CI).

## Commit messages

We follow [Conventional Commits](https://www.conventionalcommits.org/).
A template is configured automatically:

```bash
git config commit.template .gitmessage.txt
```

```
feat(backend): add JWT refresh rotation
fix(frontend): correct pool weight zero-division
docs: add Caddyfile template for masquerade ports
chore(deploy): bump Caddy to 2.8
```

`BREAKING CHANGE:` footer implies a major version bump.

## Releases and versioning

- [Semantic Versioning](https://semver.org/) — `vMAJOR.MINOR.PATCH`.
- Each release is a `git tag` annotated with the date and a generated
  CHANGELOG section.
- The release pipeline lives in `.github/workflows/release.yml` and
  builds the panel / UI container images into `ghcr.io/QAdversif/`.

To cut a release locally (does not push):

```bash
tools/scripts/release.sh 0.1.0 --snapshot    # dry-run
tools/scripts/release.sh 0.1.0             # local commit + tag
tools/scripts/release.sh 0.1.0 --push      # push branch + tag
tools/scripts/release.sh 0.1.0 --push --github-release
```

## Backup, restore, recovery

Aegis ships a small toolkit in `tools/scripts/`:

- `backup.sh` — `git bundle` of the entire repository plus manifest and
  sha256. The bundle is restorable with `git clone <bundle>`.
- `restore.sh` — checks out a previous tag. `--hard` rewinds the current
  branch. Always leaves a `safety/<date>` branch so no state is lost.
- `branch-start.sh` — creates a Conventional-Commits feature/fix branch.

These are designed for solo development: no external CI required, no
remote required, but every change is recoverable.

## Testing

- **Backend:** `go test -race -count=1 ./...`
- **Frontend:** `pnpm run test` (Vitest)
- **End-to-end:** `make docker-dev` then drive the panel with `curl`
  or a UI session.
- **Smoke:** the `playbooks/node.yml` Ansible playbook can be run
  against a throwaway VM to verify the BYO Node onboarding end-to-end.

## Code of conduct

We follow the [Contributor Covenant](CODE_OF_CONDUCT.md). Be excellent
to each other.

## Security disclosures

Please see [SECURITY.md](SECURITY.md). **Do not** file public issues
for security-sensitive bugs.
