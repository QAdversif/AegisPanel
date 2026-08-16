# AegisPanel — on-call runbook (incident response)

**Audience**: AegisPanel operator (the user, solo dev).
**Source of truth**: this file. For PLANNED deploys (release cuts,
bounces, env rotations) see [`deploy.md`](deploy.md); for open
issues see `KNOWN_LIMITATIONS.md` at the repo root.

> **Precedence**: if anything in `tools/scripts/` contradicts this
> file, this file wins for **INCIDENTS**. `deploy.md` wins for
> **PLANNED** operations. When both apply, prefer whichever the
> current trigger is closer to (3am alert → this file; release
> tag cut → `deploy.md`).

**Last incident**: 2026-08-08, v0.8.0→v0.8.9 attempted deploy,
90-min recovery. Lessons: see `topics/aegis-deploy.md` in agent
memory (worktree-local, NOT in this repo).

**Scope**: this file covers symptoms an alert will fire on.
Release-cut failures (dry-run, real cut, mid-cut cancel) are in
§8; everything else is "subsystem X is broken" triage.

---

## 0. Definitions

1. **Operator**: solo dev. SSHs in as `aegis-deploy@<prod-host-ip>`
   with key `<operator-ssh-key-path>`.
2. **Panel**: `aegis-panel` container, image
   `ghcr.io/qadversif/aegispanel:<version>`.
3. **UI**: `aegis-ui` container, image
   `ghcr.io/qadversif/aegispanel-ui:v<version>`.
4. **Adjacent infra**: `aegis-postgres`, `aegis-redis`,
   `aegis-nats`. All on the same `aegis-net` bridge network.
