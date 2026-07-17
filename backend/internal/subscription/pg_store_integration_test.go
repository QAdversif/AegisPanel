// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for SubscriptionPgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/subscription/...
//
// The `//go:build integration` tag keeps `go test ./...`
// fast and dependency-free. CI runs the tagged suite
// with a service-container Postgres.
//
// # Setup
//
// Each test starts from a fresh database (created by
// `testutil.MustNewPool`, which DROPs + CREATEs the
// target DB and applies every migration in
// `migrations/`). The seeded default row from
// migration 0010 + the schema for users / plans /
// host_pools / host_pool_members / plan_pool are
// present at the start of every test. The tests seed
// their own data via raw SQL so the assertions are
// independent of any future MemoryStore-only helper.

package subscription

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/testutil"
)

// helpers ----------------------------------------------------------------

// runPgStore creates a fresh PgStore from the shared
// integration pool and returns the pool too — the
// helpers below need raw SQL access for seeding and
// for asserting on table state directly.
func runPgStore(t *testing.T) (*PgStore, *pgxpool.Pool) {
	t.Helper()
	pool := testutil.MustNewPool(t)
	return NewPgStore(pool), pool
}

// seedUser inserts a single user row with the given
// sub_token. Other fields are set to deterministic
// defaults so the test can read them back. Returns
// the row's id.
//
// The function deliberately writes a minimal row:
// the only fields the Store reads are the ones it
// has to. Future fields (e.g. telegram_id) are
// included in the INSERT so the row is
// "schema-complete" — a model change in the future
// that reads a new field will get a value back, not
// a NULL.
func seedUser(t *testing.T, pool *pgxpool.Pool, subToken string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO users (
			id, username, status, sub_token,
			traffic_limit_bytes, traffic_used_bytes, device_limit,
			hosts_allowlist, hosts_blocklist
		) VALUES (
			$1, $2, 'active', $3,
			$4, 0, 5,
			'[]'::JSONB, '[]'::JSONB
		)`
	if _, err := pool.Exec(context.Background(), q,
		id, "user-"+id.String()[:8], subToken,
		// traffic_limit_bytes: 100 GB
		100*1024*1024*1024,
	); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	return id
}

// seedPlan inserts a single plan row. Returns the id.
func seedPlan(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO plans (
			id, name, traffic_limit_bytes, duration,
			device_limit, reset_period, price_cents
		) VALUES (
			$1, $2, $3, INTERVAL '30 days',
			5, 'monthly', 0
		)`
	if _, err := pool.Exec(context.Background(), q,
		id, name,
		// traffic_limit_bytes: 100 GB
		100*1024*1024*1024,
	); err != nil {
		t.Fatalf("seed plan %q: %v", name, err)
	}
	return id
}

// seedPool inserts a single host_pools row. Returns
// the id.
func seedPool(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO host_pools (id, name, strategy, antiaffinity)
		VALUES ($1, $2, 'all', TRUE)`
	if _, err := pool.Exec(context.Background(), q, id, name); err != nil {
		t.Fatalf("seed pool %q: %v", name, err)
	}
	return id
}

// linkPlanPool inserts a row into `plan_pool`. The
// FK references are enforced by the migration.
func linkPlanPool(t *testing.T, pool *pgxpool.Pool, planID, hostPoolID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`INSERT INTO plan_pool (plan_id, pool_id) VALUES ($1, $2)`, planID, hostPoolID,
	); err != nil {
		t.Fatalf("link plan_pool: %v", err)
	}
}

// linkPlanToUser sets the user's `plan_id`. The
// `users.plan_id` column has no FK constraint in
// migration 0001, so the update is a simple column
// update.
func linkPlanToUser(t *testing.T, pool *pgxpool.Pool, userID, planID uuid.UUID) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		`UPDATE users SET plan_id = $1 WHERE id = $2`, planID, userID,
	); err != nil {
		t.Fatalf("link user to plan: %v", err)
	}
}

// seedPoolMember inserts a single host_pool_members
// row. The host must already exist (the FK enforces
// it); this helper takes a `hostID` directly. The
// test creates the host via the hosts.PgStore or a
// raw insert (for tests that do not need a real
// host row, a fake UUID would fail the FK).
func seedPoolMember(t *testing.T, pool *pgxpool.Pool, hostPoolID, hostID uuid.UUID, weight int) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO host_pool_members (pool_id, host_id, weight)
		VALUES ($1, $2, $3)`, hostPoolID, hostID, weight,
	); err != nil {
		t.Fatalf("seed pool member: %v", err)
	}
}

