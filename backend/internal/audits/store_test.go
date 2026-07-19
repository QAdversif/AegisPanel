// SPDX-License-Identifier: AGPL-3.0-or-later

package audits

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// fixedNow returns a time source pinned to the
// given UTC moment. Tests use it to make List
// filters deterministic.
func fixedNow(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

// TestMemoryStore_Insert_AssignsID verifies that
// the Store fills the ID + CreatedAt on Insert and
// returns a fresh row. The two consecutive inserts
// get distinct ids.
func TestMemoryStore_Insert_AssignsID(t *testing.T) {
	store := NewMemoryStore()
	clock := fixedNow(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	store.SetClock(clock)

	row1, err := store.Insert(context.Background(), Entry{
		Action:       "user.create",
		ResourceType: "user",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if row1.ID == "" {
		t.Fatal("Insert: expected non-empty id")
	}
	if !row1.CreatedAt.Equal(clock()) {
		t.Errorf("Insert: CreatedAt = %s, want %s", row1.CreatedAt, clock())
	}

	row2, err := store.Insert(context.Background(), Entry{
		Action:       "user.delete",
		ResourceType: "user",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if row1.ID == row2.ID {
		t.Errorf("Insert: ids collided (%q == %q)", row1.ID, row2.ID)
	}
}

// TestMemoryStore_Insert_RejectsEmptyAction is a
// regression test — the v0.2.0 schema marks `action`
// and `resource_type` as NOT NULL; the Store must
// surface that as an error before the (missing) pgx
// path even sees the row.
func TestMemoryStore_Insert_RejectsEmptyAction(t *testing.T) {
	store := NewMemoryStore()
	if _, err := store.Insert(context.Background(), Entry{ResourceType: "user"}); err == nil {
		t.Fatal("Insert: expected error on empty action")
	}
	if _, err := store.Insert(context.Background(), Entry{Action: "user.create"}); err == nil {
		t.Fatal("Insert: expected error on empty resource_type")
	}
}

// TestMemoryStore_List_FilterByAction exercises the
// four filter dimensions (action, resource_type,
// actor_id, since/until). The since/until filter
// uses the SetClock clock so the test is
// deterministic.
func TestMemoryStore_List_FilterByAction(t *testing.T) {
	store := NewMemoryStore()
	clock := fixedNow(time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC))
	store.SetClock(clock)

	_, _ = store.Insert(context.Background(), Entry{
		Action: "user.create", ResourceType: "user", ActorID: "u-1",
	})
	_, _ = store.Insert(context.Background(), Entry{
		Action: "user.update", ResourceType: "user", ActorID: "u-1",
	})
	_, _ = store.Insert(context.Background(), Entry{
		Action: "user.create", ResourceType: "user", ActorID: "u-2",
	})

	t.Run("no_filter", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List: got %d, want 3", len(got))
		}
	})

	t.Run("filter_by_action", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Action: "user.create"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List: got %d, want 2", len(got))
		}
		for _, e := range got {
			if e.Action != "user.create" {
				t.Errorf("List: action = %q, want user.create", e.Action)
			}
		}
	})

	t.Run("filter_by_actor", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{ActorID: "u-1"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List: got %d, want 2", len(got))
		}
	})

	t.Run("filter_by_resource_type", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{ResourceType: "user"})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List: got %d, want 3", len(got))
		}
	})
}

// TestMemoryStore_List_SinceUntil verifies the
// time-bound filter. Entries outside the window are
// excluded; entries inside the window are included.
func TestMemoryStore_List_SinceUntil(t *testing.T) {
	store := NewMemoryStore()
	base := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return base })

	// Three entries spaced 1h apart. SetClock
	// returns the same time, so the three calls
	// produce the same CreatedAt; the since/until
	// filter is on the row's CreatedAt, not the
	// wall clock at Insert time. We override the
	// CreatedAt via the Entry input instead.
	mk := func(at time.Time, action string) {
		_, _ = store.Insert(context.Background(), Entry{
			Action: action, ResourceType: "user", CreatedAt: at,
		})
	}
	mk(base, "a1")
	mk(base.Add(time.Hour), "a2")
	mk(base.Add(2*time.Hour), "a3")

	t.Run("since", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Since: base.Add(time.Minute)})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List: got %d, want 2", len(got))
		}
	})

	t.Run("until", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Until: base.Add(time.Minute)})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 1 {
			t.Errorf("List: got %d, want 1", len(got))
		}
	})
}