5. **On-call window**: 24/7 (solo operator).
6. **Escalation** (no team — if you're stuck, the only chain is):
   1. This runbook (`docs/RUNBOOKS/oncall.md`)
   2. `deploy.md` (planned operations, but its `docker` + `ssh`
      snippets are the same primitives this file uses)
   3. `KNOWN_LIMITATIONS.md` (open issues + the closed-but-recurring
      silent-bug chain)
   4. Agent memory topics — `topics/aegis-deploy.md` and
      `topics/aegis-architecture.md` (operator-side, in the agent
      data dir; not in this repo)
   5. Git history — every fix has a PR with the diff + the
      post-mortem in the body
7. **Container log convention**: every `docker logs` snippet in
   this file uses `--tail N` with a small N (50–500) so you can
   see the most recent context without paging through history. If
   the relevant line is older, follow up with
   `docker logs --since 10m aegis-panel` (adjust the window).

---

## 1. Triage flowchart

Start here for ANY incoming alert. Goal: route to the right
scenario in §2–§7 within 60 seconds.

```text
Alert arrives
  │
  ├── Which subsystem?
  │     │
  │     ├── Panel container (aegis-panel)             → §2  (P0)
  │     ├── Node (aegis-agent on demo-нода or user)   → §3  (P1)
  │     ├── Database (aegis-postgres)                 → §4  (P1)
  │     ├── Message bus (aegis-nats)                  → §5  (P1)
  │     ├── Backup file not produced / > 24h old      → §6  (P2)
  │     ├── Credential / secret rotation emergency    → §7  (P1)
  │     └── Release cut dry-run / mid-cut failure     → §8  (P0)
  │
  └── Severity check (in priority order, override the route above):
        1. Panel is unreachable AND UI is up              → §2  (P0)
        2. Demo-нода is "offline" in panel UI > 5 min     → §3  (P1)
        3. Backup files older than 24 h                   → §6  (P2)
        4. Anything else                                 → §X matching
```

**If triage takes more than 5 minutes**, jump to §2 (Panel down)
and verify the panel is not the root cause. Most other symptoms
cascade from a panel failure: a dead panel means a dead agent
provisioning loop, a dead webhook, and a dead backup cron.

---

## 2. 🔴 P0 — Panel down

**Symptom**: `https://<prod-host>/<panel-sub-path>/` returns
non-200 (5xx, 502, 503, connection refused, TLS error), OR the
UI shows "panel unreachable" on every page, OR you can no longer
SSH-tunnel to the panel and `curl` the admin API from inside.

### 2.1 Triage (60 s)

```bash
# 1. Is the container even running?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker ps -a --filter name=aegis-panel --format '{{.Names}} {{.Status}} {{.State.ExitCode}}'"
```

Decision tree:

- **Container is not in the list at all** (no exit code) → it was
  never started, or the daemon was restarted. See §2.2.
- **Container exists with `Exit 137` (SIGKILL)** → §2.3 (OOM or
  external kill).
- **Container exists with `Exit 139` (SIGSEGV)** → Go runtime
  crash. Capture the stack from `docker logs` and file a P0 bug.
- **Container exists with `Exit 143` (SIGTERM)** → graceful
  shutdown was requested. Restart it (§2.2) and watch for
  recurrence.
- **Container is `Up` but `Restarting (1) X seconds ago`** →
  fatal-loop. Read `docker logs --tail 200 aegis-panel` to see
  why. Most common: missing migration on disk, `memory` backend
  in production, or JWT secret too short.
- **Container is `Up` (healthy) but the UI still 5xx's** → the
  issue is between Caddy and the panel, or Caddy and the
  browser. See §2.4.

### 2.2 Container won't start

```bash
# Re-run the panel with the same env it last had
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker start aegis-panel 2>&1 || sudo docker run -d \
     --name aegis-panel \
     --network aegis-net \
     --restart unless-stopped \
     -v /var/lib/aegis/migrations:/app/migrations:ro \
     -v /var/lib/aegis/known_hosts:/var/lib/aegis/known_hosts:ro \
     \$(sudo docker inspect --format='{{range .Config.Env}}{{println \"-e \" .}}{{end}}' aegis-panel 2>/dev/null || true) \
     ghcr.io/qadversif/aegispanel:<last-known-good-tag>"

# Verify it stays up
sleep 5
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker logs --tail 50 aegis-panel"
```

If the panel still won't start, verify the host-side state:

```bash
# Required host paths
ssh aegis-deploy@<prod-host-ip> \
  "sudo ls -la /var/lib/aegis/migrations/ /var/lib/aegis/known_hosts /etc/aegis/age.key"
# Expected:
#   migrations/    — owned by aegis-deploy, contains NNNN_*.sql
#   known_hosts    — owned by aegis-deploy, mode 0666 (chmod-666
#                    workaround; see KNOWN_LIMITATIONS.md
#                    §"known_hosts temp-file creation")
#   age.key        — owned by 65532:65532 (the distroless
#                    nonroot UID), mode 0640
```

If any of those are missing, you're looking at a host-side state
loss — check the backup cron (§6) and the deploy history file
(`<operator-ssh-key-path>`-side `aegis-deploy-deploy-history.md`)
to see what the last known-good state was.

### 2.3 OOM-killed panel (exit 137)

```bash
# Confirm OOM via kernel ring buffer
ssh aegis-deploy@<prod-host-ip> \
  "sudo dmesg --since '10 minutes ago' 2>/dev/null | grep -E 'oom|aegis-panel' | tail -20"
```

If OOM: raise the container's `--memory` limit in the panel
container run line (see `deploy.md` §3.3 "Start the new panel")
and bounce the panel (see §3.5 "Bounce the UI"). If the limit
is already ≥ 512 MiB and it's still OOM'ing,
capture a heap profile (`docker exec aegis-panel curl
http://localhost:6060/debug/pprof/heap > heap.out`) before
bouncing, and file a P0 bug with the heap snapshot attached.

### 2.4 Container is Up but the request still 5xx's

```bash
# Bypass Caddy: hit the panel's internal health endpoint
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel curl -sS http://localhost:8080/api/v1/health"
# Expected: {"status":"ok","version":"X.Y.Z"}
```

- **If 200 OK**: the panel is fine, the issue is Caddy ↔ panel
  networking, or Caddy ↔ browser (browser cache, TLS, decoy
  sub-path mismatch). See `deploy.md` §"Caddyfile override for
  UI" for the bind-mount that wires Caddy to the panel.
- **If 5xx**: read the server log:
  ```bash
  ssh aegis-deploy@<prod-host-ip> \
    "sudo docker logs --tail 500 aegis-panel | grep -E 'ERR|PANIC|FATAL' | tail -30"
  ```
  Common root causes in v0.8.x:
  - **DB connection lost** → §4.
  - **NATS partition** → §5.
  - **Backup cron panic** → §6.
  - **Secret decryption error** → §7.
  - **`sops: failed to decrypt`** in the logs → §7.

**If still broken after 10 minutes**: revert to the previous
image. For a full container rebuild (e.g. image rollback to
`aegis-panel-prev2`), see `deploy.md` §5 "Rollback".

---

## 3. 🟠 P1 — Agent down (demo-нода or user node)

**Symptom**: panel UI shows the node with `state="offline"` for
more than 5 minutes, OR
`ssh <node-user>@<node-host> 'systemctl status aegis-agent.service'`
reports `inactive` / `failed` on the node.

### 3.1 Triage (60 s)

```bash
# On the node (replace <node-user> / <node-host> with the node's
# SSH user + host; the demo-нода and user nodes are tracked in
# the panel's nodes list)
ssh <node-user>@<node-host> \
  'systemctl status aegis-agent.service --no-pager -n 20'
```

Decision tree:

- **`active (running)`** but panel says offline → network issue.
  Check §5 (NATS partition); the agent pushes state via NATS, so
  a NATS outage will surface as "panel says offline" even when
  the agent is fine.
- **`inactive (dead)`** → §3.2.
- **`failed`** → §3.3.
- **`activating` for more than 2 minutes** → §3.4 (stuck restart
  loop).

### 3.2 Service is dead (`inactive`)

```bash
# What killed it? Read the last 100 log lines before the death.
ssh <node-user>@<node-host> \
  'journalctl -u aegis-agent.service -n 100 --no-pager'

# Manual restart
ssh <node-user>@<node-host> \
  'sudo systemctl restart aegis-agent.service'

# Verify it stays up
ssh <node-user>@<node-host> \
  'systemctl status aegis-agent.service --no-pager -n 20'
```

Common root causes in v0.8.x (the silent-bug chain — see
`KNOWN_LIMITATIONS.md` §"v0.8.16..v0.8.25 — the silent-bug chain"):

- **v0.8.25 ETXTBSY**: SFTP upload happened while the binary was
  still in use. The v0.8.25 fix is `Client.UploadAndSwap`, which
  SIGTERM's the service FIRST, waits 5 s, THEN uploads. If you
  see ETXTBSY in the logs on a v0.8.25+ agent, the agent
  re-provision ran against an older binary — bounce the panel
  and re-provision the node from the UI.
- **v0.8.20 known_hosts TOFU unreachable**: empty
  `/var/lib/aegis/known_hosts` short-circuited the TOFU policy.
  Fixed in v0.8.20; pre-v0.8.20 agents are NOT recoverable in
  place — re-provision.
- **v0.8.21 wire-vs-line fingerprint**: the panel's expected
  fingerprint didn't match the SSH server's actual key because
  `ssh.FingerprintSHA256` hashed the authorized_keys line
  format, not the binary wire format. Fixed in v0.8.21. If the
  log shows `host key mismatch` on a v0.8.21+ agent, the
  operator-pinned fingerprint is wrong; re-pin via the panel UI
  (or accept and re-provision).

### 3.3 Service is `failed`

```bash
# Find the exit cause
ssh <node-user>@<node-host> \
  'journalctl -u aegis-agent.service -n 200 --no-pager | grep -E "exit|signal|panic" | tail -20'
```

Decision tree on the exit-cause line:

- **`signal: killed (9)`** → OOM-killer. Same procedure as §2.3:
  `dmesg | tail -20` on the node, raise the systemd unit's
  `MemoryMax=`, restart, and watch.
- **`exit code 2`** → config error. Read
  `/etc/aegis/agent.yaml` on the node; the most common cause
  is a stale `NATS_URL` after a panel-side NATS container
  rename.
- **`exit code 1`** → Go panic. Capture the stack from the
  journal, file a P1 bug. Restart the service to recover the
  node while the bug is investigated.

### 3.4 Stuck restart loop

```bash
# systemd stops trying after StartLimitBurst in
# StartLimitIntervalSec. Default is 3 / 60s.
ssh <node-user>@<node-host> \
  'systemctl show aegis-agent.service -p RestartSec,StartLimitBurst,StartLimitIntervalSec'

# Reset the counter, then restart.
ssh <node-user>@<node-host> \
  'sudo systemctl reset-failed aegis-agent.service && \
   sudo systemctl restart aegis-agent.service'
```

If the loop resumes (3 fast restarts → stuck again), the root
cause is not transient — go back to §3.2 and find the underlying
panic / OOM before resetting.

---

## 4. 🟠 P1 — DB connection lost (panel ↔ aegis-postgres)

**Symptom**: panel logs show `pgx: failed to connect`,
`connection refused`, `connection pool exhausted`, or
`pg_stat_activity: too many clients already`. The UI shows 500
errors on most pages (login, node list, user list — anything
that hits the DB).

### 4.1 Triage (60 s)

```bash
# 1. Is the DB container running?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker ps -a --filter name=aegis-postgres --format '{{.Names}} {{.Status}} {{.State.ExitCode}}'"

# 2. Can the panel reach it?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel pg_isready -h aegis-postgres -p 5432"

# 3. Recent DB errors
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker logs --tail 100 aegis-postgres | grep -E 'ERROR|FATAL' | tail -20"
```

Decision tree:

- **DB container down** → §4.2.
- **DB up, panel can't reach** → §4.3 (bridge network / DNS).
- **Both up, but `pg_isready` says "no"** → §4.4 (DB is starting
  up; wait 30 s and re-check).
- **Both up, `pg_isready` says "accepting connections"** but
  the panel still 500's → §4.5 (pool exhaustion / long
  queries).

