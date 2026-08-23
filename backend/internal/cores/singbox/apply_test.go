// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
)

// fakeNodeResolver returns a fixed (address, bearer) pair
// for any UUID. Tests use it to drive Apply without
// touching the real nodes package.
//
// v0.8.8: the fake also implements
// `RefreshBearer` for the 401 →
// auto-refresh → retry path. The
// `refreshedBearer` field, if
// non-empty, is returned from
// `RefreshBearer` and updates the
// `bearer` field on the first call
// (so a single resolver instance
// can model "the agent rotated the
// bearer" without a second Resolve
// call). The `refreshCalls` atomic
// counter records the number of
// refresh invocations for assertion.
// The `refreshFailWith` field, if
// non-nil, makes `RefreshBearer`
// return that error (used to test
// the "refresh failed" error path).
type fakeNodeResolver struct {
	addr   string
	bearer string
	// failWith, if non-nil, is returned from Resolve()
	// (overrides the addr/bearer return).
	failWith error
	// calls records the number of Resolve invocations.
	calls atomic.Int32

	// refreshCalls records the number of
	// RefreshBearer invocations.
	refreshCalls atomic.Int32
	// refreshedBearer, if non-empty, is
	// returned from RefreshBearer. The
	// fake also mutates `bearer` to
	// this value on the first refresh
	// so subsequent Resolve calls
	// return the new bearer (the
	// production `nodes.Service`
	// would do the same — the
	// `SetAgentBearer` call inside
	// `RefreshAgentBearer` updates
	// the row).
	refreshedBearer string
	// refreshFailWith, if non-nil,
	// makes RefreshBearer return the
	// error. When set, the
	// `refreshedBearer` field is
	// ignored.
	refreshFailWith error
}

func (f *fakeNodeResolver) Resolve(_ context.Context, _ uuid.UUID) (string, string, error) {
	f.calls.Add(1)
	if f.failWith != nil {
		return "", "", f.failWith
	}
	return f.addr, f.bearer, nil
}

// RefreshBearer implements the v0.8.8
// extension to singbox.NodeResolver.
// The fake's behaviour matches the
// production `nodes.Service.RefreshAgentBearer`:
// on success, the new bearer is written
// to the row (here, the fake's `bearer`
// field) AND returned to the caller.
// On failure, the configured error is
// returned.
func (f *fakeNodeResolver) RefreshBearer(_ context.Context, _ uuid.UUID) (string, error) {
	f.refreshCalls.Add(1)
	if f.refreshFailWith != nil {
		return "", f.refreshFailWith
	}
	// First-call semantics: write the
	// new bearer to the row so
	// subsequent Resolve calls return
	// it. Subsequent calls leave the
	// field unchanged (idempotent).
	if f.refreshedBearer != "" {
		f.bearer = f.refreshedBearer
	}
	return f.refreshedBearer, nil
}

// TestApply_HappyPath verifies the v0.4.0 happy path:
//   - Apply POSTs to the node's address + /v1/apply.
//   - The body is {"config": <cfg>} (the shape the agent
//     expects).
//   - The Authorization: Bearer <bearer> header is set.
//   - A 2xx response yields nil.
func TestApply_HappyPath(t *testing.T) {
	const wantBearer = "test-bearer-do-not-use"
	var (
		gotMethod  string
		gotPath    string
		gotAuth    string
		gotCT      string
		gotBody    []byte
		serverHits atomic.Int32
	)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHits.Add(1)
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Match the v0.3.0 agent response shape (v0.4.0-b
		// extends with a `verify` field but keeps the
		// existing keys for backward compat).
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted":    true,
			"received_at": "2026-01-01T00:00:00Z",
			"bytes":       len(gotBody),
		})
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: wantBearer}
	p := New()
	p.Configure(resolver, agent.Client())

	nodeID := uuid.New().String()
	cfg := []byte(`{"inbounds":[{"type":"vless","tag":"in-1"}]}`)
	if err := p.Apply(context.Background(), nodeID, cfg); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if serverHits.Load() != 1 {
		t.Fatalf("server hit count = %d, want 1", serverHits.Load())
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/apply" {
		t.Errorf("path = %q, want /v1/apply", gotPath)
	}
	if gotAuth != "Bearer "+wantBearer {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer "+wantBearer)
	}
	if !strings.HasPrefix(gotCT, "application/json") {
		t.Errorf("Content-Type = %q, want application/json…", gotCT)
	}
	// Body must be the envelope with the config inside.
	var env applyEnvelope
	if err := json.Unmarshal(gotBody, &env); err != nil {
		t.Fatalf("envelope unmarshal: %v (body=%q)", err, gotBody)
	}
	if string(env.Config) != string(cfg) {
		t.Errorf("envelope.Config = %q, want %q", env.Config, cfg)
	}
	if resolver.calls.Load() != 1 {
		t.Errorf("resolver calls = %d, want 1", resolver.calls.Load())
	}
}

