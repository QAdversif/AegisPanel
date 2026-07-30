// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

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
// path end-to-end: validation, ID/timestamp
// generation, and round-trip.
func TestService_Create_HappyPath(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	in := CreateInput{
		Name:              "starter",
		TrafficLimitBytes: 5_000_000_000,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       3,
		PriceCents:        500,
	}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ID == uuid.Nil {
		t.Errorf("ID is zero")
	}
	if p.Name != "starter" {
		t.Errorf("Name = %q, want %q", p.Name, "starter")
	}
	if p.ResetPeriod != ResetMonthly {
		t.Errorf("ResetPeriod = %q, want %q (default)", p.ResetPeriod, ResetMonthly)
	}
	if !p.CreatedAt.Equal(fixedClock()) {
		t.Errorf("CreatedAt = %v, want %v", p.CreatedAt, fixedClock())
	}
	if !p.UpdatedAt.Equal(fixedClock()) {
		t.Errorf("UpdatedAt = %v, want %v", p.UpdatedAt, fixedClock())
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
		{"empty-name", CreateInput{}},
		{"whitespace-name", CreateInput{Name: "   "}},
		{"too-long-name", CreateInput{Name: string(make([]byte, MaxNameLen+1))}},
		{"bad-reset-period", CreateInput{Name: "x", Duration: 1, ResetPeriod: "yearly"}},
		{"zero-duration", CreateInput{Name: "x", Duration: 0}},
		{"neg-duration", CreateInput{Name: "x", Duration: -1 * time.Hour}},
		{"tiny-duration", CreateInput{Name: "x", Duration: time.Second}},
		{"huge-duration", CreateInput{Name: "x", Duration: 100 * 365 * 24 * time.Hour}},
		{"neg-traffic", CreateInput{Name: "x", Duration: 1, TrafficLimitBytes: -1}},
		{"neg-devices", CreateInput{Name: "x", Duration: 1, DeviceLimit: -1}},
		{"neg-price", CreateInput{Name: "x", Duration: 1, PriceCents: -1}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc(t)
			_, err := svc.Create(ctx, tc.in)
			if err == nil {
				t.Fatalf("Create(%s): want error, got nil", tc.name)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Create(%s): err = %v, want ErrInvalid", tc.name, err)
			}
		})
	}
}

