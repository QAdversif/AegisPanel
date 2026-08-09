# chore(docs): sync to v0.8.9 (README, operator-guide, SECURITY, quickstart, deploy/secrets)

Brings the user-facing documentation into line with the v0.8.9
production state. The previous docs were frozen at v0.8.1 (or
v0.5.0 in some cases), so most entry points were stale on the
v0.8.2–v0.8.9 batch and the v0.8.x contract changes
(decrypt-on-operator, sops 3.13+, /api/v1/health alias removal,
cosign shipped, argon2id, refresh-bearer, 401 auto-refresh).

## What this PR ships

### `README.md` (root) — full refresh to v0.8.9

The previous README claimed v0.8.1 was the latest. This is a
full rewrite:

- **Opening paragraph** updated to v0.8.9 with the full
  v0.8.0–v0.8.9 batch summarised (multi-user + operator
  recovery + cosign re-sign on every release).
- **Status section** rewritten: the v0.8.0 paragraph kept
  for context, the v0.8.1 paragraph kept for context, the
  v0.8.0–v0.8.9 batch summarised as a single block, the
  milestone table updated to mark v0.8.2 through v0.8.9 as
  shipped (was "planned"), the v0.8.x row expanded to list
  PR #192 (host→node mapping) and PR #193 (subscription
  URL) as shipped.
- **Repository layout** refreshed: migration count 19 → 20
  (with the correct description of migration 0020), package
  count corrected, `crypto` package added (the v0.8.1 envelope
  extraction was missing from the old listing), the
  `docs/RUNBOOKS/` directory added to the doc subtree, the
  `docs/comparison/` directory added.
- **OpenAPI spec** reference corrected to 0.8.1 (was
  "0.7.0 — v0.8.0 same API surface", which was already
  stale at v0.8.1).
- **Contributing** section: added a privacy rule note (never
  publish deploy URLs, IPs, credentials, JWT secrets, SSH
  key paths in PRs / issues / commits) pointing at
  `deploy/secrets/README.md` for the full context.

### `docs/ROADMAP.md`

The v0.8.9 row was still marked `⏳` (in progress) even
though the release shipped on 2026-08-08 (PR #190,
commit `035c77e5`). Single-line flip to `✅ shipped (#190)`.
The rest of the ladder was already current.

### `docs/operator-guide.md` — v0.8.x refresh

- **New `## v0.8.x secret-decryption contract` section**
  at the top. The previous guide claimed the age private
  key must be on the panel host, which is the OPPOSITE of
  the v0.8.x contract: the age private key lives on the
  **operator's** local machine; the panel host only gets
  the public counterpart (mounted as `/etc/aegis/age.key`
  for runtime decrypts of webhook secrets +
  nodes.ssh_private_key_ciphertext, chown 65532:65532
  chmod 0640 so the distroless `nonroot` user can read
  it). The section explains the canonical v0.8.x
  decrypt-on-operator pattern + the worked `sops -d` +
  `docker run -e` flag builder. The 2026-08-09 fresh
  install on the reset VPS is the canonical worked
  example.
- **New "5 minutes from zero" flow** matches the v0.8.x
  manual `docker run` path (the canonical install today),
  not the v0.5.0-era Ansible-only path. The Ansible
  path is still supported but is now explicitly optional.
- **Prerequisites table**: sops 3.7+ → 3.13+ (sops is not
  in the apt repo on Ubuntu 24.04, the canonical install
  pulls v3.13.3 from GitHub releases), age 1.2+ → 1.1+,
  added a note that Ansible is now optional.
- **Health check section**: `/healthz` → `/api/v1/health`
  (the v0.5.0 alias was removed in v0.8.0, but the
  guide still referenced it).
- **"What this guide does NOT cover"** section: removed
  the v0.5.x-era "Cosign-signed panel images — v0.5.x+
  follow-up" entry (shipped in v0.8.9) and the "S3 backup
  storage — v0.5.x+ follow-up" entry (still pending but
  the wording was stale), added the per-user credential
  filter as a required-for-v1.0.0 item.

