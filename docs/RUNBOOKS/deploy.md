# AegisPanel production deploy runbook

**Audience**: AegisPanel operator (the user, solo dev).
**Source of truth**: this file. If anything in `tools/scripts/` or
`docs/` contradicts this, this file wins.
**Last incident**: 2026-08-08, v0.8.0→v0.8.9 attempted deploy, 90-min
recovery. Lessons: see `topics/aegis-deploy.md` in agent memory.

---

## 0. Definitions

- **Panel image**: `ghcr.io/qadversif/aegispanel:X.Y.Z` (no `v` prefix on the tag)
- **UI image**: `ghcr.io/qadversif/aegispanel-ui:vX.Y.Z` (with `v` prefix; this asymmetry is intentional and tracked in deploy.local.md)
- **VPS**: Ubuntu 24.04, `aegis-deploy@<prod-host-ip>`, SSH key `<operator-ssh-key-path>`
- **Adjacent infra**: `aegis-postgres` (postgres:16-alpine), `aegis-redis` (redis:7-alpine), `aegis-nats` (nats:2.10-alpine)
- **Public URL**: `https://the live server.click/<panel-sub-path>/`
- **State on host**:
  - Migrations dir: `/var/lib/aegis/migrations/` (host → container at `/app/migrations`)
  - Agent TOFU: `/var/lib/aegis/known_hosts` (host → container at `/var/lib/aegis/known_hosts`)
  - Caddyfile override for UI: `/tmp/ui-Caddyfile:/etc/caddy/Caddyfile:ro`
  - Backups: `/var/lib/aegis/backups/`
- **Local notes** (operator-only, OUTSIDE the repo, never in public channels):
  - `~/.aegis/deploy.local.md` — env, image tags, deploy conventions
  - `~/.ssh/aegis-deploy-deploy-history.md` — append-only log of every deploy's env (THIS FILE MUST EXIST)
  - `<operator-ssh-key-path>` — SSH private key (ed25519, chmod 600)
  - `<operator-age-key-path>` — age private key (icacls 600 on Windows) — only after sops+age setup
  - `~/.aegis/.sops.yaml` — sops config, pins the age public key as the recipient for `aegis-env*.env`
  - `~/.aegis/aegis-env.env` — plain env (the source for encryption; icacls 600)
  - `~/.aegis/aegis-env.enc.env` — sops-encrypted env (at-rest on operator AND on server)
  - `~/.aegis/aegis-env.plain.env` — temp output of `sops -d` at deploy time (delete after deploy)
- **Server-side sops+age state** (after first production deploy):
  - `/etc/aegis/age.key` — age private key (chown 65532:65532, chmod 0640; readable by panel container)
  - `/etc/aegis/aegis-env.enc.env` — sops-encrypted env (chmod 0600 root; at-rest only)

---

## 1. Pre-flight (10 min, do this BEFORE touching the server)

### 1.1 Verify release artifacts are in GHCR

```bash
# panel
gh api /users/qadversif/packages/container/aegispanel/versions?per_page=20 \
  --jq '.[] | select(.metadata.container.tags | index("0.8.9")) | {tags, name, created_at}'

# UI
gh api /users/qadversif/packages/container/aegispanel-ui/versions?per_page=20 \
  --jq '.[] | select(.metadata.container.tags | index("v0.8.9")) | {tags, name, created_at}'
```

Both must show a digest. If either is missing, do NOT deploy — the release workflow failed.

### 1.2 Verify local migrations match the release

```bash
ls -1 backend/migrations | wc -l      # local count
ssh aegis-deploy@<prod-host-ip> \
  "sudo ls -1 /var/lib/aegis/migrations | wc -l"  # server count
```

The two counts must match. If server is BEHIND local, scp the
missing files first:

```bash
LOCAL=$(ls -1 backend/migrations | sort)
REMOTE=$(ssh aegis-deploy@<prod-host-ip> "sudo ls -1 /var/lib/aegis/migrations" | sort)
comm -13 <(echo "$LOCAL") <(echo "$REMOTE")  # files missing on server
# for each missing file:
scp backend/migrations/NNNN_name.sql aegis-deploy@<prod-host-ip>:/tmp/
ssh aegis-deploy@<prod-host-ip> "sudo cp /tmp/NNNN_name.sql /var/lib/aegis/migrations/ && sudo rm /tmp/NNNN_name.sql"
```