// TestApply_AgentError4xx verifies that a 4xx response from
// the agent produces a wrapped error.
func TestApply_AgentError4xx(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"bad config"}`, http.StatusBadRequest)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, agent.Client())
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on 4xx")
	}
	if !strings.Contains(err.Error(), "400") {
		t.Errorf("error should mention status code, got %q", err.Error())
	}
	if !strings.Contains(err.Error(), "bad config") {
		t.Errorf("error should include body, got %q", err.Error())
	}
}

// TestApply_AgentError5xx verifies that a 5xx response
// surfaces as a wrapped error.
func TestApply_AgentError5xx(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `internal failure`, http.StatusInternalServerError)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, agent.Client())
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on 5xx")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error should mention 500, got %q", err.Error())
	}
}

// TestApply_NetworkError verifies that a network-level
// failure (connection refused) is reported.
func TestApply_NetworkError(t *testing.T) {
	p := New()
	// Use an address that will refuse connections. 127.0.0.1:1
	// is reserved for tcpmux and almost never listening.
	p.Configure(&fakeNodeResolver{addr: "127.0.0.1:1", bearer: "x"}, &http.Client{Timeout: 500 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on connection refused")
	}
	if !strings.Contains(err.Error(), "singbox apply: POST") {
		t.Errorf("error should be a POST error, got %q", err.Error())
	}
}

// TestApply_InvalidNodeID verifies that a non-UUID nodeID
// is rejected before any HTTP call.
func TestApply_InvalidNodeID(t *testing.T) {
	p := New()
	p.Configure(&fakeNodeResolver{addr: "127.0.0.1:1", bearer: "x"}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), "not-a-uuid", []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should reject non-UUID nodeID")
	}
	if !strings.Contains(err.Error(), "not a UUID") {
		t.Errorf("error should mention UUID parse, got %q", err.Error())
	}
}

// TestApply_ResolverError verifies that a resolver error
// is propagated without any HTTP call.
func TestApply_ResolverError(t *testing.T) {
	want := errors.New("node offline")
	p := New()
	p.Configure(&fakeNodeResolver{failWith: want}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should propagate resolver error")
	}
	if !errors.Is(err, want) {
		t.Errorf("error should wrap %v, got %v", want, err)
	}
}

// TestApply_NotConfigured verifies the no-Configure path.
func TestApply_NotConfigured(t *testing.T) {
	p := New()
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if !errors.Is(err, ErrApplyNotConfigured) {
		t.Errorf("Apply without Configure: %v, want ErrApplyNotConfigured", err)
	}
}

// TestApply_EmptyAddress verifies the guard against
// "resolver returned an empty address" (a bug we
// specifically want to catch in tests, not in
// production).
func TestApply_EmptyAddress(t *testing.T) {
	p := New()
	p.Configure(&fakeNodeResolver{addr: "", bearer: "x"}, &http.Client{Timeout: 100 * time.Millisecond})
	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should fail on empty address")
	}
	if !strings.Contains(err.Error(), "empty address") {
		t.Errorf("error should mention empty address, got %q", err.Error())
	}
}

// TestApply_ContextCanceled verifies that a cancelled
// context aborts the request.
func TestApply_ContextCanceled(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Block until the client disconnects.
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer agent.Close()
	p := New()
	p.Configure(&fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "x"}, &http.Client{Timeout: 5 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled before Apply
	err := p.Apply(ctx, uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply with cancelled ctx should fail")
	}
}

// ============================================================================
// v0.8.8: 401 → auto-refresh → retry tests
// ============================================================================

// TestApply_401_AutoRefresh_RetrySucceeds is the
// v0.8.8 happy path for the auto-refresh:
// the agent returns 401 on the first
// POST, the resolver's RefreshBearer
// returns a new bearer, the second
// POST succeeds. Apply returns nil
// (the recovery is invisible to the
// BatchedApplier caller).
func TestApply_401_AutoRefresh_RetrySucceeds(t *testing.T) {
	const oldBearer = "stale-bearer"
	const newBearer = "fresh-bearer-abc"
	var (
		hits       atomic.Int32
		firstAuth  atomic.Value // string
		secondAuth atomic.Value // string
	)
	firstAuth.Store("")
	secondAuth.Store("")
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		auth := r.Header.Get("Authorization")
		if n == 1 {
			firstAuth.Store(auth)
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
			return
		}
		// n == 2: the retry. The auth
		// header MUST be the new
		// bearer (proves the retry
		// used the refreshed
		// bearer, not the old one).
		secondAuth.Store(auth)
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"accepted": true,
		})
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          oldBearer,
		refreshedBearer: newBearer,
	}
	p := New()
	p.Configure(resolver, agent.Client())

	nodeID := uuid.New().String()
	err := p.Apply(context.Background(), nodeID, []byte(`{"rendered":true}`))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	// Two POSTs (one 401, one 200).
	if h := hits.Load(); h != 2 {
		t.Errorf("agent hits: want 2, got %d", h)
	}
	// First POST used the OLD bearer.
	if got, _ := firstAuth.Load().(string); got != "Bearer "+oldBearer {
		t.Errorf("first POST Authorization: want %q, got %q", "Bearer "+oldBearer, got)
	}
	// Second POST used the NEW bearer.
	if got, _ := secondAuth.Load().(string); got != "Bearer "+newBearer {
		t.Errorf("second POST Authorization: want %q, got %q", "Bearer "+newBearer, got)
	}
	// Refresh was called exactly once.
	if rc := resolver.refreshCalls.Load(); rc != 1 {
		t.Errorf("RefreshBearer calls: want 1, got %d", rc)
	}
	// Resolve was called exactly once
	// (the retry uses the bearer
	// returned from RefreshBearer,
	// not a second Resolve call).
	if rc := resolver.calls.Load(); rc != 1 {
		t.Errorf("Resolve calls: want 1, got %d", rc)
	}
	// The fake's `bearer` field was
	// updated to the new bearer
	// (proves the adapter-style
	// row-mutation is modelled).
	if resolver.bearer != newBearer {
		t.Errorf("resolver.bearer after refresh: want %q, got %q", newBearer, resolver.bearer)
	}
}

// TestApply_401_RefreshFails_OriginalErrorPropagated
// verifies that when the auto-refresh
// itself fails, the original 401 is
// preserved as the error context and
// the refresh error is wrapped. The
// BatchedApplier caller sees a
// combined message that includes both
// signals.
func TestApply_401_RefreshFails_OriginalErrorPropagated(t *testing.T) {
	const oldBearer = "stale-bearer"
	refreshErr := errors.New("agent unreachable for refresh")
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          oldBearer,
		refreshFailWith: refreshErr,
	}
	p := New()
	p.Configure(resolver, agent.Client())

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error when both POST and refresh fail")
	}
	// The error must mention the 401
	// (the original signal) AND the
	// refresh failure (the root
	// cause).
	msg := err.Error()
	if !strings.Contains(msg, "401") {
		t.Errorf("error must mention 401, got %q", msg)
	}
	if !strings.Contains(msg, "auto-refresh failed") {
		t.Errorf("error must mention auto-refresh, got %q", msg)
	}
	if !errors.Is(err, refreshErr) {
		t.Errorf("error must wrap the refresh error %v, got %v", refreshErr, err)
	}
	// Only one POST (the failing
	// 401). The retry did NOT fire
	// because the refresh failed
	// before the retry.
	if h := hits.Load(); h != 1 {
		t.Errorf("agent hits: want 1, got %d", h)
	}
	if rc := resolver.refreshCalls.Load(); rc != 1 {
		t.Errorf("RefreshBearer calls: want 1, got %d", rc)
	}
}

// TestApply_401_RetryAlsoFails_Propagates401OnRetry
// is the "panel and agent are in an
// unrecoverable state" case: the
// refresh succeeds, the new bearer
// is also rejected by the agent with
// another 401. The Apply path
// returns the retry's 401 error;
// the BatchedApplier caller sees the
// second 401 (not the first) because
// that's the most recent signal.
func TestApply_401_RetryAlsoFails_Propagates401OnRetry(t *testing.T) {
	const oldBearer = "stale-bearer-1"
	const newBearer = "stale-bearer-2"
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		// Both POSTs return 401.
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          oldBearer,
		refreshedBearer: newBearer,
	}
	p := New()
	p.Configure(resolver, agent.Client())

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error when retry also returns 401")
	}
	// The error must mention 401 (the
	// retry's 401, since the refresh
	// succeeded and the second POST
	// was made with the new bearer).
	msg := err.Error()
	if !strings.Contains(msg, "401") {
		t.Errorf("error must mention 401, got %q", msg)
	}
	if !strings.Contains(msg, "retry") {
		t.Errorf("error must mention retry, got %q", msg)
	}
	// Two POSTs (the original 401 +
	// the retry 401).
	if h := hits.Load(); h != 2 {
		t.Errorf("agent hits: want 2, got %d", h)
	}
	// One refresh.
	if rc := resolver.refreshCalls.Load(); rc != 1 {
		t.Errorf("RefreshBearer calls: want 1, got %d", rc)
	}
	// Two Resolve calls (initial +
	// retry). The v0.8.8 code does
	// NOT do a second Resolve on
	// the retry path — the retry
	// uses the bearer returned
	// from RefreshBearer.
	// (Wait — the actual code DOES
	// use the bearer returned
	// from RefreshBearer, so
	// Resolve is only called once.
	// Let me re-check.)
	if rc := resolver.calls.Load(); rc != 1 {
		t.Errorf("Resolve calls: want 1 (refresh returns the new bearer, no second Resolve), got %d", rc)
	}
}

// TestApply_500_NoAutoRefresh verifies that
// non-401 errors (e.g. 500) do NOT
// trigger the auto-refresh path. The
// 500 is a server-side problem, not
// a stale-bearer problem, so the
// refresh would just waste an SSH
// session and not fix anything.
func TestApply_500_NoAutoRefresh(t *testing.T) {
	const wantBearer = "test-bearer-do-not-use"
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"agent oops"}`))
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:   strings.TrimPrefix(agent.URL, "http://"),
		bearer: wantBearer,
		// If RefreshBearer IS called
		// despite the 500, the
		// refresh would return a
		// new bearer, which would
		// then be detected as a
		// missed assertion below.
		refreshedBearer: "should-not-be-used",
	}
	p := New()
	p.Configure(resolver, agent.Client())

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error on 500")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("error must mention 500, got %q", err.Error())
	}
	// Exactly ONE POST. The 500 did
	// not trigger a refresh + retry.
	if h := hits.Load(); h != 1 {
		t.Errorf("agent hits: want 1 (no retry on 500), got %d", h)
	}
	// Zero refreshes.
	if rc := resolver.refreshCalls.Load(); rc != 0 {
		t.Errorf("RefreshBearer calls: want 0 (500 is not 401), got %d", rc)
	}
	// The bearer was NOT updated
	// (proves the no-refresh path
	// didn't touch the resolver).
	if resolver.bearer != wantBearer {
		t.Errorf("resolver.bearer should be unchanged, want %q, got %q", wantBearer, resolver.bearer)
	}
}

