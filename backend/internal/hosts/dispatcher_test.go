// SPDX-License-Identifier: AGPL-3.0-or-later

package hosts

import (
	"context"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesHostCreated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventHostCreated)

	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	env.svc.WithWebhooks(spy.Svc())

	_, err := env.svc.Create(context.Background(), validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventHostCreated)
}

func TestService_Delete_DispatchesHostDeleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventHostDeleted)

	nodeID := uuid.New()
	env := makeSvc(t, nodeID)
	env.svc.WithWebhooks(spy.Svc())

	h, err := env.svc.Create(context.Background(), validCreateInput(nodeID, env.inboundFor(nodeID)))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := env.svc.Delete(context.Background(), h.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventHostDeleted)
}
