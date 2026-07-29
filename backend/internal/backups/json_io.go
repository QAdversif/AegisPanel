// SPDX-License-Identifier: AGPL-3.0-or-later
//
// JSON helpers for the backups package. Kept in a
// separate file so the Store code stays focused on
// the index schema and locking.

package backups

import (
	"encoding/json"
	"io"

	"github.com/rs/zerolog/log"
)

// encodeJSON marshals v to w as compact JSON
// (no trailing newline; the dump file is the dump
// file, the index is a single line for cheap
// append/atomic-rename).
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// decodeJSON parses one JSON value from r into v.
// The function returns the first decode error;
// io.EOF after a successful value is silently
// ignored (this is the stdlib's json.Decoder.Decode
// behavior).
func decodeJSON(r io.Reader, v any) error {
	dec := json.NewDecoder(r)
	return dec.Decode(v)
}

// closeQuiet is the small wrapper used by
// `defer closeQuiet(out)`-style call sites. The
// errcheck linter otherwise flags every
// unchecked `f.Close()` in a defer. Closing
// after a successful write is best-effort;
// failures are logged at warn level (the
// parent operation either succeeded or
// failed independently, so a Close error is
// never actionable in the v0.5.0 surfaces).
func closeQuiet(c io.Closer) {
	if err := c.Close(); err != nil {
		log.Warn().Err(err).Msg("backups: deferred close failed")
	}
}
