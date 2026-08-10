---
title: Quickstart
---

# Quickstart

The 5-minute "from a fresh VPS to a panel running" flow. The
[operator guide](../operator-guide) is the long form; this page
is the impatient version.

## Prerequisites

You need:

- A Linux x86_64 VPS (Ubuntu 24.04+ recommended) with `root`
  or `aegis-deploy` SSH access. The v0.8.x canonical path is
  manual `docker run` on the panel host (the Ansible role-based
  path is a v0.5.0-era option that's still maintained but no
  longer the only option).
- Docker 24+ and Docker Compose v2 on the VPS.
- `sops` 3.13+ and `age` 1.1+ on your **local** machine.
- Ansible 9+ on your **local** machine — **only** if you take
  the role-based path. The manual path doesn't need it.
- An HTTPS-capable domain pointed at the VPS (for Caddy + the
  public panel URL).

## 0. Install the local tools

```bash
# macOS
brew install sops age ansible

# Debian / Ubuntu
sudo apt install sops age ansible

# Windows (via winget)
winget install Mozilla.SOPS.Filippo
winget install FiloSottile.age
winget install Ansible.Ansible
```

## 1. Generate the age keypair (one-time)

```bash
age-keygen -o ~/.aegis/age.key
cat ~/.aegis/age.key.pub
# → age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

**Back up `~/.aegis/age.key` offline.** Lose it → lose every
encrypted secret in the repo.

## 2. Update `.sops.yaml` with your public key

```bash
$EDITOR .sops.yaml
# Replace the recipient under &main with your age1... key
git add .sops.yaml
git commit -m "chore(ops): add operator age public key"
```

## 3. Fill in the encrypted secrets

```bash
cp deploy/secrets/secrets.example.yml deploy/secrets/secrets.yml
$EDITOR deploy/secrets/secrets.yml
# Replace REPLACE_ME_* placeholders with real values.
# See deploy/secrets/README.md for the field-by-field guide.
sops --encrypt --in-place deploy/secrets/secrets.yml
git add deploy/secrets/secrets.yml
git commit -m "chore(ops): bootstrap secrets file"
```

## 4. Stage on the target host

```bash
# Encrypted env (at-rest storage on the host)
scp ~/.aegis/aegis-env.enc.env root@panel.example.com:/etc/aegis/
# Age key (the server-side counterpart; needed by the distroless
# container to decrypt webhooks secrets + nodes.ssh_private_key_ciphertext)
scp ~/.ssh/aegis.age.key root@panel.example.com:/etc/aegis/age.key
# On the server, fix the perms for the distroless nonroot UID
ssh root@panel.example.com 'chown 65532:65532 /etc/aegis/age.key && chmod 0640 /etc/aegis/age.key && chmod 0600 /etc/aegis/aegis-env.enc.env'
```

> **v0.8.x note**: the age private key lives on the **operator's
> local machine** (`~/.ssh/aegis.age.key`, mode 0600), not on
> the panel host. The panel host only needs the public
> counterpart to verify signatures, and a copy of the private
> key as `/etc/aegis/age.key` for runtime decrypts of
> `webhook_endpoints.secret` and `nodes.ssh_private_key_ciphertext`.
> The v0.5.0-era `secrets.env` on the host is no longer the
> source of truth — the v0.8.x contract is decrypt-on-operator
> - `docker run -e KEY=VALUE` flags. See
> [`operator-guide.md#v0.8.x-secret-decryption-contract`](../operator-guide.md)
> for the full pattern.

## 5. Decrypt on the operator, ship the panel + UI

```bash
# On your local machine, decrypt the env into a temp file
SOPS_AGE_KEY_FILE=~/.ssh/aegis.age.key \
sops --config ~/.aegis/.sops.yaml -d ~/.aegis/aegis-env.enc.env \
  > /tmp/aegis-env.plain

# Build the -e flag list (a small python or awk one-liner)
# See docs/RUNBOOKS/deploy.md §6.4 for the worked example,
# or the operator's local .tmp-build-env-flags.py script.

# Pull the images on the panel host
ssh root@panel.example.com 'docker pull ghcr.io/qadversif/aegispanel:0.8.14'
ssh root@panel.example.com 'docker pull ghcr.io/qadversif/aegispanel-ui:v0.8.14'

# Run the panel (one shot, with all the -e flags from above)
ssh root@panel.example.com "docker run -d --name aegis-panel --network aegis-net \
  --restart unless-stopped \
  -v /var/lib/aegis/migrations:/app/migrations:ro \
  -v /var/lib/aegis/known_hosts:/var/lib/aegis/known_hosts:ro \
  -v /etc/aegis/age.key:/etc/aegis/age.key:ro \
  -p 127.0.0.1:8080:8080 \
  <the -e flags from the decrypted env> \
  ghcr.io/qadversif/aegispanel:0.8.9"
```

