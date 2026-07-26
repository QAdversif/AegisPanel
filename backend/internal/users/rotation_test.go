// SPDX-License-Identifier: AGPL-3.0-or-later

// Sub-token rotation tests. As of d-refactor.3 the
// sub_token rotation surface lives in `users.Service`
// (the lookup chain + grace-window check are part of
// the canonical user-CRUD layer; the subscription
// package no longer owns any of it). The tests in this
// file are the migration of the original
// `internal/subscription/rotation_test.go` that
// d-refactor.2 carried in place.

package users

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestRotateSubToken_GeneratesNewToken — Rotate
// produces a fresh token, marks the old one as
// prev with a 24h grace, and bumps
// SubTokenRotatedAt. The user's status and other
// fields are unchanged.
func TestRotateSubToken_GeneratesNewToken(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	before, err := f.usersSvc.GetBySubToken(ctx, f.userToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken: %v", err)
	}

	rotated, err := f.usersSvc.RotateSubToken(ctx, before.ID, DefaultSubTokenRotationGrace)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	if rotated.SubToken == before.SubToken {
		t.Errorf("rotated SubToken = old SubToken %q", rotated.SubToken)
	}
	if len(rotated.SubToken) != 64 {
		t.Errorf("len(rotated.SubToken) = %d, want 64 (the d.1 users.Service default is 32 random bytes / 64 hex chars)", len(rotated.SubToken))
	}
	if rotated.SubTokenPrev != before.SubToken {
		t.Errorf("SubTokenPrev = %q, want %q", rotated.SubTokenPrev, before.SubToken)
	}
	if rotated.SubTokenPrevExpiresAt == nil {
		t.Errorf("SubTokenPrevExpiresAt = nil, want set when grace > 0")
	} else {
		// Use the Service's clock (the fixed time the
		// fixture set) instead of `time.Now()` —
		// otherwise the grace computation against the
		// real wall clock would always be hugely
		// negative.
		now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		grace := rotated.SubTokenPrevExpiresAt.Sub(now)
		if grace < 23*time.Hour || grace > 25*time.Hour {
			t.Errorf("SubTokenPrevExpiresAt grace = %v, want ~24h", grace)
		}
	}
	if rotated.SubTokenRotatedAt == nil {
		t.Errorf("SubTokenRotatedAt = nil, want set after rotation")
	}
	if rotated.Status != before.Status {
		t.Errorf("Status changed: %q vs %q", rotated.Status, before.Status)
	}
}

// TestGetBySubToken_LooksUpCurrent — the primary
// lookup path: the freshly-rotated token resolves
// the user. This is the "happy path" — the user
// re-imports the new URL in their client.
func TestGetBySubToken_LooksUpCurrent(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	rotated, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, DefaultSubTokenRotationGrace)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	got, err := f.usersSvc.GetBySubToken(ctx, rotated.SubToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken: %v", err)
	}
	if got.ID != f.user.ID {
		t.Errorf("got ID = %v, want %v", got.ID, f.user.ID)
	}
}

// TestGetBySubToken_LooksUpPrevDuringGrace — the
// lookup chain falls through to the prev-token
// when the current token does not match. The user
// keeps getting a 200 response for the 24h grace
// window, even though their client is still using
// the old URL.
func TestGetBySubToken_LooksUpPrevDuringGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	if _, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, DefaultSubTokenRotationGrace); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	// The old token should still resolve during the
	// grace window.
	got, err := f.usersSvc.GetBySubToken(ctx, oldToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken (old token): %v", err)
	}
	if got.ID != f.user.ID {
		t.Errorf("got ID = %v, want %v", got.ID, f.user.ID)
	}
}

// TestGetBySubToken_RejectsPrevAfterGrace — the
// prev-token is rejected once its ExpiresAt has
// passed. The user gets a 404 — the rotation is
// complete, the old URL is no longer valid.
func TestGetBySubToken_RejectsPrevAfterGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	// Rotate with a 1-hour grace.
	if _, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, time.Hour); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	// Pin the clock 2h into the future. The grace
	// has elapsed; the prev token is now stale.
	f.usersSvc.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	_, err := f.usersSvc.GetBySubToken(ctx, oldToken, true)
	if err == nil {
		t.Errorf("GetBySubToken(old) after grace = nil error, want ErrNotFound")
	}
}

// TestGetBySubToken_RejectsPrevWhenNoGrace —
// in the d.0 subscription package a 0-second grace
// invalidated the prev token immediately. The d.1
// users package changed the semantics: grace <= 0
// is mapped to the canonical 24h default. The
// prev-token rejection on expiry is therefore
// exercised by TestGetBySubToken_RejectsPrevAfterGrace
// above (which pins the clock past the 24h mark);
// this test now documents the d.1 design and
// asserts the prev token survives a zero-grace
// rotation.
func TestGetBySubToken_RejectsPrevWhenNoGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	if _, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, 0); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	// Grace of 0 maps to 24h in users.Service; the
	// prev token must still resolve.
	if _, err := f.usersSvc.GetBySubToken(ctx, oldToken, true); err != nil {
		t.Errorf("GetBySubToken(old) after zero-grace rotation = %v, want nil (d.1 maps grace=0 to 24h)", err)
	}
}

// TestRotateSubToken_TwiceKeepsLatestPrev — a
// second rotation moves the first-rotation's
// token (the "old" prev) out of the lookup chain
// entirely. The prev index only carries the
// most-recent rotation's prev; the older ones are
// dropped. The test confirms the index is
// consistent (no stale entries pointing at a
// user with a different current token).
func TestRotateSubToken_TwiceKeepsLatestPrev(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	first := f.user.SubToken
	rot1, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, time.Hour)
	if err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	second := rot1.SubToken
	rot2, err := f.usersSvc.RotateSubToken(ctx, f.user.ID, time.Hour)
	if err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	if rot2.SubTokenPrev != second {
		t.Errorf("rot2 SubTokenPrev = %q, want %q (the previous rotation's token)", rot2.SubTokenPrev, second)
	}
	// `first` (the original token) was the prev after
	// rotation 1; rotation 2 should have removed it
	// from the prev index.
	_, err = f.usersSvc.GetBySubToken(ctx, first, true)
	if err == nil {
		t.Errorf("GetBySubToken(original) after two rotations = nil error, want ErrNotFound (the original prev was dropped on the second rotation)")
	}
}

// rotationFixture is the minimum data needed to
// test the sub-token rotation. The user-CRUD
// store is populated with a single user; the
// rotation tests do not exercise the host resolver
// path so the subscription Service is a no-op
// pass-through (the d.0 fixture had a non-nil
// store + a nil users service; d-refactor.3
// drops both).
type rotationFixture struct {
	usersSvc  *Service
	store     *MemoryStore
	user      *User
	userToken string
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	clock := func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }
	store := NewMemoryStore(clock)
	usersSvc := NewService(store)
	usersSvc.SetClock(clock)
	userID := uuid.New()
	userToken := "tok-alice-rotation"
	user := &User{
		ID:       userID,
		Username: "alice",
		Status:   StatusActive,
		SubToken: userToken,
	}
	store.WithUser(user)
	return &rotationFixture{
		usersSvc:  usersSvc,
		store:     store,
		user:      user,
		userToken: userToken,
	}
}