### 4.2 DB container is down

```bash
# Try a normal restart first
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker restart aegis-postgres"

# Wait for "database system is ready to accept connections"
for i in 1 2 3 4 5 6 7 8 9 10; do
  LOG=$(ssh aegis-deploy@<prod-host-ip> \
    "sudo docker logs --tail 5 aegis-postgres 2>&1 | grep 'ready to accept connections'")
  if [ -n "$LOG" ]; then
    echo "postgres is up after ${i} attempts"
    break
  fi
  sleep 3
done

# Bounce the panel so it re-pools
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker restart aegis-panel"
```

If `docker restart` fails (exit 137 = OOM, 139 = SIGSEGV),
`docker logs --tail 200 aegis-postgres` will show why. The most
common cause on Phase 1 is the named volume `aegis-postgres-data`
corrupting — restore from the latest backup (§6).

### 4.3 DB is up, panel can't reach it

```bash
# Same bridge network?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker network inspect aegis-net --format '{{range .Containers}}{{.Name}} {{end}}'"
# Both aegis-postgres and aegis-panel must be listed.

# DNS working?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel getent hosts aegis-postgres"
# Expected: an A record on the aegis-net subnet.
```

If one of the two is missing from the network, the panel was
started with `--network` not pointing at `aegis-net` (or was
started on a different network at deploy time). Stop the panel
and re-`docker run` it with `--network aegis-net` (see
`deploy.md` §3.3 for the canonical run line).

