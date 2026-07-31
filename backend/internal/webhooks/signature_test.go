// SPDX-License-Identifier: AGPL-3.0-or-later

package webhooks

import (
	"errors"
	"strings"
	"testing"
)

func TestSignAndVerify_RoundTrip(t *testing.T) {
	t.Parallel()
	body := []byte(`{"hello":"world","n":42}`)
	const secret = "webhook-fixture-shared-secret-aaaaaaaaaaaaaaaa"
	header, err := Sign(body, secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if !strings.HasPrefix(header, SignatureHeader) {
		t.Fatalf("header %q does not start with %q", header, SignatureHeader)
	}
	if err := Verify(body, secret, header); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

func TestVerify_WrongSecret(t *testing.T) {
	t.Parallel()
	body := []byte(`{"a":1}`)
	header, err := Sign(body, "webhook-fixture-shared-secret-dddddddddddddd")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify(body, "webhook-fixture-shared-secret-eeeeeeeeeeeeee", header); err == nil {
		t.Fatalf("expected ErrBadSignature, got nil")
	} else if !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature, got %v", err)
	}
}

func TestVerify_TamperedBody(t *testing.T) {
	t.Parallel()
	const secret = "webhook-fixture-shared-secret-bbbbbbbbbbbbbbbb"
	header, err := Sign([]byte(`{"a":1}`), secret)
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	if err := Verify([]byte(`{"a":2}`), secret, header); err == nil {
		t.Fatalf("expected ErrBadSignature, got nil")
	}
}

func TestVerify_MissingPrefix(t *testing.T) {
	t.Parallel()
	body := []byte(`{}`)
	header, err := Sign(body, "webhook-fixture-shared-secret-cccccccccccccc")
	if err != nil {
		t.Fatalf("Sign: %v", err)
	}
	// Strip the "sha256=" prefix.
	bare := header[len(SignatureHeader):]
	if err := Verify(body, "webhook-fixture-shared-secret-cccccccccccccc", bare); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature on bare hex, got %v", err)
	}
}

func TestVerify_MalformedHex(t *testing.T) {
	t.Parallel()
	if err := Verify([]byte(`{}`), "webhook-fixture-shared-secret-cccccccccccccc", "sha256=zzz"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature on malformed hex, got %v", err)
	}
}

func TestVerify_EmptySecret(t *testing.T) {
	t.Parallel()
	// Sign with empty secret returns an error.
	if _, err := Sign([]byte(`{}`), ""); err == nil {
		t.Fatalf("expected error on empty secret, got nil")
	}
	// Verify with empty secret returns ErrBadSignature regardless.
	if err := Verify([]byte(`{}`), "", "sha256=deadbeef"); !errors.Is(err, ErrBadSignature) {
		t.Fatalf("expected ErrBadSignature on empty secret, got %v", err)
	}
}

func TestSign_Deterministic(t *testing.T) {
	t.Parallel()
	body := []byte(`{"x":"y"}`)
	const secret = "webhook-fixture-shared-secret-ffffffffffffff"
	a, err := Sign(body, secret)
	if err != nil {
		t.Fatalf("Sign a: %v", err)
	}
	b, err := Sign(body, secret)
	if err != nil {
		t.Fatalf("Sign b: %v", err)
	}
	if a != b {
		t.Fatalf("expected deterministic signature, got %q != %q", a, b)
	}
}