### `docs/SECURITY.md` — v0.8.9 threat model

- **bcrypt → argon2id** in the threat model (Aegis
  switched from bcrypt to argon2id back in PR #18; the
  SECURITY.md still said bcrypt). Confirmation:
  `internal/auth/users.go:54` uses
  `github.com/alexedwards/argon2id`, PHC format
  `$argon2id$v=19$m=...,t=...,p=...$salt$hash`.
- **Supported versions**: v0.5.x → v0.8.9 (latest
  tagged release 2026-08-08), added a note about the
  pre-#182 `auth.me` 500 on pg (fixed in v0.8.2) and
  the pre-#188 manual agent-bearer rotation (automated
  in v0.8.7).
- **Threat model** table: added the v0.8.7 stale-bearer
  mitigation (RefreshAgentBearer + 401 auto-refresh in
  BatchedApplier), added the v0.8.9 cosign-re-sign
  mitigation (every release is re-signed + verified
  against the GitHub Actions OIDC issuer). Also fixed
  the "Operator's VPS compromised" row — the
  distroless-uid 65532 / age-key ownership gotcha
  (chown 65532:65532, chmod 0640 — NOT 0600 root, which
  boot-loops the panel with "permission denied" on the
  age key).
- **"Not designed to defend against"** section: removed
  the v0.5.0-era "Cosign signing — v0.5.x+ follow-up"
  entry (shipped in v0.8.9, see above), updated the
  "malicious panel maintainer" row to the v0.8.9 trust
  model (cosign-verifiable against the OIDC issuer, not
  "trust the maintainer").
- **Docker images supply chain** section: removed the
  v0.5.0-era "Signing: not yet" entry, replaced with the
  v0.8.9 cosign-re-sign-and-verify contract + the worked
  `cosign verify` consumer-side command. Added
  `pnpm-audit` and `govulncheck` to the vulnerability
  scanning list (these are in the CI gate since 2026-08-06).

### `docs/guide/quickstart.md` — v0.8.x refresh

- **Prerequisites**: Ansible is now optional (marked as
  "only if you take the role-based path"). Ubuntu
  22.04+ → 24.04+ recommended.
- **Step 4 (Stage on the target host)**: rewritten to
  use the v0.8.x env file (`aegis-env.enc.env`) + the
  distroless-uid 65532 ownership pattern. Added a
  v0.8.x callout box explaining the decrypt-on-operator
  contract.
- **Step 5 (Run the panel playbook)**: split into the
  v0.8.x canonical manual `docker run` path (with
  worked `sops -d` + image pull + `docker run` snippet)
  AND the optional Ansible path (kept as a single line
  for operators who prefer the role-based flow).
- **Step 6 (Smoke test)**: `/healthz` → `/api/v1/health`,
  with a callout that the v0.5.0 alias was removed in
  v0.8.0.
- **Step 7 (Add the first node)**: split into Ansible
  and manual (panel-UI-driven) options.

### `docs/README.md` — status table refresh

The project-status table was frozen at v0.8.0. Now
includes the v0.8.1–v0.8.9 rows (each with the PR
number where applicable) and the v0.8.x-bucket rows
that are now shipped (PR #192 host→node mapping, PR
#193 subscription URL). The "Inbound-templates",
"Merged Add-node+Provision dialog", "shadcn-vue
RadioGroup", and "pre-existing eslint warnings
cleanup" rows are now marked `⏳ v0.8.x+` (the
remaining v0.8.x UX work). The per-user credential
filter is added as `⏳ v0.8.x+ / v0.9.0` with a
pointer to KNOWN_LIMITATIONS.md — required for
v1.0.0 GA.

### `deploy/secrets/README.md` — v0.8.x contract notes

Added a new "v0.8.x contract notes" section:

- Required tooling: sops 3.13+ (not in apt on Ubuntu
  24.04), age 1.1+. The `aegis admin add` CLI needs the
  Linux amd64 build (the distroless panel image has no
  shell).
