---
title: Quickstart
---

# Quickstart

The 5-minute "from a fresh VPS to a panel running" flow. The
[operator guide](../operator-guide) is the long form; this page
is the impatient version.

## Prerequisites

You need:

- A Linux x86_64 VPS (Ubuntu 22.04+ recommended) with
  `aegis-deploy` SSH access.
- Docker 24+ and Docker Compose v2 on the VPS.
- `sops` and `age` on your **local** machine.
- Ansible 9+ on your **local** machine.
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
scp deploy/secrets/secrets.yml \
  aegis-deploy@panel.example.com:/etc/aegis/secrets.yml.enc
scp ~/.aegis/age.key \
  aegis-deploy@panel.example.com:/etc/aegis/age.key
ssh aegis-deploy@panel.example.com 'sudo chmod 0600 /etc/aegis/age.key'
```

The encrypted file lands at `/etc/aegis/secrets.yml.enc`; the
age private key lands at `/etc/aegis/age.key` (mode 0600,
owner root). Both paths are the `configure_secrets` role's
defaults; override in `group_vars/all.yml` if your topology
differs.

## 5. Run the panel playbook

```bash
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/panel.yml
```

The playbook runs three roles:

1. `bootstrap_node` — creates the `aegis-deploy` user, base
   packages, the `/etc/aegis/` tree.
2. `configure_secrets` — installs sops+age, decrypts
   `secrets.yml.enc` to `secrets.env` (mode 0600, owner
   `aegis-deploy`).
3. `install_panel` — drops `docker-compose.prod.yml`, pulls
   the panel image, starts the stack.

## 6. Smoke test

```bash
# Loopback health check
ssh aegis-deploy@panel.example.com \
  'curl -fsS http://127.0.0.1:8080/healthz'

# Public panel URL (Caddy is the public ingress; the loopback
# port 8080 is not exposed)
xdg-open https://panel.example.com
```

The default admin login is `admin` + the password you set in
`aegis.admin_password` (decrypted from the secrets file).
Change the password on first login.

## 7. Add the first node

```bash
ansible-playbook -i deploy/ansible/inventories/prod/hosts.ini \
  deploy/ansible/playbooks/node.yml
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
