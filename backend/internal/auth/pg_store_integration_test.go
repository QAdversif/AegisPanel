// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/auth/...
//
// The `//go:build integration` tag keeps `go test ./...` fast and
// dependency-free for the default development loop. CI runs the
// tagged suite with a service-container Postgres.
package auth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// seedAdmin inserts a single admin row and returns its UUID. The
// password hash is a pre-computed argon2id value — we never log in
// against this user in the integration tests; we only exercise
// LookupUser and the refresh-token SQL paths.
func seedAdmin(t *testing.T, store *PgStore, username, role string, enabled bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO admins (id, username, email, password_hash, role, enabled)
		VALUES ($1, $2, $3, $4, $5, $6)`
	_, err := store.pool.Exec(context.Background(), q,
		id, username, username+"@example.test",
		"$argon2id$v=19$m=65536,t=1,p=4$AAAA$BBBB", // opaque to the tests
		role, enabled,
	)
	if err != nil {
		t.Fatalf("seed admin %q: %v", username, err)
	}
	return id
}

// ---------------------------------------------------------------------------
// LookupUser
// ---------------------------------------------------------------------------

func TestPgStore_LookupUser_Found(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "alice", "super-admin", true)

	got, err := store.LookupUser(context.Background(), "alice")
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	if got.Username != "alice" {
		t.Fatalf("username = %q, want alice", got.Username)
	}
	if !got.Scopes.Has(ScopeAdmin) {
		t.Fatalf("super-admin should have ScopeAdmin, got %v", got.Scopes)
	}
}

func TestPgStore_LookupUser_OperatorRole(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "bob", "operator", true)

	got, err := store.LookupUser(context.Background(), "bob")
	if err != nil {
		t.Fatalf("LookupUser: %v", err)
	}
	if got.Scopes.Has(ScopeAdmin) {
		t.Fatalf("operator should NOT have ScopeAdmin, got %v", got.Scopes)
	}
	if !got.Scopes.Has(ScopeRead) || !got.Scopes.Has(ScopeWrite) {
		t.Fatalf("operator should have read+write, got %v", got.Scopes)
	}
}

func TestPgStore_LookupUser_UnknownReturnsErrUnauthorised(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	_, err := store.LookupUser(context.Background(), "ghost")
	if !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("err = %v, want ErrUnauthorised", err)
	}
}

func TestPgStore_LookupUser_DisabledReturnsErrUnauthorised(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "carol", "viewer", false) // enabled=false

	_, err := store.LookupUser(context.Background(), "carol")
	if !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("disabled admin should collapse to ErrUnauthorised, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SaveRefresh + ConsumeRefresh — happy path
// ---------------------------------------------------------------------------

func TestPgStore_SaveRefresh_AndConsume(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	const hash = "deadbeef00000000deadbeef00000000deadbeef00000000deadbeef00000000" // 64 hex chars
	expires := time.Now().Add(1 * time.Hour).UTC()

	if err := store.SaveRefresh(context.Background(), uid.String(), hash, expires); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}

	got, err := store.ConsumeRefresh(context.Background(), hash)
	if err != nil {
		t.Fatalf("ConsumeRefresh: %v", err)
	}
	if got != uid.String() {
		t.Fatalf("userID = %q, want %q", got, uid.String())
	}
}

// ---------------------------------------------------------------------------
// ConsumeRefresh — error paths
// ---------------------------------------------------------------------------

func TestPgStore_ConsumeRefresh_AlreadyUsedReturnsErrInvalidToken(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	const hash = "cafebabe" + "00000000"
	if err := store.SaveRefresh(context.Background(), uid.String(), hash, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}
	if _, err := store.ConsumeRefresh(context.Background(), hash); err != nil {
		t.Fatalf("first consume: %v", err)
	}
	// Second consume — must fail.
	_, err := store.ConsumeRefresh(context.Background(), hash)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("second consume err = %v, want ErrInvalidToken", err)
	}
}

func TestPgStore_ConsumeRefresh_ExpiredReturnsErrInvalidToken(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	// expires_at in the past — the row exists but should not be claimable.
	const hash = "feedface" + "11111111"
	if err := store.SaveRefresh(context.Background(), uid.String(), hash, time.Now().Add(-1*time.Minute).UTC()); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}
	_, err := store.ConsumeRefresh(context.Background(), hash)
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired consume err = %v, want ErrInvalidToken", err)
	}
}

func TestPgStore_ConsumeRefresh_UnknownHashReturnsErrInvalidToken(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "alice", "super-admin", true)

	_, err := store.ConsumeRefresh(context.Background(), "deadbeef"+"22222222")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("unknown hash err = %v, want ErrInvalidToken", err)
	}
}

func TestPgStore_ConsumeRefresh_BadHexReturnsErrInvalidToken(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "alice", "super-admin", true)

	_, err := store.ConsumeRefresh(context.Background(), "not-hex-zzz")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("bad hex err = %v, want ErrInvalidToken", err)
	}
}

// ---------------------------------------------------------------------------
// RevokeChain
// ---------------------------------------------------------------------------

func TestPgStore_RevokeChain_MarksAllLiveTokens(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	// Three live tokens, one already consumed (RevokeChain should
	// only touch the live ones; touching consumed ones is a no-op).
	save := func(h string) {
		if err := store.SaveRefresh(context.Background(), uid.String(), h, time.Now().Add(time.Hour).UTC()); err != nil {
			t.Fatalf("SaveRefresh %s: %v", h, err)
		}
	}
	save("11111111" + "11111111")
	save("22222222" + "22222222")
	save("33333333" + "33333333")
	if _, err := store.ConsumeRefresh(context.Background(), "1111111111111111"); err != nil {
		t.Fatalf("pre-consume: %v", err)
	}

	if err := store.RevokeChain(context.Background(), uid.String()); err != nil {
		t.Fatalf("RevokeChain: %v", err)
	}

	// All three are now consumed (the first one was already, the
	// other two got marked). Subsequent consumes must fail.
	for _, h := range []string{"1111111111111111", "2222222222222222", "3333333333333333"} {
		_, err := store.ConsumeRefresh(context.Background(), h)
		if !errors.Is(err, ErrInvalidToken) {
			t.Fatalf("token %s should be revoked, got err = %v", h, err)
		}
	}
}

func TestPgStore_RevokeChain_LeavesOtherUsersAlone(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	alice := seedAdmin(t, store, "alice", "super-admin", true)
	bob := seedAdmin(t, store, "bob", "viewer", true)

	for _, h := range []string{"aaaaffff" + "aaaaffff", "bbbbcccc" + "bbbbcccc"} {
		if err := store.SaveRefresh(context.Background(), alice.String(), h, time.Now().Add(time.Hour).UTC()); err != nil {
			t.Fatalf("save alice %s: %v", h, err)
		}
	}
	bobHash := "dddddddd" + "eeeeeeee"
	if err := store.SaveRefresh(context.Background(), bob.String(), bobHash, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("save bob: %v", err)
	}

	if err := store.RevokeChain(context.Background(), alice.String()); err != nil {
		t.Fatalf("RevokeChain alice: %v", err)
	}

	// Alice's tokens are gone.
	if _, err := store.ConsumeRefresh(context.Background(), "aaaaffffaaaaffff"); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("alice token should be revoked, got %v", err)
	}
	// Bob's token is still live.
	got, err := store.ConsumeRefresh(context.Background(), bobHash)
	if err != nil {
		t.Fatalf("bob token should be claimable, got err %v", err)
	}
	if got != bob.String() {
		t.Fatalf("bob userID = %q, want %q", got, bob.String())
	}
}

func TestPgStore_RevokeChain_EmptyIsNoop(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	if err := store.RevokeChain(context.Background(), uid.String()); err != nil {
		t.Fatalf("RevokeChain on empty: %v", err)
	}
}

// ---------------------------------------------------------------------------
// FindRefreshUser
// ---------------------------------------------------------------------------

func TestPgStore_FindRefreshUser_DoesNotConsume(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	const hash = "99887766" + "55443322"
	if err := store.SaveRefresh(context.Background(), uid.String(), hash, time.Now().Add(time.Hour).UTC()); err != nil {
		t.Fatalf("SaveRefresh: %v", err)
	}

	// FindRefreshUser twice — both must succeed and return the same
	// user. Critically, neither call should mark the token consumed.
	for i := 0; i < 2; i++ {
		got, err := store.FindRefreshUser(context.Background(), hash)
		if err != nil {
			t.Fatalf("FindRefreshUser #%d: %v", i, err)
		}
		if got != uid.String() {
			t.Fatalf("FindRefreshUser #%d: userID = %q, want %q", i, got, uid.String())
		}
	}

	// Token is still claimable.
	if _, err := store.ConsumeRefresh(context.Background(), hash); err != nil {
		t.Fatalf("ConsumeRefresh after FindRefreshUser: %v", err)
	}
}

func TestPgStore_FindRefreshUser_UnknownReturnsErrInvalidToken(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	seedAdmin(t, store, "alice", "super-admin", true)

	_, err := store.FindRefreshUser(context.Background(), "deadbeef"+"abcdef01")
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
}

// ---------------------------------------------------------------------------
// End-to-end: reuse-revokes-chain (same scenario as the unit test,
// but on real SQL so the atomic UPDATE + the chain revocation are
// both proven against a real Postgres engine).
// ---------------------------------------------------------------------------

func TestPgStore_ReuseRevokesChain(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "alice", "super-admin", true)

	exp := time.Now().Add(time.Hour).UTC()

	// Two independent login chains.
	const tA = "1111111122222222"
	const tB = "3333333344444444"
	if err := store.SaveRefresh(context.Background(), uid.String(), tA, exp); err != nil {
		t.Fatalf("save A: %v", err)
	}
	if err := store.SaveRefresh(context.Background(), uid.String(), tB, exp); err != nil {
		t.Fatalf("save B: %v", err)
	}

	// Normal use of A.
	if _, err := store.ConsumeRefresh(context.Background(), tA); err != nil {
		t.Fatalf("first A consume: %v", err)
	}

	// Replay A — fails AND (via the chain revocation policy in
	// service.go) RevokeChain is called. In a unit test the service
	// does that for us; here we model the same call sequence to
	// prove the SQL handles it.
	if _, err := store.ConsumeRefresh(context.Background(), tA); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("replay A should be ErrInvalidToken, got %v", err)
	}
	if err := store.RevokeChain(context.Background(), uid.String()); err != nil {
		t.Fatalf("RevokeChain: %v", err)
	}

	// B is now revoked too.
	if _, err := store.ConsumeRefresh(context.Background(), tB); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("B should be revoked after chain policy, got %v", err)
	}
}
