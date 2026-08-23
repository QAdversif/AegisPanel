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

// maxAgentBodyBytes caps how much of the agent's
// /v1/apply response body postApply reads into memory
// (v0.8.28.7, #289/C4). The agent answers with a small
// JSON ack; the cap exists so a misbehaving agent that
// streams megabytes cannot balloon panel memory — the
// error wrapper truncates to 512 bytes anyway via
// truncateBody.
const maxAgentBodyBytes = 64 << 10 // 64 KiB

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
//
// # Bearer rotation support (v0.8.8)
//
// `RefreshBearer` is the panel-initiated
// recovery path for a stale agent
// bearer. The v0.8.7 PR added the
// operator-side `POST
// /api/v1/nodes/{id}/refresh-agent-bearer`
// endpoint + the `nodes.Service.RefreshAgentBearer`
// method. v0.8.8 wires that same
// method into the Apply path: when
// the agent returns 401 (the panel's
// stored bearer no longer matches the
// agent's), the Apply path calls
// `RefreshBearer` to re-fetch the
// current bearer from the node, then
// retries the POST once with the new
// bearer. The retry is bounded (one
// attempt; a second 401 surfaces the
// error to the BatchedApplier caller
// without looping). The race
// between two BatchedApplier
// goroutines refreshing the same node
// is benign (both reads return the
// same agent.env value; the DB write
// is idempotent at the row level).
type NodeResolver interface {
	Resolve(ctx context.Context, id uuid.UUID) (address, bearer string, err error)
	// RefreshBearer triggers a panel-side
	// recovery for a stale agent bearer.
	// The method SSHes into the node
	// (using the stored panel SSH key
	// from the v0.8.1 persistent-key
	// feature), reads
	// `/etc/aegis/agent.env`, and
	// updates `nodes.agent_bearer`.
	// Returns the new bearer value
	// (the result is also written to
	// the DB; the return value lets
	// the Apply path retry without a
	// second Resolve call when only
	// the bearer changed). The
	// adapter is responsible for
	// wrapping `nodes.Service.RefreshAgentBearer`.
	RefreshBearer(ctx context.Context, id uuid.UUID) (newBearer string, err error)
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
//
// # v0.8.8: 401 → auto-refresh → retry (one attempt)
//
// When the agent returns 401 (the panel's
// stored bearer no longer matches), the
// Apply path:
//  1. Calls `RefreshBearer` on the
//     resolver (which SSHes into the
//     node, reads agent.env, and
//     updates `nodes.agent_bearer`).
//  2. Re-POSTs with the NEW bearer
//     returned by the refresh (one
//     retry attempt; no loop).
//  3. If the retry succeeds, returns
//     nil (the recovery is invisible
//     to the BatchedApplier caller).
//  4. If the retry fails (any
//     non-2xx), returns the retry's
//     error — the original 401 is
//     preserved as the wrapped error
//     when the retry also returns 401.
//
// The retry is bounded to one
// attempt to prevent an infinite
// loop when the agent and the panel
// are stuck in a state where neither
// knows the right bearer (e.g. the
// operator wiped /etc/aegis/agent.env
// on the node and the panel's stored
// key is also gone). The caller (the
// BatchedApplier) sees the error and
// the per-delta retry worker can
// decide what to do.
//
// The auto-refresh records the
// `node.agent-bearer.refresh` audit
// row via the v0.8.7
// `nodes.Service.RefreshAgentBearer`.
// The audit row's `ActorID` is empty
// for auto-refresh (the BatchedApplier
// goroutine has no `auth.Claims` in
// context); the v0.8.7
// operator-initiated path has a
// non-empty `ActorID`. The shape
// distinguishes the two callers in
// the audit UI.
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
	// First attempt with the panel's
	// current bearer.
	status, respBody, postErr := p.postApply(ctx, url, body, bearer)
	if postErr != nil {
		return postErr
	}
	if status == http.StatusUnauthorized {
		// v0.8.8: the panel's stored
		// bearer is stale. Try to
		// refresh it via the
		// resolver. RefreshBearer
		// returns the new bearer
		// (already written to
		// `nodes.agent_bearer`); we
		// use the return value to
		// retry without a second
		// Resolve call.
		newBearer, refreshErr := p.nodes.RefreshBearer(ctx, id)
		if refreshErr != nil {
			// Refresh failed
			// (no stored
			// key, SSH
			// failure,
			// agent.env
			// parse
			// failure,
			// etc.).
			// Surface
			// the
			// original
			// 401
			// with
			// the
			// refresh
			// error
			// wrapped
			// so the
			// operator
			// sees
			// both
			// signals.
			return fmt.Errorf("singbox apply: agent %s returned 401 (stale bearer); auto-refresh failed: %w", url, refreshErr)
		}
		// Second attempt with the
		// new bearer. One retry
		// only — if the agent
		// also rejects this
		// bearer, the panel and
		// the agent are in an
		// unrecoverable state
		// and the operator must
		// intervene (e.g. SSH
		// in manually and re-run
		// `aegis admin node
		// rotate-panel-key`).
		//
		// v0.8.28.7: postApply now returns
		// the body already read ([]byte)
		// and closed; no io.ReadAll at
		// the call sites.
		status2, respBody2, postErr2 := p.postApply(ctx, url, body, newBearer)
		if postErr2 != nil {
			return postErr2
		}
		if status2 == http.StatusUnauthorized {
			return fmt.Errorf("singbox apply: agent %s returned 401 on retry with refreshed bearer: %s", url, truncateBody(respBody2, 512))
		}
		if status2 < 200 || status2 >= 300 {
			return fmt.Errorf("singbox apply: agent %s returned %d on retry: %s", url, status2, truncateBody(respBody2, 512))
		}
		// Retry succeeded. The
		// recovery is invisible
		// to the BatchedApplier
		// caller (return nil
		// matches the
		// no-recovery happy
		// path).
		return nil
	}
	if status < 200 || status >= 300 {
		return fmt.Errorf("singbox apply: agent %s returned %d: %s",
			url, status, truncateBody(respBody, 512))
	}
	return nil
}

// postApply fires a single POST /v1/apply with the
// supplied bearer and returns (status, body, error).
//
// v0.8.28.7 (#289/C4): the function now OWNS the response
// lifecycle — `resp.Body` is closed here via defer, and
// the body is read into memory (capped at
// maxAgentBodyBytes) before returning `[]byte`. The
// previous contract returned the raw `io.ReadCloser` and
// pushed the read/close responsibility onto the caller;
// none of the five call-path branches closed it, so every
// successful apply (and most error branches) leaked one
// pooled connection until Go's idle-connection timer
// reclaimed it — under a steady apply cadence this
// exhausted the panel process's file descriptors.
//
// The 64 KiB cap bounds a misbehaving agent that streams
// megabytes of HTML: the read stops after the cap, the
// connection is closed, and `truncateBody` keeps the log
// line short.
func (p *Provider) postApply(ctx context.Context, url string, body []byte, bearer string) (status int, respBody []byte, postErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("singbox apply: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("singbox apply: POST %s: %w", url, err)
	}
	// Close is unconditional once Do succeeded — net/http
	// requires it for connection reuse, and skipping the
	// read (early return below) does not exempt us. The
	// error is deliberately discarded (errcheck-clean):
	// a Close failure on a fully-read body only affects
	// connection reuse, never the apply result.
	defer func() { _ = resp.Body.Close() }()
	respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxAgentBodyBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("singbox apply: POST %s: read response body: %w", url, err)
	}
	return resp.StatusCode, respBody, nil
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
