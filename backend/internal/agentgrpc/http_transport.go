// SPDX-License-Identifier: AGPL-3.0-or-later
//
// HTTP+bearer transport. v0.8.29 default. Mirrors the
// v0.4.0-b POST /v1/apply contract 1:1 and carries the
// 401 -> BearerRefresher.Refresh -> one-retry path that
// v0.8.7 (PR #188) introduced. v0.8.30 replaces the bearer
// with mTLS on the gRPC transport; the HTTP transport here
// stays for back-compat until the v0.8.32 cut.
//
// The HTTP transport is moved into its own package from
// `backend/internal/cores/singbox/apply.go` so the singbox
// Provider can consume an `agentgrpc.Client` interface
// rather than a raw `*http.Client`. The v0.8.29 PR 3 diff
// keeps the existing singbox.Configure signature; the
// Provider's `httpClient` field is replaced with a
// `*agentgrpc.Client` constructed in `app.Build`.

package agentgrpc

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

// maxAgentBodyBytes is the upper bound on the /v1/apply
// response body the HTTP transport reads into memory.
// The agent answers with a small JSON ack; the cap exists
// so a misbehaving agent that streams a multi-MB response
// cannot exhaust the panel's memory. Mirrors the
// `maxAgentBodyBytes` constant that lived in
// `singbox/apply.go` pre-PR-3; relocated here so the
// transport owns its own safety bound.
const maxAgentBodyBytes = 64 << 10 // 64 KiB

// httpClient is the contract the HTTP transport needs from
// the underlying `*http.Client`. It is satisfied by
// `*http.Client` (and by `httptest.Server.Client()` in
// tests) without modification. The linter would otherwise
// flag the default `*http.Client` as "exotic" because
// nothing in the package uses its exported fields directly.
type httpClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// ErrAgentStaleBearer is returned when the 401 refresh path
// exhausts its retry budget. The singbox/Provider surfaces
// this as a non-retryable BatchedApplier failure (the
// operator must investigate the agent on the node; the
// panel cannot recover on its own).
var ErrAgentStaleBearer = errors.New("agentgrpc: stale agent bearer; refresh exhausted")

// httpTransport is the HTTP+bearer `Client` implementation.
// One per panel process; constructed by `newHTTPTransport`.
// The `client` field is the underlying `*http.Client`; the
// `resolver` is the `nodes.Service` wrapper that resolves
// nodes to (address, bearer) and refreshes the bearer on
// 401.
type httpTransport struct {
	client   httpClient
	resolver NodeResolver
}

// newHTTPTransport returns the default v0.4.0-b compatible
// `Client`. The returned `*httpClient` has per-request
// timeouts set by the caller (via `context.WithTimeout`);
// the underlying `*http.Client` has no global timeout.
//
// The error return is reserved for v0.8.30+ mTLS
// (the cert bootstrap may fail; today the function
// is infallible). Callers handle the error path
// uniformly with the gRPC transport's.
//
//nolint:unparam // error return reserved for v0.8.30 mTLS
func newHTTPTransport(resolver NodeResolver) (*httpTransport, error) {
	return &httpTransport{
		client:   &http.Client{}, // #nosec G107 -- per-request timeouts are set by the caller
		resolver: resolver,
	}, nil
}

// Close is a no-op for the HTTP transport. The
// `*http.Client` has no persistent state to release.
func (t *httpTransport) Close() error {
	return nil
}

// applyEnvelope is the JSON body the agent expects on
// POST /v1/apply. Mirrors the v0.4.0-b `applyEnvelope`
// in `singbox/apply.go` (pre-PR-3). The wire format is
// unchanged so the v0.8.29 transport switch is invisible
// to the agent.
type applyEnvelope struct {
	Config json.RawMessage `json:"config"`
}

// applyResponse is the JSON body the agent returns on a
// successful POST /v1/apply. Defined for documentation
// purposes; the production httpTransport does not
// currently decode the response body (the HTTP path
// treats 2xx as success and surfaces the status code
// only). v0.8.30+ may add `reloaded` / `reload_took_ms`
// to the BatchedApplier's per-node metrics.
//
//nolint:unused // kept for documentation; the production transport does not currently decode the body
type applyResponse struct {
	Reloaded     bool  `json:"reloaded"`
	ReloadTookMS int64 `json:"reload_took_ms"`
}

