// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTML sub-page QR code + per-client URL rendering
// (ARCHITECTURE.md §10.4 / §10.5).
//
// The HTML landing page serves three audiences at once:
//
//   - a phone camera scanning the QR code (the most
//     common path on iOS / Android);
//   - a desktop VPN client that has a "paste URL"
//     field and ignores the HTML body;
//   - a human operator who wants to copy / share a
//     per-format URL.
//
// The page therefore embeds all three subscription
// URLs (base64 / singbox / clash) plus a QR code that
// encodes the default (base64) URL.
//
// The QR code is a PNG, base64-embedded as a `data:`
// URI — no separate static asset, no second request.
// `go-qrcode` is a single-dep, MIT-licensed library
// that produces PNG via `qrcode.QRCode.PNG`.
//
// The page is intentionally framework-free: a phone
// camera must be able to render it without JavaScript
// and within the first second of the request
// returning. All interactivity is a tiny vanilla JS
// snippet (one click → one `navigator.clipboard.writeText`).
// The snippet degrades gracefully: no clipboard API
// → the user types / pastes manually.

package subscription

import (
	"encoding/base64"
	"fmt"

	"github.com/skip2/go-qrcode"
)

// buildQRCodePng encodes `content` as a QR code and
// returns the PNG bytes (suitable for direct write
// or further base64 wrapping). Size is the pixel
// width / height of the output image; the QR is
// square so a single number covers both. A recovery
// level of "Medium" handles the real-world scan
// robustness the panel needs: a phone camera with
// reflective screen protectors, scratched screens,
// etc. "Low" produces a smaller image that scans
// cleanly under ideal conditions but breaks easily
// in the field.
func buildQRCodePng(content string, size int) ([]byte, error) {
	q, err := qrcode.New(content, qrcode.Medium)
	if err != nil {
		return nil, fmt.Errorf("build qr: %w", err)
	}
	png, err := q.PNG(size)
	if err != nil {
		return nil, fmt.Errorf("render qr png: %w", err)
	}
	return png, nil
}

// buildQRCodeDataURL is the html-page-friendly form
// of buildQRCodePng: it returns a `data:image/png;
// base64,…` URL suitable for direct use in an
// `<img src="…">` attribute. The base64 encoding
// adds ~33% to the byte count vs the raw PNG; for a
// 256x256 medium-recovery QR this is ~5 KB, well
// under the panel's response-size budget.
func buildQRCodeDataURL(content string, size int) (string, error) {
	png, err := buildQRCodePng(content, size)
	if err != nil {
		return "", err
	}
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
}