**If the new release has a migration that is NOT on the server,
do NOT proceed to step 3.** The boot will fatal-loop on
"migrations: failed to apply: read migrations dir".

### 1.3 Verify env contract

Open `~/.aegis/deploy.local.md` and confirm:
- AEGIS_PANEL_PATH is `/<panel-sub-path>`
- AEGIS_DECOY_ROOT is `/var/www/decoy`
- AEGIS_AGENT_KNOWN_HOSTS is `/var/lib/aegis/known_hosts`
- AEGIS_POSTGRES_DSN matches the local note

For v0.8.6+ in `AEGIS_ENV=production`, you also need:
- `AEGIS_AUTH_BACKEND=pg`
- `AEGIS_HOSTS_BACKEND=pg`
- `AEGIS_NODES_BACKEND=pg`
- `AEGIS_INBOUNDS_BACKEND=pg`
- `AEGIS_SUBSCRIPTION_BACKEND=pg`
- `AEGIS_USERS_BACKEND=pg`
- `AEGIS_PLANS_BACKEND=pg`
- `AEGIS_WEBHOOKS_BACKEND=pg` (also requires sops+age envelope, see §5)
- `AEGIS_PANELCFG_BACKEND=pg`
- `AEGIS_AUDITS_BACKEND=pg`
- `AEGIS_CREDENTIALS_BACKEND=pg` (also requires sops+age envelope, see §5)

If you're on `AEGIS_ENV=development`, you can skip plans/credentials/webhooks
(but you still need them for v0.8.6+).

### 1.4 Verify the deploy history file

```bash
ls -la ~/.ssh/aegis-deploy-deploy-history.md
```

The file must exist. If it doesn't, create it (chmod 600) and
add today's date + the env you'll deploy with. This is the
source of truth for "what JWT secret is on the server right now".

### 1.5 Verify cosign signatures (optional but recommended)

```bash
cosign verify \
  --certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*" \
  --certificate-oidc-issuer "https://token.actions.githubusercontent.com" \
  ghcr.io/qadversif/aegispanel:0.8.9
# repeat for the UI image
```

If `cosign` is not on the operator's machine, the GHCR UI shows
the signature status. The release workflow's `Re-sign and verify`
step (§3) is the trust anchor — the panel's OIDC keyless signing
chain is what consumers verify against.

### 1.6 Dry-run validation (Tier 1 #5)

Before the v0.9.0 release cut, validate the release tooling
itself — verify that `branch-start.sh` and `release.sh` would
succeed, without actually creating a branch or tag. This is
predictive validation: it catches the two highest-impact
"I would have failed" scenarios (dirty working tree, tag
already exists) before any mutation happens.

#### 1.6.1 Validate branch creation

```bash
$ tools/scripts/branch-start.sh feat backend/example --dry-run
✓ would create branch: feat/backend/example (base: main)
```

Exit code 0 = would-succeed. Exit code 2 = would-fail
(invalid type, branch exists, bad args). The dry-run is
non-destructive: zero local mutation, zero network calls.

`--dry-run` is accepted in any position
(`--dry-run feat backend/example` works the same way).
`--help` prints the usage block. Unknown `--flag` exits 2.

#### 1.6.2 Validate release cut

