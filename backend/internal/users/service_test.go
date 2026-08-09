// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// dupUUID is a stable UUID used by the
// "duplicate allowlist entry" validation subtest.
var dupUUID = uuid.MustParse("11111111-2222-3333-4444-555555555555")

// newSvc is a one-line constructor for a Service
// backed by a fresh MemoryStore. The fixed clock
// keeps timestamps deterministic. We explicitly
// call SetClock so the Service's now and the
// MemoryStore's now agree (NewService defaults to
// time.Now on the Service side; SetClock
// propagates the override to the store).
func newSvc(t *testing.T) *Service {
	t.Helper()
	svc := NewService(newMemStore())
	svc.SetClock(fixedClock)
	return svc
}

// TestService_Create_HappyPath exercises the create
// path end-to-end: validation, ID/timestamp/
// sub_token generation, and round-trip.
func TestService_Create_HappyPath(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	in := CreateInput{
		Username:          "alice",
		TrafficLimitBytes: 5_000_000_000,
		DeviceLimit:       3,
		Email:             "alice@example.com",
	}
	u, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if u.ID == uuid.Nil {
		t.Errorf("ID is zero")
	}
	if u.Username != "alice" {
		t.Errorf("Username = %q, want %q", u.Username, "alice")
	}
	if u.Status != StatusActive {
		t.Errorf("Status = %q, want %q (default)", u.Status, StatusActive)
	}
	if u.SubToken == "" {
		t.Errorf("SubToken is empty")
	}
	if len(u.SubToken) != 64 { // 32 bytes hex
		t.Errorf("SubToken len = %d, want 64", len(u.SubToken))
	}
	if !u.CreatedAt.Equal(fixedClock()) {
		t.Errorf("CreatedAt = %v, want %v", u.CreatedAt, fixedClock())
	}
	if u.Email != "alice@example.com" {
		t.Errorf("Email = %q, want %q", u.Email, "alice@example.com")
	}
	if len(u.HostsAllowlist) != 0 || u.HostsAllowlist == nil {
		t.Errorf("HostsAllowlist = %v, want non-nil empty slice", u.HostsAllowlist)
	}
}

// TestService_Create_ValidationFailures is the
// negative-path test for the validators. Each
// subtest triggers a different field error.
func TestService_Create_ValidationFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		in   CreateInput
	}{
		{"empty-username", CreateInput{}},
		{"too-short-username", CreateInput{Username: "ab"}},
		{"too-long-username", CreateInput{Username: strings.Repeat("a", 33)}},
		{"leading-dot", CreateInput{Username: ".alice"}},
		{"trailing-dot", CreateInput{Username: "alice."}},
		{"invalid-char-space", CreateInput{Username: "ali ce"}},
		{"bad-status", CreateInput{Username: "alice", Status: "nope"}},
		{"neg-traffic-limit", CreateInput{Username: "alice", TrafficLimitBytes: -1}},
		{"neg-traffic-used", CreateInput{Username: "alice", TrafficUsedBytes: -1}},
		{"neg-device-limit", CreateInput{Username: "alice", DeviceLimit: -1}},
		{"bad-email", CreateInput{Username: "alice", Email: "not-an-email"}},
		{"huge-telegram-id", CreateInput{Username: "alice", TelegramID: ptrInt64(99_999_999_999)}},
		{"empty-allowlist-entry", CreateInput{Username: "alice", HostsAllowlist: []uuid.UUID{uuid.Nil}}},
		{"dup-allowlist-entry", CreateInput{Username: "alice", HostsAllowlist: []uuid.UUID{dupUUID, dupUUID}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc(t)
			_, err := svc.Create(ctx, tc.in)
			if err == nil {
				t.Fatalf("Create(%+v) returned no error", tc.in)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("err = %v, want errors.Is(err, ErrInvalid)", err)
			}
			var ve *ValidationError
			if !errors.As(err, &ve) {
				t.Errorf("err = %v, want *ValidationError", err)
			}
		})
	}
}

