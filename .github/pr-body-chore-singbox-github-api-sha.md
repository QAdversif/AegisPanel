<!--
This file is the PR body for #123. It is committed
alongside the code so the body is part of the PR's
git history (the `gh pr create --body-file` path
mirrors the `git log` PR for posterity).
-->

# chore(ops): install_singbox — runtime SHA-256 via GitHub Releases API

Replace the v0.4.0-c hardcoded SHA-256 default with a
runtime fetch through the GitHub Releases API. Bumping
`aegis_singbox_version` is now a one-line change in
`group_vars/all.yml`; the role looks up the matching
`assets[].digest` and uses it as the `get_url
checksum:` argument.

## What this PR ships

- `deploy/ansible/roles/install_singbox/defaults/main.yml`:
  removed `aegis_singbox_sha256`; added
  `aegis_singbox_release_api_url` (default
  `https://api.github.com/repos/SagerNet/sing-box/releases/tags/v{{ version }}`)
  and `aegis_singbox_release_api_token` (optional,
  Bearer token for rate-limit headroom on busy CI
  matrices).
- `deploy/ansible/roles/install_singbox/tasks/main.yml`:
  replaced the `Refuse to run without a SHA-256 pin`
  assert with two new tasks that run after the
  per-arch tarball name is computed:
  1. `Look up the sing-box SHA-256 via the GitHub
     Releases API` — `ansible.builtin.uri` GET to
     the configured `aegis_singbox_release_api_url`,
     with `Accept: application/vnd.github+json`,
     `X-GitHub-Api-Version: 2022-11-28`, and an
     optional `Authorization: Bearer ...` header
     (3 retries, 5s delay, expected status 200).
  2. `Extract the SHA-256 of the target tarball
     from the API response` — filters
     `aegis_singbox_release.json.assets` by
     `name == aegis_singbox_tarball`, takes the
     `digest` field, strips the `sha256:` prefix
     with `regex_replace`, fails with a clear
     "no asset" error if the arch is missing
     for the version.
  The rest of the pipeline (per-arch name, download,
  unpack, systemd unit, enable) is unchanged.
- `docs/guide/getting-started.md`: new `Operator
  quickstart (v0.5.0+)` section that walks the
  two-step `playbooks/panel.yml` + `playbooks/node.yml`
  install flow and points the operator at the
  sops+age indirection from #119.

## What this PR does NOT ship

- **GPG / SHA256SUMS detached signature
  verification.** The original scope included a
  detached signature check, but research during
  this PR showed that SagerNet does NOT publish
  `SHA256SUMS` or detached GPG/minisign signatures
  for sing-box GitHub releases. The only integrity
  metadata is the per-asset `digest` field in the
  API JSON. The trust model is therefore the GitHub
  API response itself (authenticated by the
  standard `X-GitHub-...` headers and TLS).
  Cosign signing of our own Docker images (panel +
  agent) is the v0.5.x equivalent for the panel /
  agent supply chain and is a separate, future PR.

## Operator workflow

The role's external contract is unchanged except
for the removed default. Operators on a clean
`group_vars/all.yml` need no change; operators
who had a custom `aegis_singbox_sha256` override
should drop it (the variable is no longer read).

To bump the sing-box version, edit one line:

```yaml
# group_vars/all.yml
aegis_singbox_version: "1.14.0-beta.2"  # bump freely
```

The role will:

1. Hit the GitHub Releases API for the new tag.
2. Find the asset whose name matches
   `sing-box-{{ version }}-linux-{{ arch }}{{-glibc|-musl}}.tar.gz`.
3. Take the `digest` field, strip the `sha256:`
   prefix, and pass the hex to `get_url checksum:`.
4. Fail with "no asset" if the version does not
   ship the requested arch (e.g. the v0.5.0
   release notes will note when this is the case).

For hermetic / air-gapped operators, point
`aegis_singbox_release_base_url` and
`aegis_singbox_release_api_url` at a local mirror
that serves the same JSON shape. Or stay on the
v0.4.0-c hardcoded-hash flow by pinning the role
to a v0.4.0 tag.

## Verification

Local:

```bash
# Syntax check (no ansible runtime needed)
python -c "import yaml; yaml.safe_load(open('deploy/ansible/roles/install_singbox/defaults/main.yml')); yaml.safe_load(open('deploy/ansible/roles/install_singbox/tasks/main.yml'))"

# Defaults render correctly
ansible -i deploy/ansible/inventories/local/hosts.ini \
  -m debug -a 'msg={{ aegis_singbox_release_api_url }}' \
  all

# ansible-lint runs in CI
ansible-lint deploy/ansible/roles/install_singbox/
```

The CI matrix runs the same playbook on the
Ubuntu + Debian test hosts and asserts the
service is `active (running)` after the
install. v0.5.0 will add a test host with
rate-limit mocked to confirm the Bearer auth
path; that is a v0.5.x follow-up.