```bash
$ bash tools/scripts/release.sh 0.9.0 --snapshot
snapshot release of v0.9.0 (no changes applied)
previous tag: v0.8.26
range:        v0.8.26..HEAD
commits:
  6bc0b2e chore(deps): bump go 1.26.5 → 1.26.6 (govulncheck 6 stdlib advisories) (#248)
  398d416 ci(release): add smoke-test gate before cosign re-sign (#247)
  27e407a docs: recreate docs/gap-analysis-v0.8.24.md (3 broken links) (#246)
  07d8584 chore(docs): scrub historical banned-value leaks in RUNBOOKS/deploy.md + fix scanner regex anchor (#245)
  10e93ad chore(repo): scrub historical banned-value leaks (14 docs/code files) (#244)
  15262ba chore(test): replace real banned value in ssh_test.go with synthetic fixture (#243)
  49ebaff chore(repo): gitignore release-notes drafts + sync AGENTS.md (#242)
  f4543e6 feat(repo): anti-leak infrastructure (AGENTS.md + scanner + pre-commit + CI) (#241)
  5fe6a7d chore(release): cut v0.8.26 - CHANGELOG surgery (#240)
  (... ~127 more commits since v0.8.26, full list from `git log v0.8.26..HEAD` ...)
```

Preconditions (`working tree clean`, `tag v0.9.0 does not
exist`) run BEFORE the snapshot branch in `release.sh`, so
the two highest-impact failure modes surface in snapshot
mode too:

- `error: working tree has uncommitted changes` → commit
  or stash first
- `error: tag v0.9.0 already exists` → pick a new version
  or delete the tag first

If you see either of those in snapshot mode, fix it BEFORE
the real cut (this is the whole point of the dry-run). If
your actual output matches the structure above, the release
cut is safe to perform for real.

Updated: 2026-08-16 (most recent v0.9.0-pre dry-run).

---

## 2. Backup (5 min, do this BEFORE bouncing anything)

```bash
TS=$(date -u +%Y%m%d-%H%M%S)
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-postgres pg_dump -U aegis -d aegis | gzip > /var/lib/aegis/backups/pre-vX.Y.Z-${TS}.sql.gz"

# verify the backup is non-empty and has the expected size (~ 18KB compressed for the current Phase 1 schema)
ssh aegis-deploy@<prod-host-ip> \
  "ls -la /var/lib/aegis/backups/pre-vX.Y.Z-${TS}.sql.gz"
```

If the backup is < 10KB, the dump probably failed (empty DB, wrong
DSN, etc.). Re-check the DSN before proceeding.

---

## 3. Deploy (10 min, mostly waiting for docker pull + boot)

### 3.1 Pull images on the server

```bash
ssh aegis-deploy@<prod-host-ip> "sudo docker pull ghcr.io/qadversif/aegispanel:0.8.9"
ssh aegis-deploy@<prod-host-ip> "sudo docker pull ghcr.io/qadversif/aegispanel-ui:v0.8.9"
```

If the pull fails (auth, network), the GHCR token in the daemon's
auth store has expired. Re-login with `sudo docker login ghcr.io`
on the server.

### 3.2 Stop the current panel

```bash
ssh aegis-deploy@<prod-host-ip> "sudo docker stop aegis-panel && sudo docker rm aegis-panel"
```

The container is gone but the data is in `aegis-postgres-data` (named
volume, untouched). The migrations dir is on the host filesystem
(`/var/lib/aegis/migrations`), untouched. Caddyfile override is
on the host filesystem, untouched.

### 3.3 Start the new panel

**CRITICAL**: include the migrations volume mount. Skipping it
will fatal-loop the container. The bind mount is
`-v /var/lib/aegis/migrations:/app/migrations:ro` (read-only).

