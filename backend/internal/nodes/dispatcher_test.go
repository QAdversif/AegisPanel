// SPDX-License-Identifier: AGPL-3.0-or-later

package nodes

import (
	"context"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesNodeCreated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventNodeCreated)

	svc := NewService(NewMemoryStore()).WithWebhooks(spy.Svc())
	_, err := svc.Create(context.Background(), CreateInput{
		Name:    "dispatch-node",
		Region:  "eu-west-1",
		Address: "10.0.0.1:22",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventNodeCreated)
}

func TestService_Delete_DispatchesNodeDeleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventNodeDeleted)

	svc := NewService(NewMemoryStore()).WithWebhooks(spy.Svc())
	n, err := svc.Create(context.Background(), CreateInput{
		Name:    "dispatch-node-delete",
		Region:  "eu-west-1",
		Address: "10.0.0.2:22",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), n.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventNodeDeleted)
}
