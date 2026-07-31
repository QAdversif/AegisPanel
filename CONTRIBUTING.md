# Contributing to Aegis

Thanks for your interest in Aegis. This document covers the
practical side of contributing: how to set up a development
environment, run the tests, write a commit, and open a pull
request.

> If you only want to use Aegis and not contribute code, you
> can skip this file.

## Quick start

```bash
# 1. Fork and clone
git clone git@github.com:<your-username>/aegis.git
cd aegis

# 2. Install toolchain (Go 1.26+, Node 20+, npm, Docker 24+, Make)
make --version   # GNU make is required
node --version   # Node 20+
go version       # Go 1.26+

# 3. Install the pre-push gate (recommended)
make pre-pr-install
# (installs .git/hooks/pre-push delegating to tools/scripts/pre-pr.sh)

# 4. Start the dev stack
make dev
# (Postgres + Redis + NATS + panel on :8080 + UI on :5173)
```

## Branching and pull requests

Aegis uses a single integration branch, `main`. There is no
`develop` branch. Feature / fix / chore branches are cut off
`main` and merged back via squash-merge.

```bash
# Create a branch
git checkout -b feat/backend/some-feature main
git checkout -b fix/ui/some-bug main
git checkout -b chore/repo/some-cleanup main

# Push and open a PR
git push -u origin HEAD
gh pr create --body-file .github/pr-body-<name>.md

# Merge (squash, delete branch)
gh pr merge --admin --squash --delete-branch
```

Branch-protection (when the public repo is up) will require
PRs into `main` to pass CI and a code-owner review.

The branch-naming convention:

- `feat/<scope>/<name>` — new feature (e.g. `feat/backend/
  webhooks-internal-package`, `feat/ui/webhooks-view`)
- `fix/<scope>/<name>` — bug fix (e.g. `fix/ui/formfield-
  usefield-getter`, `fix/ci/release-workflow-latest-on-
  tag-push`)
- `chore/<scope>/<name>` — tooling / dependency / hygiene
  (e.g. `chore/repo/gitignore-aegis-operator-scripts`,
  `chore/deps/vue-i18n-11`)
- `refactor/<scope>/<name>` — refactor without behaviour
  change (e.g. `refactor/users-admin-handler-move`)
- `docs/<scope>/<name>` — documentation only (e.g.
  `docs/architecture-rev10`, `docs/sync-v0.7.0-post-
  release`)

## Pre-PR local gate

```bash
make pre-pr-install   # installs .git/hooks/pre-push
```

The hook delegates to `tools/scripts/pre-pr.sh`, which runs
the CI-equivalent checks locally before every push. A red
build is caught at the laptop, not 4 minutes into a CI run.
The script has scope flags for partial runs:

```bash
tools/scripts/pre-pr.sh --backend   # gofmt, golangci-lint v2, go test -short
tools/scripts/pre-pr.sh --frontend  # vue-tsc, eslint, vite build
tools/scripts/pre-pr.sh --docs      # markdownlint-cli2 on **/*.md
tools/scripts/pre-pr.sh --quick     # lint-only (~30s)
```

The pre-PR gate catches ~80% of the issues that would
otherwise bounce in CI (markdownlint MD errors, shellcheck
SC2164, gofmt, golangci-lint, vue-tsc, eslint, the npm
codegen freshness check). What it does NOT catch:
ansible-lint (not installed locally), Linux-only test
failures (Windows runners don't see `/bin/true` as a
non-existent binary), markdownlint MD012 trailing newlines
in legacy files, and post-merge supply-chain bugs (cosign,
OIDC, post-release verification). The first three are
documented per-PR as they come up; the fourth is caught by
the cron `pr-NN-ci-watch` pattern that watches the
post-merge re-run.

## Coding style

- **Go:** `gofmt` + `goimports` + `golangci-lint v2`
  (config in `backend/.golangci.yml`). Every source file
  carries `// SPDX-License-Identifier: AGPL-3.0-or-later`.
  Note: `goimports` separates blank imports into their own
  group at the end of the import block; mixing them with
  regular imports trips CI on Linux.
- **Vue 3 / TypeScript:** ESLint + Prettier + `vue-tsc
  --noEmit`. Every source file carries
  `<!-- SPDX-License-Identifier: AGPL-3.0-or-later -->`.
- **Shell:** `shellcheck` with `--severity=warning` (see CI).
  The pre-PR script uses `set -uo pipefail` (not `set -e`)
  intentionally to capture per-step exit codes; every `cd`
  needs `|| exit 1` to satisfy SC2164.
