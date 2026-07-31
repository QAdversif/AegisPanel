# docs: sync to v0.7.0 + the post-v0.7.0 4-PR dependency batch

Brings the documentation surface in line with the
state of the `main` branch on 2026-07-31. The
project has shipped five milestones (v0.4.0 through
v0.7.0) plus the 4-PR Go+frontend dependency batch
(#141–#144) since the last docs sync, and the root
README, KNOWN_LIMITATIONS, and the docs status table
were all out of date.

## What changed

| File | Why |
| --- | --- |
| `README.md` | Was on the v0.3.0 status (shipped PR #67, b/c pending). Now reflects the full v0.x ladder with v0.7.0 as the current state; links to the per-milestone PRs; updated repository layout to show the v0.5.0+ additions (`cmd/aegis-pg-backup`, `cmd/aegis-pg-restore`, `cmd/aegis-agent` write/reload, `internal/webhooks`, `internal/plans`, `internal/backups`, `deploy/secrets/`, `deploy/docker/`, `tools/scripts/pre-pr.sh`). Pre-PR gate added to the Contributing section (the script was missing from the README). |
| `KNOWN_LIMITATIONS.md` | Was titled `v0.4.0`. New `v0.7.0 — currently open` section lists the 5 v0.7.x follow-up items (call-site wiring, sops envelope on `webhook_endpoints.secret`, background worker for retry, event-type multi-select, shared zod schema). New `v0.7.0 — closed in v0.7.0` and `Closed in v0.6.0` and `Closed in v0.5.0` tables (the v0.5.0 sops+age + backups + pre-pr + singbox API SHA + container wiring + operator guide + SECURITY + quickstart batch; the v0.6.0 plans package + handler + OpenAPI + UI; the v0.7.0 webhooks package + handler + OpenAPI + UI + cosign + JSON logs + `latest`-tag fix). |
| `CHANGELOG.md` | New "Changed (Go+frontend dependency batch, post-v0.7.0, #141–#144)" subsection under `[Unreleased]`. Per-PR summary: #141 Go minors (prometheus 1.24.1, env 11.4.1, zerolog 1.35.1); #142 pinia 4 + @vue/devtools-api (and the pnpm-store artifact conflict footgun); #143 vue-tsc 3 + the WebhooksView prop-name fix (pre-existing bug from PR #139); #144 vue-i18n 11 (zero source code changes). |
| `docs/guide/architecture.md` | New `v9.3 (2026-07-31) — post-v0.7.0 sync.` entry at the top of "Current version" covering the v0.4.0 / v0.4.0-post / v0.5.0 / v0.6.0 / v0.7.0 milestones and the 4-PR Go+frontend dependency batch. The doc body itself is unchanged from v9.2. |
| `CONTRIBUTING.md` | Updated for the npm standardization (per #87; the project no longer uses pnpm), the single-branch model (`main` only, no `develop`), the pre-PR local gate section, the PowerShell commit-message backtick note, the post-#111 release workflow contract, and the project testing flow. |
| `docs/README.md` | Project status table refreshed to v0.7.0: cabinet, S3-compatible backup storage, webhook call-site wiring, sops envelope on `webhook_endpoints.secret`, and the background worker for retry are now in the `🟡 v0.7.x+` row. Cosign sign + verify, JSON logs, and webhook outgoing surface moved from `🟡` to `✅ v0.7.0`. |

## Files touched (6)

```
 CHANGELOG.md               |  90 ++++++++++++++++++
 CONTRIBUTING.md            | 225 +++++++++++++++++++++++++++++++++++----------
 KNOWN_LIMITATIONS.md       | 208 +++++++++++++++++++++++++++--------------
 README.md                  | 215 +++++++++++++++++++++++++------------------
 docs/README.md             |  43 +++++----
 docs/guide/architecture.md |  23 +++++
 6 files changed, 579 insertions(+), 225 deletions(-)
```

## Verification

The pre-PR local gate (markdownlint-cli2 v0.17.2 +
markdownlint v0.37.4 — the same versions as the CI
docs job) reports 0 errors across 102 markdown files.

## Why this PR

The root `README.md` was on the v0.3.0 status (the
v0.3.0 BYO-node batch — "wip (a done, b/c pending)"
— was the last milestone mentioned). The user noticed
during the post-batch review that the docs surface no
longer matches `main`. This PR brings the surface
back in line without touching the `ARCHITECTURE.md`
Russian body (the v9.3 "Current version" note in
`docs/guide/architecture.md` is the only doc-body
change; the v9.3 §25 entry in `ARCHITECTURE.md` itself
is a separate, larger follow-up).