The Ansible-based path is still supported (the roles
`bootstrap_node` + `configure_secrets` + `install_panel` under
`deploy/ansible/roles/` cover the same flow as one-liner
playbook invocations), but the v0.8.x canonical install
is the manual `docker run` path above.

## 6. Smoke test

```bash
# Loopback health check (the only health endpoint in v0.8.x;
# /healthz was a v0.5.0-era alias, removed in v0.8.0)
ssh root@panel.example.com 'curl -fsS http://127.0.0.1:8080/api/v1/health'
# → {"status":"ok","version":"dev"}

# Public panel URL (Caddy is the public ingress; the loopback
# port 8080 is not exposed)
xdg-open https://panel.example.com
```

The default admin login is `admin` + the password you set in
`AEGIS_JWT_SECRET`'s neighbouring admin password field (or
the default `***REMOVED***` if you used the
v0.8.x first-run fixture — change it on first login). The
admin is created via `aegis admin add admin --email <email>
--role super-admin` on the panel host (the aegis binary
on the server).

## 7. Add the first node

```bash
# Option A: Ansible role-based
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/node.yml

# Option B: Manual via the panel UI
# 1. SSH to the node, install sing-box + aegis-agent manually
# 2. In the panel UI: Settings → Nodes → Add
# 3. Paste the operator's SSH key (or the panel's stored key
#    if you ran the v0.8.1+ rotate-panel-key flow)
# 4. The node is marked `online` and the BatchedApplier starts
#    pushing sing-box configs.
```

The node playbook installs sing-box (with the GitHub-API
SHA-256 verification from #123), the aegis-agent, and
fail2ban. After it finishes, register the node in the panel
UI (Settings → Nodes → Add). The panel will start pushing
sing-box configs as soon as the node is marked `online`.

## 8. Configure backups

```bash
# One-shot: take a backup right now
ssh aegis-deploy@panel.example.com \
  'AEGIS_BACKUPS_DIR=/var/lib/aegis/backups \
   AEGIS_POSTGRES_DSN=postgres://aegis:...@127.0.0.1:5432/aegis?sslmode=disable \
   aegis-pg-backup create'

# Cron: nightly at 02:00
cat <<'EOF' | ssh aegis-deploy@panel.example.com 'sudo tee /etc/cron.d/aegis-backup'
SHELL=/bin/bash
PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

AEGIS_BACKUPS_DIR=/var/lib/aegis/backups
AEGIS_POSTGRES_DSN=postgres://aegis:...@127.0.0.1:5432/aegis?sslmode=disable

0 2 * * *  aegis-pg-backup create >> /var/log/aegis/backup.log 2>&1
0 3 * * *  aegis-pg-backup list | jq -r '.[] | select(.status == "ok") | .id' | head -n -30 | xargs -r aegis-pg-backup delete
EOF
```

The `aegis-pg-backup` and `aegis-pg-restore` binaries are in
the panel container (the `aegis-panel` image ships them at
`/usr/local/bin/`). Mount the same env file the panel uses:

```bash
ssh aegis-deploy@panel.example.com \
  'docker exec aegis-panel aegis-pg-backup list | jq'
```

The full restore flow is in the
[operator guide → Restores](../operator-guide#restores--aegis-pg-restore).

## What's next?

- The [operator guide](../operator-guide) — full daily-ops
  reference: rotations, upgrades, observability, common
  pitfalls.
- The [security policy](../security) — the threat model and
  the supply-chain trust.
- The [architecture doc](./architecture) — what the panel
  actually does.

If something in the playbook fails, the most common cause is
the `aegis_secrets_install_method` default (`apt`) on a
non-Debian host. Set it to `download` in `group_vars/all.yml`
or pre-bake `sops` and `age` into your image.
