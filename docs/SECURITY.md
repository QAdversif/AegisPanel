---
title: Security policy
---

# Security policy

This document covers how to report a vulnerability, what Aegis
defends against (and what it does not), and the trust model for
the supply chain. The [operator guide](./operator-guide) covers
the day-to-day installation and operations; this page is the
**threat-model and disclosure** page.

## Reporting a vulnerability

Please **do not** file a public GitHub issue for security
vulnerabilities. Use GitHub's
[private security advisory](https://github.com/QAdversif/AegisPanel/security/advisories/new)
flow:

1. Go to the [Security Advisories](https://github.com/QAdversif/AegisPanel/security/advisories/new)
   page on the AegisPanel repo.
2. Click "New draft security advisory".
3. Fill in the affected version, a short description, and the
   reproduction steps.
4. Submit as a draft; the maintainers will be notified.

Alternatively, email `security@qadversif.com` (the address is a
placeholder; replace with the published security contact before
the public launch). GPG-encrypted reports are accepted; the
public key is on the same advisory page.

The maintainers aim to:

- **Acknowledge** within 72 hours of the report.
- **Triage** within 7 days, with an assessment of severity
  (Critical / High / Medium / Low) and an expected fix window.
- **Coordinate disclosure** with a CVE assignment via GitHub
  Security Advisories. The default disclosure window is 90 days
  from the report, with extensions granted on request when a fix
  is in progress.

Aegis does **not** run a paid bug-bounty program in the v0.8.x
window. Credit in the CHANGELOG is the standard acknowledgement.

## Supported versions

Only the latest minor release receives security fixes. The
project follows semver:

- **v0.8.x** is the current GA target line (v0.8.14 is the
  latest tagged release, 2026-08-10). Security fixes land on
  `main` and ship in the next `v0.8.y` patch release.
- **v0.7.x** and earlier are **not** supported. Operators on
  an older release should upgrade. The pre-#119 secrets surface
  (env vars on the host, no sops+age indirection) had known
  limitations that were fixed in v0.5.0. The pre-#182 `auth.me`
  bug (returned 500 on pg backend) was fixed in v0.8.2. The
  pre-#188 manual agent-bearer rotation was automated in v0.8.7.

For the v0.8.x → v1.0.0 window, security fixes are
backwards-compatible: no breaking changes to the public API,
the data model, or the on-disk format. Operators can roll
forward without a migration.

## Threat model

Aegis is designed to defend against:

| Threat | Mitigation |
| --- | --- |
| **Operator's VPS compromised** (root on the panel host) | The panel container is distroless + nonroot (uid 65532) and the age key bind mount is read-only (chown 65532:65532, chmod 0640 so the distroless user can read it; not 0600 root — that boot-loops the panel with "permission denied" on the age key). The panel itself has no shell. A container escape is the only path to a privileged shell. |
| **Network sniffing between panel ↔ node** | The agent uses TLS to the panel (Let's Encrypt cert validated by the agent). Configs are signed; the agent rejects unsigned configs. |
| **A malicious sing-box tarball** | The `install_singbox` role looks up the SHA-256 from the GitHub Releases API at install time and verifies the download with `get_url checksum:`. A tampered tarball fails the install. |
| **An attacker with the panel's DB read access** | All admin passwords are argon2id-hashed (PHC string `$argon2id$v=19$m=...,t=...,p=...$salt$hash`); subscription tokens are opaque random hex. The JWT secret is the only plaintext credential in the DB and is operator-rotatable. |
| **A stale `aegis-agent` bearer token** (agent regenerated its bearer out-of-band) | `nodes.Service.RefreshAgentBearer` (v0.8.7) decrypts the stored panel SSH key, SSHes into the node, reads `/etc/aegis/agent.env`, parses `AEGIS_AGENT_BEARER`, and updates `nodes.agent_bearer`. The BatchedApplier's `Apply` path (v0.8.8) auto-invokes this on a 401 from `POST /v1/apply` — the operator does not need to act. One retry only; 500/404 do NOT trigger refresh. |
| **A backup-tampering attacker** | Every backup has a sidecar `<id>.dump.gz.sha256` file in `sha256sum -c` format. Operators can `sha256sum -c` the sidecar before a restore. |
| **A typo in the operator's secrets file** | The `configure_secrets` role runs a round-trip decrypt after writing the plaintext, catching corruption. The role fails loudly on a mismatch, not silently. |
| **A leaked CI secret** | The CI does not decrypt. The CI does not have access to the operator's age private key. CI never holds plaintext secrets. |
| **A tampered container image from GHCR** | Every release re-signs and re-verifies via cosign (v0.8.9, PR #190). The release workflow's `Settle GHCR after push` step (30s) handles `latest` tag-mutation drift; the re-sign step uses the build's recorded digest and emits a fresh transparency-log entry; the `cosign verify` step uses the same OIDC flags a consumer would. The trust anchor is `--certificate-oidc-issuer https://token.actions.githubusercontent.com` — verify with the same flag the release workflow uses. |
| **An XSS payload in the admin UI** (the audit-3.1 fix chain, v0.8.13 + v0.8.14) | Three layers of defense-in-depth. (1) HttpOnly refresh cookie (PR #214, server-side): the refresh token is set as a `Set-Cookie: aegis_rt=...; HttpOnly; SameSite=Strict; Path=/; Max-Age=2592000; Secure` header on `/auth/login` and `/auth/refresh`; XSS cannot exfiltrate it (HttpOnly is unreachable from JS). The body field that v0.8.13 emitted for one release as a backwards-compat shim is closed in v0.8.14 (PR #217). (2) Frontend `withCredentials` (PR #215): the access token is in-memory only (Pinia `ref`); `withCredentials: true` on the axios instance attaches the cookie to every `/api/v1` request; the previous `localStorage` 'aegis.tokens' surface is deleted. (3) Strict CSP (PR #216): `default-src 'self'`, `script-src 'self'`, `style-src 'self' 'unsafe-inline'` (Vue 3 runtime CSS-in-JS trade-off), `img-src 'self' data:`, `connect-src 'self'`, `frame-ancestors 'none'`, `base-uri 'self'`, `form-action 'self'`, `object-src 'none'` — applied to the `/s3cr3t-p4n3l-*/*` admin path in `deploy/caddy/Caddyfile.panel`. An injected `<script>` cannot phone home, exfiltrate data, or rewrite the DOM. The 15-min access token in memory remains the residual risk; rotation+chain-revocation from the v0.8.10+ per-user credential filter keeps the actual exposure window at one use. |

Aegis is **not** designed to defend against:

- **A compromised operator workstation.** If an attacker has
  your age private key, the secrets are theirs. Back the key up
  offline.
- **A compromised git remote.** The encrypted secrets file is
  public-readable by design; the security boundary is the age
  key.
- **A nation-state adversary with TLS-MITM capabilities.** The
  panel trusts the public CA system (Let's Encrypt). Operators
  in adversarial jurisdictions should evaluate this against
  their threat model.
- **Side-channel attacks on the panel's Go runtime.** Aegis is
  a standard Go service; we do not audit the runtime for
  Spectre-class vulnerabilities. Operators with high-assurance
  requirements should pin the Go toolchain version.
- **A malicious panel maintainer.** v0.8.9 adds cosign
  re-sign + verify on every release, which closes the
  "trust the maintainer" gap: every consumer can `cosign
  verify --certificate-identity-regexp "https://github.com/QAdversif/AegisPanel/.*"
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
  ghcr.io/qadversif/aegispanel:0.8.14` and the signature is
  verifiable against the GitHub Actions OIDC issuer. The
  trust model is now: OIDC issuer + OPA signature, not
  "trust the maintainer".
- **An XSS payload in the admin UI that exfiltrates
  the refresh token** (the audit-3.1 finding, closed
  in v0.8.13 + v0.8.14, PRs #214 / #215 / #216 /
  #217). The defense-in-depth chain: HttpOnly cookie
  plus frontend in-memory only and strict CSP. See the
  audit-3.1 row above for the full description. The
  body's `refresh_token` field is closed in v0.8.14
  (was a v0.8.13 backwards-compat shim) so a
  pre-v0.8.14 client cannot exfiltrate even if it
  tries.

## Cryptography

### JWT secret

- Algorithm: HS256 (HMAC-SHA-256). The key is 48 random bytes
  from `/dev/urandom`, base64-encoded. Operators can change
  the algorithm to RS256 with a per-tenant keypair; the
  supported config lives in `internal/auth/middleware.go`.
- Storage: only in the sops+age secrets file. Never in env
  vars on the operator's local machine; never in CI; never in
  the repo plaintext.
- Rotation: see the [operator guide](./operator-guide#rotate-the-jwt-secret).

### Age keypair (sops+age)

- Algorithm: X25519 + ChaCha20-Poly1305 (age's default).
- The private key is `mode 0600`, owner `root`, on the panel
  host. The public key is in `.sops.yaml` (repo root) and in
  every encrypted file's `sops.age.recipient` field.
- **The private key is irreplaceable.** Lose it → lose every
  encrypted secret → the panel cannot start. The operator
  workflow backs it up offline (encrypted USB, password
  manager, paper in a safe).

### Sing-box tarball

- The sing-box project does **not** publish detached GPG
  signatures or a `SHA256SUMS` file on its GitHub releases.
  The v0.4.0-c hardcoded digest is gone in v0.5.0 (#123); the
  install role now queries the GitHub Releases API at install
  time and pulls the digest from the `assets[].digest` field
  (format `sha256:<hex>`).
- The trust model is therefore: **the GitHub API response is
  signed by GitHub via TLS + the standard `X-GitHub-...`
  headers.** This is the same trust model as `npm install` or
  `go get`-from-GitHub; it is not a stronger guarantee than
  "trust GitHub".
- For higher-assurance environments, mirror the sing-box
  tarball to an internal artifact store and pin
  `aegis_singbox_release_base_url` in `group_vars/all.yml`. The
  role downloads from the override URL and verifies against
  the GitHub-API-derived digest (the operator can verify the
  mirror's digest matches the upstream's in a one-off
  procedure, then trust the mirror).

### Backup integrity

- Every backup is a `pg_dump -Fc | gzip` stream with a sidecar
  `<id>.dump.gz.sha256` in `sha256sum -c` format.
- The CLI `aegis-pg-restore` does not re-verify the sidecar
  (the panel's own `backups.Service` does, on the operator's
  behalf). To re-verify out-of-band:

  ```bash
  cd /var/lib/aegis/backups
  sha256sum -c bck_2026_07_29_xxx.dump.gz.sha256
  ```

### Container isolation

- **Base image**: distroless (`gcr.io/distroless/static:nonroot`).
  No shell, no package manager, no `/bin/true`. A compromised
  panel process cannot `exec /bin/sh`.
- **User**: `nonroot` (uid 65532, gid 65532).
- **Filesystem**: read-only root. The only writable mount is
  the volume for `/var/lib/aegis` (reserved for v0.5.x
  backups).
- **Port**: `127.0.0.1:8080:8080` (loopback only). Caddy is
  the public ingress.
- **Secrets mount**: `/etc/aegis/secrets.env` bind-mounted
  read-only via `env_file:` (the `required: true` directive
  makes the container refuse to start without the file).
- **Network**: `extra_hosts: ["host.docker.internal:host-gateway"]`
  so the panel can reach host-side Postgres / Redis / NATS.
  No `network_mode: host`. No privileged mode.

### Privilege boundaries

- **`aegis-deploy` (panel host)** — a dedicated Linux user
  with passwordless sudo for the specific commands the
  playbooks run (in v0.5.0 this is `NOPASSWD: ALL`; the
  Phase 2 follow-up restricts to a command allowlist).
- **`aegis-agent` (node host)** — runs as its own systemd
  unit, dedicated `aegis-agent` user. Cannot write anywhere
  except `/etc/sing-box/config.json` and the journal.
- **`_sing-box` (node host)** — Debian package convention. The
  systemd unit's `User=` and `Group=` are pinned to this user.
  Cannot write outside `/var/log/sing-box` and the systemd
  runtime dir.

The data services (Postgres, Redis, NATS) are operator-managed
and outside Aegis' trust boundary. The panel's `aegis.*` env
vars point at the external services; the secrets file holds
the credentials.

## Supply chain

### Docker images

- **Source**: GitHub Actions (`release.yml`) pushes to
  `ghcr.io/qadversif/aegispanel` and `ghcr.io/qadversif/aegispanel-ui`
  on every `v*` tag.
- **Tags**: the release pipeline emits `[X.Y.Z, X.Y, latest]`
  (with `latest` skipped for prerelease tags, per
  `flavor: latest=auto`).
- **Signing**: cosign re-sign + verify on every release (v0.8.9,
  PR #190). After the first `cosign sign`, the workflow waits
  30s (let GHCR OIDC settle), then re-signs each image and
  runs `cosign verify` with the same OIDC flags a consumer
  would use. The transparency-log entry is keyed to the
  actual digest the build published. Verify with:
  ```bash
  cosign verify --certificate-identity-regexp \
    "https://github.com/QAdversif/AegisPanel/.*" \
    --certificate-oidc-issuer \
    https://token.actions.githubusercontent.com \
    ghcr.io/qadversif/aegispanel:0.8.9
  ```
- **Vulnerability scanning**: the CI runs `trivy` on every
  build. Critical / High CVEs fail the build. The
  `.trivyignore` file lists accepted false positives (e.g.
  the CVE on the `latest` tag's base image, when the patched
  image is still propagating). `pnpm-audit` (frontend) and
  `govulncheck` (Go) gate the build on dependency CVEs.

### Panel / agent binaries

- Built from the same repo, same release pipeline. Same
  trust model as the Docker images.

### Sing-box binary

- Pulled from `github.com/SagerNet/sing-box/releases`. SHA-256
  verified via the GitHub Releases API. See the
  [Cryptography → Sing-box tarball](#sing-box-tarball) section
  above for the trust model.

### sops+age

- `sops` is downloaded from
  `github.com/getsops/sops/releases` (the v0.5.0
  `configure_secrets` role pins the version in
  `group_vars/all.yml`).
- `age` is downloaded from
  `github.com/FiloSottile/age/releases` (the v0.5.0 role
  pins the version).

Both downloads are over TLS. Neither is checksum-verified at
install time (a v0.5.x+ follow-up — pin the SHA-256 in
`group_vars/all.yml` once the role supports it).

## What to do if you suspect a compromise

1. **Rotate everything.** The age key (via `sops updatekeys
   --yes`), the JWT secret, the admin password, the agent
   bearer, the Postgres password. Order matters: the
   compromised key was used to encrypt the new secrets too, so
   the rotation is meaningful only after the new age key is
   in place.
2. **Pull the latest backup off-site** (if you haven't
   already). Don't restore it yet — you want a forensic
   snapshot of the compromised state.
3. **File a security advisory** with the timeline (when you
   first suspected, what the indicators were). The
   maintainers can coordinate a CVE if the root cause is
   in-panel.
4. **Rebuild.** Re-provision the VPS, re-run the panel
   playbook from a freshly-generated age keypair, restore
   from a pre-compromise backup.

## Where to next?

- [Operator guide](./operator-guide) — the install / daily-ops
  / disaster-recovery flow.
- [Secrets workflow](../deploy/secrets/README.md) — the
  sops+age flow, step by step.
- [Architecture](./guide/architecture) — the full design
  document.
