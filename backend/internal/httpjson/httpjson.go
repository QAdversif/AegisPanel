// Package httpjson is the single point in the panel that writes
// JSON HTTP responses. It replaces the hand-rolled `writeJSON`,
// `writeError`, `jsonString`, and `jsonEscape` copies that lived
// in eleven handler files across the backend.
//
// # Why this package exists
//
// The pre-existing copies each contained a manual string escaper
// that formatted non-ASCII runes as `\u%04X` — a 4-hex-digit
// escape. That is invalid JSON for any rune outside the Basic
// Multilingual Plane (BMP): emoji (U+1F600 and up) and rare
// CJK / historical-script characters format as 5–6 hex digits,
// which strict JSON parsers reject outright and lenient parsers
// corrupt into a different character. `bootstrap/handler.go`
// went further and escaped *every* letter as `\uXXXX`, truncating
// non-BMP runes to their low 16 bits.
//
// `encoding/json` handles the full Unicode range correctly and
// emits proper surrogate pairs for non-BMP runes, so the
// migration is mechanical: replace every hand-rolled escaper
// with a call to `json.Marshal` (or `String` in this package
// when the call site needs an embedded string literal).
//
// # Response envelope contract
//
// The error envelope is `{"error": "<msg>"}`. This matches
// the `Error` schema in `docs/openapi.yaml` (see `schemas.Error`)
// and is the only error response shape the panel emits. Any
// new error response should be built with `WriteError`, not
// with a hand-rolled writer.
package httpjson

import (
	"encoding/json"
	"net/http"
)

// contentType is the single Content-Type the panel emits on
// JSON responses. `charset=utf-8` is mandatory: without it,
// browsers default to ISO-8859-1 for application/json, which
// mangles any non-ASCII body bytes. The panel has historically
// emitted bodies that contain non-ASCII content (node names,
// host remarks, user display names), so this is not a
// theoretical concern.
const contentType = "application/json; charset=utf-8"

// WriteJSON writes v as JSON to w with the given HTTP status.
// It sets Content-Type, writes the status, and encodes v. The
// encode error is intentionally swallowed: at this point the
// status line and headers have already been sent, so the only
// way to surface a failure is to abort the connection, which
// would corrupt the response framing for the client. Logging
// is the operator's signal; the client's signal is a truncated
// body, which is correct: the response was malformed mid-flight.
//
// If v is a `*json.RawMessage`, `json.Encoder` will emit it
// verbatim — the package does not second-guess the caller's
// shape.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// WriteError writes a single-field error envelope: `{"error":
// "<msg>"}` with the given HTTP status. This is the canonical
// error response shape for the panel; it is the only error body
// the OpenAPI document declares, and it is the only shape the
// frontend `toApiError` client knows how to parse.
//
// The frontend's `Error` type expects `{code, message}`, but
// the OpenAPI document declares `{error: string}` and the
// backend has always emitted the latter. The mismatch is
// tracked separately in #289 C1-batch and is not addressed by
// this package; this package just enforces consistency between
// the documented shape and the wire shape, which is its
// narrower scope.
func WriteError(w http.ResponseWriter, status int, msg string) {
	WriteJSON(w, status, struct {
		Error string `json:"error"`
	}{Error: msg})
}

// String returns the JSON-encoded form of s as a string literal
// (i.e. the value is wrapped in double quotes and special
// characters are escaped per RFC 8259). It is a convenience
// wrapper over `json.Marshal` for the call sites that need to
// embed a string value into a larger JSON construction without
// allocating a temporary `[]byte`.
//
// The string s is encoded with `encoding/json`, which handles
// the full Unicode range correctly. Non-BMP runes (emoji,
// rare CJK, etc.) are emitted as proper surrogate pairs
// (`\uD83D\uDE80` for U+1F680), not the malformed 5-6 digit
// `\u1F680` that the previous hand-rolled escapers produced.
func String(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
