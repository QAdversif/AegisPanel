// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Real Apply for the aegis-agent. v0.4.0-b replaces
// the v0.3.0 stub (which only validated the JSON
// envelope) with the actual side effects: the
// rendered sing-box config is written to disk and
// the reload command is invoked.
//
// # The two side effects
//
//  1. Atomic write of the config to
//     `AEGIS_AGENT_SINGBOX_CONFIG_PATH` (default
//     `/etc/sing-box/config.json`). The write is
//     atomic on POSIX (write to a temp file in the
//     same directory, fsync, then `os.Rename`)
//     so a partial write cannot be observed by
//     sing-box even if the agent crashes mid-Apply.
//  2. Run the reload command from
//     `AEGIS_AGENT_SINGBOX_RELOAD_CMD` (default
//     `systemctl reload sing-box`). The reload
//     re-reads the config from disk, so a successful
//     `systemctl reload` implies both the write
//     landed and sing-box accepted the new config.
//
// # Order of operations
//
// Write happens first, then reload. If the reload
// fails, the new config is on disk but sing-box is
// running the old one. Returning 5xx to the panel
// makes the failure visible in the BatchedApplier
// retry path; rolling back the rename is a future
// enhancement (out of scope for v0.4.0-b).
//
// # Why no shell for the reload command
//
// The reload command is split on whitespace and
// invoked via `exec.CommandContext(name, args...)`
// — no `sh -c`, no shell interpolation. Operators
// who need pipes/redirects/env-vars can wrap their
// command in a shell script and call that instead.
// The default `systemctl reload sing-box` has no
// shell metacharacters, so the default is safe.
//
// # File permissions
//
// The atomic write uses mode 0640, owner=root,
// group=root. sing-box running as a different user
// needs to be in the root group to read the config.
// The default Debian/Ubuntu sing-box package runs
// as user `_sing-box` with group `_sing-box`; the
// agent's env file documents the override path
// (operators can chgrp the file post-install, or
// set the agent to run as the sing-box user).

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// defaultConfigPath is the rendered sing-box config
// path. The agent reads `AEGIS_AGENT_SINGBOX_CONFIG_PATH`
// at process start; an empty value falls back to this
// default. The path is what sing-box's own systemd
// unit expects (`-c /etc/sing-box/config.json`).
const defaultConfigPath = "/etc/sing-box/config.json"

// defaultReloadCmd is the default reload command.
// The agent reads `AEGIS_AGENT_SINGBOX_RELOAD_CMD`
// at process start; an empty value falls back to this
// default. The command is space-separated; no shell.
// If operators want pipes, they wrap their command
// in a script and point this env var at the script.
const defaultReloadCmd = "systemctl reload sing-box"

// defaultReloadTimeout is the budget for the reload
// command. systemd `Type=notify` reloads complete in
// well under a second; 5s is a 5x safety margin. The
// HTTP server's `WriteTimeout` (30s) is the hard
// upper bound; this env var is the soft budget.
const defaultReloadTimeout = 5 * time.Second

// singboxConfigPath is the target path for the
// atomic write. Read from `AEGIS_AGENT_SINGBOX_CONFIG_PATH`
// in `run()`. Set to the test's tempdir by the
// test helpers (mirrors the `bearerSecret` pattern).
var singboxConfigPath = ""

// singboxReloadCmd is the reload command string.
// Read from `AEGIS_AGENT_SINGBOX_RELOAD_CMD` in
// `run()`. Tests set it to a cross-platform stub
// (`cmd /c exit 0` on Windows, `true` on Linux).
var singboxReloadCmd = ""

// singboxReloadTimeout is the budget for the reload
// subprocess. Read from `AEGIS_AGENT_SINGBOX_RELOAD_TIMEOUT`
// (parsed as time.Duration) in `run()`. Empty or
// unparseable values fall back to the default.
var singboxReloadTimeout = defaultReloadTimeout

// applyEnvelope is the JSON body the panel sends on
// POST /v1/apply. The wire contract is the same
// v0.3.0 shape — `config` is a JSON object (the
// rendered sing-box config). v0.4.0-b adds the
// requirement that `config` is an object (not a
// string/number/null) so a malformed panel call
// cannot accidentally overwrite sing-box's config
// with garbage.
type applyEnvelope struct {
	Config json.RawMessage `json:"config"`
}

// applyResponse is the body the agent returns on a
// successful Apply. v0.4.0-b extends the v0.3.0
// shape with `reloaded` and `reload_took_ms` so the
// panel can observe the side-effect latency. The
// panel's `singbox/apply.go` does not parse this
// shape (it only checks the HTTP status code), so
// the field set is informational and may grow in
// future minor versions without a wire change.
type applyResponse struct {
	Accepted     bool   `json:"accepted"`
	ReceivedAt   string `json:"received_at"`
	Bytes        int    `json:"bytes"`
	Reloaded     bool   `json:"reloaded"`
	ReloadTookMS int64  `json:"reload_took_ms,omitempty"`
}

