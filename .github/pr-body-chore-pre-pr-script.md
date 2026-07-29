<!--
This file is the PR body for #124. It is committed
alongside the code so the body is part of the PR's
git history (the `gh pr create --body-file` path
mirrors the `git log` PR for posterity).
-->

# chore(ops): tools/scripts/pre-pr.sh + pre-push hook

Run the CI-equivalent checks locally before pushing a
PR. The script catches the lint / test / markdown
formatting failures that otherwise cost a 5+ minute
round-trip through GitHub Actions; the v0.5.0 PR
batch (#120, #121) shipped with a `fix(ci)` follow-up
commit on each push because the local gate did not
exist.

## What this PR ships

- `tools/scripts/pre-pr.sh` — the canonical script.
  Ten checks: gofmt, go build, go test -short,
  golangci-lint v2, npm ci (skipped if `node_modules`
  is present), `npm run codegen:check` (openapi-typescript
  up to date), `npm run type-check` (vue-tsc), `npm run
  lint` (eslint + check-raw-text), `npm run build`,
  and `markdownlint-cli2` on `**/*.md`. Each step
  prints pass/fail with elapsed seconds; the failing
  step's output is dumped verbatim so the operator can
  fix and re-run.
- `tools/scripts/install-pre-push.sh` — installs
  `.git/hooks/pre-push` to delegate to `pre-pr.sh`.
  Idempotent (re-running rewrites the stub). One-line
  uninstall: `rm .git/hooks/pre-push`.
- `Makefile` — new `pre-pr` and `pre-pr-install`
  targets (so `make pre-pr` and `make pre-pr-install`
  work alongside the existing `test` / `lint` / `build`
  targets).
- Scope flags: `--backend`, `--frontend`, `--docs`,
  `--quick`. The default is `all` (everything, full
  set).

## What this PR does NOT ship

- A pre-commit hook that runs the same gate on
  `git commit` (rather than `git push`). The pre-push
  gate is enough for the v0.5.0 polish; a pre-commit
  gate would be annoying during a work-in-progress
  commit chain.
- A parallel orchestrator (e.g. `pre-pr.sh --parallel`).
  The per-scope flags are in place but the script is
  sequential today. The CI matrix already parallelises
  per job, so a local parallel mode is a convenience,
  not a correctness gate.

## Operator workflow

```bash
# One-time install (per clone):
make pre-pr-install
# ...or:
tools/scripts/install-pre-push.sh

# Now any `git push` will refuse to send refs
# until pre-pr.sh exits 0. Manual run:
make pre-pr
# ...or:
tools/scripts/pre-pr.sh

# Quick mode (skip go test + npm run build, ~30s
# instead of ~3min):
tools/scripts/pre-pr.sh --quick

# Just one scope:
tools/scripts/pre-pr.sh --backend
tools/scripts/pre-pr.sh --frontend
tools/scripts/pre-pr.sh --docs
```

## Why the local gate exists

The CI matrix on every push takes ~5 minutes. The
backups package (#120) and backups UI (#121) PRs
each shipped with a `fix(ci)` follow-up commit
because the local gate did not exist:

- #120: `TestServiceRestoreBlockedByDefault`
  passed on Windows (where `/bin/true` does not
  exist) but failed on the Linux CI runner (where
  `/bin/true` is a real binary that exits 0
  silently). The fix was a one-line tweak to use
  a non-existent path. A local gate on a Linux
  runner would have caught it on the first push.
- #121: `CHANGELOG.md:31` had a `+` continuation
  at the start of a wrapped bullet; markdownlint
  reads the leading `+` as a new plus-style list
  item, fails the docs job. The fix was to rewrite
  the line as `, plus` inline. A local gate would
  have caught it on the first push.

The script intentionally duplicates the CI
commands (rather than calling `act` or
`gh workflow run`) so the feedback is fast
(seconds for the lint-only set, ~3 minutes for
the full set) and offline-capable.

## Verification

Local checks (manual):

```bash
# Help works
tools/scripts/pre-pr.sh --help

# Each scope is independently runnable
tools/scripts/pre-pr.sh --docs       # markdownlint only
tools/scripts/pre-pr.sh --backend    # gofmt + go build + go test + golangci-lint
tools/scripts/pre-pr.sh --frontend   # npm + codegen + typecheck + lint + build
tools/scripts/pre-pr.sh --quick      # everything except go test + npm build

# Hook install is idempotent
tools/scripts/install-pre-push.sh
cat .git/hooks/pre-push
```

The CI matrix (this PR's pipeline) runs the
canonical jobs — gofmt, go test -race, golangci-lint
v2, eslint + check-raw-text, vue-tsc, vite build,
markdownlint-cli2 — and is expected to pass green
on the first push.