// TestService_Create_DefaultResetPeriod verifies
// that an empty ResetPeriod is filled in with
// ResetMonthly (the "new user picks the most common
// option" default).
func TestService_Create_DefaultResetPeriod(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	p, err := svc.Create(ctx, CreateInput{
		Name:     "starter",
		Duration: 30 * 24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if p.ResetPeriod != ResetMonthly {
		t.Errorf("ResetPeriod = %q, want %q (default)", p.ResetPeriod, ResetMonthly)
	}
}

// TestService_Get covers the Get and GetByName
// paths plus the zero-id and bad-name guards.
func TestService_Get(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	in := CreateInput{Name: "starter", Duration: 30 * 24 * time.Hour}
	p, err := svc.Create(ctx, in)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Get by id.
	got, err := svc.Get(ctx, p.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.ID != p.ID {
		t.Errorf("Get: ID = %s, want %s", got.ID, p.ID)
	}
	// Get by name.
	got2, err := svc.GetByName(ctx, "starter")
	if err != nil {
		t.Fatalf("GetByName: %v", err)
	}
	if got2.ID != p.ID {
		t.Errorf("GetByName: ID = %s, want %s", got2.ID, p.ID)
	}
	// Zero id.
	if _, err := svc.Get(ctx, uuid.Nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Get(zero id): err = %v, want ErrInvalid", err)
	}
	// Bad name.
	if _, err := svc.GetByName(ctx, ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetByName(empty): err = %v, want ErrInvalid", err)
	}
	if _, err := svc.GetByName(ctx, "   "); !errors.Is(err, ErrInvalid) {
		t.Errorf("GetByName(whitespace): err = %v, want ErrInvalid", err)
	}
}

// TestService_List covers the sorted list path and
// the empty-store branch.
func TestService_List(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	// Empty store.
	out, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("List (empty): %v", err)
	}
	if len(out) != 0 {
		t.Errorf("List (empty): len = %d, want 0", len(out))
	}
	// Three plans.
	for _, name := range []string{"a", "b", "c"} {
		if _, err := svc.Create(ctx, CreateInput{Name: name, Duration: 30 * 24 * time.Hour}); err != nil {
			t.Fatalf("Create %s: %v", name, err)
		}
	}
	out, err = svc.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(out) != 3 {
		t.Errorf("List: len = %d, want 3", len(out))
	}
}

// TestService_Update_PartialPatch exercises the
// partial-update path. Only the fields marked
// (non-nil) are touched.
func TestService_Update_PartialPatch(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	p, err := svc.Create(ctx, CreateInput{
		Name:              "starter",
		TrafficLimitBytes: 5_000_000_000,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       3,
		PriceCents:        500,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// Advance the service clock so the Update's
	// UpdatedAt stamp is strictly after Create's.
	svc.SetClock(func() time.Time { return fixedClock().Add(1 * time.Hour) })
	// Patch only the price.
	newPrice := int64(700)
	got, err := svc.Update(ctx, p.ID, UpdateInput{PriceCents: &newPrice})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if got.PriceCents != 700 {
		t.Errorf("PriceCents after Update = %d, want 700", got.PriceCents)
	}
	// Unchanged fields must remain.
	if got.TrafficLimitBytes != 5_000_000_000 {
		t.Errorf("TrafficLimitBytes after Update = %d, want 5_000_000_000 (unchanged)", got.TrafficLimitBytes)
	}
	if got.Name != "starter" {
		t.Errorf("Name after Update = %q, want %q (unchanged)", got.Name, "starter")
	}
	// UpdatedAt must advance.
	if !got.UpdatedAt.After(p.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want after %v", got.UpdatedAt, p.UpdatedAt)
	}
}

// TestService_Update_ValidationFailures covers the
// negative-path update tests: each field is patched
// with a value the validator rejects.
func TestService_Update_ValidationFailures(t *testing.T) {
	ctx := context.Background()
	cases := []struct {
		name string
		in   UpdateInput
	}{
		{"empty-name", UpdateInput{Name: ptrString("")}},
		{"whitespace-name", UpdateInput{Name: ptrString("   ")}},
		{"too-long-name", UpdateInput{Name: ptrString(string(make([]byte, MaxNameLen+1)))}},
		{"bad-reset", UpdateInput{ResetPeriod: ptrReset(ResetPeriod("yearly"))}},
		{"zero-duration", UpdateInput{Duration: ptrDuration(0)}},
		{"neg-duration", UpdateInput{Duration: ptrDuration(-time.Hour)}},
		{"huge-duration", UpdateInput{Duration: ptrDuration(100 * 365 * 24 * time.Hour)}},
		{"neg-traffic", UpdateInput{TrafficLimitBytes: ptrInt64(-1)}},
		{"neg-devices", UpdateInput{DeviceLimit: ptrInt(-1)}},
		{"neg-price", UpdateInput{PriceCents: ptrInt64(-1)}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := newSvc(t)
			p, err := svc.Create(ctx, CreateInput{Name: "starter", Duration: 30 * 24 * time.Hour})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			_, err = svc.Update(ctx, p.ID, tc.in)
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("Update(%s): err = %v, want ErrInvalid", tc.name, err)
			}
		})
	}
}

// TestService_Update_NotFound covers the
// "update a row that does not exist" branch.
func TestService_Update_NotFound(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	if _, err := svc.Update(ctx, uuid.New(), UpdateInput{Name: ptrString("x")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update missing: err = %v, want ErrNotFound", err)
	}
}

// TestService_Update_DuplicateName covers the
// (name) UNIQUE constraint on rename. The second
// plan keeps its original name; the first's rename
// must fail with ErrDuplicate.
func TestService_Update_DuplicateName(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	p1, err := svc.Create(ctx, CreateInput{Name: "first", Duration: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("Create first: %v", err)
	}
	if _, err := svc.Create(ctx, CreateInput{Name: "second", Duration: 30 * 24 * time.Hour}); err != nil {
		t.Fatalf("Create second: %v", err)
	}
	// Try to rename p1 to "second".
	if _, err := svc.Update(ctx, p1.ID, UpdateInput{Name: ptrString("second")}); !errors.Is(err, ErrDuplicate) {
		t.Errorf("Update rename to existing: err = %v, want ErrDuplicate", err)
	}
}

// TestService_Delete covers the happy path and the
// zero-id guard.
func TestService_Delete(t *testing.T) {
	ctx := context.Background()
	svc := newSvc(t)
	p, err := svc.Create(ctx, CreateInput{Name: "starter", Duration: 30 * 24 * time.Hour})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(ctx, p.ID); err != nil {
		t.Errorf("Delete: %v", err)
	}
	if _, err := svc.Get(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Delete: err = %v, want ErrNotFound", err)
	}
	// Zero id.
	if err := svc.Delete(ctx, uuid.Nil); !errors.Is(err, ErrInvalid) {
		t.Errorf("Delete(zero id): err = %v, want ErrInvalid", err)
	}
	// Re-delete is ErrNotFound.
	if err := svc.Delete(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Re-Delete: err = %v, want ErrNotFound", err)
	}
}

// --- pointer helpers ---------------------------------------------------

func ptrString(s string) *string                 { return &s }
func ptrInt(i int) *int                          { return &i }
func ptrInt64(i int64) *int64                    { return &i }
func ptrDuration(d time.Duration) *time.Duration { return &d }
func ptrReset(r ResetPeriod) *ResetPeriod        { return &r }