// errApplyReloadFailed is returned (wrapped) by
// runReload when the reload subprocess exits
// non-zero. The error wraps the original `*exec.ExitError`
// so callers can use `errors.As` if they need the
// exit code; the helper also includes a truncated
// copy of stdout/stderr in the message.
var errApplyReloadFailed = errors.New("aegis-agent: reload command failed")

// errApplyWriteFailed is returned (wrapped) by
// writeAtomic when the temp file cannot be created,
// written, renamed, or chmod'd. The error wraps
// the original `*os.PathError` so callers can use
// `errors.Is(err, fs.ErrNotExist)` etc.
var errApplyWriteFailed = errors.New("aegis-agent: write config to disk failed")

// applyConfig is the HTTP-handler-side entry point.
// It owns the read body / decode envelope / write
// / reload / respond sequence. The package-level
// globals `singboxConfigPath`, `singboxReloadCmd`,
// and `singboxReloadTimeout` are read here (not as
// arguments) so the HTTP handler signature stays
// unchanged and the existing tests do not need
// to thread extra state.
//
// Returns the HTTP status code to write and the
// (possibly nil) response body bytes. The HTTP
// handler writes the response using these.
//
// On 5xx, the response body is a plain text error
// message (the panel's apply.go renders it via
// `truncateBody`, which handles text fine). On
// 2xx, the body is JSON-encoded `applyResponse`.
func applyConfig(r *http.Request) (status int, body []byte) {
	// 1 MiB cap. The real sing-box config for a
	// busy panel is on the order of 100 KiB; 1 MiB
	// is a 10x safety margin. The cap is read from
	// `AEGIS_AGENT_APPLY_MAX_BYTES` in `run()` (set
	// in the package-level `applyMaxBytes` var).
	r.Body = http.MaxBytesReader(nil, r.Body, applyMaxBytes)
	defer func() { _ = r.Body.Close() }()
	raw, err := io.ReadAll(r.Body)
	if err != nil {
		return http.StatusBadRequest, []byte("read body: " + err.Error())
	}
	if len(raw) == 0 {
		return http.StatusBadRequest, []byte("empty body")
	}
	var env applyEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return http.StatusBadRequest, []byte("invalid JSON envelope: " + err.Error())
	}
	if len(env.Config) == 0 {
		return http.StatusBadRequest, []byte("missing config field")
	}
	// The inner `config` must be a JSON object.
	// sing-box configs are always objects; accepting
	// a string/number/null here would let a buggy
	// panel call overwrite sing-box's config with
	// garbage.
	if trimmed := bytes.TrimLeft(env.Config, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return http.StatusBadRequest, []byte("config must be a JSON object")
	}
	// The inner object must itself parse as JSON.
	// (json.RawMessage is opaque until you unmarshal
	// it; the envelope decode does not validate the
	// inner shape.)
	var probe any
	if err := json.Unmarshal(env.Config, &probe); err != nil {
		return http.StatusBadRequest, []byte("config is not valid JSON: " + err.Error())
	}
	if singboxConfigPath == "" {
		return http.StatusInternalServerError, []byte("AEGIS_AGENT_SINGBOX_CONFIG_PATH is not configured")
	}
	if singboxReloadCmd == "" {
		return http.StatusInternalServerError, []byte("AEGIS_AGENT_SINGBOX_RELOAD_CMD is not configured")
	}
	// Write first. If the write fails, the reload
	// is skipped and the on-disk config is left at
	// the previous (working) state. sing-box
	// continues running the old config.
	if err := writeAtomic(singboxConfigPath, env.Config); err != nil {
		return http.StatusInternalServerError,
			[]byte("write config: " + err.Error())
	}
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	// Reload. Use the request context so a client
	// disconnect cancels the subprocess; layer the
	// configured timeout on top.
	took, err := runReload(r.Context(), singboxReloadCmd, singboxReloadTimeout)
	if err != nil {
		// The new config is on disk. Return 500 so
		// the BatchedApplier retries; the operator
		// sees a clear error in the panel log.
		return http.StatusInternalServerError,
			[]byte("reload failed: " + err.Error())
	}
	// Success. Update the in-memory last-apply
	// timestamp so /v1/status reflects the new
	// value, and serialize the response.
	lastApplyISO = receivedAt
	log.Printf("apply ok: bytes=%d reload_took_ms=%d target=%s",
		len(env.Config), took.Milliseconds(), singboxConfigPath)
	resp := applyResponse{
		Accepted:     true,
		ReceivedAt:   receivedAt,
		Bytes:        len(env.Config),
		Reloaded:     true,
		ReloadTookMS: took.Milliseconds(),
	}
	buf, err := json.Marshal(resp)
	if err != nil {
		// Marshalling a fixed-shape struct should
		// never fail. If it does, it's a bug, not
		// a runtime condition.
		return http.StatusInternalServerError,
			[]byte("marshal response: " + err.Error())
	}
	return http.StatusAccepted, buf
}