### 4.4 DB is starting up

This is the "I bounced the DB 30 seconds ago, give it a minute"
state. `pg_isready` will return "no" until the recovery /
startup finishes. Watch the log:

```bash
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker logs --tail 20 aegis-postgres | grep -E 'redo|recovery|ready'"
```

- **`redo done`** then **`ready to accept connections`** → done.
  Bounce the panel (§4.2 last line).
- **`redo done`** but no **`ready to accept connections`** after
  60 s → stuck recovery. Restore from the latest backup
  (§6). The volume is likely corrupt.

### 4.5 Pool exhaustion

```bash
# Find the long-running queries
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-postgres psql -U aegis -d aegis -c \
   \"SELECT pid, state, query_start, query FROM pg_stat_activity \
     WHERE state != 'idle' ORDER BY query_start LIMIT 20;\""
```

If a query is hung (state `active in transaction`, `query_start`
more than 5 minutes ago), kill it:

```bash
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-postgres psql -U aegis -d aegis -c \
   \"SELECT pg_terminate_backend(<pid>);\""
```

If 10+ backends are in `idle in transaction` and won't release,
the panel is leaking conns — bounce the panel
(`sudo docker restart aegis-panel`) to clear its pool, then
file a P1 bug with the backends list.

---

## 5. 🟠 P1 — NATS partition (panel ↔ aegis-nats)

