# AegisPanel — Agent Contract

This file is the **canonical, system-loaded contract** that every
Mavis subagent (worker, verifier, explorer, planner) reads on
session start. It is the durable repository-side counterpart to
the operator's `~/.aegis/deploy.local.md` (which lives outside
the repo and is **never** to be copied into a tracked file).

The rules in this file are enforced by THREE independent
mechanisms (see "Enforcement" below):

1. **Pre-commit hook** — `tools/scripts/check-sensitive.sh`
   scans staged diffs and refuses to commit on a hit.
2. **CI gate** — `.github/workflows/secret-scan.yml` runs the
   same scanner against every PR diff and blocks merge.
3. **Subagent briefing template** — every `task` invocation
   from root re-states the banned list explicitly. The subagent
   does not depend on having read this file.

If you are a subagent and you find yourself wanting to put any
of the items in "DO NOT" into a commit, PR body, issue, code
comment, or chat message — **stop and re-read this file**.

## What is this project?

AegisPanel is a self-hosted VPN control plane. One Go process
(`aegis-panel`, distroless) serves the admin UI (Vue 3 SPA,
shadcn-vue, shadcn design system) over an HTTPS endpoint behind
a Caddy reverse-proxy. Nodes run `sing-box` + a Go agent
(`aegis-agent`) that pulls per-user configs from the panel over
NATS.

v0.8.x is the UX-polish / silent-bug-chain era. Phase 1 is
deployed. v0.9.0 is the pre-GA hardening window (release smoke
gates, restore-drill, backup scheduler).

Stack: Go 1.24, Vue 3 + Vite + TypeScript, Postgres 16, Redis
7, NATS 2.10, Caddy 2, sops+age for envelope encryption.

## DO

- Use **`the live server`**, **`<prod-host>`**, **the prod
  instance** for the running VPS in any tracked text.
- Use **`the demo node`** for the test node. Address: omit
  unless the user explicitly asked for a connection test in
  this turn.
- Use **`the operator's age key`** / **`the operator's SSH
  key`** for paths to operator-side secrets.
- Use **`/app/...`** for paths inside the panel container.
- Use the **canonical 11-backend `AEGIS_*_BACKEND=pg` set**
  (auth, hosts, nodes, inbounds, subscription, users, plans,
  webhooks, audits, credentials, inbound_templates) plus
  `AEGIS_SECRETS_BACKEND=sops`.
- Use **the `pre-vX.Y.Z-upgrade-YYYYMMDD-HHMMSS.sql.gz`
  pattern** for backup filenames.
- **Run `tools/scripts/pre-pr.sh --docs --quick` (or
  `--quick` for full) before any commit** that touches
  more than a comment line. The pre-commit hook installed by
  `tools/scripts/install-pre-push.sh` enforces the scanner
  portion automatically.
- **Read `AGENTS.md` (this file) on session start.** The
  `<bootstrap_check>` system reminder fires for cold starts in
  git workspaces with no `AGENTS.md`; if you are mid-session
  and have not yet loaded this file, load it via the `init`
  skill before producing any artifact.

## DO NOT

- **Public IP** of the live server, the demo node, or any
  other host. Use `<prod-host>` / `<demo-host>` placeholders.
- **Public hostname** of the live server or any of its
  subdomains (e.g. CDN or demo-node hostnames). Use
  `<prod-host>`.
- **Operator's SSH password** for the live server.
- **Admin password** for the `admin` user (current or any
  past rotation; fixtures included).
- **DB password** for the `aegis-postgres` user (current or
  any past rotation; fixtures included).
- **JWT secret** (the `AEGIS_JWT_SECRET` value, 64-char
  base64).
- **age recipient public key** (the operator's `age1...`
  identity).
- **age private key file path** on the operator's machine
  (e.g. `~/.ssh/aegis.age.key`).
- **SSH host key fingerprint** of the live server, the demo
  node, or any other host (the `SHA256:...` value).
- **SSH private key file path** on the operator's machine
  (e.g. `~/.ssh/aegis-deploy`).
- **Decoy sub-path** of the live server (the
  `AEGIS_PANEL_PATH` value, e.g. `/***REMOVED***`).
- **Operator email** at the live server's domain
  (e.g. `ops@<prod-host>`).
- **Container names** in CHANGELOG / PR bodies / release
  notes that combine the host with sensitive context
  (e.g. `"aegis-panel on <prod-host> ***REMOVED***"`).
  The container names themselves (`aegis-panel`,
  `aegis-ui`, `aegis-postgres`, etc.) are NOT banned — they
  appear in the code, operator-guide, and `aegis-deploy`
  scripts.
- **Voice / chat output** to the user: avoid pasting the
  literal banned values into the chat even when the
  user-facing task involves a real prod operation. Pass
  values through pipes (plink + `base64 -d`, sops -d, etc.)
  and reference them as "the live server", "the prod
  instance", or by the operator's 1Password entry name.
- **Draft / scratch files** at the repo root that capture
  banned values (`.git-pr-title*.txt`, `.git-tag-*.txt`,
  `.github/release-notes-*.md`, `.tmp-*`). All of
  these patterns are gitignored. If a draft file slips
  in, the secret-scan gate will fail it.

## Banned patterns (machine-checked)

The list below is the canonical banned set, mirrored in
`tools/scripts/check-sensitive.sh`. The master copy lives in
the operator's `~/.aegis/deploy.local.md`; this repo copy is
the published, de-redacted public version (the patterns are
the secrets — they are not the values; values stay operator-side).

