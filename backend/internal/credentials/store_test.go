// SPDX-License-Identifier: AGPL-3.0-or-later
//
// MemoryStore unit tests for the Phase 2
// multi-user credentials data model.
//
// The test surface is the Store contract: Insert /
// Update / Delete / GetByID / ListByUser /
// ListByInbound. The Service layer is tested
// separately in service_test.go.

package credentials

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

func newMemStore() *MemoryStore {
	return NewMemoryStore()
}

func TestMemoryStore_Insert_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	userID := uuid.New()
	inbID := uuid.New()
	row, err := s.Insert(context.Background(), Credential{
		UserID:          userID,
		InboundID:       inbID,
		CredentialValue: "cred-vless-1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if row.ID == uuid.Nil {
		t.Error("ID: got zero UUID, want a fresh uuid")
	}
	if row.UserID != userID {
		t.Errorf("UserID: got %s, want %s", row.UserID, userID)
	}
	if row.InboundID != inbID {
		t.Errorf("InboundID: got %s, want %s", row.InboundID, inbID)
	}
	if row.CredentialValue != "cred-vless-1" {
		t.Errorf("CredentialValue: got %q, want %q", row.CredentialValue, "cred-vless-1")
	}
	if row.CreatedAt.IsZero() {
		t.Error("CreatedAt: got zero, want a fresh timestamp")
	}
	if row.UpdatedAt.IsZero() {
		t.Error("UpdatedAt: got zero, want a fresh timestamp")
	}
}

func TestMemoryStore_Insert_RejectsZeroFields(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	tests := []struct {
		name string
		c    Credential
		want string
	}{
		{
			name: "zero user_id",
			c:    Credential{InboundID: uuid.New(), CredentialValue: "x"},
			want: "user_id is required",
		},
		{
			name: "zero inbound_id",
			c:    Credential{UserID: uuid.New(), CredentialValue: "x"},
			want: "inbound_id is required",
		},
		{
			name: "empty credential_value",
			c:    Credential{UserID: uuid.New(), InboundID: uuid.New()},
			want: "credential_value is required",
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := s.Insert(context.Background(), tt.c)
			if err == nil {
				t.Fatalf("Insert: expected error, got nil")
			}
			if err.Error() != "credentials: Insert: "+tt.want {
				t.Errorf("Insert: got %q, want %q", err.Error(), "credentials: Insert: "+tt.want)
			}
		})
	}
}

func TestMemoryStore_Insert_Duplicate(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	userID := uuid.New()
	inbID := uuid.New()
	if _, err := s.Insert(context.Background(), Credential{
		UserID:          userID,
		InboundID:       inbID,
		CredentialValue: "first",
	}); err != nil {
		t.Fatalf("first Insert: %v", err)
	}
	_, err := s.Insert(context.Background(), Credential{
		UserID:          userID,
		InboundID:       inbID,
		CredentialValue: "second",
	})
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("second Insert: got %v, want ErrDuplicate", err)
	}
}

func TestMemoryStore_Update_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	row, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       uuid.New(),
		CredentialValue: "v1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	updated, err := s.Update(context.Background(), row.ID, "v2")
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if updated.CredentialValue != "v2" {
		t.Errorf("CredentialValue: got %q, want %q", updated.CredentialValue, "v2")
	}
	if !updated.UpdatedAt.After(row.UpdatedAt) && !updated.UpdatedAt.Equal(row.UpdatedAt) {
		t.Errorf("UpdatedAt: got %s, want > %s", updated.UpdatedAt, row.UpdatedAt)
	}
	if !updated.CreatedAt.Equal(row.CreatedAt) {
		t.Errorf("CreatedAt: got %s, want %s (immutable)", updated.CreatedAt, row.CreatedAt)
	}
}

func TestMemoryStore_Update_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	_, err := s.Update(context.Background(), uuid.New(), "x")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_Update_RejectsEmptyValue(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	row, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       uuid.New(),
		CredentialValue: "v1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	_, err = s.Update(context.Background(), row.ID, "")
	if err == nil {
		t.Errorf("Update: expected error, got nil")
	}
}

func TestMemoryStore_Delete_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	row, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       uuid.New(),
		CredentialValue: "v1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if err := s.Delete(context.Background(), row.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err = s.GetByID(context.Background(), row.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID after Delete: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_Delete_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	err := s.Delete(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_GetByID_RoundTrip(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	row, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       uuid.New(),
		CredentialValue: "v1",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	got, err := s.GetByID(context.Background(), row.ID)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.ID != row.ID {
		t.Errorf("ID: got %s, want %s", got.ID, row.ID)
	}
}

func TestMemoryStore_GetByID_NotFound(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	_, err := s.GetByID(context.Background(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID: got %v, want ErrNotFound", err)
	}
}

func TestMemoryStore_ListByUser(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	userID := uuid.New()
	otherUser := uuid.New()
	// 3 credentials for userID (across 3 inbounds)
	// + 1 for otherUser.
	for i := 0; i < 3; i++ {
		if _, err := s.Insert(context.Background(), Credential{
			UserID:          userID,
			InboundID:       uuid.New(),
			CredentialValue: "v",
		}); err != nil {
			t.Fatalf("Insert user %d: %v", i, err)
		}
	}
	if _, err := s.Insert(context.Background(), Credential{
		UserID:          otherUser,
		InboundID:       uuid.New(),
		CredentialValue: "other",
	}); err != nil {
		t.Fatalf("Insert other: %v", err)
	}
	rows, err := s.ListByUser(context.Background(), userID)
	if err != nil {
		t.Fatalf("ListByUser: %v", err)
	}
	if len(rows) != 3 {
		t.Errorf("len: got %d, want 3", len(rows))
	}
	for _, r := range rows {
		if r.UserID != userID {
			t.Errorf("UserID: got %s, want %s", r.UserID, userID)
		}
	}
	// Empty result for a user with no credentials.
	none, err := s.ListByUser(context.Background(), uuid.New())
	if err != nil {
		t.Fatalf("ListByUser (empty): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("len (empty): got %d, want 0", len(none))
	}
}

func TestMemoryStore_ListByInbound(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	inbID := uuid.New()
	otherInb := uuid.New()
	// 4 users on the same inbound.
	for i := 0; i < 4; i++ {
		if _, err := s.Insert(context.Background(), Credential{
			UserID:          uuid.New(),
			InboundID:       inbID,
			CredentialValue: "v",
		}); err != nil {
			t.Fatalf("Insert user %d: %v", i, err)
		}
	}
	if _, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       otherInb,
		CredentialValue: "other",
	}); err != nil {
		t.Fatalf("Insert other: %v", err)
	}
	rows, err := s.ListByInbound(context.Background(), inbID)
	if err != nil {
		t.Fatalf("ListByInbound: %v", err)
	}
	if len(rows) != 4 {
		t.Errorf("len: got %d, want 4", len(rows))
	}
	for _, r := range rows {
		if r.InboundID != inbID {
			t.Errorf("InboundID: got %s, want %s", r.InboundID, inbID)
		}
	}
}

func TestMemoryStore_SetClock(t *testing.T) {
	t.Parallel()
	s := newMemStore()
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	s.SetClock(func() time.Time { return now })
	row, err := s.Insert(context.Background(), Credential{
		UserID:          uuid.New(),
		InboundID:       uuid.New(),
		CredentialValue: "v",
	})
	if err != nil {
		t.Fatalf("Insert: %v", err)
	}
	if !row.CreatedAt.Equal(now) {
		t.Errorf("CreatedAt: got %s, want %s", row.CreatedAt, now)
	}
}