```bash
# build env from the live container's env (capture before stopping) + override AEGIS_ENV
ENV_ARGS=(
  -e AEGIS_DECOY_ROOT=/var/www/decoy
  -e AEGIS_LOG_LEVEL=info
  -e AEGIS_AUTH_BACKEND=pg
  -e AEGIS_POSTGRES_DSN='postgres://aegis:the v0.8.x fixture DB password (see deploy.local.md)@aegis-postgres:5432/aegis?sslmode=disable'
  -e AEGIS_JWT_SECRET="$(cat ~/.ssh/aegis-deploy-deploy-history.md | grep AEGIS_JWT_SECRET= | head -1 | cut -d= -f2-)"
  -e AEGIS_ENV=production          # or development, see §1.3
  -e AEGIS_AGENT_KNOWN_HOSTS=/var/lib/aegis/known_hosts
  -e AEGIS_PANEL_PATH=/<panel-sub-path>
  -e AEGIS_SUBSCRIPTION_BACKEND=pg
  -e AEGIS_USERS_BACKEND=pg
  -e AEGIS_REDIS_ADDR=aegis-redis:6379
  -e AEGIS_NATS_URL=nats://aegis-nats:4222
  -e AEGIS_PANELCFG_BACKEND=pg
  -e AEGIS_NODES_BACKEND=pg
  -e AEGIS_HTTP_ADDR=:8080
  -e AEGIS_INBOUNDS_BACKEND=pg
  -e AEGIS_AUDITS_BACKEND=pg
  -e AEGIS_AGENT_BINARY=/usr/local/bin/aegis-agent
  -e AEGIS_SECRETS_BACKEND=memory
  -e AEGIS_HOSTS_BACKEND=pg
  # add the v0.8.6+ stores as you migrate them:
  -e AEGIS_PLANS_BACKEND=pg
  -e AEGIS_CREDENTIALS_BACKEND=pg
  -e AEGIS_WEBHOOKS_BACKEND=pg
)

ssh aegis-deploy@<prod-host-ip> "sudo docker run -d \
  --name aegis-panel \
  --network aegis-net \
  --restart unless-stopped \
  -v /var/lib/aegis/migrations:/app/migrations:ro \
  -v /var/lib/aegis/known_hosts:/var/lib/aegis/known_hosts:ro \
  ${ENV_ARGS[@]} \
  ghcr.io/qadversif/aegispanel:0.8.9"
```

The `app.Build` call on the binary's first boot runs migrations
idempotently. The panel is Up in ~5s.

### 3.4 Wait for the panel to be healthy

```bash
# (panel image has no wget/curl, so use docker logs)
for i in 1 2 3 4 5 6 7 8 9 10; do
  STATUS=$(ssh aegis-deploy@<prod-host-ip> "sudo docker inspect aegis-panel --format '{{.State.Health.Status}}' 2>/dev/null")
  LOG=$(ssh aegis-deploy@<prod-host-ip> "sudo docker logs --tail=5 aegis-panel 2>&1 | grep 'HTTP server listening'")
  if [ -n "$LOG" ]; then
    echo "panel is up after ${i} attempts"
    break
  fi
  sleep 3
done
```

If the panel is in `Restarting (1)` state, the binary is
fatal-looping. Read `docker logs aegis-panel` to see why. Common
causes: missing migration on disk, memory backend in production,
JWT secret too short.

### 3.5 Bounce the UI

