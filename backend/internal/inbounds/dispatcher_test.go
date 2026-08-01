// SPDX-License-Identifier: AGPL-3.0-or-later

package inbounds

import (
	"context"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesInboundCreated(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventInboundCreated)

	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc).WithWebhooks(spy.Svc())

	_, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nodeID,
		Name:       "dispatch-inbound",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventInboundCreated)
}

func TestService_Delete_DispatchesInboundDeleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epID := spy.Subscribe(t, webhooks.EventInboundDeleted)

	nodesSvc, nodeID := seedNodeSvc(t)
	svc := makeSvc(t, nodesSvc).WithWebhooks(spy.Svc())

	ib, err := svc.Create(context.Background(), CreateInput{
		NodeID:     nodeID,
		Name:       "dispatch-inbound-del",
		Protocol:   ProtocolVLESS,
		Listen:     "::",
		ListenPort: 444,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := svc.Delete(context.Background(), ib.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	spy.AssertDeliveredFor(t, epID, webhooks.EventInboundDeleted)
}
