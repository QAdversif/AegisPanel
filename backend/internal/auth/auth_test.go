// SPDX-License-Identifier: AGPL-3.0-or-later

package auth

import (
	"context"
	"strings"
	"testing"
	"time"
)

// newTestService returns a Service backed by an in-memory store
// with one seeded admin user. Reused across the auth test suite.
func newTestService(t *testing.T) *Service {
	t.Helper()
	hash, err := HashPassword("hunter2-correct-horse")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	store := NewMemoryStore().WithUser(&User{
		ID:           "u-1",
		Username:     "admin",
		PasswordHash: hash,
		Scopes:       Scopes{ScopeAdmin, ScopeRead, ScopeWrite},
	})
	signer := NewSigner("0123456789abcdef0123456789abcdef") // 32 bytes
	svc := NewService(signer, store)
	return svc
}

func TestLogin_Success(t *testing.T) {
	svc := newTestService(t)
	res, err := svc.Login(context.Background(), "admin", "hunter2-correct-horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if res.AccessToken == "" {
		t.Fatal("empty access token")
	}
	if res.RefreshToken == "" {
		t.Fatal("empty refresh token")
	}
	if !res.Scopes.Has(ScopeAdmin) {
		t.Fatalf("scopes missing admin: %v", res.Scopes)
	}
	if res.UserID != "u-1" {
		t.Fatalf("user id = %q, want u-1", res.UserID)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Login(context.Background(), "admin", "nope")
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
}

func TestLogin_UnknownUser_CollapsesTo401(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.Login(context.Background(), "ghost", "anything")
	if err == nil {
		t.Fatal("expected error for unknown user")
	}
	// ErrUnauthorised for both — the error must not distinguish
	// "no such user" from "wrong password".
}

func TestRefresh_RotatesToken(t *testing.T) {
	svc := newTestService(t)
	res1, err := svc.Login(context.Background(), "admin", "hunter2-correct-horse")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	res2, err := svc.Refresh(context.Background(), res1.RefreshToken)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if res2.RefreshToken == res1.RefreshToken {
		t.Fatal("refresh did not rotate token")
	}

	// Reusing the old refresh token must fail.
	if _, err := svc.Refresh(context.Background(), res1.RefreshToken); err == nil {
		t.Fatal("reused refresh token was accepted")
	}
}

func TestRefresh_BadFormat(t *testing.T) {
	svc := newTestService(t)
	if _, err := svc.Refresh(context.Background(), "not-hex"); err == nil {
		t.Fatal("expected error for malformed refresh token")
	}
}

// TestRefresh_ReuseRevokesChain simulates the most likely
// theft scenario: an attacker obtains a refresh token that
// the legitimate user already used. We do exactly what the
// attacker would — call Refresh twice with the same token. The
// second call must fail (good) and must also invalidate every
// other refresh token the user has, so the attacker cannot
// pivot to a sibling token the user might still be holding.
func TestRefresh_ReuseRevokesChain(t *testing.T) {
	svc := newTestService(t)

	// Legitimate user logs in twice. Each login yields its own
	// refresh token. The first is a "spent" chain (we will
	// reuse it below), the second is a "live" chain that must
	// be killed by the revocation.
	res1, err := svc.Login(context.Background(), "admin", "hunter2-correct-horse")
	if err != nil {
		t.Fatalf("login 1: %v", err)
	}
	res2, err := svc.Login(context.Background(), "admin", "hunter2-correct-horse")
	if err != nil {
		t.Fatalf("login 2: %v", err)
	}
	if res1.RefreshToken == res2.RefreshToken {
		t.Fatal("logins produced identical refresh tokens; expected distinct")
	}

	// Normal refresh on res1 — succeeds, issues a new pair.
	if _, err := svc.Refresh(context.Background(), res1.RefreshToken); err != nil {
		t.Fatalf("first refresh of res1: %v", err)
	}

	// Replay res1 — must fail AND must revoke res2.
	if _, err := svc.Refresh(context.Background(), res1.RefreshToken); err == nil {
		t.Fatal("replay of res1 was accepted; expected ErrInvalidToken")
	}

	// res2 must now be revoked too. The legitimate user's
	// next refresh attempt fails; they have to log in again.
	if _, err := svc.Refresh(context.Background(), res2.RefreshToken); err == nil {
		t.Fatal("res2 was not revoked after reuse of res1; expected ErrInvalidToken")
	}
}

func TestVerify_RoundTrip(t *testing.T) {
	svc := newTestService(t)
	svc.signer.now = func() time.Time { return time.Unix(1_700_000_000, 0).UTC() }
	tok, err := svc.signer.Issue("u-1", Scopes{ScopeAdmin, ScopeRead}, "jti-test")
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	claims, err := svc.signer.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if claims.Subject != "u-1" {
		t.Fatalf("subject = %q, want u-1", claims.Subject)
	}
	if !claims.HasScope(ScopeAdmin) {
		t.Fatalf("missing admin scope: %v", claims.Scopes)
	}
	if claims.JWTID != "jti-test" {
		t.Fatalf("jti = %q, want jti-test", claims.JWTID)
	}
}

func TestVerify_Tampered(t *testing.T) {
	svc := newTestService(t)
	tok, _ := svc.signer.Issue("u-1", Scopes{ScopeRead}, "jti")
	// Flip a byte in the signature segment.
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("expected 3-segment JWT, got %d", len(parts))
	}
	parts[2] = "AAAA" + parts[2][4:]
	if _, err := svc.signer.Verify(strings.Join(parts, ".")); err == nil {
		t.Fatal("tampered token was accepted")
	}
}

func TestVerify_Expired(t *testing.T) {
	svc := newTestService(t)
	// Issue at t0, then advance time past AccessTokenTTL.
	now := time.Unix(1_700_000_000, 0).UTC()
	svc.signer.SetClock(func() time.Time { return now })
	tok, _ := svc.signer.Issue("u-1", Scopes{ScopeRead}, "jti")

	// 16 minutes later — past the 15-min TTL.
	svc.signer.SetClock(func() time.Time { return now.Add(16 * time.Minute) })
	if _, err := svc.signer.Verify(tok); err == nil {
		t.Fatal("expired token was accepted")
	}
}

func TestScopesHelpers(t *testing.T) {
	s := Scopes{ScopeRead, ScopeWrite, ScopeRead} // duplicate
	if !s.Has(ScopeRead) {
		t.Fatal("Has(ScopeRead) = false, want true")
	}
	if s.Has(ScopeAdmin) {
		t.Fatal("Has(ScopeAdmin) = true, want false")
	}
	got := s.Strings()
	if len(got) != 2 {
		t.Fatalf("Strings() = %v, want 2 unique entries", got)
	}
}
