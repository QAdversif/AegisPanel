// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Smoke test for the auth stub package. The real JWT/refresh-token
// implementation lands in Phase 1 (feat/auth-jwt); until then this
// guards against accidental deletion of the Sentinel constant that
// other modules depend on.

package stub

import "testing"

func TestSentinelIsStable(t *testing.T) {
	const want = "stub"
	if Sentinel != want {
		t.Fatalf("Sentinel = %q, want %q", Sentinel, want)
	}
}

func TestSentinelNonEmpty(t *testing.T) {
	if Sentinel == "" {
		t.Fatal("Sentinel must be a non-empty string")
	}
}
