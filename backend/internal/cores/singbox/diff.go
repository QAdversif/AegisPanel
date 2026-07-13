// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"bytes"
	"fmt"

	"github.com/pmezard/go-difflib/difflib"
)

// Diff implements cores.CoreProvider. The two configs are
// compared line-by-line with a unified diff (the same format
// `git diff` produces); the result is empty when the
// payloads are byte-equal, otherwise it is a unified diff
// with 3 lines of context per hunk — enough to be readable
// in the agent activity log without flooding it.
//
// The diff is computed on the *raw* bytes, not on the parsed
// JSON. The point of the diff is to show an operator what
// changed; re-indenting or re-ordering fields would make a
// "no semantic change" render look like a 200-line diff in
// the log. The renderer's MarshalIndent is stable for a
// given input, so byte-level diffs are also semantically
// meaningful in practice.
func (p *Provider) Diff(prev, next []byte) (string, error) {
	if bytes.Equal(prev, next) {
		return "", nil
	}
	udiff := difflib.UnifiedDiff{
		A:        difflib.SplitLines(string(prev)),
		B:        difflib.SplitLines(string(next)),
		FromFile: "prev",
		ToFile:   "next",
		Context:  3,
	}
	s, err := difflib.GetUnifiedDiffString(udiff)
	if err != nil {
		return "", fmt.Errorf("singbox: diff: %w", err)
	}
	return s, nil
}