// applyMaxBytes is the upper bound for the /v1/apply
// request body. Read from `AEGIS_AGENT_APPLY_MAX_BYTES`
// in `run()` (parsed as int64). Empty or unparseable
// values fall back to the default below.
var applyMaxBytes = int64(1 << 20)

// writeAtomic writes `data` to `target` atomically:
// the data is first written to a temp file in the
// same directory, fsync'd, chmod'd, then renamed to
// `target`. The rename is atomic on POSIX (within a
// single filesystem) and uses `MoveFileEx` with
// `MOVEFILE_REPLACE_EXISTING` on Windows, so a reader
// (sing-box) always observes either the old content
// or the new content — never a partial write.
//
// The temp file is removed on any error path so a
// half-written file does not accumulate in the
// config directory. The fsync of the parent
// directory is best-effort: on Windows, opening a
// writeAtomicConfigPerm is the file mode used for
// the atomic-write target. 0640 = owner read+write,
// group read, world none. The agent runs as root;
// sing-box typically runs as its own user (e.g.
// `_sing-box` on Debian) and needs to be in the
// root group to read the config. Operators that
// use a different sing-box user can chgrp the
// file post-install; the env file documents the
// override path.
const writeAtomicConfigPerm = 0o640

// directory for sync is not supported and we silently
// skip; on Linux, the dir-fsync is the durability
// guarantee for the rename itself.
func writeAtomic(target string, data []byte) error {
	dir := filepath.Dir(target)
	// MkdirAll so the target directory exists. The
	// install_singbox role (v0.4.0-c) creates
	// `/etc/sing-box`; this is a defensive fallback
	// for the docker-compose smoke (no install
	// role, just a bind mount). 0750 is the
	// gosec G301-recommended ceiling for world-
	// readable directories.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("%w: mkdir %s: %w", errApplyWriteFailed, dir, err)
	}
	tmp, err := os.CreateTemp(dir, filepath.Base(target)+".tmp.*")
	if err != nil {
		return fmt.Errorf("%w: create temp: %w", errApplyWriteFailed, err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if !renamed {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: write: %w", errApplyWriteFailed, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("%w: fsync: %w", errApplyWriteFailed, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: close: %w", errApplyWriteFailed, err)
	}
	if err := os.Chmod(tmpName, writeAtomicConfigPerm); err != nil {
		return fmt.Errorf("%w: chmod: %w", errApplyWriteFailed, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("%w: rename: %w", errApplyWriteFailed, err)
	}
	renamed = true
	// fsync the parent directory so the rename is
	// durable across a crash. Best-effort: a
	// permission error here is non-fatal (the
	// rename already happened; a crash before the
	// dir-fsync just means a small chance of the
	// rename not surviving a power loss — the
	// on-disk content is consistent either way).
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

// runReload runs the configured reload command and
// returns the wall-clock duration. The command is
// split on whitespace (no shell) and invoked via
// `exec.CommandContext` so a context cancellation
// kills the subprocess. stdout/stderr are captured
// (and truncated into the error message on failure)
// so the panel log gets enough context to debug
// the reload without us storing megabytes of
// output.
//
// The `parts := strings.Fields(reloadCmd)` split
// means: no quoting, no env-var expansion, no pipes.
// If operators need those, they write a wrapper
// script and call that.
func runReload(ctx context.Context, reloadCmd string, timeout time.Duration) (time.Duration, error) {
	parts := strings.Fields(reloadCmd)
	if len(parts) == 0 {
		return 0, errors.New("reload command is empty")
	}
	name := parts[0]
	args := parts[1:]
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	start := time.Now()
	// The reload command + args come from
	// AEGIS_AGENT_SINGBOX_RELOAD_CMD, set in
	// /etc/aegis/agent.env by the install_agent
	// Ansible role. The role is operator-only
	// (the panel does not write to the env file);
	// the command is therefore trusted, not user-
	// controlled. gosec G204 is suppressed with
	// an inline justification.
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- reload command is operator-controlled via agent.env, not panel-supplied
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	took := time.Since(start)
	if err != nil {
		return took, fmt.Errorf("%w: %q exit: %w: stdout=%s stderr=%s",
			errApplyReloadFailed, reloadCmd, err,
			truncateForErr(outBuf.Bytes(), 512),
			truncateForErr(errBuf.Bytes(), 512))
	}
	return took, nil
}

// truncateForErr limits a byte slice for inclusion in
// an error message. The parameter is named `maxBytes`
// to avoid shadowing the built-in `max` (the gocritic
// `builtinShadow` check would otherwise reject the
// name; we keep the parameter explicit so the
// shadowing is impossible to introduce accidentally).
func truncateForErr(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "...(truncated)"
}