```
# public IPs (the live server + the demo node)
31\.77\.147\.146
193\.37\.68\.194

# public hostnames
aibeg\.click
cdn2ne\.aibeg\.click

# SSH password for the live server
***REMOVED***

# JWT secret (AEGIS_JWT_SECRET, base64 64 chars)
4sFihDUA/6CLxWNNGgDkeXg9dNLOSjpPvGgb4Y1Ldh0eOcv\+cW2UoO1Fk\+BL/h36

# DB password
***REMOVED***

# age recipient (operator's public key)
***REMOVED***

# server SSH host key fingerprint (ed25519)
DfNZC\+uWkxQNsvjZhC6YOXqGeWp5Z1p09GLiAlMF\+9c

# demo-node SSH host key fingerprint (ed25519)
***REMOVED***

# the pre-2026-08-09 server SSH host key fingerprint
# (rotated; kept here to catch any historical leak)
5CHBSqCWtaVA\+WX/zSmvJs22QmN8UbMIh\+6HalVCrmQ

# admin passwords (current + past rotation, fixtures included)
***REMOVED***
***REMOVED***

# decoy sub-path on the live server
***REMOVED***

# operator email at the live server's domain
ops@aibeg\.click

# operator's private-key file paths
/root/\.ssh/aegis-deploy
~/\.ssh/aegis-deploy
~/\.ssh/aegis\.age\.key
```

The script scans the canonical extensions under the working
tree, ignoring gitignored directories (`node_modules/`,
`dist/`, `backups/`, `.trash-*/`, `coverage/`) and the explicit
ALLOWLIST (in check-sensitive.sh). The ALLOWLIST is the
durable per-file contract; suffix-ignoring is intentionally
NOT used (a real leak inside a test file is still a real leak).

## Enforcement

The contract is enforced by THREE independent mechanisms. Any
single one would catch a leak; the three together make a leak
require **all three** to fail simultaneously.

1. **`tools/scripts/check-sensitive.sh`** — pre-commit hook,
   installed by `tools/scripts/install-pre-push.sh`. Greps the
   staged diff against the banned pattern list. On hit: `exit 1`,
   commit aborted. Locally re-runnable:
   `bash tools/scripts/check-sensitive.sh origin/main..HEAD`
   or `bash tools/scripts/check-sensitive.sh --staged`.

2. **`.github/workflows/secret-scan.yml`** — CI gate. Runs
   `check-sensitive.sh` against the PR diff on every PR to
   `main`. Branch protection requires this check (and the
   regular backend + frontend matrix) before merge. If
   `check-sensitive.sh` exits non-zero, the PR is **blocked**.

3. **Subagent briefing template** — every `task` invocation
   from the root session includes an explicit `banned_list`
   block in the prompt. The subagent's context is **isolated**
   from the root session; the subagent does not inherit the
   root's memory. The contract is delivered **per-invocation**,
   not assumed.

## Subagent roles

The agent team has three persistent subagents. Each has its
own system prompt, own memory, and own brief template. The
root (this session) coordinates, the subagent executes.

- **`aegis-planner`** — read-only research, code reading,
  task decomposition, plan writing. Output: a structured plan
  with file paths + line numbers + concrete edits. **No
  writes.** Invoked for: "разберись почему X", "составь план
  для Y", "что в файле Z".
- **`aegis-implementer`** — bounded production work. Owns a
  branch from creation to PR-open. **Writes code, commits,
  pushes the branch, opens the PR.** Does NOT merge. Invoked
  for: "сделай PR с фиксом X", "поправь скрипт Y".
- **`aegis-reviewer`** — independent PR review. Reads the PR
  diff + CI output. **Reports findings; does NOT edit.** Owns
  a checklist: banned-pattern scan, pre-pr.sh compliance,
  code-quality spot checks, schema-migration consistency.
  Invoked by root **before** any squash-merge.

## Where secrets live (operator-side, never in repo)

All sensitive values live in the operator's
`~/.aegis/deploy.local.md` (a 1Password-backed, gitignored
markdown file). The pattern is:

```
Server public hostname: <prod-host>
Server public IP: <prod-host-ip>
Server SSH password: <ssh-password>
Server SSH host key fingerprint: SHA256:<fingerprint>
DB password: <db-password>
JWT secret: <jwt-secret>
Admin password: <admin-password>
Operator's age private key: ~/.ssh/aegis.age.key
```

The corresponding public placeholders in tracked text are
`<prod-host>`, `<prod-host-ip>`, `the live server`, `the prod
instance`, `the operator's age key`, etc.

## Bumping this file

When a new sensitive value appears (a new server, a new
admin, a new token, a rotated secret):

1. Add the **regex form** to this file under "Banned patterns".
2. Add the **literal value** to `~/.aegis/deploy.local.md`
   (operator-side).
3. Bump the SHA in this file's commit history (force-push
   once, BFG once — see the existing v0.8.24 incident record
   in CHANGELOG.md).

The 3-level enforcement catches the leak on commit, in CI, or
on a subagent review — whichever fires first.

## Reference

- `docs/operator-guide.md` — the published operator contract.
  Uses only the public placeholders; never references the
  literal banned values.
- `~/.aegis/deploy.local.md` — the operator's private
  deploy notes. Out of repo. Authoritative for values.
- `CHANGELOG.md` — the v0.8.24 "BFG scrub" entry documents
  the historical leak that motivated this contract.
- `KNOWN_LIMITATIONS.md` — tracks open items that may
  surface sensitive context (e.g. the `KNOWN_LIMITATIONS.md`
  v0.8.20 entry on the `known_hosts` chmod 0666 workaround).
