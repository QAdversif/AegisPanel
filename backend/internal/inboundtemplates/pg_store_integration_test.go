// SPDX-License-Identifier: AGPL-3.0-or-later
//
//go:build integration

// Integration tests for PgStore. Run with:
//
//	make test-integration
//
// or
//
//	INTEGRATION_DATABASE_URL=postgres://... go test -tags=integration ./internal/inboundtemplates/...
//
// The `//go:build integration` tag keeps `go test ./...` fast
// and dependency-free for the default development loop. CI
// runs the tagged suite with a service-container Postgres.
package inboundtemplates

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

func TestPgStore_Roundtrip(t *testing.T) {
	pool := testutil.MustNewPool(t)

	store := NewPgStore(pool)

	// Templates are global (not per-node), so no
	// seedNode helper. The store surface is just
	// `inbound_templates`.
	tpl := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "vless-reality-eu-" + uuid.NewString()[:8],
		Protocol: ProtocolVLESS,
		Params: map[string]any{
			"flow": "xtls-rprx-vision",
			"dest": "example.com:443",
		},
		Description: "VLESS Reality for the EU fleet",
	}
	if err := store.Create(context.Background(), tpl); err != nil {
		t.Fatalf("create: %v", err)
	}

	// GetByID round-trips.
	got, err := store.GetByID(context.Background(), tpl.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != tpl.Name || got.Protocol != tpl.Protocol {
		t.Fatalf("round-trip mismatch: %+v vs %+v", got, tpl)
	}
	if v, ok := got.Params["flow"].(string); !ok || v != "xtls-rprx-vision" {
		t.Fatalf("params round-trip mismatch: %+v", got.Params)
	}

	// GetByName round-trips.
	got2, err := store.GetByName(context.Background(), tpl.Name)
	if err != nil {
		t.Fatalf("get by name: %v", err)
	}
	if got2.ID != tpl.ID {
		t.Fatalf("get by name: id mismatch")
	}

	// List returns the template.
	list, err := store.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(list) < 1 {
		t.Fatalf("list: got %d, want >= 1", len(list))
	}

	// Delete round-trips.
	if err := store.Delete(context.Background(), tpl.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := store.GetByID(context.Background(), tpl.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete: got %v, want ErrNotFound", err)
	}
}

func TestPgStore_DuplicateName(t *testing.T) {
	pool := testutil.MustNewPool(t)

	store := NewPgStore(pool)

	a := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "vless-reality-eu-" + uuid.NewString()[:8],
		Protocol: ProtocolVLESS,
		Params:   map[string]any{"flow": "xtls-rprx-vision"},
	}
	if err := store.Create(context.Background(), a); err != nil {
		t.Fatalf("create a: %v", err)
	}
	b := &InboundTemplate{
		ID:       uuid.New(),
		Name:     a.Name,
		Protocol: ProtocolHysteria2,
		Params:   map[string]any{},
	}
	if err := store.Create(context.Background(), b); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("create b: got %v, want ErrDuplicate", err)
	}
}

func TestPgStore_DescriptionNullRoundtrip(t *testing.T) {
	pool := testutil.MustNewPool(t)

	store := NewPgStore(pool)

	// Empty description round-trips as "" (nullableText
	// returns nil for empty, the column is NULL-able).
	tpl := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "no-desc-" + uuid.NewString()[:8],
		Protocol: ProtocolVLESS,
		Params:   map[string]any{},
		// Description intentionally empty.
	}
	if err := store.Create(context.Background(), tpl); err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := store.GetByID(context.Background(), tpl.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Description != "" {
		t.Fatalf("description: got %q, want empty", got.Description)
	}
}

func TestPgStore_ProtocolCheck(t *testing.T) {
	pool := testutil.MustNewPool(t)

	store := NewPgStore(pool)

	// The DB CHECK constraint rejects unknown
	// protocols even before the Go layer can
	// surface a validation error. Defensive: make
	// sure the CHECK is in place (a future migration
	// that drops it would be a security regression).
	tpl := &InboundTemplate{
		ID:       uuid.New(),
		Name:     "bad-proto-" + uuid.NewString()[:8],
		Protocol: Protocol("wireguard"),
		Params:   map[string]any{},
	}
	if err := store.Create(context.Background(), tpl); err == nil {
		t.Fatalf("create bad protocol: expected error, got nil")
	}
}