The UI image is independent. Bounce it AFTER the panel is up
(so the panel can serve the UI's API calls).

```bash
ssh aegis-deploy@<prod-host-ip> "sudo docker stop aegis-ui && sudo docker rm aegis-ui"
ssh aegis-deploy@<prod-host-ip> "sudo docker run -d \
  --name aegis-ui \
  --network aegis-net \
  --restart unless-stopped \
  -p 127.0.0.1:8081:8080 \
  -v /tmp/ui-Caddyfile:/etc/caddy/Caddyfile:ro \
  -e BACKEND_ADDR=http://aegis-panel:8080 \
  ghcr.io/qadversif/aegispanel-ui:v0.8.9"
```

### 3.6 Append the deploy to history

```bash
cat >> ~/.ssh/aegis-deploy-deploy-history.md <<EOF
$(date -u +%Y-%m-%d\ %H:%M:%S) UTC
target: <prod-host-ip>
panel_image: ghcr.io/qadversif/aegispanel:0.8.9
ui_image: ghcr.io/qadversif/aegispanel-ui:v0.8.9
AEGIS_JWT_SECRET=$(cat ~/.ssh/aegis-deploy-deploy-history.md | grep -c AEGIS_JWT_SECRET=)
EOF
chmod 600 ~/.ssh/aegis-deploy-deploy-history.md
```

---

## 4. Smoke test (5 min)

```bash
# /health
curl -kfsS https://the live server.click/<panel-sub-path>/api/v1/health
# expected: {"status":"ok","version":"X.Y.Z"}

# /me without auth
curl -kfsS -o /dev/null -w '%{http_code}\n' https://the live server.click/<panel-sub-path>/api/v1/auth/me
# expected: 401

# admin login (the one user)
curl -kfsS -X POST -H 'Content-Type: application/json' \
  -d '{"username":"admin","password":"<from deploy.local.md>"}' \
  https://the live server.click/<panel-sub-path>/api/v1/auth/login
# expected: 200 + JWT in body

# admin-only endpoint (with the JWT)
TOKEN=<paste from above>
curl -kfsS -H "Authorization: Bearer $TOKEN" https://the live server.click/<panel-sub-path>/api/v1/nodes/
# expected: 200 + JSON list of nodes

# UI loads (Caddyfile serves index.html for SPA routes)
curl -kfsS -o /dev/null -w '%{http_code}\n' https://the live server.click/<panel-sub-path>/
# expected: 200

# migrations applied — check schema_migrations count
ssh aegis-deploy@<prod-host-ip> "sudo docker exec aegis-postgres \
  psql -U aegis -d aegis -c 'SELECT count(*) FROM schema_migrations;'"
# expected: should match `ls -1 backend/migrations | wc -l` on the operator's machine
```

If `/health` is OK but the admin login returns 401 with the
correct password, the JWT secret changed. Compare the live env
to the history file.

---

## 5. Rollback (15 min, only if smoke fails)

If anything is wrong, the rollback is:
1. `docker stop aegis-panel && docker rm aegis-panel`
2. Start v0.8.0 (or last known-good) panel with the same env
3. Bounce the UI to the same release line
4. Verify the JWT secret matches the history file (otherwise admin must re-login)
5. Restore DB from backup if migrations are broken
6. Append the rollback event to the history file

```bash
ssh aegis-deploy@<prod-host-ip> "sudo docker stop aegis-panel && sudo docker rm aegis-panel"
ssh aegis-deploy@<prod-host-ip> "sudo docker run -d \
  --name aegis-panel \
  --network aegis-net --restart unless-stopped \
  -v /var/lib/aegis/migrations:/app/migrations:ro \
  -v /var/lib/aegis/known_hosts:/var/lib/aegis/known_hosts:ro \
  ${ENV_ARGS_FOR_v0.8.0[@]} \
  ghcr.io/qadversif/aegispanel:0.8.0"
```

Then restore DB if needed:
```bash
ssh aegis-deploy@<prod-host-ip> "sudo docker stop aegis-postgres"
ssh aegis-deploy@<prod-host-ip> "gunzip -c /var/lib/aegis/backups/pre-vX.Y.Z-TS.sql.gz | sudo docker exec -i aegis-postgres psql -U aegis -d aegis"
ssh aegis-deploy@<prod-host-ip> "sudo docker start aegis-postgres"
```

The DB restore is destructive — only do it if a migration
corrupted the schema. The panel + UI bounce alone is usually
enough to recover from a misconfigured env.

---

## 6. sops+age setup (1-time, gates v0.8.6+ production deploys)

The v0.8.6+ hard guard forbids `memory` backends in production
for `plans`, `webhooks`, `credentials`, `secrets` (effectively).
The webhooks + credentials + secrets stores need sops+age
envelope-encrypted secrets in pg-mode.

### 6.1 Generate the age keypair (on the operator's machine)

```bash
# install age + sops (Windows)
winget install FiloSottile.age
# sops via the Mozilla winget package or grab the binary from
# https://github.com/getsops/sops/releases (v3.7.3+ confirmed working)

# generate keypair; the public key is the last line of the file
# (the line that starts with "# public key: age1...")
age-keygen -o <operator-age-key-path>
cat <operator-age-key-path>
# On Windows the file is created world-readable; tighten ACLs:
icacls "$HOME\.ssh\aegis.age.key" /inheritance:r \
  /grant:r "$($env:USERNAME):(R,W)"

# Save the public key (the age1... line) into deploy.local.md
# under AEGIS_AGE_PUBLIC_KEY. The same public key is what
# gets installed on the server in 6.2 AND what sops uses as
# the recipient in 6.4.
```

### 6.2 Install the age keypair on the server

**Important**: the panel container runs as the distroless
`nonroot` user (UID **65532**), so the age key file on the
server must be readable by that UID. The file is also a
private key, so the host-side permissions should be tight.

```bash
# Copy the operator's PRIVATE age key to the server
scp <operator-age-key-path> aegis-deploy@<prod-host-ip>:/tmp/

# On the server: install under /etc/aegis/, chown to the
# container's nonroot UID, chmod 0640
ssh aegis-deploy@<prod-host-ip> \
  "sudo install -d -m 0700 -o root -g root /etc/aegis && \
   sudo install -m 0600 -o root -g root /tmp/aegis.age.key /etc/aegis/age.key"

# CRITICAL: re-chown to the container's UID (65532) so the
# distroless nonroot user can read it. Without this step the
# panel boot-loops on:
#   fatal: webhooks: failed to build age secret cipher:
#     envelope: read identity file "/etc/aegis/age.key":
#     open /etc/aegis/age.key: permission denied
ssh aegis-deploy@<prod-host-ip> \
  "sudo chown 65532:65532 /etc/aegis/age.key && \
   sudo chmod 0640 /etc/aegis/age.key"
```

### 6.3 Build the plain env file (production shape)

The plain env lives ONLY on the operator's disk. It is the
source that sops encrypts and the source that the deploy
script reads to build the `docker run -e KEY=VALUE` flags.

```bash
# Plain env at ~/.aegis/aegis-env.env, chmod 600 (Windows:
# icacls with owner-only grant). Content shape:
#
# AEGIS_PANEL_PATH=/<panel-sub-path>
# AEGIS_HTTP_ADDR=:8080
# AEGIS_LOG_LEVEL=info
# AEGIS_ENV=production
# AEGIS_DECOY_ROOT=/var/www/decoy
# AEGIS_AGENT_BINARY=/usr/local/bin/aegis-agent
# AEGIS_AGENT_KNOWN_HOSTS=/var/lib/aegis/known_hosts
# AEGIS_REDIS_ADDR=aegis-redis:6379
# AEGIS_NATS_URL=nats://aegis-nats:4222
# AEGIS_POSTGRES_DSN=postgres://aegis:<password>@aegis-postgres:5432/aegis?sslmode=disable
# AEGIS_JWT_SECRET=<random 64-char secret, persisted to ~/.ssh/aegis-deploy-deploy-history.md>
# AEGIS_AUTH_BACKEND=pg
# AEGIS_HOSTS_BACKEND=pg
# AEGIS_NODES_BACKEND=pg
# AEGIS_INBOUNDS_BACKEND=pg
# AEGIS_SUBSCRIPTION_BACKEND=pg
# AEGIS_USERS_BACKEND=pg
# AEGIS_PLANS_BACKEND=pg
# AEGIS_PANELCFG_BACKEND=pg
# AEGIS_AUDITS_BACKEND=pg
# AEGIS_WEBHOOKS_BACKEND=pg
# AEGIS_CREDENTIALS_BACKEND=pg
# AEGIS_SECRETS_BACKEND=pg
# AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS=age1...      # public key from 6.1
# AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE=/etc/aegis/age.key
#
# Critical: AEGIS_JWT_SECRET must match the one currently
# running on the server (in ~/.ssh/aegis-deploy-deploy-history.md)
# or the admin session breaks and a re-login is required.
# The deploy history file is the source of truth for this.
```

### 6.4 Encrypt the env with sops (at-rest on operator AND server)

The sops config file is at `~/.aegis/.sops.yaml` and has
a single `creation_rules` block pinning the age public key
as the recipient. Without the config file sops 3.7.x errors
with "no matching creation rules found" on both encrypt
and decrypt.

```yaml
# ~/.aegis/.sops.yaml
creation_rules:
  - age: 'age1...your public key...'
    path_regex: aegis-env[.][^/]*env$
```

Encrypt the plain env to an age-wrapped AES file:

```bash
sops --config ~/.aegis/.sops.yaml \
  --input-type env --output-type env \
  --encrypted-regex '^(AEGIS_JWT_SECRET|AEGIS_POSTGRES_DSN|AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE)$' \
  --output ~/.aegis/aegis-env.enc.env \
  ~/.aegis/aegis-env.env
```

> The `--encrypted-regex` is optional. With it set, only
> those three keys get AES-encrypted; the rest of the env
> stays plaintext in the file (they're public config, no
> harm in storing them in the clear). Without the flag,
> every value gets encrypted — a heavier file with no
> real benefit (the AES key is wrapped with age either way).

Push the encrypted copy to the server for at-rest storage
(next to the key file, in the same 0700 root-owned dir):

```bash
scp ~/.aegis/aegis-env.enc.env \
  aegis-deploy@<prod-host-ip>:/tmp/aegis-env.enc.env
ssh aegis-deploy@<prod-host-ip> \
  "sudo install -d -m 0700 -o root -g root /etc/aegis && \
   sudo install -m 0600 -o root -g root /tmp/aegis-env.enc.env \
     /etc/aegis/aegis-env.enc.env && \
   sudo rm -f /tmp/aegis-env.enc.env"
```

### 6.5 Decrypt on operator at deploy time (the actual workflow)

**The panel binary does NOT decrypt sops+age at container
start in v0.8.x.** There is no `cmd/aegis/main.go` code
path that reads an encrypted env file and unwraps it with
age. The wiring for that is a v0.8.x follow-up
(independent of any release).

The current workflow is:

1. Operator decrypts the env locally with their age key
2. Operator parses the plain env into `-e KEY='value'`
   docker-run flags
3. Operator pushes those flags over SSH into the server's
   `docker run` invocation
4. The server's `docker run` command line is the plaintext
   env (over the SSH channel once, encrypted at rest as
   `/etc/aegis/aegis-env.enc.env`)

```bash
# Decrypt locally
SOPS_AGE_KEY_FILE=<operator-age-key-path> \
  sops --config ~/.aegis/.sops.yaml \
       -d ~/.aegis/aegis-env.enc.env \
       > ~/.aegis/aegis-env.plain.env

# Build docker -e flags from the plain env
python -c '
import sys
flags = []
for line in open(r"C:\Users\adversif\.aegis\aegis-env.plain.env",
                 encoding="utf-8"):
    line = line.strip()
    if not line or line.startswith("#") or "=" not in line:
        continue
    k, v = line.split("=", 1)
    flags.append("-e " + k + "=" + repr(v))
print(" ".join(flags))
'
# (operator-only helper script: tools/scripts/aegis-build-env-flags.py,
# untracked. Output is a single line of `-e KEY=VALUE` flags.)

# Push over SSH and run the container
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker run -d --name aegis-panel \
     --network aegis-net --restart unless-stopped \
     -v /var/lib/aegis/migrations:/app/migrations:ro \
     -v /var/lib/aegis/known_hosts:/var/lib/aegis/known_hosts:ro \
     -v /etc/aegis/age.key:/etc/aegis/age.key:ro \
     -p 127.0.0.1:8080:8080 \
     <flags-from-python> \
     ghcr.io/qadversif/aegispanel:X.Y.Z"
```

**Operational note**: the operator's
`~/.aegis/aegis-env.plain.env` is the weak link in this
workflow. The at-rest storage on the server is always the
encrypted file. The single-tenant VPS hosting
`aegis-deploy@` is the trust boundary for the plain env
on the operator side.

### 6.6 Future work (sops-decrypt in the panel binary)

A v0.8.x follow-up would plumb sops-decrypt into the panel
binary's startup (`cmd/aegis/main.go`), reading the enc
file directly and unwrapping the AES key with the same
age identity. That PR would let the server-side
`docker run` be:

```bash
sudo docker run -d --name aegis-panel \
  -e AEGIS_ENV_FILE=/etc/aegis/aegis-env.enc.env \
  -v /etc/aegis/aegis-env.enc.env:/etc/aegis/aegis-env.enc.env:ro \
  -v /etc/aegis/age.key:/etc/aegis/age.key:ro \
  ...
```

(no `-e KEY=VALUE` flags in the docker run line; the panel
binary reads + decrypts the env file at boot).

Until that ships, the decrypt-on-operator workflow above
is the only path. This is a known limitation; it is NOT a
silent failure mode.

---

## 7. What to put in deploy.local.md (operator-only notes)

After each deploy, append to `~/.aegis/deploy.local.md`:

```markdown
## Deploy YYYY-MM-DD HH:MM UTC

- panel image: ghcr.io/qadversif/aegispanel:X.Y.Z
- ui image: ghcr.io/qadversif/aegispanel-ui:vX.Y.Z
- panel SHA: <from git rev-parse origin/main>
- AEGIS_JWT_SECRET: <copy from history file>
- AEGIS_POSTGRES_DSN: <copy from history file>
- AEGIS_ENV: development | staging | production
- backends: auth=pg hosts=pg nodes=pg inbounds=pg subscription=pg users=pg
            plans=pg webhooks=pg panelcfg=pg audits=pg credentials=pg
            secrets=memory
- migration count on server: NN
- smoke: health=200 login=200 me=200 nodes=200 ui=200
- rollback tested: no | yes (see §5)
- notes: <anything weird>
```

This is the operator's debrief. If something breaks in 3 months
and the operator (or a future agent) has to debug, this is
where the trail starts.

---

## 8. Cross-references

- `~/.aegis/deploy.local.md` — env, image tags, deploy conventions
- `tools/scripts/aegis-panel-deploy.py` — full v1 deploy script
- `tools/scripts/aegis-panel-update-v0.8.0.py` — v0.4.0→v0.8.0 update script (similar shape, useful template)
- `tools/scripts/pre-pr.sh` — pre-PR local checks (lint, build, test, vet-integration, mem size)
- `.github/workflows/release.yml` — release workflow with cosign re-sign + verify
- `backend/internal/app/app.go:180` — `migrations.Up(ctx, pool, "migrations")` (where the migrations dir is read)
- `backend/internal/app/stores.go:85-89` — production memory-backend guard
- `backend/internal/config/config.go:447-459` — `usesAnyPgBackend` (boot guard for dev+pg)
- `backend/internal/crypto/envelope/age.go:43-69` — `NewAgeSecretCipher` (the single shared envelope; webhooks / nodes.stored-key / bootstrap / CLI admin-node all reuse it)
- `backend/internal/app/app.go:280-316` — webhooks envelope wiring (the only place `NewAgeSecretCipher` is called from `app.Build`; same cipher is shared with `a.Nodes.WithEnvelope(cipher)` for the v0.8.5 stored-key path)
- `agent memory: topics/aegis-deploy.md` — full incident reports (2026-08-08 deploy incident, 2026-08-09 production deploy with sops+age)
- `agent memory: topics/aegis-secrets.md` — sops+age envelope workflow (proven on live 2026-08-09)
- `agent memory: MEMORY.md` — MUST DO #4 (deploy volume mount invariant)

---

## 9. Pre-deploy checklist (run this BEFORE the backup step)

```
[ ] Local `git status` clean, on main, at the release SHA
[ ] CHANGELOG has the vX.Y.Z section in [Unreleased]
[ ] Release workflow run completed: success, all images in GHCR
[ ] cosign verify passes (or release workflow's verify step did)
[ ] Local migrations count == server migrations count (after scp)
[ ] deploy.local.md has current env (DSN, JWT, all _BACKEND)
[ ] deploy history file exists at ~/.ssh/aegis-deploy-deploy-history.md
[ ] backup path is writable, last backup is < 7 days old
[ ] ssh to server works (test with `sudo docker ps`)
[ ] UI and panel images are at the SAME release line
[ ] (v0.8.6+ only) ALL AEGIS_*_BACKEND env vars are set
[ ] (v0.8.6+ only) sops+age envelope in place OR staying on dev
[ ] smoke test plan ready (/health, /me, /login, /nodes)
[ ] rollback plan ready (panel + UI image tags pinned)
```

If any of these fail, do NOT proceed. Investigate.
