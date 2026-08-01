// SPDX-License-Identifier: AGPL-3.0-or-later

package plans

import (
	"context"
	"testing"
	"time"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesPlanCreated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventPlanCreated)

	svc := NewService(NewMemoryStore(nil)).WithWebhooks(spy.Svc())
	p, err := svc.Create(context.Background(), CreateInput{
		Name:              "plan-dispatch-test",
		TrafficLimitBytes: 1024,
		Duration:          30 * 24 * time.Hour,
		DeviceLimit:       5,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventPlanCreated)
	if p == nil || p.ID.String() == "" {
		t.Errorf("Create returned a nil plan")
	}
}

func TestService_Update_DispatchesPlanUpdated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventPlanUpdated)

	svc := NewService(NewMemoryStore(nil)).WithWebhooks(spy.Svc())
	p, err := svc.Create(context.Background(), CreateInput{
		Name:     "plan-dispatch-update",
		Duration: time.Hour,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	newName := "plan-dispatch-update-renamed"
	_, err = svc.Update(context.Background(), p.ID, UpdateInput{
		Name: &newName,
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventPlanUpdated)
}

func TestService_Delete_DispatchesPlanDeleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventPlanDeleted)

	svc := NewService(NewMemoryStore(nil)).WithWebhooks(spy.Svc())
	p, err := svc.Create(context.Background(), CreateInput{
		Name:     "plan-dispatch-delete",
		Duration: time.Hour,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), p.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventPlanDeleted)
}

func TestService_WithoutWebhooks_NoDispatch(t *testing.T) {
	t.Parallel()
	// Sanity: the existing unit tests
	// (NewService without WithWebhooks) must
	// not panic on the dispatch call. The
	// Service.webhooks field is nil; the
	// helper is a no-op.
	svc := NewService(NewMemoryStore(nil))
	_, err := svc.Create(context.Background(), CreateInput{
		Name:     "plan-no-dispatch",
		Duration: time.Hour,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
}
