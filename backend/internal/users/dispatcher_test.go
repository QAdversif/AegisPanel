// SPDX-License-Identifier: AGPL-3.0-or-later

package users

import (
	"context"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesUserCreated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventUserCreated)

	svc := NewService(NewMemoryStore(nil)).WithWebhooks(spy.Svc())
	_, err := svc.Create(context.Background(), CreateInput{
		Username: "dispatch-test",
		Email:    "dispatch@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventUserCreated)
}

func TestService_Delete_DispatchesUserDeleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventUserDeleted)

	svc := NewService(NewMemoryStore(nil)).WithWebhooks(spy.Svc())
	u, err := svc.Create(context.Background(), CreateInput{
		Username: "dispatch-delete",
		Email:    "delete@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), u.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventUserDeleted)
}

func TestService_WithoutWebhooks_NoDispatch(t *testing.T) {
	t.Parallel()
	// Sanity: existing unit tests construct
	// NewService without WithWebhooks. The
	// dispatch call must be a no-op.
	svc := NewService(NewMemoryStore(nil))
	_, err := svc.Create(context.Background(), CreateInput{
		Username: "no-dispatch",
		Email:    "nope@example.com",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}
