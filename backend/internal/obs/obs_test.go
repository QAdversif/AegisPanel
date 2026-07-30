// SPDX-License-Identifier: AGPL-3.0-or-later

package obs

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// resetLogger saves and restores the global zerolog logger so
// each test gets a clean slate. The teardown runs in t.Cleanup
// so it always executes, even on test failure.
func resetLogger(t *testing.T) {
	t.Helper()
	orig := log.Logger
	t.Cleanup(func() { log.Logger = orig })
	log.Logger = log.Logger.Level(zerolog.DebugLevel)
}

// TestConfigureLoggerTo_ProductionEmitsJSON asserts that with
// AEGIS_ENV=production, every log line is a single valid JSON
// object. This is the contract a log shipper (Vector, Loki,
// Datadog Agent) relies on.
func TestConfigureLoggerTo_ProductionEmitsJSON(t *testing.T) {
	t.Setenv("AEGIS_ENV", AEGISEnvProduction)
	resetLogger(t)

	var buf bytes.Buffer
	configureLoggerTo(&buf)
	log.Info().Str("k", "v").Msg("test line")

	// Every non-empty line must parse as a single JSON object.
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) == 0 || lines[0] == "" {
		t.Fatalf("no log output captured: %q", buf.String())
	}
	for _, line := range lines {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("line is not valid JSON: %q (err: %v)", line, err)
		}
	}
}

// TestConfigureLoggerTo_DevEmitsHumanReadable asserts that WITHOUT
// AEGIS_ENV=production, the output is NOT JSON — it contains
// the message text, suitable for a developer watching the
// terminal.
func TestConfigureLoggerTo_DevEmitsHumanReadable(t *testing.T) {
	t.Setenv("AEGIS_ENV", "")
	resetLogger(t)

	var buf bytes.Buffer
	configureLoggerTo(&buf)
	log.Info().Msg("human-readable marker")

	if !strings.Contains(buf.String(), "human-readable marker") {
		t.Errorf("dev output missing message text: %q", buf.String())
	}
	// ConsoleWriter emits level word in caps; JSON wraps it.
	// A JSON `{"level":"info"}` block would NOT match.
	if strings.Contains(buf.String(), `"level":"info"`) {
		t.Errorf("dev output is JSON (expected console): %q", buf.String())
	}
}

// TestConfigureLoggerTo_NoopWhenCalledTwice guards the
// documented "safe to call multiple times" claim: invoking
// configureLoggerTo twice with the same env must not panic and
// must leave the global logger usable.
func TestConfigureLoggerTo_NoopWhenCalledTwice(t *testing.T) {
	t.Setenv("AEGIS_ENV", AEGISEnvProduction)
	resetLogger(t)

	var buf bytes.Buffer
	configureLoggerTo(&buf)
	configureLoggerTo(&buf)
	log.Warn().Msg("still works after double call")

	if !strings.Contains(buf.String(), "still works after double call") {
		t.Errorf("double-call did not write to expected buffer: %q", buf.String())
	}
}

// TestConfigureLoggerTo_RespectsLevel is a smoke test that the
// configured logger actually filters by level — a debug log
// after the level is raised to warn is dropped. This is the
// implicit guarantee JSON log shippers rely on: only the levels
// above the threshold reach the stream.
func TestConfigureLoggerTo_RespectsLevel(t *testing.T) {
	t.Setenv("AEGIS_ENV", AEGISEnvProduction)
	resetLogger(t)

	var buf bytes.Buffer
	configureLoggerTo(&buf)
	log.Logger = log.Logger.Level(zerolog.WarnLevel)
	log.Debug().Msg("this should be dropped")
	log.Warn().Msg("this should be kept")

	out := buf.String()
	if strings.Contains(out, "this should be dropped") {
		t.Errorf("debug line leaked at WarnLevel: %q", out)
	}
	if !strings.Contains(out, "this should be kept") {
		t.Errorf("warn line missing: %q", out)
	}
}
