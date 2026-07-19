// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// mockClient is a Client that records every call
// and lets the test pre-program its return
// values. The struct is intentionally simple
// (no goroutine coordination; the installer
// is synchronous).
type mockClient struct {
	mu sync.Mutex

	connectErr error
	runOut     string
	runErr     error
	uploadErr  error

	connectCalled bool
	runCmds       []string
	uploadPaths   []string
}

func (m *mockClient) Connect(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connectCalled = true
	return m.connectErr
}

func (m *mockClient) Run(_ context.Context, cmd string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runCmds = append(m.runCmds, cmd)
	return m.runOut, m.runErr
}

func (m *mockClient) Upload(_ context.Context, src, dst string, _ os.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.uploadPaths = append(m.uploadPaths, dst)
	return m.uploadErr
}

func (m *mockClient) Close() error { return nil }

// TestInstaller_SuccessPath exercises the happy
// path: connect OK, agent upload OK, env write
// OK, unit install OK, verify returns
// "active". The InstallResult.OK is true and
// every step is recorded.
func TestInstaller_SuccessPath(t *testing.T) {
	mock := &mockClient{
		// The verify step runs `systemctl
		// is-active` and checks for the
		// "active" line; the mock returns it
		// on the first call.
		runOut: "active\n",
	}
	src := writeTempScript(t, "#!/bin/sh\nexec sleep infinity\n")

	inst := &Installer{ClientFactory: func(InstallInput) (Client, error) { return mock, nil }}
	in := InstallInput{
		NodeID:       "u-1",
		NodeName:     "test-node",
		Address:      "10.0.0.1:22",
		SSHUser:      "root",
		BearerSecret: "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef",
		AgentSource:  src,
	}
	result := inst.Install(context.Background(), in)
	if !result.OK {
		t.Errorf("Install: result.OK = false, err = %v (stage %s)", result.Err, result.Stage)
	}
	if !mock.connectCalled {
		t.Error("Connect not called")
	}
	if len(mock.uploadPaths) == 0 {
		t.Error("Upload not called")
	}
	if len(mock.runCmds) == 0 {
		t.Error("Run not called (env / unit / verify)")
	}
}

// TestInstaller_ConnectFailureShortCircuits
// verifies the first-stage failure is reported
// with the correct stage name and the rest of
// the steps are skipped.
func TestInstaller_ConnectFailureShortCircuits(t *testing.T) {
	mock := &mockClient{
		connectErr: errors.New("dial timeout"),
	}
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")

	inst := &Installer{ClientFactory: func(InstallInput) (Client, error) { return mock, nil }}
	result := inst.Install(context.Background(), InstallInput{
		Address:      "10.0.0.1:22",
		SSHUser:      "root",
		BearerSecret: "0123",
		AgentSource:  src,
	})
	if result.OK {
		t.Error("Install: result.OK = true on connect failure")
	}
	if result.Stage != "connect" {
		t.Errorf("Stage = %q, want connect", result.Stage)
	}
	if len(mock.uploadPaths) != 0 {
		t.Errorf("Upload called on connect failure: %v", mock.uploadPaths)
	}
}

// TestInstaller_UploadFailure verifies the
// upload step is reported with the correct
// stage. The verify step is not run.
func TestInstaller_UploadFailure(t *testing.T) {
	mock := &mockClient{
		uploadErr: errors.New("sftp: permission denied"),
	}
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")

	inst := &Installer{ClientFactory: func(InstallInput) (Client, error) { return mock, nil }}
	result := inst.Install(context.Background(), InstallInput{
		Address:      "10.0.0.1:22",
		SSHUser:      "root",
		BearerSecret: "0123",
		AgentSource:  src,
	})
	if result.OK {
		t.Error("Install: result.OK = true on upload failure")
	}
	if result.Stage != "upload-agent" {
		t.Errorf("Stage = %q, want upload-agent", result.Stage)
	}
}

// TestInstaller_VerifyFailure transitions the
// result to "verify" stage when the post-install
// systemctl check returns something other than
// "active". The function is the only place
// where the installer cares about the unit's
// runtime state.
func TestInstaller_VerifyFailure(t *testing.T) {
	mock := &mockClient{
		runOut: "failed\n",
	}
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")

	inst := &Installer{ClientFactory: func(InstallInput) (Client, error) { return mock, nil }}
	result := inst.Install(context.Background(), InstallInput{
		Address:      "10.0.0.1:22",
		SSHUser:      "root",
		BearerSecret: "0123",
		AgentSource:  src,
	})
	if result.OK {
		t.Error("Install: result.OK = true on verify failure")
	}
	if result.Stage != "verify" {
		t.Errorf("Stage = %q, want verify", result.Stage)
	}
}

// TestInstaller_RejectsEmptyInputs is a
// defensive guard: the install path is the only
// place where missing inputs are reported as
// 4xx (vs. an install failure 502). The
// validation is synchronous and runs before
// the network is touched.
func TestInstaller_RejectsEmptyInputs(t *testing.T) {
	inst := NewInstaller()
	cases := []struct {
		name string
		in   InstallInput
	}{
		{"empty bearer secret", InstallInput{Address: "h", AgentSource: "x"}},
		{"empty agent source", InstallInput{Address: "h", BearerSecret: "x"}},
		{"nonexistent agent source", InstallInput{Address: "h", BearerSecret: "x", AgentSource: "/no/such/file"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			result := inst.Install(context.Background(), c.in)
			if result.OK {
				t.Errorf("Install: result.OK = true for %s", c.name)
			}
			if result.Stage != "input" {
				t.Errorf("Stage = %q, want input", result.Stage)
			}
		})
	}
}

// TestInstaller_EmitsUnitAndEnvCommands verifies
// the install writes both the agent.env file
// AND the systemd unit on every run. A
// regression that omits either would silently
// break the agent at start.
func TestInstaller_EmitsUnitAndEnvCommands(t *testing.T) {
	mock := &mockClient{runOut: "active\n"}
	src := writeTempScript(t, "#!/bin/sh\nexit 0\n")

	inst := &Installer{ClientFactory: func(InstallInput) (Client, error) { return mock, nil }}
	result := inst.Install(context.Background(), InstallInput{
		Address:      "h:22",
		SSHUser:      "root",
		BearerSecret: "deadbeef",
		AgentSource:  src,
	})
	if !result.OK {
		t.Fatalf("Install: %v", result.Err)
	}
	// Look for the env-write and unit-write
	// commands in the recorded Run history.
	joined := strings.Join(mock.runCmds, "\n")
	if !strings.Contains(joined, "/etc/aegis/agent.env") {
		t.Error("install did not write /etc/aegis/agent.env")
	}
	if !strings.Contains(joined, "aegis-agent.service") {
		t.Error("install did not write aegis-agent.service")
	}
	if !strings.Contains(joined, "AEGIS_AGENT_BEARER=deadbeef") {
		t.Error("install did not embed the bearer secret in agent.env")
	}
}

// writeTempScript is a tiny helper that
// writes a shell script to t.TempDir() and
// returns the path. The chmod 0755 is
// required because os.Stat on Windows
// ignores the mode bits; the test only needs
// the file to exist + be readable.
func writeTempScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	return path
}