// TestService_Update_PatchFields is the
// pointer-fields behaviour test. Each subtest
// sets a single field via the UpdateInput and
// confirms the post-update state.
func TestService_Update_PatchFields(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	in := CreateInput{Username: "alice", TrafficLimitBytes: 1_000, DeviceLimit: 1, Email: "alice@example.com"}
	created, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	newStatus := StatusDisabled
	traffic := int64(2_000)
	dev := 5
	planID := uuid.New()
	updated, err := svc.Update(ctx, created.ID, UpdateInput{
		Status:            &newStatus,
		TrafficLimitBytes: &traffic,
		DeviceLimit:       &dev,
		PlanID:            &planID,
		Email:             ptrString(""),
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.Status != StatusDisabled {
		t.Errorf("Status = %q, want %q", updated.Status, StatusDisabled)
	}
	if updated.TrafficLimitBytes != 2_000 {
		t.Errorf("TrafficLimitBytes = %d, want 2000", updated.TrafficLimitBytes)
	}
	if updated.DeviceLimit != 5 {
		t.Errorf("DeviceLimit = %d, want 5", updated.DeviceLimit)
	}
	if updated.PlanID == nil || *updated.PlanID != planID {
		t.Errorf("PlanID = %v, want %s", updated.PlanID, planID)
	}
	if updated.Email != "" {
		t.Errorf("Email = %q, want empty (cleared)", updated.Email)
	}
	// Username was not in the patch, should be preserved.
	if updated.Username != "alice" {
		t.Errorf("Username = %q, want %q (preserved)", updated.Username, "alice")
	}
}

// TestService_Update_RenameCollision exercises the
// (username) UNIQUE-constraint on Update.
func TestService_Update_RenameCollision(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	if _, err := svc.Create(ctx, CreateInput{Username: "alice"}); err != nil {
		t.Fatalf("Create alice: %v", err)
	}
	bob, err := svc.Create(ctx, CreateInput{Username: "bob"})
	if err != nil {
		t.Fatalf("Create bob: %v", err)
	}
	_, err = svc.Update(ctx, bob.ID, UpdateInput{Username: ptrString("alice")})
	if !errors.Is(err, ErrDuplicate) {
		t.Errorf("err = %v, want ErrDuplicate", err)
	}
}

// TestService_RotateSubToken exercises migration
// 0011. The prev-token should be honoured for the
// configured grace window; a manual GetByID should
// reflect the rotation.
func TestService_RotateSubToken(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	created, err := svc.Create(ctx, CreateInput{Username: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	originalToken := created.SubToken
	rotated, err := svc.RotateSubToken(ctx, created.ID, 24*time.Hour)
	if err != nil {
		t.Fatalf("RotateSubToken: %v", err)
	}
	if rotated.SubToken == originalToken {
		t.Errorf("SubToken did not change after rotation")
	}
	if rotated.SubTokenPrev != originalToken {
		t.Errorf("SubTokenPrev = %q, want %q (old token)", rotated.SubTokenPrev, originalToken)
	}
	if rotated.SubTokenPrevExpiresAt == nil {
		t.Fatalf("SubTokenPrevExpiresAt is nil")
	}
	expected := fixedClock().Add(24 * time.Hour)
	if !rotated.SubTokenPrevExpiresAt.Equal(expected) {
		t.Errorf("SubTokenPrevExpiresAt = %v, want %v", rotated.SubTokenPrevExpiresAt, expected)
	}
	// Old token still works (within grace).
	got, err := svc.GetBySubToken(ctx, originalToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken(prev, usePrev=true): %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID via prev-token = %s, want %s", got.ID, created.ID)
	}
	// New token also works (current).
	got, err = svc.GetBySubToken(ctx, rotated.SubToken, true)
	if err != nil {
		t.Fatalf("GetBySubToken(current, usePrev=true): %v", err)
	}
	if got.ID != created.ID {
		t.Errorf("ID via current = %s, want %s", got.ID, created.ID)
	}
}

// TestService_Delete exercises the hard-delete
// path.
func TestService_Delete(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	u, err := svc.Create(ctx, CreateInput{Username: "alice"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = svc.Get(ctx, u.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
}

// TestService_UsernameValidation exercises the
// username format / charset rules. Each subtest
// asserts a single character-class boundary.
func TestService_UsernameValidation(t *testing.T) {
	cases := []struct {
		username string
		want     bool
	}{
		{"abc", true},
		{"alice", true},
		{"alice.bob", true},
		{"alice-bob", true},
		{"alice_bob", true},
		{"Alice", true}, // uppercase allowed (legacy Cabinet imports)
		{"123", true},
		{"a", false},                     // too short
		{"ab", false},                    // too short
		{strings.Repeat("a", 32), true},  // max
		{strings.Repeat("a", 33), false}, // too long
		{".alice", false},                // leading dot
		{"alice.", false},                // trailing dot
		{"ali ce", false},                // space
		{"alice@", false},                // invalid char
		{"alice/bob", false},             // invalid char
		{"", false},                      // empty
	}
	for _, tc := range cases {
		t.Run(tc.username, func(t *testing.T) {
			svc := newSvc(t)
			_, err := svc.Create(context.Background(), CreateInput{Username: tc.username})
			if tc.want && err != nil {
				t.Errorf("Create(%q) err = %v, want nil", tc.username, err)
			}
			if !tc.want && err == nil {
				t.Errorf("Create(%q) returned no error, want validation failure", tc.username)
			}
		})
	}
}

// ptrString / ptrInt64 are tiny helpers to take
// the address of a literal in UpdateInput fields.
func ptrString(s string) *string { return &s }
func ptrInt64(n int64) *int64    { return &n }

// TestService_AllowedUsersForNode exercises the
// v0.8.10+ per-node user allow-set that the Builder
// uses to filter per-inbound credentials. The
// underlying `expandHostsToNodes` logic is
// shared with `enqueueUserDelta` (v0.8.x PR #192);
// the `AllowedUsersForNode` method is the
// reverse-direction read of the same set (which
// users are on this node, instead of which nodes
// is this user on). The cases here mirror the
// fan-out tests in `batchapplier_fanout_test.go`
// so the read and write sides of the filter stay
// consistent.
func TestService_AllowedUsersForNode(t *testing.T) {
	nodeA := uuid.MustParse("aaaaaaaa-1111-1111-1111-aaaaaaaaaaaa")
	nodeB := uuid.MustParse("bbbbbbbb-2222-2222-2222-bbbbbbbbbbbb")
	hostX := uuid.MustParse("11111111-aaaa-aaaa-aaaa-111111111111")
	hostY := uuid.MustParse("22222222-bbbb-bbbb-bbbb-222222222222")

	// Create a few users with mixed filter
	// shapes. uNoFilter has neither allow nor
	// block list (default-allow, included on
	// every node). uAllowX has an allowlist
	// that points at hostX (which references
	// nodeA only). uBlockY has a blocklist
	// that points at hostY (which references
	// nodeB). uBoth has an allowlist (hostX)
	// AND a blocklist (hostY) — included only
	// on nodeA, excluded from nodeB.
	svc := newSvc(t)
	mk := func(name string, allow, block []uuid.UUID) *User {
		u, err := svc.Create(context.Background(), CreateInput{
			Username:       name,
			HostsAllowlist: allow,
			HostsBlocklist: block,
		})
		if err != nil {
			t.Fatalf("setup: Create %s: %v", name, err)
		}
		return u
	}
	uNoFilter := mk("u_no_filter", nil, nil)
	uAllowX := mk("u_allow_x", []uuid.UUID{hostX}, nil)
	uBlockY := mk("u_block_y", nil, []uuid.UUID{hostY})
	uBoth := mk("u_both", []uuid.UUID{hostX}, []uuid.UUID{hostY})
	// Inactive user — must be excluded by the
	// StatusActive filter in ListByStatus.
	disabled := StatusDisabled
	if _, err := svc.Create(context.Background(), CreateInput{
		Username: "u_inactive",
		Status:   disabled,
	}); err != nil {
		t.Fatalf("setup: Create inactive: %v", err)
	}

	svc.WithHosts(&stubHosts{
		hostToNodes: map[uuid.UUID][]uuid.UUID{
			hostX: {nodeA},
			hostY: {nodeB},
		},
	})

	// nodeA: uNoFilter (default-allow) +
	// uAllowX (allowlist matches) + uBlockY
	// (blocklist is hostY → nodeB, so nodeA is
	// not blocked) + uBoth (allowlist matches,
	// blocklist does not include nodeA) = 4
	// users.
	gotA, err := svc.AllowedUsersForNode(context.Background(), nodeA)
	if err != nil {
		t.Fatalf("AllowedUsersForNode(nodeA) error: %v", err)
	}
	if len(gotA) != 4 {
		t.Errorf("nodeA allow-set size = %d, want 4 (got = %v)", len(gotA), gotA)
	}
	if !containsAll(gotA, []uuid.UUID{uNoFilter.ID, uAllowX.ID, uBlockY.ID, uBoth.ID}) {
		t.Errorf("nodeA allow-set = %v, want contains [%v %v %v %v]",
			gotA, uNoFilter.ID, uAllowX.ID, uBlockY.ID, uBoth.ID)
	}

	// nodeB: uNoFilter (default-allow) is
	// included. uAllowX is excluded (allowlist
	// points at hostX = nodeA). uBlockY is
	// excluded (blocklist matches nodeB). uBoth
	// is excluded (blocklist matches nodeB).
	// 1 user total.
	gotB, err := svc.AllowedUsersForNode(context.Background(), nodeB)
	if err != nil {
		t.Fatalf("AllowedUsersForNode(nodeB) error: %v", err)
	}
	if len(gotB) != 1 || gotB[0] != uNoFilter.ID {
		t.Errorf("nodeB allow-set = %v, want [%v]", gotB, uNoFilter.ID)
	}
}

// TestService_AllowedUsersForNode_NilHosts asserts
// the fail-closed semantic: a non-empty allowlist
// with a nil `s.hosts` lookup yields an empty
// allow-set (the user is NOT in the result, even
// though the filter said "this host"). The
// `enqueueUserDelta` fan-out has the same
// behaviour; the read side mirrors it. Without
// this guard, a misconfigured panel (forgot to
// call `WithHosts` in main) would silently grant
// access on every node.
func TestService_AllowedUsersForNode_NilHosts(t *testing.T) {
	svc := newSvc(t)
	hostX := uuid.MustParse("11111111-aaaa-aaaa-aaaa-111111111111")
	if _, err := svc.Create(context.Background(), CreateInput{
		Username:       "u_allow_only",
		HostsAllowlist: []uuid.UUID{hostX},
	}); err != nil {
		t.Fatalf("setup: Create: %v", err)
	}
	// No svc.WithHosts(...) call — `s.hosts` is
	// nil. The user has a non-empty allowlist,
	// so AllowedUsersForNode must return an
	// empty set.
	got, err := svc.AllowedUsersForNode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("AllowedUsersForNode error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("nil-hosts + non-empty allowlist: got = %v, want empty (fail-closed)", got)
	}
}

// TestService_AllowedUsersForNode_DefaultAllow covers
// the "no allowlist, no blocklist" path: every
// active user is included regardless of the host
// lookup state.
func TestService_AllowedUsersForNode_DefaultAllow(t *testing.T) {
	svc := newSvc(t)
	u1, err := svc.Create(context.Background(), CreateInput{Username: "u1_default"})
	if err != nil {
		t.Fatalf("setup: Create u1: %v", err)
	}
	u2, err := svc.Create(context.Background(), CreateInput{Username: "u2_default"})
	if err != nil {
		t.Fatalf("setup: Create u2: %v", err)
	}
	// nil `s.hosts` is fine for the default-allow
	// case (no filter, no lookup needed).
	got, err := svc.AllowedUsersForNode(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("AllowedUsersForNode error: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("default-allow: got size = %d, want 2 (got = %v)", len(got), got)
	}
	if !containsAll(got, []uuid.UUID{u1.ID, u2.ID}) {
		t.Errorf("default-allow: got = %v, want contains [%v %v]", got, u1.ID, u2.ID)
	}
}

// containsAll reports whether `s` contains every
// element of `want` (order-independent).
func containsAll(s, want []uuid.UUID) bool {
	have := make(map[uuid.UUID]struct{}, len(s))
	for _, id := range s {
		have[id] = struct{}{}
	}
	for _, id := range want {
		if _, ok := have[id]; !ok {
			return false
		}
	}
	return true
}