- **SQL:** `sqlfluff` (see CI). Style: each DDL statement on
  one line. `ALTER TABLE x\n    ADD COLUMN y` continuation
  indent fails LT02.
- **YAML / Markdown:** `yamllint` / `markdownlint-cli2` (see CI).
  Markdown: avoid `+` at the start of a wrapped list-item
  continuation (MD004); avoid `#NN` at the start of a wrapped
  line (MD018); avoid backslash-escape inside a single-backtick
  code span (MD038 cascade).
- **Ansible:** `ansible-lint` (see CI). Every `command:` /
  `shell:` task needs explicit `changed_when:`.

## Commit messages

We follow
[Conventional Commits](https://www.conventionalcommits.org/).
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

**PowerShell note:** do not use backticks inside a `-m`
string (PowerShell execution policy splits the message at
the backtick). For multi-line commits on Windows, write
the message to a `.git-commit-*.txt` file (gitignored as
a throwaway) and `git commit --file <path>`. The
`pr-body-*.md` files in `.github/` are tracked
intentionally (the PR description source of truth) and
are NOT matched by the throwaway gitignore pattern.

## Releases and versioning

- [Semantic Versioning](https://semver.org/) — `vMAJOR.MINOR.PATCH`.
- Each release is a `git tag` annotated with the date and a
  generated CHANGELOG section.
- The release pipeline lives in
  `.github/workflows/release.yml` and builds the panel / UI
  container images into `ghcr.io/qadversif/`. The pipeline
  has a stable two-event contract (tag-push + workflow_dispatch
  re-runs produce identical `[version, short, latest]` tag
  lists for both images; UI is tagged `vX.Y.Z`); see
  `docs/ROADMAP.md` §"v0.4.0 release workflow contract" for
  the consolidated post-#111 contract.
- The `release.yml` workflow also signs both images with
  cosign (post-#129); the `.github/workflows/verify-images.yml`
  re-verifies the `.sig` artifacts on every release `workflow_run`
  and on a weekly schedule.

To cut a release locally (does not push):

```bash
# Tag the commit (annotated)
git tag -a v0.x.y -m "v0.x.y: <one-line summary>"
git push origin v0.x.y
# (triggers the release workflow)
```

## Backup, restore, recovery

Aegis ships a small toolkit in `tools/scripts/`:

- `pre-pr.sh` — the local CI gate. Run before every push.
  Installed as a pre-push hook via `install-pre-push.sh` /
  `make pre-pr-install`.
- `branch-start.sh` — creates a Conventional-Commits
  feature/fix branch.
- `smoke-frontend.sh` — builds the frontend, starts
  `vite preview`, and verifies the served HTML + asset
  graph. Does NOT exercise the CRUD flows — those have
  Go integration tests.
- `release.sh` — local tag + changelog regeneration
  helper. The CI workflow is the canonical path.
- `backup.sh` — `git bundle` of the entire repository
  plus manifest and sha256. Restorable with a plain
  `git clone <bundle>`. (Operator-side backup of the
  repository, not the panel DB; the panel DB backups
  are the `aegis-pg-backup` + `aegis-pg-restore` binaries
  in `backend/cmd/`.)
- `restore.sh` — checks out a previous tag. `--hard`
  rewinds the current branch. Always leaves a
  `safety/<date>` branch so no state is lost.

These are designed for solo development: no external CI
required, no remote required, but every change is
recoverable.

## Testing

- **Backend:** `go test -race -count=1 ./...`
  (memory stores only). For the pgx integration tests:
  ```bash
  docker run -d --name aegis-test-pg -p 5432:5432 \
    -e POSTGRES_USER=aegis -e POSTGRES_PASSWORD=aegis \
    -e POSTGRES_DB=aegis_test postgres:16-alpine
  AEGIS_DATABASE_URL=postgres://aegis:aegis@localhost:5432/aegis_test \
    go test -count=1 -tags=integration ./...
  ```
  The integration test suite uses `backend/testutil/`
  for shared fixtures.
- **Frontend:** `npm run test` (Vitest, no longer `pnpm
  test` per #87 — the project is standardized on `npm ci`
  against the committed `package-lock.json`).
- **End-to-end:** `make docker-dev` then drive the panel
  with `curl` or a UI session.
- **Smoke:** the `playbooks/node.yml` Ansible playbook
  can be run against a throwaway VM to verify the BYO
  Node onboarding end-to-end.

## Code of conduct

We follow the [Contributor Covenant](CODE_OF_CONDUCT.md).
Be excellent to each other.

## Security disclosures

Please see [SECURITY.md](SECURITY.md). **Do not** file
public issues for security-sensitive bugs.
