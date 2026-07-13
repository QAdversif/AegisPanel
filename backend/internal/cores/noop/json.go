// SPDX-License-Identifier: AGPL-3.0-or-later

package noop

import (
	"bytes"
	"encoding/json"
)

// renderJSON serialises v with a trailing newline so the
// rendered config is a normal text file on disk and in logs.
// The newline matters for the agent, which often uses
// line-based change tracking to skip no-op writes.
func renderJSON(v any) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return buf.String(), nil
}

// jsonValid is a cheap "is this byte slice a valid JSON
// document?" check. We do not care about the result beyond
// yes/no, so we do not decode into a value.
func jsonValid(raw []byte) bool {
	return json.Valid(raw)
}