**Symptom**: panel logs show `nats: connection lost`,
`reconnecting`, `no responders available for request`, or
`NATS: timeout`. Agent → panel push updates stop propagating
(configs don't reach the agent; new nodes don't appear in the
UI until a manual refresh).

### 5.1 Triage (60 s)

```bash
# 1. Is the NATS container running?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker ps -a --filter name=aegis-nats --format '{{.Names}} {{.Status}}'"

# 2. Recent NATS errors
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker logs --tail 50 aegis-nats | grep -E 'WARN|ERROR' | tail -10"

# 3. Can the panel reach it?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel bash -c 'echo > /dev/tcp/aegis-nats/4222 && echo OK || echo NO'"
# Expected: OK. NO means port 4222 is unreachable.
```

### 5.2 NATS container is down

```bash
# JetStream state is on the aegis-nats-data named volume; no
# data loss on a clean restart.
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker restart aegis-nats"

# Wait for "Server is ready"
for i in 1 2 3 4 5 6 7 8 9 10; do
  LOG=$(ssh aegis-deploy@<prod-host-ip> \
    "sudo docker logs --tail 5 aegis-nats 2>&1 | grep 'Server is ready'")
  if [ -n "$LOG" ]; then
    echo "nats is up after ${i} attempts"
    break
  fi
  sleep 3
done
```

Agents reconnect within ~30 s. The panel reconnects on its
next request; if you want it to re-subscribe immediately,
`sudo docker restart aegis-panel`.

### 5.3 NATS is up, panel can't reach it

Same diagnosis as §4.3: bridge network, DNS, or port mapping.
The fix is the same — `sudo docker network inspect aegis-net`
should list both `aegis-nats` and `aegis-panel`.

### 5.4 Stale state after the partition heals

If the agents buffered events during the partition, the panel's
view of node state may be stale (e.g. a node that reconnected
mid-partition shows "offline" in the UI). Force a refresh by
re-provisioning the affected node from the panel UI (the
provision handshake re-asserts state). There is no
`aegis admin refresh-state` CLI in v0.8.x — the UI provision
flow is the supported path.

---

## 6. 🟡 P2 — Backup failure alert

**Symptom**: panel UI shows backup age > 24 h, OR
`/var/lib/aegis/backups/_index.json` is missing recent entries,
OR an alert fires on "backup failed".

### 6.1 Triage (60 s)

```bash
# 1. Are backups being written on disk?
ssh aegis-deploy@<prod-host-ip> \
  "sudo ls -la /var/lib/aegis/backups/ | tail -20"

# 2. Is the panel container's view of the same dir OK?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel ls -la /var/lib/aegis/backups/ 2>&1"
# If "No such file or directory": the volume mount is missing.
# The fix is in the panel run line in `deploy.md` §3.3: the
# `-v` flag must include
#   -v /var/lib/aegis/backups:/app/var/backups
# (the host path is bind-mounted into the container at the
# same path; the panel reads it as /var/lib/aegis/backups,
# not /app/var/backups — see the panel run line in
# `deploy.md` §3.3 for the exact `-v` flag).
```

### 6.2 No backups in 24 h

```bash
# Trigger a manual backup from the panel UI:
#   Settings → Backups → Run now
#
# If the button returns an error: read the panel log.
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker logs --tail 100 aegis-panel | grep -E 'backup|pg_dump' | tail -20"
```

Common root causes:

- **Panel can't write to `/var/lib/aegis/backups/`** → volume
  mount missing (§6.1).
