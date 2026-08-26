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
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
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

// TestRender_ConcurrentUsers_NoCrossLeak pins the
// v0.8.28.8 (#289/C3) fix: the per-render credential
// map is RENDER-LOCAL.
//
// Pre-fix, the credential map lived on the Service and
// was overwritten by every Render* call. Two concurrent
// renders for different users raced on the map (Go maps
// are not safe for concurrent write — `go test -race`
// flags this immediately) and, worse, the second
// render's precompute could overwrite the first
// render's view mid-loop, putting user B's credential
// into user A's subscription payload.
//
// This test hammers both renderers with interleaved
// renders for two users who each hold a credential on
// the SAME inbound, then asserts each output carries
// only its own credential. Run under `-race` in CI;
// pre-fix it fails within the first few rounds (race +
// leak), post-fix it is deterministic.
func TestRender_ConcurrentUsers_NoCrossLeak(t *testing.T) {
	t.Parallel()
	svc, eps, uA, inbID := phase2Fixture(t)
	credsSvc := credentials.NewService(credentials.NewMemoryStore())

	const (
		credA = "phase2-conc-uuid-aaaaaaaa-111111111111111111111111"
		credB = "phase2-conc-uuid-bbbbbbbb-222222222222222222222222"
	)
	if _, err := credsSvc.Create(context.Background(), uA.ID, inbID, credA); err != nil {
		t.Fatalf("seed user A credential: %v", err)
	}
	uB := &User{
		ID:       uuid.MustParse("50000000-0000-0000-0000-000000000002"),
		Username: "bob",
		Status:   UserStatusActive,
		ExpireAt: uA.ExpireAt,
	}
	if _, err := credsSvc.Create(context.Background(), uB.ID, inbID, credB); err != nil {
		t.Fatalf("seed user B credential: %v", err)
	}
	svc.WithCreds(credsSvc)

	const rounds = 200
	var wg sync.WaitGroup
	errCh := make(chan error, 4*rounds)
	render := func(u *User, own, other string, format string) {
		defer wg.Done()
		var (
			out []byte
			err error
		)
		switch format {
		case "singbox":
			out, err = svc.RenderSingbox(context.Background(), u, eps)
		case "clash":
			out, err = svc.RenderClash(context.Background(), u, eps)
		}
		if err != nil {
			errCh <- fmt.Errorf("%s (%s): render: %w", u.Username, format, err)
			return
		}
		body := string(out)
		if !strings.Contains(body, own) {
			errCh <- fmt.Errorf("%s (%s): output does not contain the user's OWN credential — Phase 2 lookup broken:\n%s", u.Username, format, body)
		}
		if strings.Contains(body, other) {
			errCh <- fmt.Errorf("%s (%s): output contains ANOTHER user's credential — cross-user leak (#289/C3):\n%s", u.Username, format, body)
		}
	}

	for i := 0; i < rounds; i++ {
		format := "singbox"
		if i%2 == 1 {
			format = "clash"
		}
		wg.Add(2)
		go render(uA, credA, credB, format)
		go render(uB, credB, credA, format)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Error(err)
	}
}

// TestRenderBase64_Phase2_UsesUserCredential is the
// v0.8.32.2 (#303) regression guard for the base64
// subscription render path. The HTML subscription page
// (the one operators hand to users) renders URIs through
// RenderBase64 and then base64-encodes the joined string
// for the QR code / sub URL. Pre-fix, RenderBase64
// ignored the per-user credential map and every user's
// base64 carried the shared operator UUID/password from
// `inbounds.params`. The per-user credential was wired
// in v0.7.x (PR #167/#168/#198) but only the sing-box
// and clash renderers consumed it — the base64 path
// silently regressed to Phase 1 single-credential
// behaviour. A per-user revocation therefore never
// cut off a user's base64 access; the revoked user
// kept getting the common secret.
//
// The test seeds the user with a credential on the
// single VLESS inbound, calls RenderBase64, base64-
// decodes the result, and asserts the URI's `uuid`
// query parameter is the per-user credential (not the
// operator's `aaaa` uuid from inbounds.params).
// Pre-fix: the uri carries `00000000-...-aaa` and the
// test fails. Post-fix: the uri carries the user's
// own credential and the test passes.
func TestRenderBase64_Phase2_UsesUserCredential(t *testing.T) {
	svc, eps, u, inbID := phase2Fixture(t)
	credsSvc := credentials.NewService(credentials.NewMemoryStore())
	const userUUIDValue = "phase2-b64-uuid-11111111-aaaaaaaaaaaaaa"
	if _, err := credsSvc.Create(context.Background(), u.ID, inbID, userUUIDValue); err != nil {
		t.Fatalf("seed user credential: %v", err)
	}
	svc.WithCreds(credsSvc)

	out, err := svc.RenderBase64(context.Background(), u, eps)
	if err != nil {
		t.Fatalf("RenderBase64: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("RenderBase64 returned empty body; expected a base64 of the URI list")
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatalf("base64.StdEncoding.DecodeString: %v (output: %q)", err, string(out))
	}
	body := string(decoded)
	if !strings.Contains(body, userUUIDValue) {
		t.Errorf("RenderBase64 output does not contain the per-user credential uuid; per-user lookup broken (#303):\n%s", body)
	}
	// The operator's params-based uuid ends in `aaa`
	// (see phase2Fixture). A render that fell back to
	// params would surface that suffix. We assert the
	// output does NOT contain the operator uuid as a
	// belt-and-braces check — the base64 path must be
	// strictly Phase 2 when a per-user credential exists.
	if strings.Contains(body, "00000000-0000-0000-0000-000000000aaa") {
		t.Errorf("RenderBase64 output carries the operator's params-based uuid; per-user cred not honoured:\n%s", body)
	}
}

// TestRenderBase64_Phase2_OtherUserCredNotLeaked is the
// cross-user isolation guard for the base64 render path.
// Two users share an inbound; user A has a credential,
// user B does not. RenderBase64 for user B must NOT
// include user A's credential. Pre-fix, both users
// saw the same operator uuid (params), so the test
// was implicitly OK. Post-fix, user B's render uses
// the params fallback (which is the correct Phase 1
// path for a user who has not been issued a per-
// inbound credential) and the test continues to pass;
// if a future refactor accidentally starts cross-
// threading creds from other users into B's render,
// the test catches it.
func TestRenderBase64_Phase2_OtherUserCredNotLeaked(t *testing.T) {
	svc, eps, uA, inbID := phase2Fixture(t)
	credsSvc := credentials.NewService(credentials.NewMemoryStore())
	const userACredValue = "phase2-b64-leak-uuid-22222222-bbbbbbbb"
	if _, err := credsSvc.Create(context.Background(), uA.ID, inbID, userACredValue); err != nil {
		t.Fatalf("seed user A credential: %v", err)
	}
	svc.WithCreds(credsSvc)
	uB := &User{
		ID:       uuid.MustParse("50000000-0000-0000-0000-000000000099"),
		Username: "bob",
		Status:   UserStatusActive,
		ExpireAt: uA.ExpireAt,
	}
	out, err := svc.RenderBase64(context.Background(), uB, eps)
	if err != nil {
		t.Fatalf("RenderBase64: %v", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(string(out))
	if err != nil {
		t.Fatalf("base64.StdEncoding.DecodeString: %v (output: %q)", err, string(out))
	}
	body := string(decoded)
	if strings.Contains(body, userACredValue) {
		t.Errorf("RenderBase64 for user B contains user A's credential — cross-user auth boundary leak:\n%s", body)
	}
}
