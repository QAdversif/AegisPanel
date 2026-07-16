// SPDX-License-Identifier: AGPL-3.0-or-later

package panelcfg

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestValidatePath — the path validator is the
// gatekeeper for URL-segment safety. The valid
// charset is [a-z0-9-] (lowercase letters, digits,
// dash) with length 4-64. Anything else returns
// ErrInvalidPath.
func TestValidatePath(t *testing.T) {
	cases := []struct {
		path    string
		wantErr bool
	}{
		// Valid paths.
		{"abcd", false},
		{"a1b2c3d4", false},
		{"s3cr3t-sub-1234567890", false},
		{strings.Repeat("a", 64), false},
		// Too short.
		{"", true},
		{"abc", true},
		// Too long.
		{strings.Repeat("a", 65), true},
		// Invalid characters.
		{"abc/def", true},
		{"abc def", true},
		{"ABC", true},        // uppercase
		{"abc_def", true},    // underscore
		{"abc.def", true},    // dot
		{"абвг", true},        // non-ASCII
		{"abc?", true},       // punctuation
		{"abc\ndef", true},    // newline
	}
	for _, c := range cases {
		err := ValidatePath(c.path)
		if (err != nil) != c.wantErr {
			t.Errorf("ValidatePath(%q) err = %v, wantErr = %v", c.path, err, c.wantErr)
		}
	}
}

// TestNewRandomSubPath — the random path generator
// returns a 16-char hex string, and consecutive
// calls produce distinct values (the random source
// is working).
func TestNewRandomSubPath(t *testing.T) {
	first, err := NewRandomSubPath()
	if err != nil {
		t.Fatalf("NewRandomSubPath: %v", err)
	}
	if len(first) != 16 {
		t.Errorf("len(first) = %d, want 16", len(first))
	}
	if err := ValidatePath(first); err != nil {
		t.Errorf("ValidatePath(%q) = %v, want nil", first, err)
	}
	second, err := NewRandomSubPath()
	if err != nil {
		t.Fatalf("NewRandomSubPath (2): %v", err)
	}
	if first == second {
		t.Errorf("two random calls produced the same value %q", first)
	}
}

// TestMemoryStore_GetActive_Default — the seeded
// default row is the empty sub_path with no
// rotation. The router uses this as the "no
// rotation" signal.
func TestMemoryStore_GetActive_Default(t *testing.T) {
	s := NewMemoryStore()
	got, err := s.GetActive(context.Background())
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if got.SubPath != DefaultSubPath {
		t.Errorf("SubPath = %q, want %q", got.SubPath, DefaultSubPath)
	}
	if !got.IsActive {
		t.Errorf("IsActive = false, want true")
	}
	if got.ID != SentinelID {
		t.Errorf("ID = %v, want sentinel %v", got.ID, SentinelID)
	}
}

// TestMemoryStore_SetActive_Rotates — SetActive
// inserts a new row and marks the old one inactive.
// The most recent active row is the one returned by
// GetActive.
func TestMemoryStore_SetActive_Rotates(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	first, err := s.SetActive(ctx, "path-aaaa11112222", 0)
	if err != nil {
		t.Fatalf("SetActive 1: %v", err)
	}
	got, err := s.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if got.ID != first.ID {
		t.Errorf("active ID = %v, want %v (the new row)", got.ID, first.ID)
	}
	if got.SubPath != "path-aaaa11112222" {
		t.Errorf("active SubPath = %q, want %q", got.SubPath, "path-aaaa11112222")
	}
	// The default row is now inactive.
	def, err := s.GetByID(ctx, SentinelID)
	if err != nil {
		t.Fatalf("GetByID(sentinel): %v", err)
	}
	if def.IsActive {
		t.Errorf("default row is_active = true, want false after rotation")
	}
}

