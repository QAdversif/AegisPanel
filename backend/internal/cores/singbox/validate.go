// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// ValidateConfig implements cores.CoreProvider. The check is
// intentionally shallow: we confirm the payload is non-empty
// valid JSON and that it carries an "inbounds" array. A full
// schema-level validation would either re-implement sing-box's
// own internal schema (which is large, version-coupled, and
// has a different shape for every protocol) or shell out to
// the sing-box binary on the node — both of those belong to
// the agent, not the panel.
//
// The shallow check still catches the two most common
// mistakes before a config reaches the node:
//
//   - The agent has been pointed at a stale / truncated file
//     and is sending us back garbage. Empty payload or
//     non-JSON text fails here with a clear error.
//   - A render produced something the panel did not expect
//     (a future version of the renderer accidentally drops
//     the inbounds array). We catch that at apply time
//     instead of letting the agent retry in a loop.
func (p *Provider) ValidateConfig(_ context.Context, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("singbox: empty config")
	}
	// Trim trailing whitespace — the renderer always adds
	// a newline, and the agent often strips it. Validating
	// the trimmed body means neither side has to care.
	trimmed := bytes.TrimSpace(raw)
	if !json.Valid(trimmed) {
		return fmt.Errorf("singbox: config is not valid JSON")
	}
	// Peek into the top-level object to confirm the shape.
	// A more thorough check would unmarshal into a struct,
	// but that brings the schema back into the package —
	// exactly what the shallow validation tries to avoid.
	var top map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &top); err != nil {
		return fmt.Errorf("singbox: top-level is not a JSON object: %w", err)
	}
	inb, ok := top["inbounds"]
	if !ok {
		return fmt.Errorf("singbox: config is missing the %q array", "inbounds")
	}
	// The "inbounds" value must be an array. We don't
	// check it is non-empty here — a config with no
	// inbounds is structurally valid and the panel UI
	// catches the empty-state case before sending the
	// config to the agent.
	var arr []json.RawMessage
	if err := json.Unmarshal(inb, &arr); err != nil {
		return fmt.Errorf("singbox: %q must be a JSON array: %w", "inbounds", err)
	}
	return nil
}
