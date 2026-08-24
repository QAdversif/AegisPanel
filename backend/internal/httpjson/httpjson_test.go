package httpjson

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestString_Correctness pins the JSON output for the cases that
// the previous hand-rolled escapers handled incorrectly
// (non-BMP runes) and for the cases they claimed to handle
// correctly (ASCII, control chars, quotes, backslashes, the
// HTML-unsafe triangle chars).
//
// IMPORTANT: this test compares against the *exact byte string*
// that `encoding/json.Marshal` produces. Two facts about that
// encoder drive the literals below:
//
//  1. Printable ASCII (< 0x80) is emitted verbatim, with the
//     single exception of `<`, `>`, `&`, the double quote,
//     the backslash, and the four standard JSON short-escape
//     control characters (\\b, \\f, \\n, \\r, \\t). Other
//     non-printable ASCII is escaped as `\\u00XX`.
//
//  2. Non-ASCII runes (BMP and non-BMP) are emitted as raw
//     UTF-8 bytes. The Go encoder does NOT auto-escape them
//     to `\\uXXXX` short escapes, because the spec-compliant
//     JSON parser handles raw UTF-8 perfectly well. The old
//     hand-rolled escapers that *did* escape them were
//     inconsistent (some escaped only the BMP, some escaped
//     every letter) and were the source of the `\\u1F680` bug
//     for non-BMP runes — but the encoder's *omission* of the
//     escape is not a bug, it is the correct behaviour, and
//     the wire format it produces is *also* valid JSON.
//
//  3. `<`, `>`, `&` are HTML-escaped to `\\u003c`, `\\u003e`,
//     `\\u0026` even in a JSON body. This is the default
//     `encoding/json` behaviour and we keep it on purpose:
//     the operator-facing UI embeds JSON inside <script>
//     tags, and HTML-escape makes that safe by default. If
//     you really want the raw form, opt out per-package
//     with `json.Encoder.SetEscapeHTML(false)`.
func TestString_Correctness(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "empty",
			in:   "",
			want: `""`,
		},
		{
			name: "ascii",
			in:   "hello world",
			want: `"hello world"`,
		},
		{
			name: "double-quote",
			in:   `say "hi"`,
			// Inside a Go raw string, `\"` is two
			// characters: backslash and double-quote.
			// json.Marshal escapes " as \" (also two
			// characters: backslash and double-quote).
			want: `"say \"hi\""`,
		},
		{
			name: "backslash",
			in:   `a\b\c`,
			want: `"a\\b\\c"`,
		},
		{
			name: "control-chars-newline-cr-tab",
			in:   "a\nb\rc\td",
			// Short escapes for the four control
			// characters that have them.
			want: `"a\nb\rc\td"`,
		},
		{
			name: "control-below-0x20-emitted-as-u00xx",
			// The old escapers dropped these silently.
			// json.Marshal keeps them as \u00XX. We pin
			// the new behaviour here.
			in:   "before\x01after",
			want: `"before\u0001after"`,
		},
		{
			name: "html-unsafe-triangle-and-amp",
			// Default json.Marshal HTML-escapes these
			// (see comment 2 above). Pin the behaviour.
			in:   "<script>&amp;",
			want: `"\u003cscript\u003e\u0026amp;"`,
		},
		{
			name: "cyrillic-bmp",
			in:   "привет",
			// BMP non-ASCII is NOT escaped (see
			// comment 2 above); the encoder keeps it
			// as raw UTF-8 bytes. The wire format
			// is valid JSON, the response is shorter,
			// and the round-trip through Unmarshal
			// recovers the original string exactly.
			want: `"привет"`,
		},
		{
			name: "emoji-non-bmp-u-1f680-rocket",
			// The whole point of this package:
			// non-BMP runes are emitted as raw UTF-8
			// (4 bytes for the rocket), which is
			// *valid* JSON. The old hand-rolled
			// escapers emitted 5- or 6-hex-digit
			// `\u1F680`, which is NOT a valid JSON
			// string escape and was rejected by
			// strict parsers.
			in:   "🚀",
			want: `"🚀"`,
		},
		{
			name: "emoji-mixed-with-text",
			in:   "hello 🚀 world",
			want: `"hello 🚀 world"`,
		},
		{
			name: "emoji-grinning-face",
			in:   "😀",
			want: `"😀"`,
		},
		{
			name: "long-message-truncation-not-applied",
			// The old code had a max length on error
			// messages (see `bootstrap/handler.go`).
			// We removed that here: there is no size
			// cap on error strings. If a future
			// requirement needs one, it should live
			// in WriteError's caller, not in the JSON
			// escaper.
			in:   strings.Repeat("x", 4096),
			want: `"` + strings.Repeat("x", 4096) + `"`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := String(c.in)
			if got != c.want {
				t.Fatalf("String(%q):\n got = %s\nwant = %s",
					c.in, got, c.want)
			}
			// Round-trip: the produced literal must
			// be valid JSON that decodes back to
			// c.in. This is the actual safety net —
			// the table comparison catches
			// regressions in *form*, this catches
			// regressions in *validity*.
			var rt string
			if err := json.Unmarshal([]byte(got), &rt); err != nil {
				t.Fatalf("String(%q) produced invalid JSON: %v\n  got = %s",
					c.in, err, got)
			}
			if rt != c.in {
				t.Fatalf("String(%q) round-trip mismatch:\n  got = %q\n want = %q",
					c.in, rt, c.in)
			}
		})
	}
}