// seedHost inserts a single hosts row. We use the
// raw SQL because the hosts package's Service is
// not part of the test scope (the test only needs
// the host row to exist for the FK).
func seedHost(t *testing.T, pool *pgxpool.Pool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	const q = `
		INSERT INTO hosts (
			id, remark, type, enabled, priority,
			status_filter, address, sni, host,
			transport_settings, http_headers
		) VALUES (
			$1, $2, 'direct', TRUE, 100,
			'[]'::JSONB, '[]'::JSONB, '[]'::JSONB, '[]'::JSONB,
			'{}'::JSONB, '{}'::JSONB
		)`
	if _, err := pool.Exec(context.Background(), q, id, "host-"+id.String()[:8]); err != nil {
		t.Fatalf("seed host: %v", err)
	}
	return id
}

// GetUserBySubToken ----------------------------------------------------

// TestPgStore_GetUserBySubToken_Found — the primary
// lookup path. A seeded user is addressable by its
// `sub_token`.
func TestPgStore_GetUserBySubToken_Found(t *testing.T) {
	store, pool := runPgStore(t)
	const token = "token-aaaa-bbbb-cccc"
	id := seedUser(t, pool, token)

	got, err := store.GetUserBySubToken(context.Background(), token)
	if err != nil {
		t.Fatalf("GetUserBySubToken: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %v, want %v", got.ID, id)
	}
	if got.SubToken != token {
		t.Errorf("SubToken = %q, want %q", got.SubToken, token)
	}
	if got.Status != UserStatusActive {
		t.Errorf("Status = %q, want %q", got.Status, UserStatusActive)
	}
}

