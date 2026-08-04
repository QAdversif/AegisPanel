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

// ---------------------------------------------------------------------------
// GetByID — v0.8.2 fix for /me 500 on the pg backend.
// Pre-v0.8.2, Service.lookupByID type-asserted the store
// to *MemoryStore and fell through to "only supported
// for MemoryStore" on PgStore, which the /me handler
// mapped to HTTP 500. GetByID makes the call site
// store-agnostic. The integration test below is the
// canonical regression: it exercises the same SQL the
// production /me call now uses.
// ---------------------------------------------------------------------------

// TestPgStore_GetByID_Found pins the happy path: an admin
// row is seeded, GetByID returns the matching *User with
// the correct fields, including the role-derived Scopes.
func TestPgStore_GetByID_Found(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "dave", "super-admin", true)

	got, err := store.GetByID(context.Background(), uid.String())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != uid.String() {
		t.Fatalf("id = %q, want %q", got.ID, uid.String())
	}
	if got.Username != "dave" {
		t.Fatalf("username = %q, want dave", got.Username)
	}
	if !got.Scopes.Has(ScopeAdmin) {
		t.Fatalf("super-admin should have ScopeAdmin, got %v", got.Scopes)
	}
	if !got.Enabled {
		t.Fatal("enabled flag should be true")
	}
}

// TestPgStore_GetByID_OperatorRole pins that an operator
// row resolves to a non-admin scope set. The role -> scopes
// mapping is the only place where the wire role enum meets
// the internal Scope vocabulary; a bug here would
// accidentally grant (or withhold) admin on /me.
func TestPgStore_GetByID_OperatorRole(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "eve", "operator", true)

	got, err := store.GetByID(context.Background(), uid.String())
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Scopes.Has(ScopeAdmin) {
		t.Fatalf("operator should NOT have ScopeAdmin, got %v", got.Scopes)
	}
	if !got.Scopes.Has(ScopeRead) || !got.Scopes.Has(ScopeWrite) {
		t.Fatalf("operator should have read+write, got %v", got.Scopes)
	}
}

// TestPgStore_GetByID_UnknownReturnsErrUnauthorised pins
// the "not found" -> ErrUnauthorised collapse. A malformed
// / missing id is a 401 on the wire, not a 500.
func TestPgStore_GetByID_UnknownReturnsErrUnauthorised(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	bogus := uuid.New().String()
	if _, err := store.GetByID(context.Background(), bogus); !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("err = %v, want ErrUnauthorised", err)
	}
}

// TestPgStore_GetByID_DisabledReturnsErrUnauthorised pins
// the "disabled" -> ErrUnauthorised collapse. A disabled
// admin row should not be discoverable via /me; the
// collapse matches LookupUser so the wire response is
// indistinguishable from a missing id.
func TestPgStore_GetByID_DisabledReturnsErrUnauthorised(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "frank", "viewer", false) // enabled=false

	if _, err := store.GetByID(context.Background(), uid.String()); !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("disabled admin should collapse to ErrUnauthorised, got %v", err)
	}
}

// TestPgStore_GetByID_MalformedIDReturnsErrUnauthorised
// pins the malformed-UUID guard. A tampered JWT whose
// Subject is not a valid UUID is the same security class
// as a JWT whose Subject points at a missing row: the
// caller is unauthenticated. Returning a 4xx (mapped from
// ErrUnauthorised) is the right semantics; returning a 5xx
// would leak "we tried to parse the id and failed" to the
// caller, which is a side-channel the project does not
// want.
func TestPgStore_GetByID_MalformedIDReturnsErrUnauthorised(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	if _, err := store.GetByID(context.Background(), "not-a-uuid"); !errors.Is(err, ErrUnauthorised) {
		t.Fatalf("err = %v, want ErrUnauthorised", err)
	}
}

// TestPgStore_MeStyleLookup exercises the canonical
// /me call path against a real Postgres: seed an admin,
// resolve the user by ID through GetByID, confirm the
// returned *User has the right ID / username / scopes.
// Pre-v0.8.2, this would fail with the "only supported
// for MemoryStore" error from the type-asserted walk
// in Service.lookupByID. v0.8.2 makes the call site
// store-agnostic; the regression here is the closest
// thing to an end-to-end /me test we can do without
// spinning up the HTTP layer.
func TestPgStore_MeStyleLookup(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	uid := seedAdmin(t, store, "grace", "super-admin", true)

	// Mirror Service.Me: claims.Subject -> store.GetByID.
	got, err := store.GetByID(context.Background(), uid.String())
	if err != nil {
		t.Fatalf("me-style lookup: %v", err)
	}
	if got.ID != uid.String() || got.Username != "grace" {
		t.Fatalf("got %+v, want id=%q username=grace", got, uid.String())
	}
	if !got.Scopes.Has(ScopeAdmin) {
		t.Fatalf("scopes missing admin: %v", got.Scopes)
	}
}