// TestString_NonBMP_DoesNotProduceInvalidEscape is the
// regression-pinning negative test for the pre-2026-08-24
// `\u%04X` escaper bug. With the old hand-rolled escapers,
// formatting a non-BMP rune (anything above U+FFFF) produced
// a 5- or 6-hex-digit `\u` escape, which is not a valid JSON
// string escape: strict parsers (jq, Go's encoding/json with
// DisallowInvalidUTF8, Rust serde_json) reject the whole
// response; lenient parsers (JavaScript's JSON.parse) silently
// corrupt the character. This test fails if `String` ever
// regresses to producing those malformed escapes.
func TestString_NonBMP_DoesNotProduceInvalidEscape(t *testing.T) {
	// Five non-BMP runes covering common emoji ranges.
	nonBMP := []rune{
		'\U0001F680', // ROCKET
		'\U0001F600', // GRINNING FACE
		'\U0001F4A9', // PILE OF POO
		'\U0001F1FA', // regional indicator U
		'\U0001F1E6', // regional indicator A
	}
	for _, r := range nonBMP {
		got := String(string(r))
		// The wire format must be valid JSON that
		// round-trips through Unmarshal back to the
		// original rune. The old hand-rolled
		// escapers produced `"\u1F680"`, which is
		// *not* a valid JSON string escape and is
		// rejected by `encoding/json.Unmarshal` with
		// "invalid Unicode escape sequence". The
		// new path passes the round-trip.
		var rt string
		if err := json.Unmarshal([]byte(got), &rt); err != nil {
			t.Errorf("String(U+%04X) produced invalid JSON: %v\n  got = %s",
				r, err, got)
		} else if []rune(rt)[0] != r {
			t.Errorf("String(U+%04X) round-trip:\n  got = %q (U+%04X)\n want = %q (U+%04X)",
				r, rt, []rune(rt)[0], string(r), r)
		}
	}
}

// TestWriteJSON_ContentTypeAndStatus pins the Content-Type
// header and the status code. A surprising number of the old
// hand-rolled writers forgot `charset=utf-8`; the new package
// does not.
func TestWriteJSON_ContentTypeAndStatus(t *testing.T) {
	type payload struct {
		Hello string `json:"hello"`
	}
	cases := []struct {
		name   string
		status int
		in     any
	}{
		{"200", http.StatusOK, payload{Hello: "world"}},
		{"201", http.StatusCreated, payload{Hello: "created"}},
		{"400", http.StatusBadRequest, payload{Hello: "bad"}},
		{"500", http.StatusInternalServerError, payload{Hello: "fail"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteJSON(rr, c.status, c.in)
			if got := rr.Code; got != c.status {
				t.Errorf("status: got %d, want %d", got, c.status)
			}
			if got := rr.Header().Get("Content-Type"); got != contentType {
				t.Errorf("Content-Type: got %q, want %q", got, contentType)
			}
		})
	}
}

// TestWriteError_Envelope pins the error response shape. The
// OpenAPI document declares `{"error": "<msg>"}`; this test
// asserts the wire format matches, so any drift is caught at
// unit-test time rather than at frontend-parse time.
func TestWriteError_Envelope(t *testing.T) {
	cases := []struct {
		name   string
		status int
		msg    string
	}{
		{"simple", http.StatusBadRequest, "bad request"},
		{"emoji-msg", http.StatusBadRequest, "не получилось: 🚀"},
		{"quotes-msg", http.StatusBadRequest, `bad "input"`},
		{"empty-msg", http.StatusInternalServerError, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			WriteError(rr, c.status, c.msg)

			if got := rr.Code; got != c.status {
				t.Errorf("status: got %d, want %d", got, c.status)
			}
			if got := rr.Header().Get("Content-Type"); got != contentType {
				t.Errorf("Content-Type: got %q, want %q", got, contentType)
			}

			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
				t.Fatalf("response body is not valid JSON: %v\n  body = %s",
					err, rr.Body.String())
			}
			if body.Error != c.msg {
				t.Errorf("error field round-trip:\n  got = %q\n want = %q",
					body.Error, c.msg)
			}
		})
	}
}
