// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newSvc(t *testing.T) (*Service, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	return NewService(store), store
}

func TestService_Create_OK(t *testing.T) {
	svc, store := newSvc(t)
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	svc.SetClock(func() time.Time { return now })

	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "alpha",
		Region:  "eu-west-1",
		Address: "10.0.0.1:22",
		Tags:    []string{"  vless  ", "reality", "vless", ""}, // dedup + trim
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.ID == uuid.Nil {
		t.Fatal("ID not assigned")
	}
	if !n.CreatedAt.Equal(now) {
		t.Fatalf("CreatedAt = %v, want %v", n.CreatedAt, now)
	}
	if got := n.Tags; len(got) != 2 || got[0] != "vless" || got[1] != "reality" {
		t.Fatalf("Tags = %v, want [vless reality]", got)
	}
	if _, err := store.GetByName(context.Background(), "alpha"); err != nil {
		t.Fatalf("store lookup: %v", err)
	}
}

func TestService_Create_DefaultsState(t *testing.T) {
	svc, _ := newSvc(t)
	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "alpha",
		Region:  "eu",
		Address: "10.0.0.1:22",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if n.State != StateNew {
		t.Fatalf("State = %q, want %q", n.State, StateNew)
	}
}

func TestService_Create_ValidationErrors(t *testing.T) {
	svc, _ := newSvc(t)
	cases := []struct {
		name string
		in   CreateInput
		want string
	}{
		{"empty name", CreateInput{Name: "", Region: "eu", Address: "1.1.1.1:22"}, "name"},
		{"name with space", CreateInput{Name: "al pha", Region: "eu", Address: "1.1.1.1:22"}, "name"},
		{"name too long", CreateInput{Name: stringRepeat("a", maxNameLen+1), Region: "eu", Address: "1.1.1.1:22"}, "name"},
		{"empty region", CreateInput{Name: "a", Region: "", Address: "1.1.1.1:22"}, "region"},
		{"region too long", CreateInput{Name: "a", Region: stringRepeat("r", maxRegionLen+1), Address: "1.1.1.1:22"}, "region"},
		{"empty address", CreateInput{Name: "a", Region: "eu", Address: ""}, "address"},
		{"address no port", CreateInput{Name: "a", Region: "eu", Address: "1.1.1.1"}, "address"},
		{"address no host", CreateInput{Name: "a", Region: "eu", Address: ":22"}, "address"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := svc.Create(context.Background(), tc.in)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var vErr *ValidationError
			if !errors.As(err, &vErr) {
				t.Fatalf("err = %v, want ValidationError", err)
			}
			if vErr.Field != tc.want {
				t.Fatalf("Field = %q, want %q (msg=%q)", vErr.Field, tc.want, vErr.Message)
			}
		})
	}
}

func TestService_Create_DuplicateName(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	if _, err := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "1.1.1.1:22"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	_, err := svc.Create(ctx, CreateInput{Name: "alpha", Region: "us", Address: "2.2.2.2:22"})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("err = %v, want ErrDuplicate", err)
	}
}

func TestService_Create_UnknownState(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Create(context.Background(), CreateInput{
		Name: "alpha", Region: "eu", Address: "1.1.1.1:22", State: State("bogus"),
	})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "state" {
		t.Fatalf("err = %v, want state ValidationError", err)
	}
}

func TestService_Get_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Get(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_Get_NilID(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Get(context.Background(), uuid.Nil)
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "id" {
		t.Fatalf("err = %v, want id ValidationError", err)
	}
}

func TestService_Update_Partial(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, err := svc.Create(ctx, CreateInput{
		Name: "alpha", Region: "eu", Address: "1.1.1.1:22", Tags: []string{"vless"},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}

	region := "ap-southeast-1"
	updated, err := svc.Update(ctx, created.ID, UpdateInput{
		Region: &region,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Region != region {
		t.Fatalf("Region = %q, want %q", updated.Region, region)
	}
	if updated.Name != "alpha" {
		t.Fatalf("Name changed despite not being in patch: %q", updated.Name)
	}
	if len(updated.Tags) != 1 || updated.Tags[0] != "vless" {
		t.Fatalf("Tags changed despite not being in patch: %v", updated.Tags)
	}
}

func TestService_Update_RejectsBadField(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "1.1.1.1:22"})

	bad := "   "
	_, err := svc.Update(ctx, created.ID, UpdateInput{Region: &bad})
	var vErr *ValidationError
	if !errors.As(err, &vErr) || vErr.Field != "region" {
		t.Fatalf("err = %v, want region ValidationError", err)
	}
}

func TestService_Update_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	_, err := svc.Update(context.Background(), uuid.New(), UpdateInput{})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestService_Delete_OK(t *testing.T) {
	svc, _ := newSvc(t)
	ctx := context.Background()
	created, _ := svc.Create(ctx, CreateInput{Name: "alpha", Region: "eu", Address: "1.1.1.1:22"})
	if err := svc.Delete(ctx, created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := svc.Get(ctx, created.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}

func TestService_Delete_NotFound(t *testing.T) {
	svc, _ := newSvc(t)
	err := svc.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestNormaliseTags(t *testing.T) {
	cases := []struct {
		name string
		in   []string
		want []string
	}{
		{"nil stays nil", nil, nil},
		{"empty stays nil", []string{}, nil},
		{"all blanks", []string{"", "  ", "\t"}, nil},
		{"dedup", []string{"a", "b", "a"}, []string{"a", "b"}},
		{"trim", []string{"  a  ", "b"}, []string{"a", "b"}},
		{"too long dropped", []string{stringRepeat("x", maxTagLen+1), "ok"}, []string{"ok"}},
		// uniqueValues emits maxTags+5 distinct values so dedup
		// does not collapse the slice; the helper exists
		// because every value needs to be different for the
		// cap-at-maxTags check to be meaningful.
		{"cap at maxTags", uniqueValues(maxTags + 5), uniqueValues(maxTags)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normaliseTags(tc.in)
			if !stringSliceEqual(got, tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSplitHostPort(t *testing.T) {
	cases := []struct {
		in       string
		wantHost string
		wantPort string
		wantOK   bool
	}{
		{"10.0.0.1:22", "10.0.0.1", "22", true},
		{"[::1]:22", "[::1]", "22", true},
		{"example.com:2222", "example.com", "2222", true},
		{"noport", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			h, p, ok := splitHostPort(tc.in)
			if ok != tc.wantOK || h != tc.wantHost || p != tc.wantPort {
				t.Fatalf("got (%q, %q, %v), want (%q, %q, %v)",
					h, p, ok, tc.wantHost, tc.wantPort, tc.wantOK)
			}
		})
	}
}

// --- tiny test helpers (kept local; tiny enough not to need a shared util)

func stringRepeat(s string, n int) string {
	if n <= 0 {
		return ""
	}
	out := make([]byte, 0, len(s)*n)
	for i := 0; i < n; i++ {
		out = append(out, s...)
	}
	return string(out)
}

func makeSlice(n int, fill string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fill
	}
	return out
}

// uniqueValues returns n distinct strings, each prefixed with
// the index so dedup never collapses them. Used to test the
// cap-at-maxTags path without entangling it with the dedup path.
func uniqueValues(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "t" + itoa(i)
	}
	return out
}

// itoa is a tiny base-10 int-to-string helper so we don't pull
// strconv into a test file just for one call.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
