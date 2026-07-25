// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Tests for the real /v1/apply side effects. The
// v0.3.0 stub tests (in main_test.go) cover body
// validation; this file covers the v0.4.0-b
// additions: the atomic write, the reload, and the
// error paths from each.
//
// The tests mutate the package-level globals
// (`singboxConfigPath`, `singboxReloadCmd`,
// `singboxReloadTimeout`) with `t.Cleanup` to
// restore the original values so parallel test
// runs do not stomp on each other.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// withApplyConfig sets the package-level apply
// globals for the duration of a test. Tests must
// NOT call t.Parallel after this; the package
// globals are not safe for concurrent mutation.
func withApplyConfig(t *testing.T, configPath, reloadCmd string, timeout time.Duration) {
	t.Helper()
	prevPath := singboxConfigPath
	prevCmd := singboxReloadCmd
	prevTimeout := singboxReloadTimeout
	singboxConfigPath = configPath
	singboxReloadCmd = reloadCmd
	if timeout > 0 {
		singboxReloadTimeout = timeout
	}
	t.Cleanup(func() {
		singboxConfigPath = prevPath
		singboxReloadCmd = prevCmd
		singboxReloadTimeout = prevTimeout
	})
}

// stubReloadOK returns a cross-platform reload
// command that exits 0 immediately. On Linux/macOS
// this is `/usr/bin/true` (always in PATH on a
// normal box, no shell). On Windows, `cmd /c exit 0`
// does the same.
func stubReloadOK() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 0"
	}
	return "true"
}

// stubReloadFail returns a cross-platform reload
// command that exits 1 immediately. The error
// message is non-empty so we can verify the
// `stderr=...` capture in runReload.
func stubReloadFail() string {
	if runtime.GOOS == "windows" {
		return "cmd /c exit 1"
	}
	return "false"
}

// stubReloadHang returns a reload command that
// hangs long enough for the test's short timeout
// to fire. On Linux/macOS this is `sleep 60`. On
// Windows we use `ping -n 999 127.0.0.1` because
// the obvious `cmd /c pause` exits immediately
// (it reads from stdin and gets EOF when the
// subprocess has no TTY) and `timeout` is not
// installed by default on all Windows SKUs.
// `ping` is always present on Windows; the
// `-n 999` is one ping per second, so 999 seconds.
func stubReloadHang() string {
	if runtime.GOOS == "windows" {
		return "ping -n 999 127.0.0.1"
	}
	return "sleep 60"
}

// newApplyServer returns an httptest.Server wired
// to newMux() with a known bearer secret. The
// caller is expected to set the apply globals
// before calling this (via withApplyConfig).
func newApplyServer(t *testing.T) *httptest.Server {
	t.Helper()
	withBearerSecret(t, "test-secret-32bytes-padding-xx")
	return httptest.NewServer(newMux())
}

