# feat(agent): real /v1/apply writes config to disk and reloads sing-box

## What this PR does

Makes the aegis-agent side of the Apply pathway real. Until now, the agent
just validated the JSON envelope and ACKed; the rendered sing-box config
never landed on disk, and sing-box never reloaded. This PR closes that gap
so the panel's BatchedApplier (landed in #92) can actually drive
configuration changes end-to-end.

After this PR:

1. Panel flushes accumulated Delta events through the BatchedApplier
2. BatchedApplier POSTs the rendered sing-box config to the agent
3. Agent atomically writes the config to
   `AEGIS_AGENT_SINGBOX_CONFIG_PATH` (default `/etc/sing-box/config.json`)
4. Agent runs `AEGIS_AGENT_SINGBOX_RELOAD_CMD` (default
   `systemctl reload sing-box`) with a 5s timeout
5. Agent returns 202 Accepted with `reloaded: true` + reload wall-clock
   duration; BatchedApplier marks the flush done

If the reload exits non-zero, the agent returns 500. The new config is
on disk (atomic write succeeded), but sing-box is still running the old
config — the BatchedApplier will retry on the next window. Operators see
the error in the panel log; rolling back the rename on reload failure
is a future enhancement (out of scope for v0.4.0-b).

## Files changed

- `backend/cmd/aegis-agent/apply.go` (new, 360 lines) — extracted
  `applyConfig` (HTTP handler body), `writeAtomic` (atomic write
  helper), `runReload` (subprocess runner). The HTTP handler in
  `main.go` is now a thin wrapper that calls `applyConfig`.
- `backend/cmd/aegis-agent/apply_test.go` (new, 470 lines) — 14 tests
  covering happy path, reload failure, reload timeout, write failure,
  non-object config rejection, atomic replacement of existing file,
  cleanup of temp files on failure, status endpoint surfacing
  `lastApplyISO` after apply, and direct unit tests for `writeAtomic`
  and `runReload`.
- `backend/cmd/aegis-agent/main.go` — `handleApply` body moved to
  `apply.go`; `run()` reads the four new env vars; top doc comment
  rewritten to reflect v0.4.0-b reality; old `applyRequest` /
  `applyResponse` removed (replaced by `applyEnvelope` /
  `applyResponse` in `apply.go`).
- `backend/cmd/aegis-agent/main_test.go` — `TestApply_AcceptsValidConfig`
  now sets apply globals (target path + reload command) so it exercises
  the new write+reload path; imports `filepath` and `time`.
- `deploy/ansible/roles/install_agent/files/aegis-agent.service` —
  `ReadWritePaths` extended with `/etc/sing-box` so the agent can write
  the rendered config there under `ProtectSystem=strict`.
- `deploy/ansible/roles/install_agent/defaults/main.yml` — four new
  defaults: `aegis_singbox_config_path`, `aegis_singbox_reload_cmd`,
  `aegis_singbox_reload_timeout`, `aegis_apply_max_bytes`.
- `deploy/ansible/roles/install_agent/templates/agent.env.j2` — the
  four new env vars written to `/etc/aegis/agent.env`.

## New env vars (agent-side)

| Var | Default | Purpose |
| --- | --- | --- |
| `AEGIS_AGENT_SINGBOX_CONFIG_PATH` | `/etc/sing-box/config.json` | Atomic-write target |
| `AEGIS_AGENT_SINGBOX_RELOAD_CMD` | `systemctl reload sing-box` | Reload command (space-separated, no shell) |
| `AEGIS_AGENT_SINGBOX_RELOAD_TIMEOUT` | `5s` | Subprocess budget |
| `AEGIS_AGENT_APPLY_MAX_BYTES` | `1048576` (1 MiB) | Request body cap |

All four are set by the `install_agent` role from the new defaults.
Operators on a non-standard sing-box layout (Arch, custom build, s6,
supervisord) override via `group_vars/all.yml` or per-host vars.

## Why no shell for the reload command

The reload command is split on whitespace and invoked via
`exec.CommandContext(name, args...)` — no `sh -c`, no shell
interpolation. Operators who need pipes/redirects/env-vars can wrap
their command in a shell script and call that instead. The default
`systemctl reload sing-box` has no shell metacharacters, so the
default is safe. The split also makes the command trivially testable
on Windows (`cmd /c exit 0`) and Linux (`true`) without needing a
POSIX shell in the test environment.

## Why mode 0640 on the written file

The agent runs as root (systemd `User=root`). sing-box typically runs
as its own user (e.g. `_sing-box` on Debian). For sing-box to read the
config, either (a) the file is world-readable, or (b) the file is
group-readable and sing-box is in the root group. 0640 with owner=root,
group=root covers (b) without making the file world-readable. The env
file documents the override path (operators can chgrp the file
post-install, or set the agent to run as the sing-box user).

## Why the atomic-write target is in the same directory

`os.Rename` is atomic on POSIX only when source and target are on the
same filesystem. By creating the temp file in `filepath.Dir(target)`,
the rename is guaranteed atomic on a single filesystem (which is the
common case — `/etc/sing-box` is its own directory on Debian/Ubuntu,
or a subdir of `/etc` otherwise). The `os.CreateTemp` call picks a
unique name in the same directory, and the subsequent `os.Rename`
replaces the target atomically.