// TestMemoryStore_SetActive_GraceWindow — the old
// row carries an `ExpiresAt` value when a grace
// window is set. GetActive does NOT return the old
// row even if its expiry is in the future (the
// "active" predicate is `is_active = true`).
func TestMemoryStore_SetActive_GraceWindow(t *testing.T) {
	s := NewMemoryStore()
	s.SetClock(func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) })
	ctx := context.Background()

	old, err := s.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	_, err = s.SetActive(ctx, "path-cccc33334444", time.Hour)
	if err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	def, err := s.GetByID(ctx, old.ID)
	if err != nil {
		t.Fatalf("GetByID(old): %v", err)
	}
	if def.IsActive {
		t.Errorf("old row still active after rotation")
	}
	if def.ExpiresAt == nil {
		t.Errorf("old row ExpiresAt = nil, want set when grace > 0")
	} else {
		// The rotation sets expiry = now + grace. With
		// a fixed clock, that is 12:00 + 1h = 13:00.
		want := time.Date(2026, 7, 16, 13, 0, 0, 0, time.UTC)
		if !def.ExpiresAt.Equal(want) {
			t.Errorf("ExpiresAt = %v, want %v", def.ExpiresAt, want)
		}
	}
}

// TestMemoryStore_SetActive_NoGrace — when the grace
// is zero, the old row has `ExpiresAt = nil` (the
// rotation is immediate).
func TestMemoryStore_SetActive_NoGrace(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	old, err := s.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	_, err = s.SetActive(ctx, "path-eeee55556666", 0)
	if err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	def, err := s.GetByID(ctx, old.ID)
	if err != nil {
		t.Fatalf("GetByID(old): %v", err)
	}
	if def.ExpiresAt != nil {
		t.Errorf("old row ExpiresAt = %v, want nil when grace = 0", def.ExpiresAt)
	}
}

// TestMemoryStore_SetActive_InvalidPath — an
// invalid path is rejected before any write.
func TestMemoryStore_SetActive_InvalidPath(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	cases := []string{"", "abc", "ABC", "abc/def", strings.Repeat("a", 65)}
	for _, p := range cases {
		_, err := s.SetActive(ctx, p, 0)
		if err == nil {
			t.Errorf("SetActive(%q) succeeded, want error", p)
		}
	}
}

// TestMemoryStore_Reset — Reset deactivates the
// rotated path and re-activates the default empty
// row. The active path is then the empty string
// again.
func TestMemoryStore_Reset(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()

	if _, err := s.SetActive(ctx, "path-ffff77778888", 0); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	reset, err := s.Reset(ctx)
	if err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if reset.SubPath != DefaultSubPath {
		t.Errorf("after Reset, SubPath = %q, want %q", reset.SubPath, DefaultSubPath)
	}
	got, err := s.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if got.SubPath != DefaultSubPath {
		t.Errorf("GetActive SubPath = %q, want %q", got.SubPath, DefaultSubPath)
	}
}

// TestService_Rotate_GeneratesRandomPath — the
// Service.Rotate path generates a fresh 16-char
// hex path. Two consecutive rotations produce
// different paths.
func TestService_Rotate_GeneratesRandomPath(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	first, err := svc.Rotate(ctx, 0)
	if err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	if err := ValidatePath(first.SubPath); err != nil {
		t.Errorf("rotated path %q: %v", first.SubPath, err)
	}
	second, err := svc.Rotate(ctx, 0)
	if err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	if first.SubPath == second.SubPath {
		t.Errorf("two rotations produced the same path %q", first.SubPath)
	}
}

// TestService_RotateTo_RejectsInvalid — RotateTo
// applies the validator to the user-supplied path.
// Invalid paths return ErrInvalidPath; the active
// row is not modified.
func TestService_RotateTo_RejectsInvalid(t *testing.T) {
	svc := NewService(NewMemoryStore())
	ctx := context.Background()
	before, err := svc.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	_, err = svc.RotateTo(ctx, "BAD/Path", 0)
	if err == nil {
		t.Errorf("RotateTo(BAD/Path) succeeded, want error")
	}
	after, err := svc.GetActive(ctx)
	if err != nil {
		t.Fatalf("GetActive: %v", err)
	}
	if after.SubPath != before.SubPath {
		t.Errorf("active path changed after a failed RotateTo: %q vs %q", after.SubPath, before.SubPath)
	}
}
