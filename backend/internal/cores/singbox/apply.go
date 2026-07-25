// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Real Apply for the sing-box provider. v0.4.0-mvp-batched
// replaces the v0.3.0 stub with an HTTP POST to the
// node's aegis-agent. The wire contract is the same
// `/v1/apply` endpoint the v0.3.0 agent already accepts
// (and the v0.4.0-b agent actually writes to disk and
// reloads sing-box).
//
// # Where the address + bearer come from
//
// The Provider holds a NodeResolver that maps a node UUID
// to the panel-side (address, bearer) pair. The panel
// wires this in cmd/aegis/main.go after both the sing-box
// package and the nodes package are constructed. The
// resolver is an interface defined here (not in nodes)
// so the sing-box package does not have to import nodes
// (which would create a cycle once the user-management
// layer pulls in both).
//
// # Why a plain http.Client
//
// The agent speaks plain HTTP+JSON. There is no gRPC, no
// mTLS, no retry — the bearer secret in the Authorization
// header is the only auth. A bespoke HTTP client wrapper
// (e.g. a generator that pulls a "agent client" from
// somewhere) is over-engineering for v0.4.0; the plain
// `http.Client` is enough. The panel-level BatchedApplier
// handles the retry/timeout policy.
//
// # When this is called
//
// The user-management layer pushes a Delta into the
// per-node BatchedApplier; the BatchedApplier's FlushFn
// (constructed in cmd/aegis/main.go) re-renders the
// affected node's full config and calls Apply on the
// Provider registered in the cores registry. v0.4.0-b
// (the agent-side work) makes the other half real:
// without it, the agent just ACKs without touching
// sing-box. With it, /v1/apply writes the new config to
// /etc/sing-box/config.json and runs `systemctl reload
// sing-box`.

package singbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ErrApplyNotConfigured is returned by Apply when the
// panel main() has not called Configure() on the
// provider. The v0.3.0 stub returned a different error
// (ErrApplyNotImplemented, "agent transport not yet wired
// up"); that error was renamed in v0.4.0 to reflect the
// new state — the transport IS wired (the panel calls
// Configure at boot), Apply just has not been given its
// runtime deps yet. Callers should check with errors.Is.
var ErrApplyNotConfigured = errors.New("singbox: apply: provider not configured (call Configure first)")

// NodeResolver is the panel-side source of truth for
// "where is this node's agent and what bearer does it
// expect". The singbox package declares the interface
// here (not in nodes) to keep the dependency direction
// pointed the right way: the panel wires an adapter that
// implements this interface in terms of nodes.Service.
type NodeResolver interface {
	Resolve(ctx context.Context, id uuid.UUID) (address, bearer string, err error)
}

// httpClient is the contract singbox.Apply needs from the
// HTTP client. It is satisfied by *http.Client (and by
// httptest.Server in tests). Keeping it as an interface
// avoids the linter flagging the default *http.Client as
// "exotic" and lets tests inject a clocked client
// without BuildKit-cache gymnastics.
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Configure wires the provider's transport dependencies.
// Must be called before any Apply. The first call wins;
// subsequent calls are no-ops so a re-run of a
// configuration test does not swap the client out from
// under an in-flight apply.
//
// In production, main() calls this once at boot. Tests
// call it per-test in a fresh Provider.
func (p *Provider) Configure(nodes NodeResolver, client httpClient) {
	if p.nodes != nil || p.client != nil {
		return
	}
	p.nodes = nodes
	p.client = client
}

// Apply implements cores.CoreProvider. The v0.4.0
// implementation POSTs the rendered config to the
// node's aegis-agent `/v1/apply` endpoint and returns
// nil on 2xx, or a wrapped error on transport / non-2xx /
// node-resolution failure.
//
// The previous v0.3.0 stub returned ErrApplyNotImplemented
// unconditionally; that error is kept exported (so
// callers that explicitly want the "not wired" path can
// still detect it via errors.Is) but no longer returned
// from this method.
func (p *Provider) Apply(ctx context.Context, nodeID string, cfg []byte) error {
	if p.nodes == nil || p.client == nil {
		return fmt.Errorf("singbox apply: provider not configured (call singbox.Configure first): %w", ErrApplyNotConfigured)
	}
	id, err := uuid.Parse(nodeID)
	if err != nil {
		return fmt.Errorf("singbox apply: node %q: not a UUID: %w", nodeID, err)
	}
	addr, bearer, err := p.nodes.Resolve(ctx, id)
	if err != nil {
		return fmt.Errorf("singbox apply: resolve node %s: %w", id, err)
	}
	if addr == "" {
		return fmt.Errorf("singbox apply: node %s: empty address", id)
	}
	body, err := json.Marshal(applyEnvelope{Config: cfg})
	if err != nil {
		return fmt.Errorf("singbox apply: marshal envelope: %w", err)
	}
	url := "http://" + addr + "/v1/apply"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("singbox apply: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("singbox apply: POST %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("singbox apply: agent %s returned %d: %s",
			url, resp.StatusCode, truncateBody(respBody, 512))
	}
	return nil
}

// applyEnvelope is the JSON body the aegis-agent expects on
// POST /v1/apply. The v0.3.0 agent already accepts this
// shape (with `config` as a JSON object); v0.4.0-b
// extends the response to include a `verify` field
// (sing-box validation result) but does not change the
// request format.
type applyEnvelope struct {
	Config json.RawMessage `json:"config"`
}

// truncateBody limits a non-2xx response body in the error
// message so a misbehaving agent that returns megabytes of
// HTML does not blow up the panel log. The parameter is
// named `maxBytes` to avoid shadowing the built-in `max`
// (the gocritic `builtinShadow` check would otherwise
// reject the name; we keep the parameter explicit so the
// shadowing is impossible to introduce accidentally).
func truncateBody(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "...(truncated)"
}

// NewHTTPClient returns the default *http.Client used
// for the panel<->agent POST. Per-request timeouts
// are set on the request context (via
// NewRequestWithContext), so the client itself does
// not carry one — the BatchedApplier's window is the
// effective budget.
//
// Exported (capital N) so the panel's main.go can pass
// it to singbox.Configure. Tests use their own
// httptest.Server.Client() so this constructor is not
// used in unit tests.
func NewHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
	}
}

// Ensure compile-time check: *http.Client satisfies
// the httpClient interface declared above. If a future
// refactor breaks this, the package stops building
// rather than failing at runtime.
var _ httpClient = (*http.Client)(nil)