// TestPgStore_GetUserBySubToken_UnknownReturnsErrNotFound — a
// token that was never assigned must return
// ErrNotFound, not a nil error.
func TestPgStore_GetUserBySubToken_UnknownReturnsErrNotFound(t *testing.T) {
	store, _ := runPgStore(t)
	_, err := store.GetUserBySubToken(context.Background(), "no-such-token-zzzz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// GetUserByPrevSubToken -------------------------------------------------

// TestPgStore_GetUserByPrevSubToken_Found — after a
// rotation, the prior `sub_token` is queryable via
// the prev-token index. The partial UNIQUE index
// (migration 0011) keeps the lookup O(log n).
func TestPgStore_GetUserByPrevSubToken_Found(t *testing.T) {
	store, pool := runPgStore(t)
	const oldToken = "old-token-aaaa"
	id := seedUser(t, pool, oldToken)

	// Rotate to install the new + prev. The default
	// `prevExpiresAt = nil` is fine for the lookup
	// test (the Store does not consult the grace).
	if err := store.UpdateSubToken(context.Background(), id, "new-token-bbbb", nil); err != nil {
		t.Fatalf("UpdateSubToken: %v", err)
	}

	got, err := store.GetUserByPrevSubToken(context.Background(), oldToken)
	if err != nil {
		t.Fatalf("GetUserByPrevSubToken: %v", err)
	}
	if got.ID != id {
		t.Errorf("ID = %v, want %v", got.ID, id)
	}
	if got.SubToken != "new-token-bbbb" {
		t.Errorf("SubToken = %q, want %q (the new primary)", got.SubToken, "new-token-bbbb")
	}
	if got.SubTokenPrev != oldToken {
		t.Errorf("SubTokenPrev = %q, want %q", got.SubTokenPrev, oldToken)
	}
}

// TestPgStore_GetUserByPrevSubToken_UnknownReturnsErrNotFound — a
// token that was never a prev must return ErrNotFound.
func TestPgStore_GetUserByPrevSubToken_UnknownReturnsErrNotFound(t *testing.T) {
	store, _ := runPgStore(t)
	_, err := store.GetUserByPrevSubToken(context.Background(), "no-such-prev-zzzz")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// GetUserByID -----------------------------------------------------------

// TestPgStore_GetUserByID_Found — the simplest read
// path. Returns the same user that
// GetUserBySubToken would, but by primary key.
func TestPgStore_GetUserByID_Found(t *testing.T) {
	store, pool := runPgStore(t)
	const token = "token-uuid-lookup"
	id := seedUser(t, pool, token)

	got, err := store.GetUserByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if got.SubToken != token {
		t.Errorf("SubToken = %q, want %q", got.SubToken, token)
	}
}

// TestPgStore_GetUserByID_UnknownReturnsErrNotFound — a
// non-existent UUID must return ErrNotFound.
func TestPgStore_GetUserByID_UnknownReturnsErrNotFound(t *testing.T) {
	store, _ := runPgStore(t)
	_, err := store.GetUserByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// UpdateSubToken -------------------------------------------------------

// TestPgStore_UpdateSubToken_MovesPrimaryToPrev —
// after a rotation, the new primary token resolves
// via GetUserBySubToken, and the old primary
// resolves via GetUserByPrevSubToken. The
// `sub_token_rotated_at` and `updated_at` fields are
// bumped to the same `clock_timestamp()` value.
func TestPgStore_UpdateSubToken_MovesPrimaryToPrev(t *testing.T) {
	store, pool := runPgStore(t)
	const oldToken = "rotate-old-aaaa"
	id := seedUser(t, pool, oldToken)
	const newToken = "rotate-new-bbbb"

	// Read the prior `updated_at` so we can assert the
	// column was bumped after the rotation. The Go
	// model does not expose `updated_at` so we read
	// it via raw SQL.
	var beforeUpdatedAt time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM users WHERE id = $1`, id,
	).Scan(&beforeUpdatedAt); err != nil {
		t.Fatalf("read before updated_at: %v", err)
	}

	expires := time.Now().Add(24 * time.Hour).UTC()
	if err := store.UpdateSubToken(context.Background(), id, newToken, &expires); err != nil {
		t.Fatalf("UpdateSubToken: %v", err)
	}

	// updated_at must have been bumped to a value
	// after `beforeUpdatedAt`.
	var afterUpdatedAt time.Time
	if err := pool.QueryRow(context.Background(),
		`SELECT updated_at FROM users WHERE id = $1`, id,
	).Scan(&afterUpdatedAt); err != nil {
		t.Fatalf("read after updated_at: %v", err)
	}
	if !afterUpdatedAt.After(beforeUpdatedAt) {
		t.Errorf("updated_at = %v, want > %v (must be bumped)", afterUpdatedAt, beforeUpdatedAt)
	}

	// New primary.
	got, err := store.GetUserBySubToken(context.Background(), newToken)
	if err != nil {
		t.Fatalf("GetUserBySubToken(new): %v", err)
	}
	if got.ID != id {
		t.Errorf("GetUserBySubToken(new) ID = %v, want %v", got.ID, id)
	}
	if got.SubToken != newToken {
		t.Errorf("SubToken = %q, want %q", got.SubToken, newToken)
	}
	if got.SubTokenPrev != oldToken {
		t.Errorf("SubTokenPrev = %q, want %q", got.SubTokenPrev, oldToken)
	}
	if got.SubTokenPrevExpiresAt == nil {
		t.Errorf("SubTokenPrevExpiresAt = nil, want set when grace is set")
	} else if !got.SubTokenPrevExpiresAt.Equal(expires) {
		t.Errorf("SubTokenPrevExpiresAt = %v, want %v", got.SubTokenPrevExpiresAt, expires)
	}
	if got.SubTokenRotatedAt == nil {
		t.Errorf("SubTokenRotatedAt = nil, want set after rotation")
	}

	// Old primary is now the prev.
	prev, err := store.GetUserByPrevSubToken(context.Background(), oldToken)
	if err != nil {
		t.Fatalf("GetUserByPrevSubToken(old): %v", err)
	}
	if prev.ID != id {
		t.Errorf("GetUserByPrevSubToken(old) ID = %v, want %v", prev.ID, id)
	}
}

// TestPgStore_UpdateSubToken_NotFound — updating a
// non-existent user must return ErrNotFound.
func TestPgStore_UpdateSubToken_NotFound(t *testing.T) {
	store, _ := runPgStore(t)
	err := store.UpdateSubToken(context.Background(), uuid.New(), "new-tok", nil)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_UpdateSubToken_DropsEarlierPrimary —
// after a rotation, the old primary is no longer
// addressable via the primary lookup. This is the
// invariant the MemoryStore enforces with its
// `delete(usersByToken, old)` step; the PgStore
// enforces it via the SQL UPDATE that overwrites the
// primary column.
func TestPgStore_UpdateSubToken_DropsEarlierPrimary(t *testing.T) {
	store, pool := runPgStore(t)
	const oldToken = "rot-old-zzzz"
	id := seedUser(t, pool, oldToken)
	const newToken = "rot-new-yyyy"

	if err := store.UpdateSubToken(context.Background(), id, newToken, nil); err != nil {
		t.Fatalf("UpdateSubToken: %v", err)
	}

	// Old primary must NOT resolve via GetUserBySubToken
	// (it's now a prev, not a primary).
	_, err := store.GetUserBySubToken(context.Background(), oldToken)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("old primary after rotation: err = %v, want ErrNotFound", err)
	}
}

// ListPoolsForUser -----------------------------------------------------

// TestPgStore_ListPoolsForUser_NoPlanReturnsEmpty — a
// user without a `plan_id` (NULL) gets an empty
// pool list, not an error. Matches the MemoryStore.
func TestPgStore_ListPoolsForUser_NoPlanReturnsEmpty(t *testing.T) {
	store, pool := runPgStore(t)
	id := seedUser(t, pool, "no-plan-user")
	got, err := store.ListPoolsForUser(context.Background(), &User{ID: id})
	if err != nil {
		t.Fatalf("ListPoolsForUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(pools) = %d, want 0 (user has no plan)", len(got))
	}
}

// TestPgStore_ListPoolsForUser_PlanNotLinkedReturnsEmpty —
// a user with a plan_id that has no `plan_pool`
// entries gets an empty list.
func TestPgStore_ListPoolsForUser_PlanNotLinkedReturnsEmpty(t *testing.T) {
	store, pool := runPgStore(t)
	planID := seedPlan(t, pool, "unlinked")
	userID := seedUser(t, pool, "user-unlinked-plan")
	linkPlanToUser(t, pool, userID, planID)

	got, err := store.ListPoolsForUser(context.Background(), &User{ID: userID, PlanID: &planID})
	if err != nil {
		t.Fatalf("ListPoolsForUser: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(pools) = %d, want 0 (plan not linked to any pool)", len(got))
	}
}

// TestPgStore_ListPoolsForUser_PlanLinkedReturnsPools —
// the happy path: the user's plan is linked to one
// or more host_pools via `plan_pool`, and the Store
// returns them in id order.
func TestPgStore_ListPoolsForUser_PlanLinkedReturnsPools(t *testing.T) {
	store, pool := runPgStore(t)
	planID := seedPlan(t, pool, "linked")
	poolA := seedPool(t, pool, "eu")
	poolB := seedPool(t, pool, "us")
	poolC := seedPool(t, pool, "ap") // not linked — must not appear
	linkPlanPool(t, pool, planID, poolA)
	linkPlanPool(t, pool, planID, poolB)

	userID := seedUser(t, pool, "user-linked-plan")
	linkPlanToUser(t, pool, userID, planID)

	got, err := store.ListPoolsForUser(context.Background(), &User{ID: userID, PlanID: &planID})
	if err != nil {
		t.Fatalf("ListPoolsForUser: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("len(pools) = %d, want 2 (eu + us; ap not linked)", len(got))
	}
	// Sorted by id.
	if got[0].ID != poolA {
		t.Errorf("got[0].ID = %v, want %v", got[0].ID, poolA)
	}
	if got[1].ID != poolB {
		t.Errorf("got[1].ID = %v, want %v", got[1].ID, poolB)
	}
	// The unlinked pool must not be in the result.
	for _, p := range got {
		if p.ID == poolC {
			t.Errorf("unlinked pool %v appeared in result", p.ID)
		}
	}
}

// ListPoolsAll ---------------------------------------------------------

// TestPgStore_ListPoolsAll_EmptyDBReturnsEmptySlice —
// a fresh DB has no pools, so ListPoolsAll returns
// an empty (non-nil) slice. The non-nil property
// matters: callers `range` over the result and a
// nil slice would force a nil check.
func TestPgStore_ListPoolsAll_EmptyDBReturnsEmptySlice(t *testing.T) {
	store, _ := runPgStore(t)
	got, err := store.ListPoolsAll(context.Background())
	if err != nil {
		t.Fatalf("ListPoolsAll: %v", err)
	}
	if got == nil {
		t.Errorf("got = nil, want empty (non-nil) slice")
	}
	if len(got) != 0 {
		t.Errorf("len(pools) = %d, want 0", len(got))
	}
}

// TestPgStore_ListPoolsAll_AllSeededPoolsReturned —
// every seeded pool is returned, sorted by id.
func TestPgStore_ListPoolsAll_AllSeededPoolsReturned(t *testing.T) {
	store, pool := runPgStore(t)
	ids := []uuid.UUID{
		seedPool(t, pool, "alpha"),
		seedPool(t, pool, "beta"),
		seedPool(t, pool, "gamma"),
	}

	got, err := store.ListPoolsAll(context.Background())
	if err != nil {
		t.Fatalf("ListPoolsAll: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(pools) = %d, want 3", len(got))
	}
	for i, p := range got {
		if p.ID != ids[i] {
			t.Errorf("got[%d].ID = %v, want %v (sorted by id)", i, p.ID, ids[i])
		}
	}
}

// ListPoolMembers ------------------------------------------------------

// TestPgStore_ListPoolMembers_EmptyPoolReturnsEmptySlice —
// a pool with no members returns an empty (non-nil)
// slice.
func TestPgStore_ListPoolMembers_EmptyPoolReturnsEmptySlice(t *testing.T) {
	store, pool := runPgStore(t)
	poolID := seedPool(t, pool, "empty")

	got, err := store.ListPoolMembers(context.Background(), poolID)
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if got == nil {
		t.Errorf("got = nil, want empty (non-nil) slice")
	}
	if len(got) != 0 {
		t.Errorf("len(members) = %d, want 0", len(got))
	}
}

// TestPgStore_ListPoolMembers_AllMembersReturnedSorted —
// every member is returned, sorted by host_id.
func TestPgStore_ListPoolMembers_AllMembersReturnedSorted(t *testing.T) {
	store, pool := runPgStore(t)
	poolID := seedPool(t, pool, "members")
	h1 := seedHost(t, pool)
	h2 := seedHost(t, pool)
	h3 := seedHost(t, pool)
	seedPoolMember(t, pool, poolID, h1, 1)
	seedPoolMember(t, pool, poolID, h2, 2)
	seedPoolMember(t, pool, poolID, h3, 3)

	got, err := store.ListPoolMembers(context.Background(), poolID)
	if err != nil {
		t.Fatalf("ListPoolMembers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("len(members) = %d, want 3", len(got))
	}
	// Sorted by host_id ascending.
	want := []uuid.UUID{h1, h2, h3}
	sortUUIDs(want)
	for i, m := range got {
		if m.HostID != want[i] {
			t.Errorf("got[%d].HostID = %v, want %v", i, m.HostID, want[i])
		}
	}
	// Weights round-trip.
	wantWeights := []int{1, 2, 3}
	for i, m := range got {
		if m.Weight != wantWeights[i] {
			t.Errorf("got[%d].Weight = %d, want %d", i, m.Weight, wantWeights[i])
		}
	}
}

// TestPgStore_ListPoolMembers_OtherPoolMembersNotReturned —
// seeding a member into a different pool does not
// leak into the queried pool.
func TestPgStore_ListPoolMembers_OtherPoolMembersNotReturned(t *testing.T) {
	store, pool := runPgStore(t)
	poolA := seedPool(t, pool, "a")
	poolB := seedPool(t, pool, "b")
	h1 := seedHost(t, pool)
	h2 := seedHost(t, pool)
	seedPoolMember(t, pool, poolA, h1, 1)
	seedPoolMember(t, pool, poolB, h2, 1)

	gotA, err := store.ListPoolMembers(context.Background(), poolA)
	if err != nil {
		t.Fatalf("ListPoolMembers(A): %v", err)
	}
	if len(gotA) != 1 || gotA[0].HostID != h1 {
		t.Errorf("pool A members = %v, want just h1 (%v)", gotA, h1)
	}

	gotB, err := store.ListPoolMembers(context.Background(), poolB)
	if err != nil {
		t.Fatalf("ListPoolMembers(B): %v", err)
	}
	if len(gotB) != 1 || gotB[0].HostID != h2 {
		t.Errorf("pool B members = %v, want just h2 (%v)", gotB, h2)
	}
}

// sortUUIDs sorts a slice of UUIDs in place. The Go
// stdlib does not have a UUID sorter, and we only
// need it for the test's "expected sort order"
// helper.
func sortUUIDs(ids []uuid.UUID) {
	for i := 1; i < len(ids); i++ {
		for j := i; j > 0 && ids[j-1].String() > ids[j].String(); j-- {
			ids[j-1], ids[j] = ids[j], ids[j-1]
		}
	}
}