// Apply POSTs the rendered sing-box config to the agent's
// `/v1/apply` endpoint. The side-effect order matches
// `singbox.Apply` (pre-PR-3) and the shared `applyCore` in
// `cmd/aegis-agent/apply_core.go`: the agent writes the
// config to disk and reloads sing-box.
//
// 401 retry path: when the agent returns 401, the transport
// calls `NodeResolver.Refresh` to read the freshest
// bearer from the node, then re-issues the request once. A
// second 401 returns `ErrAgentStaleBearer`.
func (t *httpTransport) Apply(ctx context.Context, nodeID uuid.UUID, cfg []byte) error {
	addr, err := t.resolver.ResolveAddr(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	if addr == "" {
		return fmt.Errorf("agentgrpc: node %s: empty address", nodeID)
	}
	body, err := json.Marshal(applyEnvelope{Config: cfg})
	if err != nil {
		return fmt.Errorf("agentgrpc: marshal envelope: %w", err)
	}
	url := "http://" + addr + "/v1/apply"

	// First attempt: read the bearer from the panel's
	// in-memory `nodes.Service` (today: a method on
	// the resolver wrapper; v0.8.30: a field on
	// `nodes`).
	bearer, err := t.resolver.GetBearer(ctx, nodeID)
	if err != nil {
		return err
	}
	status, respBody, postErr := t.postApply(ctx, url, body, bearer)
	if postErr != nil {
		return postErr
	}
	if status != http.StatusUnauthorized {
		return t.classifyApplyResponse(status, respBody)
	}

	// Stale bearer. Refresh and retry once. The 401
	// path was added in v0.8.7 (PR #188) and
	// integrated with the v0.8.28 401->refresh
	// trigger in v0.8.28.7 (PR #292) — the
	// `RefreshAgentBearer` method logs the
	// `node.agent-bearer.refresh` audit row that
	// distinguishes "the panel did this" from
	// "the operator did this" via the `ActorID` field.
	newBearer, refreshErr := t.resolver.Refresh(ctx, nodeID)
	if refreshErr != nil {
		return fmt.Errorf("agentgrpc: agent %s returned 401 (stale bearer); refresh failed: %w", url, refreshErr)
	}
	status2, respBody2, postErr2 := t.postApply(ctx, url, body, newBearer)
	if postErr2 != nil {
		return postErr2
	}
	if status2 == http.StatusUnauthorized {
		return fmt.Errorf("%w: agent %s returned 401 on retry with refreshed bearer: %s",
			ErrAgentStaleBearer, url, truncateBody(respBody2, 512))
	}
	return t.classifyApplyResponse(status2, respBody2)
}

// postApply fires a single POST /v1/apply with the given
// body + bearer. The caller is responsible for the
// 401/retry policy. The 64 KiB body cap (maxAgentBodyBytes)
// is the v0.8.28.7 fix for the agent-FD-leak (#289/C4): a
// misbehaving agent that streams multi-MB responses can no
// longer exhaust the panel's memory or open a new file
// descriptor per apply.
func (t *httpTransport) postApply(ctx context.Context, url string, body []byte, bearer string) (status int, respBody []byte, postErr error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, nil, fmt.Errorf("agentgrpc: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("agentgrpc: POST %s: %w", url, err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()
	respBody, err = io.ReadAll(io.LimitReader(resp.Body, maxAgentBodyBytes+1))
	if err != nil {
		return resp.StatusCode, nil, fmt.Errorf("agentgrpc: POST %s: read response body: %w", url, err)
	}
	return resp.StatusCode, respBody, nil
}

// classifyApplyResponse maps the (status, body) pair from
// the agent into a transport-level error. A 2xx is
// `nil`; non-2xx is a status-carrying error so the
// BatchedApplier's retry policy can distinguish "agent is
// down" from "agent rejected the config".
func (t *httpTransport) classifyApplyResponse(status int, body []byte) error {
	if status >= 200 && status < 300 {
		return nil
	}
	return fmt.Errorf("agentgrpc: agent returned %d: %s", status, truncateBody(body, 512))
}

// refreshBearer is unused; the v0.8.29 refactor inlines
// the lookup at the two call sites (Apply, Status, Stats).
// Kept as a comment for the v0.8.30 maintainer who may
// want to add a one-shot bearer cache.
//
// The previous implementation:
//
//   func (t *httpTransport) refreshBearer(ctx, nodeID) (string, error) {
//       return t.resolver.GetBearer(ctx, nodeID)
//   }

// truncateBody caps a byte slice for inclusion in an error
// message. Mirrors the helper in `cmd/aegis-agent/apply.go`
// (v0.4.0-b); the panel-side copy avoids the cross-package
// import.
func truncateBody(b []byte, maxBytes int) string {
	if len(b) <= maxBytes {
		return string(b)
	}
	return string(b[:maxBytes]) + "...(truncated)"
}

// statusResponse is the JSON body the agent returns on
// `GET /v1/status`. Mirrors the v0.4.0-b shape; the fields
// the panel-side StatusResult consumes are listed.
type statusResponse struct {
	State          string `json:"state"`
	AgentVersion   string `json:"agent_version"`
	SingboxVersion string `json:"singbox_version"`
	UptimeSeconds  int64  `json:"uptime_seconds"`
}

// statsResponse is the JSON body the agent returns on
// `GET /v1/stats`. The `user_stats` map is keyed by user
// UUID string; v0.4.0-c will populate it from the
// sing-box clash-api.
type statsResponse struct {
	UserStats map[string]struct {
		UploadBytes   int64 `json:"upload_bytes"`
		DownloadBytes int64 `json:"download_bytes"`
	} `json:"user_stats"`
}

// Status is a thin GET /v1/status wrapper. v0.8.29 PR 3
// implements it; the BatchedApplier does not consume
// `Status` today but the operator's `nodes.Service`
// /health-probe path does.
func (t *httpTransport) Status(ctx context.Context, nodeID uuid.UUID) (StatusResult, error) {
	addr, err := t.resolver.ResolveAddr(ctx, nodeID)
	if err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	url := "http://" + addr + "/v1/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: build request: %w", err)
	}
	bearer, err := t.resolver.GetBearer(ctx, nodeID)
	if err != nil {
		return StatusResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := t.client.Do(req)
	if err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return StatusResult{}, fmt.Errorf("agentgrpc: GET %s returned %d", url, resp.StatusCode)
	}
	var body statusResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return StatusResult{}, fmt.Errorf("agentgrpc: decode %s: %w", url, err)
	}
	return StatusResult(body), nil
}

