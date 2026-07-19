// SPDX-License-Identifier: AGPL-3.0-or-later

//go:build integration

// Integration test for the audits PgStore. Run with:
//
//	INTEGRATION_DATABASE_URL=postgres://aegis:aegis@localhost:5432/aegis_it \
//	  go test -tags=integration ./internal/audits/...
//
// CI runs `golangci-lint` with GOFLAGS=-tags=integration so this
// file is included in the lint pass; the `//go:build integration`
// constraint keeps it out of the unit-test build for anyone who
// does not have a Postgres handy.

package audits

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/QAdversif/AegisPanel/testutil"
)

// TestPgStore_Insert_AssignsID verifies the pg path
// mints a fresh bigserial id and the row round-trips
// through a SELECT.
func TestPgStore_Insert_AssignsID(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	row, err := store.Insert(context.Background(), Entry{
		Action:        "user.create",
		ResourceType:  "user",
		ResourceID:    "u-1",
		ActorID:       "",
		ActorUsername: "admin",
		IP:            "127.0.0.1",
		UserAgent:     "aegis-test/1.0",
		Before:        map[string]any{"name": "alice"},
		After:         map[string]any{"name": "bob"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if row.ID == "" {
		t.Fatal("Insert: empty id")
	}
	if row.CreatedAt.IsZero() {
		t.Fatal("Insert: zero CreatedAt")
	}

	// GetByID should round-trip the same row.
	full, err := store.GetByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if full.ActorUsername != "admin" {
		t.Errorf("GetByID: ActorUsername = %q, want admin", full.ActorUsername)
	}
	if full.IP != "127.0.0.1" {
		t.Errorf("GetByID: IP = %q, want 127.0.0.1", full.IP)
	}
	if full.UserAgent != "aegis-test/1.0" {
		t.Errorf("GetByID: UserAgent = %q", full.UserAgent)
	}
}

// TestPgStore_Insert_TrimsUserAgent verifies the
// 512-char cap on the UserAgent column.
func TestPgStore_Insert_TrimsUserAgent(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	huge := ""
	for i := 0; i < 2000; i++ {
		huge += "a"
	}
	row, err := store.Insert(context.Background(), Entry{
		Action: "user.create", ResourceType: "user", UserAgent: huge,
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if len(row.UserAgent) != 512 {
		t.Errorf("Insert: UserAgent len = %d, want 512", len(row.UserAgent))
	}
}

// TestPgStore_List_FilterByAction exercises the
// four filter dimensions on the pg path.
func TestPgStore_List_FilterByAction(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	now := time.Now().UTC()
	mk := func(at time.Time, action, resourceType, actorID string) {
		_, err := store.Insert(context.Background(), Entry{
			Action: action, ResourceType: resourceType, ActorID: actorID,
			CreatedAt: at,
		})
		if err != nil {
			t.Fatalf("Insert: %v", err)
		}
	}
	mk(now.Add(-3*time.Hour), "user.create", "user", "u-1")
	mk(now.Add(-2*time.Hour), "user.update", "user", "u-1")
	mk(now.Add(-1*time.Hour), "user.create", "user", "u-2")
	mk(now, "host.create", "host", "u-1")

	t.Run("no_filter", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 4 {
			t.Errorf("List: got %d, want 4", len(got))
		}
	})

	t.Run("filter_by_action", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Action: "user.create", Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List: got %d, want 2", len(got))
		}
	})

	t.Run("filter_by_actor", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{ActorID: "u-1", Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List: got %d, want 3", len(got))
		}
	})

	t.Run("filter_by_resource_type", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{ResourceType: "user", Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 3 {
			t.Errorf("List: got %d, want 3", len(got))
		}
	})

	t.Run("filter_by_since", func(t *testing.T) {
		got, err := store.List(context.Background(), ListFilter{Since: now.Add(-90 * time.Minute), Limit: 100})
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(got) != 2 {
			t.Errorf("List: got %d, want 2", len(got))
		}
	})
}

// TestPgStore_List_ElidesBeforeAfter verifies the
// list path strips the JSONB blobs; GetByID returns
// them in full.
func TestPgStore_List_ElidesBeforeAfter(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)

	row, err := store.Insert(context.Background(), Entry{
		Action: "user.update", ResourceType: "user",
		Before: map[string]any{"name": "alice"},
		After:  map[string]any{"name": "bob"},
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := store.List(context.Background(), ListFilter{Limit: 10})
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

	full, err := store.GetByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if full.Before == nil {
		t.Error("GetByID: Before should be populated")
	}
	if full.After == nil {
		t.Error("GetByID: After should be populated")
	}
}

// TestPgStore_GetByID_NotFound is the regression test
// for the ErrNotFound sentinel.
func TestPgStore_GetByID_NotFound(t *testing.T) {
	pool := testutil.MustNewPool(t)
	store := NewPgStore(pool)
	_, err := store.GetByID(context.Background(), "99999999")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: err = %v, want ErrNotFound", err)
	}
}
