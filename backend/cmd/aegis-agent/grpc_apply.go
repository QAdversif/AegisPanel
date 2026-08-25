// SPDX-License-Identifier: AGPL-3.0-or-later
//
// gRPC handler for the Apply RPC. v0.8.29. The handler is a thin
// adapter over the transport-agnostic `applyCore` (apply_core.go):
// it decodes the protobuf request, hands the inner config bytes to
// the core, and encodes the result back. The same `errApply*`
// sentinels the core returns are mapped to gRPC status codes in
// `applyCoreErrorToGRPC` (this file); the parallel HTTP mapper is
// `applyCoreErrorToHTTP` in apply.go.

package main

import (
	"context"
	"errors"
	"fmt"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	aegisv1 "github.com/QAdversif/AegisPanel/internal/agentv1pb/aegis/v1"
)

// Apply is the gRPC handler for the panel's "write this and reload"
// RPC. The contract mirrors the v0.4.0-b HTTP /v1/apply 1:1 — the
// side-effect order is identical (write then reload; the request
// context cancels the subprocess on disconnect).
//
// The `applyMaxBytes` cap is enforced by the panel-side
// `applyEnvelope{Config: cfg}` decode in `singbox/apply.go` (1 MiB
// today); the gRPC path does not need a separate cap because the
// protobuf field size is bounded by the gRPC `MaxRecvMsgSize`
// default (4 MiB), which is 4x the HTTP cap. The mismatch is
// intentional: a 1 MiB-cap on the panel side and a 4 MiB-cap on
// the agent side means a too-large request is rejected before the
// agent's write+reload side effects fire.
func (s *agentGRPCServer) Apply(ctx context.Context, req *aegisv1.ApplyRequest) (*aegisv1.ApplyResponse, error) {
	result, err := applyCore(ctx, req.GetConfig())
	if err != nil {
		return nil, applyCoreErrorToGRPC(err)
	}
	return &aegisv1.ApplyResponse{
		Reloaded:         result.Reloaded,
		ReloadDurationMs: result.ReloadTookMS,
		// `singbox_version` is empty in v0.8.29; the
		// v0.8.30 agent populates this by shelling
		// out to `sing-box version` (mirrors the
		// sing-box version the HTTP /v1/status path
		// surfaces).
	}, nil
}

// applyCoreErrorToGRPC maps the `errApply*` sentinels to gRPC
// status codes. The mapping mirrors the HTTP wrapper's
// `applyCoreErrorToHTTP`:
//
//   - `errApplyEmptyConfig` / `errApplyNotJSONObject` /
//     `errApplyInvalidJSON` -> `codes.InvalidArgument`
//     (the panel sent something the agent will not accept;
//     retrying without a fix would loop forever).
//   - `errApplyConfigPathNotSet` / `errApplyReloadCmdNotSet` ->
//     `codes.FailedPrecondition` (the agent is misconfigured;
//     the panel surfaces this as a 5xx retry path because a
//     panel-side restart of the agent can resolve it).
//   - everything else (writeAtomic / runReload errors) ->
//     `codes.Internal` (the side effect failed; the BatchedApplier
//     retries with backoff).
//
// New sentinels added to `applyCore` MUST be mapped in both this
// function and `applyCoreErrorToHTTP`, or the corresponding
// transport will surface a 500 / `Unknown` to the caller.
func applyCoreErrorToGRPC(err error) error {
	switch {
	case errors.Is(err, errApplyEmptyConfig),
		errors.Is(err, errApplyNotJSONObject),
		errors.Is(err, errApplyInvalidJSON):
		return status.Error(codes.InvalidArgument, err.Error())
	case errors.Is(err, errApplyConfigPathNotSet),
		errors.Is(err, errApplyReloadCmdNotSet):
		return status.Error(codes.FailedPrecondition, err.Error())
	default:
		// writeAtomic and runReload errors arrive here.
		// The wrapped messages carry the file path /
		// subprocess stderr; surfacing them in
		// `status.Error` keeps them in the gRPC
		// `google.rpc.Status.message` for the
		// panel-side log.
		return status.Error(codes.Internal, fmt.Sprintf("apply: %s", err.Error()))
	}
}
