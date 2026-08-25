// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Test helpers for the agentgrpc package and its consumers.
// Compiled into the production binary because the
// `httptest.Server` package is regular (not a `_test`
// build tag) and the operator does not pay a meaningful
// cost for a 30-line wrapper. The functions are clearly
// marked as test helpers; the only callers are the
// `singbox/apply_test.go` and `agentgrpc` test suites.

package agentgrpc

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/google/uuid"
)

// NewTestClient returns a `Client` that delegates every
// RPC to the production `httpTransport`. The server's
// URL is the agent address; the supplied `resolver`
// provides the bearer + the 401-refresh hook.
//
// The wrapper exists so tests do not need to duplicate
// the 401 -> BearerRefresher.Refresh -> one-retry
// logic. The returned `func()` is a teardown that
// closes the server; tests should `defer teardown()`
// it.
//
// Tests that want to bypass the production transport
// (e.g. to inject a custom error path) can construct
// their own `Client`; this helper is the convenience
// path.
func NewTestClient(server *httptest.Server, resolver NodeResolver) (c Client, teardown func()) {
	if resolver == nil {
		// Default: a resolver that returns the
		// server's listen address and a fixed
		// empty bearer (the test server does not
		// require auth for unauthenticated RPCs).
		resolver = &defaultTestResolver{addr: strings.TrimPrefix(server.URL, "http://")}
	}
	// Override the resolver's ResolveAddr to
	// return the test server's listen address,
	// regardless of what the caller passed.
	// Tests that need a custom address override
	// (e.g. to test resolver errors) wrap their
	// own resolver around this one.
	finalResolver := &testServerResolver{
		inner:      resolver,
		serverAddr: strings.TrimPrefix(server.URL, "http://"),
	}
	tr, err := newHTTPTransport(finalResolver)
	if err != nil {
		// newHTTPTransport is currently infallible
		// (no error path). If a future change
		// surfaces an error, the helper surfaces
		// it rather than panicking.
		panic(fmt.Sprintf("agentgrpc: NewTestClient: newHTTPTransport: %v", err))
	}
	return tr, server.Close
}

// defaultTestResolver is the resolver used by
// `NewTestClient` when the caller passes `nil`. It
// returns the supplied address and an empty bearer;
// the test server does not require auth.
type defaultTestResolver struct {
	addr string
}

func (r *defaultTestResolver) ResolveAddr(_ context.Context, _ uuid.UUID) (string, error) {
	return r.addr, nil
}
func (r *defaultTestResolver) GetBearer(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}
func (r *defaultTestResolver) Refresh(_ context.Context, _ uuid.UUID) (string, error) {
	return "", nil
}

// LoadMTLS returns ErrMTLSNotConfigured so the
// gRPC transport falls back to plaintext (the
// v0.8.29 test path). Tests that want to exercise
// the mTLS path wrap their own resolver around
// the defaultTestResolver and override LoadMTLS.
func (r *defaultTestResolver) LoadMTLS(_ context.Context, _ uuid.UUID) (cert, key, ca []byte, err error) {
	return nil, nil, nil, ErrMTLSNotConfigured
}

// testServerResolver wraps a caller's resolver so the
// production httpTransport talks to the test server
// regardless of what `ResolveAddr` returns. Tests that
// need to exercise the empty-address path can wrap
// their own resolver in a way that returns "".
type testServerResolver struct {
	inner      NodeResolver
	serverAddr string
}

func (r *testServerResolver) ResolveAddr(ctx context.Context, id uuid.UUID) (string, error) {
	// Test the caller's resolver first; the production
	// httpTransport uses the error to surface
	// resolver-level failures (e.g. "node not found").
	addr, err := r.inner.ResolveAddr(ctx, id)
	if err != nil {
		return "", err
	}
	if addr == "" {
		// Empty address: surface the error to match
		// the production contract (the httpTransport
		// returns "node <id>: empty address" when
		// the resolver returns "").
		return "", nil
	}
	return r.serverAddr, nil
}

func (r *testServerResolver) GetBearer(ctx context.Context, id uuid.UUID) (string, error) {
	return r.inner.GetBearer(ctx, id)
}

func (r *testServerResolver) Refresh(ctx context.Context, id uuid.UUID) (string, error) {
	return r.inner.Refresh(ctx, id)
}

// LoadMTLS forwards to the inner resolver so tests
// that wrap a real resolver (the agentca-backed one)
// get the real mTLS material; tests that wrap the
// defaultTestResolver get the
// `ErrMTLSNotConfigured` fallback.
func (r *testServerResolver) LoadMTLS(ctx context.Context, id uuid.UUID) (cert, key, ca []byte, err error) {
	return r.inner.LoadMTLS(ctx, id)
}

// Compile-time check that `*httpTransport` (the
// production HTTP+bearer `Client`) implements the
// `Client` interface. If a future PR breaks this
// (e.g. by changing the return type of a method), the
// build fails at the package level.
var _ Client = (*httpTransport)(nil)

// Compile-time check that `*httptest.Server` is a
// regular type (not a `_test` build tag). If a future
// Go release moves `httptest` to a test-only package,
// the testClient will need a non-httptest equivalent.
var _ = (&httptest.Server{}).Close
var _ = http.MethodGet