// Stats is a thin GET /v1/stats wrapper. v0.8.29 PR 3
// implements it; the panel does not consume Stats today.
func (t *httpTransport) Stats(ctx context.Context, nodeID uuid.UUID) (StatsResult, error) {
	addr, err := t.resolver.ResolveAddr(ctx, nodeID)
	if err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	url := "http://" + addr + "/v1/stats"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: build request: %w", err)
	}
	bearer, err := t.resolver.GetBearer(ctx, nodeID)
	if err != nil {
		return StatsResult{}, err
	}
	req.Header.Set("Authorization", "Bearer "+bearer)
	resp, err := t.client.Do(req)
	if err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return StatsResult{}, fmt.Errorf("agentgrpc: GET %s returned %d", url, resp.StatusCode)
	}
	var body statsResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return StatsResult{}, fmt.Errorf("agentgrpc: decode %s: %w", url, err)
	}
	out := StatsResult{UserStats: make(map[string]UserTrafficStats, len(body.UserStats))}
	for k, v := range body.UserStats {
		out.UserStats[k] = UserTrafficStats{
			UploadBytes:   v.UploadBytes,
			DownloadBytes: v.DownloadBytes,
		}
	}
	return out, nil
}

// Health is a thin GET /healthz wrapper. Mirrors the
// v0.4.0-b /healthz contract: bearer-less in insecure
// mode, 200 in normal mode.
func (t *httpTransport) Health(ctx context.Context, nodeID uuid.UUID) error {
	addr, err := t.resolver.ResolveAddr(ctx, nodeID)
	if err != nil {
		return fmt.Errorf("agentgrpc: resolve node %s: %w", nodeID, err)
	}
	// 1-second budget matches the v0.8.29 panel's
	// health-probe cadence. Operators tuning
	// /healthz latency can extend the per-call
	// context.
	callCtx, cancel := context.WithTimeout(ctx, 1*time.Second)
	defer cancel()
	url := "http://" + addr + "/healthz"
	req, err := http.NewRequestWithContext(callCtx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("agentgrpc: build request: %w", err)
	}
	resp, err := t.client.Do(req)
	if err != nil {
		return fmt.Errorf("agentgrpc: GET %s: %w", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agentgrpc: GET %s returned %d", url, resp.StatusCode)
	}
	return nil
}
