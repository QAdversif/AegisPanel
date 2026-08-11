// SPDX-License-Identifier: AGPL-3.0-or-later

package backups

import (
	"context"
	"errors"
	"testing"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

func TestService_Create_DispatchesBackupCreatedAndCompleted(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epCreated := spy.Subscribe(t, webhooks.EventBackupCreated)
	epCompleted := spy.Subscribe(t, webhooks.EventBackupCompleted)

	svc, _ := newTestService(t, []byte("dispatch-pg-dump-bytes\n"))
	svc.WithWebhooks(spy.Svc())
	ctx := context.Background()
	row, err := svc.Create(ctx, TriggerManual)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if row.Status != StatusOK {
		t.Fatalf("row.Status = %s, want %s", row.Status, StatusOK)
	}
	// The Create flow fires BOTH the
	// "created" event (when the running row
	// is inserted) and the "completed" event
	// (when the row is finalised to OK).
	spy.AssertDeliveredFor(t, epCreated, webhooks.EventBackupCreated)
	spy.AssertDeliveredFor(t, epCompleted, webhooks.EventBackupCompleted)
}

func TestService_Create_Failure_DispatchesBackupFailed(t *testing.T) {
	t.Parallel()
	spy := webhooks.NewSpy()
	epCreated := spy.Subscribe(t, webhooks.EventBackupCreated)
	epFailed := spy.Subscribe(t, webhooks.EventBackupFailed)

	svc, _ := newTestService(t, []byte("data"))
	// Pre-PR-#228: SetDumpFn returning (nil, err).
	// Post-PR-#228: a Dumper whose Dump returns
	// the same error. The Service.Create path is
	// identical (runDumpToFile:675 short-circuits
	// on the Dump error).
	svc.SetDumper(errDumper{err: errors.New("simulated pg_dump failure")})
	svc.WithWebhooks(spy.Svc())
	ctx := context.Background()
	row, err := svc.Create(ctx, TriggerManual)
	if err == nil {
		t.Fatalf("Create: expected error from failed dump")
	}
	if row == nil || row.Status != StatusFailed {
		t.Fatalf("row = %+v, want Status=%s", row, StatusFailed)
	}
	// The created event fires when the
	// running row is inserted; the failed
	// event fires when the dump fails.
	spy.AssertDeliveredFor(t, epCreated, webhooks.EventBackupCreated)
	spy.AssertDeliveredFor(t, epFailed, webhooks.EventBackupFailed)
}
