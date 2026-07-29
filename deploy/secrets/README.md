# `deploy/secrets/` — sops+age encrypted secrets

This directory holds the encrypted form of the
secrets the panel needs at boot. Plaintext
**never** lives in the repo; only the sops-encrypted
form is committed.

## Files

| File | Committed? | Purpose |
|---|---|---|
| `secrets.example.yml` | yes (encrypted) | Schema reference + rotation example. **Decrypt to read.** |
| `secrets.yml` | **no** (gitignored) | The real secrets. Encrypted in place after the operator fills in real values. |
| `.sops.yaml` | n/a (at repo root) | sops config: defines which files are encrypted and for which recipient. |
| `.gitignore` | yes | Bans plaintext `secrets.yml`. |

## Operator workflow

### 1. Generate the age keypair (one-time)

```bash
age-keygen -o ~/.aegis/age.key
# Output includes:
#   Public key: age1xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
# Save this public key. The private key is in
# ~/.aegis/age.key — back it up; losing it means
# losing every encrypted secret in the repo.
```

### 2. Update `.sops.yaml` with your public key

Edit the repo-root `.sops.yaml` and replace the
`&main` value under `creation_rules` with your public
key. Commit the change.

### 3. Decrypt the example to see the schema

```bash
export SOPS_AGE_KEY_FILE=~/.aegis/age.key
sops --decrypt deploy/secrets/secrets.example.yml
```

This prints the plaintext schema (placeholder values).
Note the structure — what keys exist, what the
operator-side `aegis.user` field expects, etc.

### 4. Generate real values

| Field | How to generate |
|---|---|
| `aegis.jwt_secret` | `head -c 48 /dev/urandom \| base64` |
| `aegis.admin_password` | `openssl rand -base64 18` |
| `aegis.postgres_password` | `head -c 32 /dev/urandom \| base64` |
| `aegis.agent_bearer` | `head -c 32 /dev/urandom \| base64` |
| `panel_path.admin` | `head -c 6 /dev/urandom \| xxd -p` |
| `panel_path.sub` | same |
| `dev.singbox.sha256` | `curl -sL https://github.com/SagerNet/sing-box/releases/download/v1.14.0-beta.2/sing-box-1.14.0-beta.2-linux-amd64.tar.gz \| sha256sum \| awk '{print $1}'` |

### 5. Copy the example, fill in real values

```bash
cp deploy/secrets/secrets.example.yml deploy/secrets/secrets.yml
# Edit deploy/secrets/secrets.yml, replacing the
# REPLACE_ME_* placeholders with the values from step 4.
# The file is now plaintext — do NOT commit it before
# step 6.
```

### 6. Encrypt in place

```bash
sops --encrypt --in-place deploy/secrets/secrets.yml
# The file is now sops-encrypted in place. The plaintext
# is gone. `cat deploy/secrets/secrets.yml` shows the
# #ENC[AES256_GCM,...] header lines.
```

### 7. Commit and push

```bash
git add deploy/secrets/secrets.yml
git commit -m "chore(ops): rotate secrets via sops+age"
git push
```

### 8. On the target host: copy the age key and run the role

```bash
scp ~/.aegis/age.key aegis-deploy@<host>:/etc/aegis/age.key
ssh aegis-deploy@<host> sudo chmod 0600 /etc/aegis/age.key
# Then in your local checkout, run the Ansible play:
ansible-playbook -i inventory/prod playbooks/deploy.yml \
  --tags configure_secrets
```

The role writes `/etc/aegis/secrets.env` (mode 0600,
owner `aegis-deploy`). The panel container can then
mount this file at `/run/aegis/secrets.env` and read it
via `--env-file`.

## Rotation

To rotate any secret:

1. `sops --decrypt --in-place deploy/secrets/secrets.yml`
2. Edit the value
3. `sops --encrypt --in-place deploy/secrets/secrets.yml`
4. Commit
5. Re-run `configure_secrets` Ansible role
6. Restart the panel container

The age private key itself can be rotated via
`sops updatekeys --yes deploy/secrets/secrets.yml`
after updating `.sops.yaml` with the new public key.

## SECURITY

- The age private key is the **only** decryption
  capability. Treat it like an SSH private key.
  Back it up. Encrypt the backup.
- Never commit `secrets.yml` before encryption. The
  `.gitignore` blocks it, but a `git add -f` would
  override that — review staged changes carefully.
- CI's gitleaks detector watches for any committed
  file matching the `generic-api-key` / `age` rules
  and will fail the docs job if a private key leaks.
- The example public key in `.sops.yaml` is
  intentionally a throwaway — it has a matching
  private key on the original author's machine only,
  so an attacker who clones the repo cannot decrypt
  even the example. Replace it with your own.
