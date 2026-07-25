# feat(ansible): install_singbox role + 0o644 fix to agent writeAtomic

## What this PR does

Adds the `install_singbox` Ansible role and wires it into
`playbooks/node.yml`. This is the third (and final) v0.4.0-mvp-batched
sub-PR. With this PR, an operator can run `playbooks/node.yml` against
a fresh Debian/Ubuntu box and end up with:

- Caddy reverse proxy (decoy + secret admin paths)
- fail2ban (SSH + panel-login jails)
- **sing-box installed, configured, and running** (this PR)
- aegis-agent installed, registered with the panel, and ready
- decoy site rendered

End-to-end the panel can now render a sing-box config, push it through
the BatchedApplier, and have sing-box actually run it.

This PR also fixes a real bug surfaced by the new role: the
aegis-agent's `writeAtomic` used mode 0o640 which would lock sing-box
out of its own config (sing-box runs as unprivileged `_sing-box`, not
in the root group). Bumped to 0o644 with a thorough comment about why
the world-read bit is harmless in a 0750 directory.

## Files changed

### New role: `deploy/ansible/roles/install_singbox/`

- `defaults/main.yml` (108 lines) — pins sing-box 1.14.0-beta.2
  (matches the panel's `ProviderVersion` from #92), with the SHA-256
  hardcoded (sing-box releases do not publish sidecar `.sha256` files;
  the digest is in the GitHub API asset metadata). Defaults for
  user/group (`_sing-box`), config dir (`/etc/sing-box`), log dir
  (`/var/log/sing-box`), binary path (`/usr/local/bin/sing-box`),
  release base URL.

- `tasks/main.yml` (174 lines) — 8 tasks:
  1. Refuse unsupported architectures (only x86_64 + aarch64)
  2. Refuse missing SHA-256 pin (defensive: avoid silent
     no-verify installs)
  3. Create `_sing-box` system group + user (no shell, no home)
  4. Create `/etc/sing-box` (root:sing-box, 0750) and
     `/var/log/sing-box` (`_sing-box:_sing-box`, 0750)
  5. Compute per-arch tarball name (amd64 / arm64, with optional
     `-glibc` / `-musl` suffix)
  6. Download with `get_url` + SHA-256 checksum verification
  7. Unpack ONLY the binary (drop the .db files; v0.4.0-c does not
     use GeoIP rule_sets)
  8. Install the systemd unit, reload daemon, enable + start

- `files/sing-box.service` (60 lines) — `Type=simple` unit (the
  sing-box 1.14.0-beta.2 default; `Type=notify` is opt-in via
  `-enable-experimental` and is not used in v0.4.0-c). Runs as
  `_sing-box`, `ExecStart=/usr/local/bin/sing-box run -c
  /etc/sing-box/config.json`, `ExecReload=/bin/kill -USR1 $MAINPID`
  (the standard sing-box reload signal), hardening via
  `ProtectSystem=strict` + `ReadWritePaths=/etc/sing-box
  /var/log/sing-box` + `NoNewPrivileges` + `AmbientCapabilities=
  CAP_NET_BIND_SERVICE` (for operators running on 80/443). `LimitNOFILE
  =infinity` so a busy node does not hit `too many open files`.

- `handlers/main.yml` (14 lines) — `reload systemd` only. A `restart
  sing-box` handler is intentionally NOT included: the agent reloads
  sing-box via `systemctl reload` (USR1 signal) and re-runs of the
  role should be idempotent (no service restart on a no-op).

### Modified files

- `deploy/ansible/playbooks/node.yml` — added `install_singbox` to the
  role list, between `install_fail2ban` and `install_agent`. The
  ordering matters: `install_agent` writes `/etc/aegis/agent.env` with
  `AEGIS_AGENT_SINGBOX_CONFIG_PATH=/etc/sing-box/config.json` (set in
  #93), so the directory must exist before the agent starts writing
  to it. The role creates the directory at install time, so the
  ordering (singbox before agent) makes the env-var reference
  always-valid.

- `backend/cmd/aegis-agent/apply.go` — `writeAtomicConfigPerm`
  bumped from 0o640 to 0o644. Doc comment rewritten to explain why
  world-readable is fine in a 0750 directory (only root and sing-box
  can traverse, so the world bit is moot). The header doc comment in
  the package (the "File permissions" section) is also rewritten to
  reference the v0.4.0-c `_sing-box` user and link to the new
  constant.

- `backend/cmd/aegis-agent/apply_test.go` — `TestWriteAtomic_BasicRoundTrip`
  updated to assert 0o644 instead of 0o640 (the test runs on
  non-Windows, where `os.Stat().Mode().Perm()` reports the real mode).

## Why hardcoded SHA-256 (and the v0.5.0 upgrade path)

sing-box publishes release artifacts via GitHub Releases, but the
release page does NOT ship a sidecar `.sha256` file (confirmed by
HTTP 404 on the .sha256/.SHA256SUMS/sha256sum.txt paths for
v1.14.0-beta.2). The SHA-256 is available only in the GitHub API
response, in the `assets[].digest` field.

Two ways to verify the download:

  A. **Hardcode the digest in the role defaults** (this PR). The
     operator bumps `aegis_singbox_version` AND
     `aegis_singbox_sha256` in lockstep. The role fails fast (assert
     + checksum mismatch) if they get out of sync. Risk: a
     compromised GitHub release would not be detected, but SagerNet's
     verified GPG commit signing (visible on the GitHub release
     page) makes this attack hard.
  B. **Runtime fetch via the GitHub API** (v0.5.0 work). Requires an
     API token (rate-limited), increases role complexity, harder to
     test in CI.

Option A is the v0.4.0-c choice. The role's defaults include a
clear comment pointing to the v0.5.0 upgrade path.

## Why drop the GeoIP/GeoSite .db files from the tarball

The sing-box tarball ships `sing-box-geoip.db` and
`sing-box-geosite.db` alongside the binary. v0.4.0-c does not
configure GeoIP-based `rule_set` in the rendered config (the
panel's singbox provider targets inbound-only proxies for the
MVP). Leaving the .db files in `/usr/local/bin` pollutes the
directory; they would belong in `/var/lib/sing-box/` if/when GeoIP
rules ship. Skipping them is the safe default — the operator can
`mv` them later if GeoIP rules are added.

The role's `unarchive` uses `--wildcards "sing-box-*/sing-box"` to
extract ONLY the binary; the .db files stay in the tarball on disk
until the role removes it via the `Remove the downloaded tarball`
task. No files left behind.

## Why `Type=simple` and not `Type=notify`

sing-box 1.13.x added sd_notify support for `Type=notify`, but
1.14.0-beta.2 still defaults to simple mode. The notify code path
is opt-in via the `-enable-experimental` build flag. We pin to
`Type=simple` for v0.4.0-c; v0.4.0-d or v0.5.0 can switch to notify
once 1.14.0 GA ships. With `Type=simple`, `systemctl reload
sing-box` (which the agent invokes as the reload command) sends
USR1 to the sing-box process, which is the canonical sing-box
reload signal — works in all sing-box versions.

## Why a permissive file mode on the config (0o644)

The original v0.4.0-b PR (#93) used 0o640 with the rationale that
"sing-box needs to be in the root group to read the config". With
the v0.4.0-c install_singbox role running sing-box as
unprivileged `_sing-box` (Debian package convention), that user is
NOT in the root group, so 0o640 would lock sing-box out of its own
config.

The fix is to bump the perm to 0o644. The world-read bit is
effectively a no-op because the parent directory is mode 0750 owned
by root:sing-box — only root and the sing-box group can list or
traverse the directory in the first place. The on-disk secrets in
the config (per-user VLESS/VMess passwords in the inbounds) are
known to the end user anyway (they use them to connect); there are
no panel-side secrets in the file.

The role's `/etc/sing-box` is mode 0750 root:sing-box, so the file
is functionally only readable by root and sing-box. 0o644 is just
the cleanest way to express that.

## Verification

Local:

- `go test ./cmd/aegis-agent` — all 14 tests pass, including the
  updated `TestWriteAtomic_BasicRoundTrip` (0o644 perm check)
- `golangci-lint v2 --config backend/.golangci.yml -tags=integration`
  — 0 issues
- `python -c "import yaml; yaml.safe_load(...)"` for the role's
  YAML files — all parse OK (the systemd unit file is INI-style,
  not YAML, so it's not in the YAML parse check)

CI:

- The `ansible` CI job runs `ansible-lint` against the role. The
  project's `.ansible-lint` file already excludes several rules
  (var-naming pattern, name conventions, schema[meta]) that would
  otherwise fire on a Phase 0/1 role. The role follows the same
  conventions as `install_caddy` and `install_fail2ban` which are
  known to pass the existing lint config.
- The role is also exercised by the `containers` CI job (a
  docker-build check that the Dockerfile + Ansible playbook still
  build). No container changes in this PR, so this is a
  cross-check that the playbook structure is valid.

## Out of scope (deferred)

- `sing-box` GeoIP rule_sets — v0.5.0+ (when the panel's rule_set
  editor lands)
- GPG signature verification of the sing-box tarball — v0.5.0+
- GitHub-API-based SHA-256 fetch — v0.5.0+ (replaces the hardcoded
  digest)
- `Type=notify` for the sing-box unit — v0.5.0+ (after 1.14.0 GA)
- Per-node metrics (CPU, memory, sing-box goroutine count) in
  `/v1/stats` — future
- mTLS replacement for the bearer-secret gate — v1.1.0+

## Refs

- ARCHITECTURE.md §7.5 (Apply pipeline)
- #92 (v0.4.0-a) — BatchedApplier + real Apply transport on the
  **panel** side
- #93 (v0.4.0-b) — real `/v1/apply` on the **agent** side
- v0.3.0 agent (validation-only stub): commit `d1000de`
- v0.3.0 install_agent role: `deploy/ansible/roles/install_agent/`
- sing-box 1.14.0-beta.2 release: https://github.com/SagerNet/sing-box/releases/tag/v1.14.0-beta.2
- sing-box 1.14.0-beta.2 API: https://api.github.com/repos/SagerNet/sing-box/releases/tags/v1.14.0-beta.2