- **`pg_dump: not found`** → the panel image is missing the
  `postgresql-client` package. This is the **v0.8.15 bug** that
  started the silent-bug chain (see
  `KNOWN_LIMITATIONS.md` §"v0.8.16..v0.8.25 — the silent-bug chain").
  Bounce the panel to the latest image; v0.8.15 is not safe to
  keep running.

### 6.3 Backups present but `_index.json` is empty

The `_index.json` is rebuilt by an operator-side script
(`backups-build-index.py`, not in the repo). Run it from the
operator's machine:

```bash
# Operator-side (NOT on the panel host)
python3 ~/.aegis/scripts/backups-build-index.py \
  --backups-dir /var/lib/aegis/backups/  # via the SSHFS mount
# (or the equivalent path; the script lives in the operator's
# data dir, not in the repo).
```

### 6.4 Backups present, panel UI shows the wrong list

The panel reads its backup list from the same `/var/lib/aegis/backups/`
bind mount. If the on-disk list is correct but the UI shows
something else, the mount is stale inside the panel container
(e.g. a Docker volume was re-created). Bounce the panel:

```bash
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker restart aegis-panel"
```

---

## 7. 🟠 P1 — Secret rotation emergency

**Symptom**: panel logs show `sops: failed to decrypt`,
`age: no identity matched`, or
`envelope: read identity file "/etc/aegis/age.key":
permission denied`. You've rotated the age key on the server
and the panel can't unseal the encrypted env.

### 7.1 Triage (60 s)

```bash
# 1. Is the age key readable by the panel's nonroot user?
ssh aegis-deploy@<prod-host-ip> \
  "sudo ls -la /etc/aegis/age.key"
# Expected: -rw-r----- 65532 65532 /etc/aegis/age.key
# If owned by root:root, the distroless nonroot user (UID
# 65532) cannot read it. Fix per §7.2.

# 2. Can sops + age decrypt the env?
ssh aegis-deploy@<prod-host-ip> \
  "sudo docker exec aegis-panel sops -d /etc/aegis/aegis-env.enc.env 2>&1 | head -5"
# Expected: a few AEGIS_*=… lines.
# If "permission denied" → §7.2.
# If "no identity matched" → §7.3.
```

### 7.2 Permission denied on `age.key`

```bash
# The distroless nonroot user is UID 65532. The age key must
# be owned by that UID and mode 0640.
ssh aegis-deploy@<prod-host-ip> \
  "sudo chown 65532:65532 /etc/aegis/age.key && \
   sudo chmod 0640 /etc/aegis/age.key && \
   sudo docker restart aegis-panel"
```

This is the v0.8.0 → v0.8.9 deployment gotcha — see
`KNOWN_LIMITATIONS.md` §"`docs/RUNBOOKS/deploy.md` §6 sops+age
workflow was misleading".

### 7.3 `age: no identity matched`

The server's age keypair doesn't match the recipient the env was
sealed to. Two paths:

- **You rotated the age key on the server but did not re-encrypt
  the env** → re-encrypt the env on the operator's machine
  (using the NEW server-side public key as the recipient) and
  scp the new `aegis-env.enc.env` to the server. See
  `docs/operator-guide.md` §"Secret rotation" for the full
  procedure.
- **You copied the wrong age key to the server** → re-copy
  `<operator-age-key-path>` to `/etc/aegis/age.key` on the
  server, fix permissions (§7.2), restart the panel.

### 7.4 Decryption works, app still fails

The panel may be reading a stale `aegis-env.enc.env` from
somewhere other than `/etc/aegis/`. Verify the file's
timestamp:

```bash
ssh aegis-deploy@<prod-host-ip> \
  "sudo ls -la /etc/aegis/aegis-env.enc.env /var/lib/aegis/aegis-env.enc.env 2>/dev/null"
# The panel reads /etc/aegis/aegis-env.enc.env. If the timestamp
# there is older than the rotation, re-deploy to refresh.
```

If the timestamp is fresh, the env decryption succeeded but the
app still can't reach the DB / NATS / age-protected webhook
secret. The most common cause is a stale in-memory cache; bounce
the panel.