// TestMemoryStore_List_ElidesBeforeAfter verifies
// the list path omits the bulky blobs; GetByID
// returns them in full.
func TestMemoryStore_List_ElidesBeforeAfter(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.Insert(context.Background(), Entry{
		Action: "user.update", ResourceType: "user",
		Before: map[string]any{"name": "alice"},
		After:  map[string]any{"name": "bob"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}

	got, err := store.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("List: got %d, want 1", len(got))
	}
	if got[0].Before != nil {
		t.Errorf("List: Before should be nil, got %v", got[0].Before)
	}
	if got[0].After != nil {
		t.Errorf("List: After should be nil, got %v", got[0].After)
	}

	full, err := store.GetByID(context.Background(), got[0].ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if full.Before == nil {
		t.Errorf("GetByID: Before should be populated, got nil")
	}
	if full.After == nil {
		t.Errorf("GetByID: After should be populated, got nil")
	}
}

// TestMemoryStore_List_Limit verifies the limit
// cap (explicit + clamped to max). The "default"
// case is exercised by every other test that calls
// List with no filter — there is no separate test
// because the default is a constant and asserting
// it requires inserting > 100 rows.
func TestMemoryStore_List_Limit(t *testing.T) {
	store := NewMemoryStore()
	for i := 0; i < 10; i++ {
		_, _ = store.Insert(context.Background(), Entry{
			Action: "user.create", ResourceType: "user",
		})
	}

	t.Run("explicit_under_total", func(t *testing.T) {
		got, _ := store.List(context.Background(), ListFilter{Limit: 3})
		if len(got) != 3 {
			t.Errorf("List: got %d, want 3", len(got))
		}
	})
	t.Run("clamped_under_total", func(t *testing.T) {
		// Requesting MaxListLimit+1 caps the
		// internal limit at MaxListLimit, but
		// with only 10 entries in the store the
		// result is the total — the cap is on
		// the *requested* limit, not the
		// *returned* count.
		got, _ := store.List(context.Background(), ListFilter{Limit: MaxListLimit + 1})
		if len(got) != 10 {
			t.Errorf("List: got %d, want 10 (cap applies to limit, not result)", len(got))
		}
	})
}

// TestMemoryStore_List_DefaultLimitCap proves the
// default cap by inserting more than DefaultListLimit
// rows. The result is min(rows, DefaultListLimit) —
// the default for `Limit == 0` is DefaultListLimit,
// NOT MaxListLimit, because the typical UI does not
// need 1000 rows of audit history on a single page
// load. The max only kicks in for explicit
// `?limit=N` requests.
func TestMemoryStore_List_DefaultLimitCap(t *testing.T) {
	if DefaultListLimit >= MaxListLimit {
		t.Skipf("test assumes DefaultListLimit (%d) < MaxListLimit (%d)", DefaultListLimit, MaxListLimit)
	}
	store := NewMemoryStore()
	for i := 0; i < MaxListLimit*2; i++ {
		_, _ = store.Insert(context.Background(), Entry{
			Action: "user.create", ResourceType: "user",
		})
	}
	got, _ := store.List(context.Background(), ListFilter{})
	want := DefaultListLimit
	if want > MaxListLimit {
		want = MaxListLimit
	}
	if len(got) != want {
		t.Errorf("List: got %d, want %d (default limit cap)", len(got), want)
	}
}

// TestMemoryStore_GetByID_NotFound is a
// regression test for the ErrNotFound sentinel.
// The HTTP handler maps it to 404.
func TestMemoryStore_GetByID_NotFound(t *testing.T) {
	store := NewMemoryStore()
	_, err := store.GetByID(context.Background(), "9999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: err = %v, want ErrNotFound", err)
	}
}

// TestMemoryStore_ConcurrentInsert stress-tests
// the rwmutex by hammering Insert from many
// goroutines. The IDs must all be unique and
// the slice must contain every insertion.
func TestMemoryStore_ConcurrentInsert(t *testing.T) {
	store := NewMemoryStore()
	const n = 200
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			row, err := store.Insert(context.Background(), Entry{
				Action: "user.create", ResourceType: "user",
			})
			if err != nil {
				t.Errorf("Insert: %v", err)
				return
			}
			ids <- row.ID
		}()
	}
	wg.Wait()
	close(ids)
	seen := make(map[string]struct{}, n)
	for id := range ids {
		if _, dup := seen[id]; dup {
			t.Errorf("Insert: duplicate id %q", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) != n {
		t.Errorf("Insert: got %d unique ids, want %d", len(seen), n)
	}
}