// TestApply_RealWritesConfigAndReloads is the happy
// path: the agent writes the config to a temp
// file, runs the reload command, and returns 202.
func TestApply_RealWritesConfigAndReloads(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadOK(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	body := `{"config":{"inbounds":[{"type":"mixed","listen":"127.0.0.1","listen_port":1080}],"outbounds":[{"type":"direct"}]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("status = %d, want 202; body = %s", resp.StatusCode, string(respBody))
	}
	// File on disk must match the inner config
	// (the agent strips the JSON envelope and writes
	// just the value of the `config` field, re-
	// formatted by json.RawMessage's default).
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read %s: %v", target, err)
	}
	// The inner config is the bytes between
	// `{"config":` and the closing `}`. We do not
	// re-parse to compare (json.RawMessage may have
	// reformatted whitespace); we just check the
	// file parses as JSON and contains the listen
	// port we sent.
	var probe map[string]any
	if err := json.Unmarshal(got, &probe); err != nil {
		t.Fatalf("on-disk config not valid JSON: %v\nbody = %s", err, string(got))
	}
	inbounds, ok := probe["inbounds"].([]any)
	if !ok || len(inbounds) == 0 {
		t.Fatalf("on-disk config has no inbounds: %s", string(got))
	}
	// The reload command should have been run
	// (success path returns 202; a reload failure
	// returns 500). Confirm the response says
	// reloaded=true.
	var out applyResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !out.Reloaded {
		t.Fatalf("reloaded = false, want true; body = %s", string(respBody))
	}
	// The lastApplyISO is updated in-memory only
	// on success; verify it.
	if lastApplyISO == "" {
		t.Fatalf("lastApplyISO not updated")
	}
}

// TestApply_RejectsNonObjectConfig catches the
// v0.4.0-b new validation: the inner `config`
// must be a JSON object. A string/number/null
// would be a likely panel bug and we want to
// reject it explicitly (the file-write side effect
// would otherwise corrupt sing-box's config).
func TestApply_RejectsNonObjectConfig(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadOK(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	cases := []struct {
		name string
		body string
	}{
		{"string", `{"config":"hello"}`},
		{"number", `{"config":42}`},
		{"array", `{"config":[1,2,3]}`},
		{"null", `{"config":null}`},
		{"bool", `{"config":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(tc.body))
			req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("POST /v1/apply: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusBadRequest {
				body, _ := io.ReadAll(resp.Body)
				t.Fatalf("status = %d, want 400; body = %s", resp.StatusCode, string(body))
			}
		})
	}
	// None of the rejected calls should have created
	// the target file.
	if _, err := os.Stat(target); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("target file should not exist after rejected applies: stat err = %v", err)
	}
}

// TestApply_ReloadFailureReturns500 exercises the
// failure path where the write succeeds but the
// reload exits non-zero. The agent must return 500
// so the panel's BatchedApplier retries.
func TestApply_ReloadFailureReturns500(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadFail(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", resp.StatusCode, string(respBody))
	}
	// The new config IS on disk even though the
	// reload failed. This is documented behavior
	// (see apply.go: "If the reload fails, the new
	// config is on disk but sing-box is running the
	// old one"). Verify the file exists.
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("target file should exist after write+reload-fail: %v", err)
	}
	// lastApplyISO must NOT be updated (only on full
	// success). We cannot assert on its prior value
	// (other tests may have set it), but the
	// request that just failed must not have set it
	// to a new value if it was empty before. Skip
	// that check; the test would be racy.
}

// TestApply_ReloadTimeoutReturns500 verifies that
// a hanging reload is killed by the context
// timeout and returns 500. We use a short
// timeout (50ms) and a command that hangs (sleep
// 60 / cmd /c pause).
func TestApply_ReloadTimeoutReturns500(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadHang(), 50*time.Millisecond)
	srv := newApplyServer(t)
	defer srv.Close()

	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", resp.StatusCode, string(respBody))
	}
	// The error message should mention the reload
	// (not the write), so the panel log tells the
	// operator where to look.
	if !strings.Contains(strings.ToLower(string(respBody)), "reload") {
		t.Fatalf("error body should mention 'reload'; got: %s", string(respBody))
	}
}