The parent directory is fsync'd after the rename (best-effort) so the
rename is durable across a crash. On Windows, opening a directory for
fsync is not supported and we silently skip; on Linux, the dir-fsync
is the durability guarantee for the rename itself. A failure to fsync
the parent is non-fatal — the on-disk content is consistent either
way; only a small window of crash-resilience is lost.

## Lint clean

`golangci-lint v2` with `backend/.golangci.yml` and `-tags=integration`
reports 0 issues for `./...`. The notable suppressions:

- `gosec G204` on `exec.CommandContext` — reload command is operator-
  controlled via `agent.env`, not panel-supplied
- `gosec G706` on the two env-var log lines — same rationale, no log
  injection from network input

The PR also encountered and resolved these v2-specific lint patterns
that v1 silently passed:

- `errorlint` wants `%w` for the inner error, not `%v` (Go 1.20+ allows
  multiple `%w` verbs in one `fmt.Errorf`)
- `gosec G301` wants `os.MkdirAll` mode ≤ 0750 (we use 0750)
- `unparam` caught the always-0o640 `perm os.FileMode` parameter;
  dropped the param and made the mode a named constant

## Test coverage (14 tests in `apply_test.go`)

- `TestApply_RealWritesConfigAndReloads` — happy path; file on disk
  matches sent config; response has `reloaded: true`; `lastApplyISO`
  updated
- `TestApply_RejectsNonObjectConfig` — sub-tests for `string`, `number`,
  `array`, `null`, `bool` inner values; all rejected with 400; target
  file not created
- `TestApply_ReloadFailureReturns500` — reload exits 1 → 500; new
  config IS on disk (documented behaviour); `lastApplyISO` not updated
- `TestApply_ReloadTimeoutReturns500` — hanging reload with 50ms
  timeout → 500; error body mentions "reload"
- `TestApply_WriteFailureReturns500` — target parent is a regular file
  (not a dir) → 500; error body mentions "write"
- `TestApply_ReplacesExistingFile` — two applies in sequence; second
  overwrites first; no leftover temp files in target dir
- `TestApply_MissingConfigPathReturns500` — empty `singboxConfigPath`
  → 500; error body mentions `CONFIG_PATH`
- `TestWriteAtomic_BasicRoundTrip` — direct helper test; mode 0640
  verified on non-Windows
- `TestWriteAtomic_ReplacesExisting` — rename replaces existing target
- `TestWriteAtomic_CleansUpTempOnError` — failure path leaves no temp
  files
- `TestRunReload_OK` / `TestRunReload_Fail` / `TestRunReload_CommandNotFound` /
  `TestRunReload_EmptyCommand` / `TestRunReload_TimeoutFires` — direct
  subprocess tests; timeout test uses `ping -n 999 127.0.0.1` on
  Windows (the obvious `cmd /c pause` exits immediately on no-TTY
  stdin) and `sleep 60` on Linux
- `TestApply_StatusReportsLastApplyISO` — `GET /v1/status` after a
  successful apply reflects the new timestamp

Cross-platform note: the reload stubs use `runtime.GOOS` to pick
between `true` / `false` / `ping` on Windows and the POSIX
equivalents. The CI runs on Linux, so the Linux paths are the
canonical tests; the Windows paths exist to keep `go test` green for
local Windows development.

## Wire contract

`POST /v1/apply` body (unchanged from v0.3.0):

```json
{ "config": { /* rendered sing-box config */ } }
```

The inner `config` MUST be a JSON object. String/number/null/array
values are rejected with 400 (v0.4.0-b new; v0.3.0 accepted them).

Response on 2xx (v0.4.0-b new fields in **bold**):

```json
{
  "accepted": true,
  "received_at": "2026-07-25T16:35:38.313Z",
  "bytes": 12345,
  "reloaded": true,
  "reload_took_ms": 12
}
```

The panel's `internal/cores/singbox/apply.go` does not parse the
response body (it only checks the HTTP status code), so the new
fields are informational and may grow in future minor versions
without a wire change.

## Migration notes for operators

- Re-running the `install_agent` role on an existing v0.3.0 node
  updates `/etc/aegis/agent.env` with the four new vars; the agent
  picks them up after a `systemctl restart aegis-agent`
- The systemd unit's `ReadWritePaths` is updated to include
  `/etc/sing-box`; reload the unit with `systemctl daemon-reload`
  and `systemctl restart aegis-agent`
- Existing sing-box installs continue to work; the agent just writes
  the new config in the same path the unit already reads
- For non-Debian sing-box layouts, override
  `aegis_singbox_config_path` and `aegis_singbox_reload_cmd` in your
  Ansible vars

## Out of scope (deferred to v0.4.0-c and beyond)

- `install_singbox` Ansible role — v0.4.0-c ships a separate role
  that installs the sing-box binary, writes its systemd unit, and
  creates `/etc/sing-box`. v0.4.0-b assumes sing-box is already
  installed (the reload command will fail otherwise)
- `GET /v1/stats` wiring to the sing-box clash-api listener —
  v0.4.0-c
- mTLS replacement for the bearer-secret gate — v1.1.0+
- Roll-back the rename on reload failure — future enhancement
- Per-node metrics (CPU, memory, sing-box goroutine count) in
  `/v1/stats` — future

## Refs

- #92 (v0.4.0-a) — BatchedApplier + real Apply transport on the
  **panel** side; without it, the agent's real Apply has nothing
  to drive it
- ARCHITECTURE.md §7.5 (Apply pipeline)
- v0.3.0 agent (validation-only stub): commit `d1000de`