// TestApply_404_NoAutoRefresh is the same
// invariant for 404: a 404 is
// "endpoint does not exist" or "node
// deleted from the agent's view" —
// the bearer is irrelevant. The
// refresh would not fix the 404.
func TestApply_404_NoAutoRefresh(t *testing.T) {
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          "x",
		refreshedBearer: "should-not-be-used",
	}
	p := New()
	p.Configure(resolver, agent.Client())

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error on 404")
	}
	if h := hits.Load(); h != 1 {
		t.Errorf("agent hits: want 1 (no retry on 404), got %d", h)
	}
	if rc := resolver.refreshCalls.Load(); rc != 0 {
		t.Errorf("RefreshBearer calls: want 0 (404 is not 401), got %d", rc)
	}
}

// TestApply_401_RefreshSucceeds_RetryNon401 verifies the
// "refresh succeeded but the retry
// returns a non-401 non-2xx" edge
// case: the agent might be in a
// state where the bearer was the
// problem AND there's something else
// wrong (e.g. sing-box config parse
// failure). The retry's error is
// returned; the operator sees both
// signals (the recovery happened,
// the agent is still broken for a
// different reason).
func TestApply_401_RefreshSucceeds_RetryNon401(t *testing.T) {
	const oldBearer = "stale"
	const newBearer = "fresh"
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		// Retry returns 500 (e.g.
		// sing-box parse failure
		// after a successful
		// bearer refresh).
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"config parse failure"}`))
	}))
	defer agent.Close()

	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          oldBearer,
		refreshedBearer: newBearer,
	}
	p := New()
	p.Configure(resolver, agent.Client())

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error on 500 retry")
	}
	msg := err.Error()
	if !strings.Contains(msg, "500") {
		t.Errorf("error must mention 500 (the retry's status), got %q", msg)
	}
	if !strings.Contains(msg, "retry") {
		t.Errorf("error must mention retry, got %q", msg)
	}
}

// ---- v0.8.28.7 (#289/C4): response-body lifecycle ----
//
// The pre-fix postApply returned the raw io.ReadCloser and
// no caller closed it: every successful apply (and most
// error branches) leaked one pooled connection until the
// transport's idle timer reclaimed it. The tests below pin
// the new contract: postApply owns the body — it is closed
// exactly once per HTTP response, on every terminal path,
// and the read is capped at maxAgentBodyBytes.

// trackingBody counts Close calls on the wrapped body.
type trackingBody struct {
	io.ReadCloser
	closes *atomic.Int32
}

func (b *trackingBody) Close() error {
	b.closes.Add(1)
	return b.ReadCloser.Close()
}

// trackingClient swaps every response body for a
// trackingBody sharing the test's counter.
type trackingClient struct {
	inner  httpClient
	closes *atomic.Int32
}

func (c *trackingClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	if err != nil {
		return resp, err
	}
	resp.Body = &trackingBody{ReadCloser: resp.Body, closes: c.closes}
	return resp, nil
}

// TestApply_PostApply_ClosesBody_HappyPath pins the
// success path: a 2xx response's body is closed even
// though the caller ignores it.
func TestApply_PostApply_ClosesBody_HappyPath(t *testing.T) {
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer agent.Close()

	var closes atomic.Int32
	resolver := &fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "b"}
	p := New()
	p.Configure(resolver, &trackingClient{inner: agent.Client(), closes: &closes})

	if err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if c := closes.Load(); c != 1 {
		t.Errorf("body Close calls = %d, want 1 (leaked fd on the happy path)", c)
	}
}

// TestApply_PostApply_BodyCappedAndClosed_Non2xx pins the
// error path: a non-2xx body is closed, the read is
// capped at maxAgentBodyBytes (a megabyte-sized body does
// not reach memory in full), and the error message stays
// truncated.
func TestApply_PostApply_BodyCappedAndClosed_Non2xx(t *testing.T) {
	huge := strings.Repeat("x", maxAgentBodyBytes*4)
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(huge))
	}))
	defer agent.Close()

	var closes atomic.Int32
	resolver := &fakeNodeResolver{addr: strings.TrimPrefix(agent.URL, "http://"), bearer: "b"}
	p := New()
	p.Configure(resolver, &trackingClient{inner: agent.Client(), closes: &closes})

	err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`))
	if err == nil {
		t.Fatal("Apply should return error on 500")
	}
	if c := closes.Load(); c != 1 {
		t.Errorf("body Close calls = %d, want 1 (leaked fd on the error path)", c)
	}
	if !strings.Contains(err.Error(), "(truncated)") {
		t.Errorf("error message must be truncated, got %q", err.Error())
	}
}

// TestApply_PostApply_ClosesBothBodies_RetryPath pins the
// 401 → refresh → retry path: BOTH the first response's
// body and the retry's body are closed (the pre-fix code
// leaked the first body on every retry branch).
func TestApply_PostApply_ClosesBothBodies_RetryPath(t *testing.T) {
	var hits atomic.Int32
	agent := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits.Add(1) == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"accepted":true}`))
	}))
	defer agent.Close()

	var closes atomic.Int32
	resolver := &fakeNodeResolver{
		addr:            strings.TrimPrefix(agent.URL, "http://"),
		bearer:          "stale",
		refreshedBearer: "fresh",
	}
	p := New()
	p.Configure(resolver, &trackingClient{inner: agent.Client(), closes: &closes})

	if err := p.Apply(context.Background(), uuid.New().String(), []byte(`{}`)); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if rc := resolver.refreshCalls.Load(); rc != 1 {
		t.Errorf("RefreshBearer calls = %d, want 1", rc)
	}
	if c := closes.Load(); c != 2 {
		t.Errorf("body Close calls = %d, want 2 (first attempt + retry both leak pre-fix)", c)
	}
}