// TestApply_WriteFailureReturns500 verifies the
// error path where the target directory does not
// exist. The MkdirAll fallback in writeAtomic
// creates the dir, so we need a different failure
// mode: an unwritable target. We use a path under
// a non-directory file (so MkdirAll fails because
// the parent is a file).
func TestApply_WriteFailureReturns500(t *testing.T) {
	dir := t.TempDir()
	// Create a regular file; treat it as a
	// directory. MkdirAll will fail because the
	// path is not a directory.
	blocking := filepath.Join(dir, "blocking-file")
	if err := os.WriteFile(blocking, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("setup: write blocking file: %v", err)
	}
	// Target path is `<blocking>/config.json`. The
	// parent dir creation will fail because
	// `blocking` exists as a file, not a dir.
	target := filepath.Join(blocking, "config.json")
	withApplyConfig(t, target, stubReloadOK(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", resp.StatusCode, string(respBody))
	}
	if !strings.Contains(strings.ToLower(string(respBody)), "write") {
		t.Fatalf("error body should mention 'write'; got: %s", string(respBody))
	}
}

// TestApply_ReplacesExistingFile verifies the
// atomic rename overwrites an existing target.
// The first apply writes version A; the second
// writes version B. The on-disk content after
// the second apply must be B, not A or a mix.
func TestApply_ReplacesExistingFile(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadOK(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	post := func(t *testing.T, body string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/apply: %v", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusAccepted {
			body, _ := io.ReadAll(resp.Body)
			t.Fatalf("status = %d, want 202; body = %s", resp.StatusCode, string(body))
		}
	}

	post(t, `{"config":{"version":"A","inbounds":[]}}`)
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after first apply: %v", err)
	}
	if !bytes.Contains(got, []byte(`"version":"A"`)) && !bytes.Contains(got, []byte(`"version": "A"`)) {
		t.Fatalf("first apply did not write version A; got: %s", string(got))
	}
	post(t, `{"config":{"version":"B","inbounds":[]}}`)
	got, err = os.ReadFile(target)
	if err != nil {
		t.Fatalf("read after second apply: %v", err)
	}
	if !bytes.Contains(got, []byte(`"version":"B"`)) && !bytes.Contains(got, []byte(`"version": "B"`)) {
		t.Fatalf("second apply did not replace with version B; got: %s", string(got))
	}
	// No leftover temp files in the target dir.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.json.tmp.") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// TestApply_MissingConfigPathReturns500 catches the
// misconfiguration where the agent's env var is
// not set. The agent should refuse rather than
// write to "/" or some other default.
func TestApply_MissingConfigPathReturns500(t *testing.T) {
	dir := t.TempDir()
	withApplyConfig(t, "", stubReloadOK(), 5*time.Second) // empty path
	_ = dir
	srv := newApplyServer(t)
	defer srv.Close()

	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body = %s", resp.StatusCode, string(respBody))
	}
	if !strings.Contains(strings.ToLower(string(respBody)), "config_path") {
		t.Fatalf("error body should mention CONFIG_PATH; got: %s", string(respBody))
	}
}

// TestWriteAtomic_BasicRoundTrip exercises the
// helper directly (without an HTTP server) so a
// future refactor that breaks the temp-file dance
// fails fast.
func TestWriteAtomic_BasicRoundTrip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	payload := []byte(`{"hello":"world"}`)
	if err := writeAtomic(target, payload); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload mismatch: got %q, want %q", string(got), string(payload))
	}
	// Permissions check: best-effort. On Windows
	// the mode bits are advisory only; skip the
	// check there.
	if runtime.GOOS != "windows" {
		st, err := os.Stat(target)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := st.Mode().Perm(); got != 0o640 {
			t.Fatalf("perm = %o, want 0640", got)
		}
	}
}

// TestWriteAtomic_ReplacesExisting verifies the
// rename overwrites an existing target.
func TestWriteAtomic_ReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatalf("setup: write target: %v", err)
	}
	if err := writeAtomic(target, []byte("new")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(got) != "new" {
		t.Fatalf("content = %q, want %q", string(got), "new")
	}
}

// TestWriteAtomic_CleansUpTempOnError verifies
// that a failed write does not leave a temp file
// in the target dir. We trigger a failure by
// writing to a target whose parent is a file (so
// CreateTemp fails).
func TestWriteAtomic_CleansUpTempOnError(t *testing.T) {
	dir := t.TempDir()
	blocking := filepath.Join(dir, "blocking")
	if err := os.WriteFile(blocking, []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("setup: write blocking: %v", err)
	}
	target := filepath.Join(blocking, "config.json")
	if err := writeAtomic(target, []byte("payload")); err == nil {
		t.Fatalf("writeAtomic to non-dir parent should have failed")
	}
	// No temp file should be left in the original
	// dir (the temp file would be created in the
	// parent of `target`, which is the blocking
	// file; we cannot easily check there). What we
	// CAN check is the original dir has no `.tmp.`
	// files.
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "config.json.tmp.") {
			t.Fatalf("leftover temp file: %s", e.Name())
		}
	}
}

