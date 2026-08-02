// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Phase 2 multi-user render tests for the
// subscription package. The Phase 1 path (no
// credentials source) falls back to per-inbound
// `params["uuid"]` / `["password"]` and is covered
// by the existing render_singbox / render_clash /
// render_vars tests. This file covers the Phase 2
// path: the subscription Service is wired with a
// `*credentials.Service` that holds per-(user,
// inbound) credentials, and the renderers must use
// the user-specific credential as the protocol
// auth material (not the operator's params value).
//
// The tests use a real `*credentials.Service`
// backed by a `*credentials.MemoryStore` — same
// pattern the production wiring in
// `cmd/aegis/main.go` uses for the dev seed.

package subscription

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/credentials"
	"github.com/QAdversif/AegisPanel/internal/hosts"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
	"github.com/QAdversif/AegisPanel/internal/nodes"
)

// phase2Fixture builds a Service + ResolvedEndpoint
// for a single VLESS inbound. The inbound's params
// carry an OPERATOR uuid (recognisable: the suffix
// `aaaa`), so a render that uses the wrong source
// is easy to spot in a test failure.
func phase2Fixture(t *testing.T) (*Service, []ResolvedEndpoint, *User, uuid.UUID) {
	t.Helper()
	const (
		operatorUUID = "00000000-0000-0000-0000-000000000aaa"
		hostID       = "10000000-0000-0000-0000-000000000001"
		endpointID   = "20000000-0000-0000-0000-000000000001"
		nodeID       = "30000000-0000-0000-0000-000000000001"
		inboundID    = "40000000-0000-0000-0000-000000000001"
		userID       = "50000000-0000-0000-0000-000000000001"
	)
	nodesStore := nodes.NewMemoryStore()
	inboundsStore := inbounds.NewMemoryStore()
	hostsStore := hosts.NewMemoryStore()
	if err := nodesStore.Create(context.Background(), &nodes.Node{ID: uuid.MustParse(nodeID), Name: "n1", Address: "node1.example.com", State: nodes.StateOnline}); err != nil {
		t.Fatalf("nodes.Create: %v", err)
	}
	inb := &inbounds.Inbound{
		ID:         uuid.MustParse(inboundID),
		NodeID:     uuid.MustParse(nodeID),
		Name:       "vless-in",
		Protocol:   inbounds.ProtocolVLESS,
		Listen:     "::",
		ListenPort: 443,
		Enabled:    true,
		Params: map[string]any{
			"port": 443,
			"uuid": operatorUUID,
			"flow": "xtls-rprx-vision",
			"tls":  map[string]any{"server_name": "cdn.example.com"},
		},
	}
	if err := inboundsStore.Create(context.Background(), inb); err != nil {
		t.Fatalf("inbounds.Create: %v", err)
	}
	host := &hosts.Host{
		ID:          uuid.MustParse(hostID),
		Remark:      "p1",
		DisplayName: "p1",
		Type:        hosts.HostTypeDirect,
		Enabled:     true,
		Priority:    10,
		Endpoints: []hosts.Endpoint{{
			ID:        uuid.MustParse(endpointID),
			NodeID:    uuid.MustParse(nodeID),
			InboundID: uuid.MustParse(inboundID),
			Weight:    1,
			Address:   []string{"cdn.example.com"},
			SNI:       []string{"cdn.example.com"},
		}},
	}
	if err := hostsStore.Create(context.Background(), host); err != nil {
		t.Fatalf("hosts.Create: %v", err)
	}
	hostsSvc := hosts.NewService(hostsStore, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	svc := NewService(NewMemoryStore(), hostsSvc, nodes.NewService(nodesStore), inbounds.NewService(inboundsStore, nodes.NewService(nodesStore)))
	expire := time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)
	u := &User{
		ID:                uuid.MustParse(userID),
		Username:          "alice",
		Status:            UserStatusActive,
		ExpireAt:          &expire,
		TrafficLimitBytes: 100 * 1024 * 1024 * 1024,
	}
	ep := ResolvedEndpoint{
		Host:     host,
		Endpoint: host.Endpoints[0],
		Node:     &nodes.Node{ID: uuid.MustParse(nodeID), Name: "n1", Address: "node1.example.com", State: nodes.StateOnline},
		Inbound:  inb,
	}
	return svc, []ResolvedEndpoint{ep}, u, uuid.MustParse(inboundID)
}

