// SPDX-License-Identifier: AGPL-3.0-or-later

package subscription

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

	before, err := f.svc.GetUserBySubToken(ctx, f.userToken)
	if err != nil {
		t.Fatalf("GetUserBySubToken: %v", err)
	}

	rotated, err := f.svc.RotateSubToken(ctx, before.ID, DefaultSubTokenRotationGrace)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	if rotated.SubToken == before.SubToken {
		t.Errorf("rotated SubToken = old SubToken %q", rotated.SubToken)
	}
	if len(rotated.SubToken) != 32 {
		t.Errorf("len(rotated.SubToken) = %d, want 32", len(rotated.SubToken))
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

// TestGetUserBySubToken_LooksUpCurrent — the
// primary lookup path: the freshly-rotated token
// resolves the user. This is the "happy path" —
// the user re-imports the new URL in their client.
func TestGetUserBySubToken_LooksUpCurrent(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	rotated, err := f.svc.RotateSubToken(ctx, f.user.ID, DefaultSubTokenRotationGrace)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	got, err := f.svc.GetUserBySubToken(ctx, rotated.SubToken)
	if err != nil {
		t.Fatalf("GetUserBySubToken: %v", err)
	}
	if got.ID != f.user.ID {
		t.Errorf("got ID = %v, want %v", got.ID, f.user.ID)
	}
}

// TestGetUserBySubToken_LooksUpPrevDuringGrace — the
// lookup chain falls through to the prev-token
// when the current token does not match. The user
// keeps getting a 200 response for the 24h grace
// window, even though their client is still using
// the old URL.
func TestGetUserBySubToken_LooksUpPrevDuringGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	if _, err := f.svc.RotateSubToken(ctx, f.user.ID, DefaultSubTokenRotationGrace); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	// The old token should still resolve during the
	// grace window.
	got, err := f.svc.GetUserBySubToken(ctx, oldToken)
	if err != nil {
		t.Fatalf("GetUserBySubToken (old token): %v", err)
	}
	if got.ID != f.user.ID {
		t.Errorf("got ID = %v, want %v", got.ID, f.user.ID)
	}
}

// TestGetUserBySubToken_RejectsPrevAfterGrace — the
// prev-token is rejected once its ExpiresAt has
// passed. The user gets a 404 — the rotation is
// complete, the old URL is no longer valid.
func TestGetUserBySubToken_RejectsPrevAfterGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	// Rotate with a 1-hour grace.
	if _, err := f.svc.RotateSubToken(ctx, f.user.ID, time.Hour); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	// Pin the clock 2h into the future. The grace
	// has elapsed; the prev token is now stale.
	f.svc.SetClock(func() time.Time { return time.Now().Add(2 * time.Hour) })
	_, err := f.svc.GetUserBySubToken(ctx, oldToken)
	if err == nil {
		t.Errorf("GetUserBySubToken(old) after grace = nil error, want NotFoundError")
	}
}

// TestGetUserBySubToken_RejectsPrevWhenNoGrace —
// when the rotation grace is zero, the prev token
// is invalid immediately. The user gets a 404.
func TestGetUserBySubToken_RejectsPrevWhenNoGrace(t *testing.T) {
	f := newRotationFixture(t)
	ctx := context.Background()

	oldToken := f.user.SubToken
	if _, err := f.svc.RotateSubToken(ctx, f.user.ID, 0); err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	_, err := f.svc.GetUserBySubToken(ctx, oldToken)
	if err == nil {
		t.Errorf("GetUserBySubToken(old) immediately after zero-grace rotation = nil error, want NotFoundError")
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
	rot1, err := f.svc.RotateSubToken(ctx, f.user.ID, time.Hour)
	if err != nil {
		t.Fatalf("Rotate 1: %v", err)
	}
	second := rot1.SubToken
	rot2, err := f.svc.RotateSubToken(ctx, f.user.ID, time.Hour)
	if err != nil {
		t.Fatalf("Rotate 2: %v", err)
	}
	if rot2.SubTokenPrev != second {
		t.Errorf("rot2 SubTokenPrev = %q, want %q (the previous rotation's token)", rot2.SubTokenPrev, second)
	}
	// `first` (the original token) was the prev after
	// rotation 1; rotation 2 should have removed it
	// from the prev index.
	_, err = f.svc.GetUserBySubToken(ctx, first)
	if err == nil {
		t.Errorf("GetUserBySubToken(original) after two rotations = nil error, want NotFoundError (the original prev was dropped on the second rotation)")
	}
}

// rotationFixture is the minimum data needed to
// test the sub-token rotation. The subscription
// store is populated with a single user; the user
// is entitled to a single host via a single pool
// (the resolver path is the same as the production
// path, but the rotation tests do not exercise it
// beyond the user lookup).
type rotationFixture struct {
	svc       *Service
	user      *User
	userToken string
}

func newRotationFixture(t *testing.T) *rotationFixture {
	t.Helper()
	store := NewMemoryStore()
	store.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	userID := uuid.New()
	userToken := "tok-alice-rotation"
	user := &User{
		ID:       userID,
		Username: "alice",
		Status:   UserStatusActive,
		SubToken: userToken,
	}
	store.WithUser(user)
	// The rotation tests do not exercise the host
	// resolver path, so the hosts / nodes / inbounds
	// services can be nil — the Service field
	// dereferences are only hit by ResolveHostsForUser
	// / ResolveEndpointsForUser, which the rotation
	// tests do not call.
	svc := NewService(store, nil, nil, nil)
	svc.SetClock(func() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) })
	return &rotationFixture{
		svc:       svc,
		user:      user,
		userToken: userToken,
	}
}