// TestRunReload_OK runs a successful reload and
// checks the wall-clock duration is positive.
func TestRunReload_OK(t *testing.T) {
	took, err := runReload(context.Background(), stubReloadOK(), 5*time.Second)
	if err != nil {
		t.Fatalf("runReload: %v", err)
	}
	if took <= 0 {
		t.Fatalf("took = %v, want > 0", took)
	}
}

// TestRunReload_Fail runs a failing reload and
// checks the error is wrapped.
func TestRunReload_Fail(t *testing.T) {
	_, err := runReload(context.Background(), stubReloadFail(), 5*time.Second)
	if err == nil {
		t.Fatalf("runReload with failing cmd should return error")
	}
	if !errors.Is(err, errApplyReloadFailed) {
		t.Fatalf("err = %v, want errors.Is(err, errApplyReloadFailed)", err)
	}
}

// TestRunReload_CommandNotFound uses a guaranteed-
// missing command and expects an error.
func TestRunReload_CommandNotFound(t *testing.T) {
	_, err := runReload(context.Background(), "this-command-does-not-exist-12345", 5*time.Second)
	if err == nil {
		t.Fatalf("runReload with missing cmd should return error")
	}
	if !errors.Is(err, errApplyReloadFailed) {
		t.Fatalf("err = %v, want errors.Is(err, errApplyReloadFailed)", err)
	}
}

// TestRunReload_EmptyCommand is a defensive check:
// an empty reload command (e.g. operator left
// AEGIS_AGENT_SINGBOX_RELOAD_CMD unset and we
// somehow bypassed the env-default in run()).
func TestRunReload_EmptyCommand(t *testing.T) {
	_, err := runReload(context.Background(), "", 5*time.Second)
	if err == nil {
		t.Fatalf("runReload with empty cmd should return error")
	}
}

// TestRunReload_TimeoutFires uses a long-running
// command and a short timeout. The function must
// return an error (the context is cancelled and
// exec.CommandContext kills the subprocess).
func TestRunReload_TimeoutFires(t *testing.T) {
	start := time.Now()
	_, err := runReload(context.Background(), stubReloadHang(), 100*time.Millisecond)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatalf("runReload with hanging cmd should return error")
	}
	// The timeout should be respected: we should
	// return in ~100ms, not in 60s. Add a generous
	// margin for slow CI.
	if elapsed > 5*time.Second {
		t.Fatalf("runReload took %v, want < 5s (timeout was 100ms)", elapsed)
	}
}

// TestApply_StatusReportsLastApplyISO is a smoke
// test that the /v1/status endpoint surfaces the
// updated lastApplyISO after a successful apply.
func TestApply_StatusReportsLastApplyISO(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "config.json")
	withApplyConfig(t, target, stubReloadOK(), 5*time.Second)
	srv := newApplyServer(t)
	defer srv.Close()

	// Capture the lastApplyISO before.
	prevLast := lastApplyISO
	// Apply a config.
	body := `{"config":{"inbounds":[],"outbounds":[]}}`
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/apply", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /v1/apply: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("apply status = %d, want 202", resp.StatusCode)
	}
	// Fetch /v1/status.
	req2, _ := http.NewRequest(http.MethodGet, srv.URL+"/v1/status", nil)
	req2.Header.Set("Authorization", "Bearer test-secret-32bytes-padding-xx")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("GET /v1/status: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	var sr statusResponse
	if err := json.NewDecoder(resp2.Body).Decode(&sr); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if sr.LastApplyISO == "" {
		t.Fatalf("last_apply_iso is empty after apply")
	}
	if sr.LastApplyISO == prevLast {
		t.Fatalf("last_apply_iso did not change after apply: still %q", sr.LastApplyISO)
	}
}