// TestRenderSingbox_Phase2_UsesUserCredential is
// the headline test: the sing-box renderer's
// per-endpoint outbound uses the per-(user,
// inbound) credential as the `uuid` field when
// the user has a credential in the Phase 2 table.
func TestRenderSingbox_Phase2_UsesUserCredential(t *testing.T) {
	t.Parallel()
	svc, eps, u, inbID := phase2Fixture(t)
	// Seed a per-(user, inbound) credential with a
	// recognisable UUID (the operator's params[uuid]
	// is `00000000-0000-0000-0000-000000000aaa`).
	credsSvc := credentials.NewService(credentials.NewMemoryStore())
	const userUUIDValue = "phase2-user-uuid-11111111-aaaaaaaaaaaaaaaaaaaaaaaa"
	if _, err := credsSvc.Create(context.Background(), u.ID, inbID, userUUIDValue); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	svc.WithCreds(credsSvc)

	out, err := svc.RenderSingbox(context.Background(), u, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, userUUIDValue) {
		t.Errorf("RenderSingbox output does not contain the user-specific UUID; Phase 2 fallback to params was taken:\n%s", body)
	}
	if strings.Contains(body, "00000000-0000-0000-0000-000000000aaa") {
		t.Errorf("RenderSingbox output contains the operator's params[uuid]; user-specific credential was not used:\n%s", body)
	}
}

// TestRenderSingbox_Phase2_FallsBackToParams is the
// migration-safety test: a user with no per-inbound
// credential in the Phase 2 table still gets a
// working sub URL via the operator's params. The
// credential source IS installed (the panel is on
// Phase 2), but this user has not been issued a
// credential yet — they still need the sub URL to
// work.
func TestRenderSingbox_Phase2_FallsBackToParams(t *testing.T) {
	t.Parallel()
	svc, eps, u, _ := phase2Fixture(t)
	// Install a credentials service that has NO
	// rows for this user.
	svc.WithCreds(credentials.NewService(credentials.NewMemoryStore()))

	out, err := svc.RenderSingbox(context.Background(), u, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, "00000000-0000-0000-0000-000000000aaa") {
		t.Errorf("RenderSingbox output does not contain the operator's params[uuid]; Phase 1 fallback was not taken for a user with no per-inbound credential:\n%s", body)
	}
}

// TestRenderClash_Phase2_UsesUserCredential mirrors
// the sing-box test for the Clash renderer. The
// per-endpoint `uuid` field uses the per-user
// credential when present.
func TestRenderClash_Phase2_UsesUserCredential(t *testing.T) {
	t.Parallel()
	svc, eps, u, inbID := phase2Fixture(t)
	credsSvc := credentials.NewService(credentials.NewMemoryStore())
	const userUUIDValue = "phase2-clash-uuid-22222222-bbbbbbbbbbbbbbbbbbbbbbbb"
	if _, err := credsSvc.Create(context.Background(), u.ID, inbID, userUUIDValue); err != nil {
		t.Fatalf("seed credential: %v", err)
	}
	svc.WithCreds(credsSvc)

	out, err := svc.RenderClash(context.Background(), u, eps)
	if err != nil {
		t.Fatalf("RenderClash: %v", err)
	}
	body := string(out)
	if !strings.Contains(body, userUUIDValue) {
		t.Errorf("RenderClash output does not contain the user-specific UUID:\n%s", body)
	}
	if strings.Contains(body, "00000000-0000-0000-0000-000000000aaa") {
		t.Errorf("RenderClash output contains the operator's params[uuid]:\n%s", body)
	}
}

// TestRenderSingbox_Phase2_OtherUserCredNotLeaked
// pins the security boundary: user A's credential
// is NOT used when rendering user B's sub URL,
// even if both credentials are in the Phase 2
// table.
func TestRenderSingbox_Phase2_OtherUserCredNotLeaked(t *testing.T) {
	t.Parallel()
	svc, eps, u, inbID := phase2Fixture(t)
	credsSvc := credentials.NewService(credentials.NewMemoryStore())
	// Seed a credential for a DIFFERENT user on
	// the same inbound. The render for `u` must
	// NOT pick this one up.
	otherUserID := uuid.MustParse("99999999-9999-9999-9999-999999999999")
	otherUserCred := "phase2-other-user-uuid-33333333-cccccccccccccccccccc"
	if _, err := credsSvc.Create(context.Background(), otherUserID, inbID, otherUserCred); err != nil {
		t.Fatalf("seed other-user credential: %v", err)
	}
	svc.WithCreds(credsSvc)

	out, err := svc.RenderSingbox(context.Background(), u, eps)
	if err != nil {
		t.Fatalf("RenderSingbox: %v", err)
	}
	body := string(out)
	if strings.Contains(body, otherUserCred) {
		t.Errorf("RenderSingbox output contains another user's credential — auth boundary leak:\n%s", body)
	}
}
