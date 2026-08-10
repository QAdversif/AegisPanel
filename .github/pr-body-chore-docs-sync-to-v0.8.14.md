Brings all user-facing documentation up to v0.8.14 state.
Touches:

- `README.md` (root) — lead-in updated to v0.8.14
  (was v0.8.12), milestone table now lists v0.8.10
  through v0.8.14 individually, v0.8.x row marked
  `done` (was `in progress`), Status section has
  v0.8.14 + v0.8.13 as the two top entries
  (was v0.8.9 only). Repo layout footer count
  bump for v0.8.13+v0.8.14 packages.
- `docs/README.md` — status table: Backend
  v0.8.12 → v0.8.14; Frontend v0.8.12 → v0.8.14
  plus the `inbound-templates` qualifier; inbound-
  templates row from `🔧 partial` to `✅ shipped`
  (v0.8.13 5-PR plan); new rows for
  eslint cleanup, shadcn-vue RadioGroup,
  audit-3.1 fix chain, v0.8.13 body-field shim
  closure.
- `docs/ROADMAP.md` — adds v0.8.10 / v0.8.11 /
  v0.8.12 / v0.8.13 / v0.8.14 milestone rows
  (one per minor release, matching the v0.8.0-v0.8.9
  shape); the verbose pre-v0.8.13 `v0.8.x` row is
  collapsed to a one-line summary that points to
  the per-minor rows.
- `docs/SECURITY.md` — Supported versions
  v0.8.12 → v0.8.14; cosign `verify` example
  v0.8.9 → v0.8.14; new threat-model row for
  the audit-3.1 fix chain (HttpOnly cookie +
  frontend in-memory only + Caddy CSP); new
  "not designed to defend against" row for
  "XSS payload exfiltrating the refresh token
  (audit-3.1, closed in v0.8.13 + v0.8.14)".
- `docs/operator-guide.md` — `:0.8.12` image
  tags → `:0.8.14` (pull + run commands);
  `aegis_panel_image_tag` example `0.5.0` →
  `0.8.14`; new "v0.8.13 → v0.8.14 upgrade" sub-
  section under "Upgrades" with the rolling-
  upgrade "server before client" pattern
  (drop the body's `refresh_token` field;
  document the previously-undocumented
  `POST /api/v1/auth/logout` endpoint).
- `docs/guide/quickstart.md` — `:0.8.12` image
  tags → `:0.8.14` in the docker pull step.
- `KNOWN_LIMITATIONS.md` — major sync: title
  v0.8.1 → v0.8.14; lead-in rewrites to v0.8.14
  state (the v0.8.13+ migration
  `0021_inbound_templates.sql` is the only schema
  change since v0.8.9; the v0.8.x bucket is fully
  shipped); the `v0.8.1 — currently open` section
  renamed to `v0.8.1 — closed (historical)` with
  a note pointing to the new
  `v0.8.14 — currently open` section at the
  bottom (admin password rotation is the only
  remaining GA-blocker). The new v0.8.14 section
  is the actionable state: it documents the
  `***REMOVED***` public-knowledge
  risk, the rotation path (`aegis admin passwd`
  with the Python `subprocess.Popen` +
  `time.sleep(1.0)` workaround for the
  `bufio.Reader` stdin drain — the canonical
  reference is `~/.aegis/deploy.local.md` "Deploy
  2026-08-09" §2), and the new-password record
  location.

Skipped per the v0.8.x-bucket docs-sync convention:
`docs/RUNBOOKS/deploy.md` (historical v0.8.0 →
v0.8.9 audit trail), `docs/comparison/remnawave.md`
(comparison baseline). The CI markdownlint gate
runs `markdownlint-cli2` against all 29 tracked
`*.md` files and is clean (0 errors).

Pre-pr --docs 0 errors. No backend / openapi / env /
schema changes.