- Decrypt-on-operator is the canonical v0.8.x pattern
  (the panel binary does not decrypt sops+age at boot;
  the operator decrypts locally, builds `docker run -e`
  flags, ships plaintext over the SSH channel).
- The bufio.Reader stdin drain gotcha (Go's default
  4096-byte buffer consumes the whole pipe in one
  Read when the pipe is < 4096 bytes; the second
  `promptPassword` call sees EOF). Two workarounds
  documented: (1) Python `subprocess.Popen` with
  `time.sleep(1.0)` between the two writes (worked
  example: `C:\Users\adversif\Documents\vpn
  \.tmp-create-admin.py`, gitignored), (2) `--password
  $AEGIS_INITIAL_PASSWORD` style flag (not yet
  implemented; tracked as a v0.8.x+ UX improvement).

## Files touched

```
README.md                              (full rewrite, 237 → ~290 lines)
docs/ROADMAP.md                        (1-line stale marker fix)
docs/operator-guide.md                 (4 sections + 1 new section)
docs/SECURITY.md                       (4 sections refreshed)
docs/guide/quickstart.md               (3 sections + 2 new callouts)
docs/README.md                         (status table refresh)
deploy/secrets/README.md                (1 new section: v0.8.x contract notes)
```

## Scope

Docs-only change. No code touched, no schema migration, no
env-var change, no `openapi.yaml` bump.

## Verification

- `tools/scripts/pre-pr.sh --docs` — 0 errors (markdownlint
  clean after fixing 2 MD004/ul-style violations on `+`
  bullets in nested blockquotes).
- Memory size 20KB (under 70KB soft cap).
- No code paths exercised; this is documentation only.

## What's NOT in this PR (intentional, tracked elsewhere)

- **CHANGELOG.md [Unreleased] → [0.8.X]** surgery: the
  v0.8.9 section is already correctly in place (commit
  `035c77e5` moved it from [Unreleased] to [0.8.9]). The
  current [Unreleased] contains 2 sections from PR #192
  and PR #193. These will be moved to a future [0.8.10]
  on the next release PR, per the established
  CHANGELOG-surgery pattern.
- **Per-user credential filter in Builder**: still
  open, tracked in KNOWN_LIMITATIONS.md. The
  implementation is a 1-PR (~300 lines) change per the
  analysis in the prior conversation; it is the only
  known high-severity security gap remaining in v0.8.9
  and is required for the v1.0.0 GA tag.
- **Cosign re-sign step docs in deploy/secrets/README.md**:
  not added; the operator's local cosign verification
  command is in SECURITY.md (where it belongs — it's a
  supply-chain trust claim, not a secrets-management
  topic).
- **Pre-existing eslint warnings cleanup (chore PR)**:
  still in the v0.8.x UX follow-ups bucket, not docs.
- **docs/RUNBOOKS/deploy.md**: already current from
  PR #191 (the v0.8.6 deploy runbook fixes). The
  bufio.Reader gotcha referenced in deploy/secrets/README.md
  also applies to the runbook (the runbook is for
  fresh install on a reset server, same path); the
  runbook mentions the worked `python` env-flag
  builder at §6.4 which is the same script.
- **docs/adr/***, docs/guide/architecture.md,
  docs/api/index.md, docs/developer/index.md,
  docs/internal/index.md, docs/user-guide/admin/index.md**:
  reviewed, no v0.8.9-specific changes needed (the ADR
  set is finalised as the v0.5.0-era MVP strategy; the
  architecture / developer / API docs are either
  auto-generated from `openapi.yaml` or stable by
  design). KNOWN_LIMITATIONS.md is current (last updated
  in PR #191). CHANGELOG.md is correct as-is.

## Privacy

This PR contains no deploy URL, server IP, admin
credentials, JWT secret, or SSH key path. The production
URL `the live server.click` and the public IP `***REMOVED***` are
referenced only in the operator-only `deploy.local.md` on
the operator's local machine, never in this PR.
