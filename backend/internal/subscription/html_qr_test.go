// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

// TestBuildQRCodePng_ReturnsValidPng — the PNG bytes
// produced by buildQRCodePng decode with the standard
// library's `image/png` package and report a square
// image of the requested size. The size is the pixel
// dimension of one side; the QR is always square.
func TestBuildQRCodePng_ReturnsValidPng(t *testing.T) {
	pngBytes, err := buildQRCodePng("https://example.com/sub/abc?target=base64", 256)
	if err != nil {
		t.Fatalf("buildQRCodePng: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	bounds := img.Bounds()
	if bounds.Dx() != 256 || bounds.Dy() != 256 {
		t.Errorf("image size = %dx%d, want 256x256", bounds.Dx(), bounds.Dy())
	}
}

// TestBuildQRCodePng_ZeroSize — the QR library
// accepts a zero size and produces a 1x1 image. The
// handler never calls with size=0 (the field is a
// 256 constant); the test documents the library's
// behaviour so future callers know not to rely on a
// panic.
func TestBuildQRCodePng_ZeroSize(t *testing.T) {
	pngBytes, err := buildQRCodePng("x", 0)
	if err != nil {
		t.Fatalf("buildQRCodePng(0): %v", err)
	}
	// A 0-size QR is a 1x1 PNG (the library's lower
	// bound on the image dimension). We assert the
	// bytes are still a valid PNG, not the size
	// itself.
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		t.Errorf("zero-size QR is not a valid PNG: %v", err)
	}
}

// TestBuildQRCodeDataURL_HasDataPrefix — the data URL
// is `data:image/png;base64,<encoded>`. The handler
// drops the result into an `<img src="…">` so the
// prefix must be exactly the form browsers expect.
func TestBuildQRCodeDataURL_HasDataPrefix(t *testing.T) {
	url, err := buildQRCodeDataURL("https://example.com", 128)
	if err != nil {
		t.Fatalf("buildQRCodeDataURL: %v", err)
	}
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Errorf("data URL prefix missing; first 40 chars = %q", url[:min(len(url), 40)])
	}
	// Encoded payload is non-empty.
	enc := strings.TrimPrefix(url, "data:image/png;base64,")
	if enc == "" {
		t.Errorf("data URL has empty payload")
	}
}

// TestBuildQRCodePng_NegativeSize — the QR library
// accepts a negative size and produces a tiny image
// (1x1). The handler never calls with a negative
// size; the test documents the library's
// accommodation so future callers know not to rely
// on an error return.
func TestBuildQRCodePng_NegativeSize(t *testing.T) {
	pngBytes, err := buildQRCodePng("x", -1)
	if err != nil {
		t.Fatalf("buildQRCodePng(-1): %v", err)
	}
	if _, err := png.Decode(bytes.NewReader(pngBytes)); err != nil {
		t.Errorf("negative-size QR is not a valid PNG: %v", err)
	}
}