---

## 8. Release cut rollback (cross-ref to `deploy.md` §1.6)

**When to use this**: dry-run failed (PR #250 §1.6 validation
caught a real problem before any tag was created) OR a real
`release.sh X.Y.Z` cut is mid-flight and something is wrong.

### 8.1 Dry-run failed BEFORE tag creation (the easy path)

If `bash tools/scripts/release.sh X.Y.Z --snapshot` exits
non-zero, NO tag was created, NO commit was made — you're
safe to fix the precondition and re-run:

- **Exit 2 (bad SemVer)** → fix the version argument
  (e.g. `0.9.0-rc.1` if you meant pre-release;
  `release.sh` accepts the form `MAJOR.MINOR.PATCH[-prerelease]`).
- **`error: working tree has uncommitted changes`** → stash
  or commit them. Re-run `--snapshot`.
- **`error: tag vX.Y.Z already exists`** → a prior cut
  attempt succeeded. Verify with `git tag --list vX.Y.Z`. If
  the tag exists, jump to §8.2 (you have a tag to clean up).
- **Other errors** → read `release.sh` output, fix, re-run
  `--snapshot`. The script is non-destructive in `--snapshot`
  mode (zero local mutation, zero network calls).

### 8.2 Real cut succeeded but the image is bad

If `release.sh X.Y.Z` ran, the tag exists on `main`, and the
`release.yml` workflow is running (build → smoke → cosign
re-sign):

- **Smoke step fails** → images are in GHCR but NOT
  cosign-signed. Delete the unsigned manifests via
  `gh api -X DELETE
  /ghcr/qadversif/.../aegispanel/manifests/<digest>` (replace
  `<digest>` with the value from
  `gh api /users/qadversif/packages/container/aegispanel/versions`).
  The git tag itself stays — delete it with
  `git push origin :vX.Y.Z` (and locally with
  `git tag -d vX.Y.Z`). Re-run `release.sh X.Y.Z` after the
  fix is in.
- **Smoke passes but you discover a bug post-release** →
  revert the live server to the previous image. The
  `aegis-panel-prev2` / `aegis-ui-prev2` tags are what
  `deploy.md` §5 rollback expects. See that section for the
  full procedure.

### 8.3 Mid-cut failure (workflow cancelled)

If `release.yml` was cancelled (e.g. you re-ran it, or manually
cancelled a run that was already in progress):

- Find the latest completed image digest in GHCR via
  `gh api /users/qadversif/packages/container/aegispanel/versions?per_page=5`
  (the `aegispanel-ui` endpoint is parallel).
- Either re-run
  `gh workflow run release.yml -f tag=X.Y.Z` to resume, or
  revert as in §8.2.

A cancelled workflow does NOT delete the GHCR manifests it had
already pushed. If you want to start fully clean, delete the
manifests (the same `gh api -X DELETE` call as §8.2 smoke-fail
path) and re-run.

---

## See also

- [`deploy.md`](deploy.md) — PLANNED deploys (release cuts,
  bounces, env rotations, sops+age setup). §1.6 is the
  dry-run validation referenced by §8 above.
- `KNOWN_LIMITATIONS.md` — open issues + the closed
  v0.8.16..v0.8.25 silent-bug chain (the source of the
  "common root causes" calls in §3.2 and §6.2).
- `docs/operator-guide.md` — sops+age secret rotation
  procedure, referenced by §7.3.
- `deploy.md` §3.3 "Start the new panel" — the canonical panel
  container run line (with `-v` flags for backups, age key,
  migrations, known_hosts), referenced by §2.2 and §6.1.
- `deploy.md` §3.5 "Bounce the UI" — the canonical UI bounce
  procedure, referenced by §2.2.
- `deploy.md` §5 "Rollback" — image rollback to `aegis-panel-prev2`,
  referenced by §2.2.
- `tools/scripts/release.sh` — the release cut script
  referenced by §8; `--snapshot` is the dry-run mode.
- Agent memory (operator-side, out of repo):
  `topics/aegis-deploy.md` and `topics/aegis-architecture.md`.
