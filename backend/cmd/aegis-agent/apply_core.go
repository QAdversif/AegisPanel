// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Transport-agnostic apply core. Shared by the HTTP /v1/apply handler
// (apply.go) and the gRPC AegisAgent.Apply RPC (grpc_apply.go). The
// side-effect order — write then reload, with the request context
// cancelling the subprocess on disconnect — is the contract both
// transports must honour, so the validation + write + reload sequence
// lives here once.
//
// # Why a shared core
//
// The HTTP and gRPC paths would otherwise duplicate the same five
// validation steps (empty config, JSON object shape, JSON validity,
// env-var presence) and the same write+reload sequence. Two copies of
// this code is a real bug surface: a future PR that tightens one
// validation (e.g. "config must be <= 256 KiB") would miss the other
// transport and the inconsistency would only show up when an operator
// tried to migrate a node. The shared core makes that class of drift
// impossible.
//
// # v0.8.29 scope
//
// This file is introduced by the mTLS+gRPC migration. Until v0.8.32
// (HTTP cut, see docs/superpowers/plans/2026-08-25-mtls-grpc-agent.md),
// the existing HTTP `applyConfig` keeps its current behaviour; the
// gRPC handler in `grpc_apply.go` reuses this core. After v0.8.32 the
// HTTP wrapper is deleted and `applyCore` is the only call site.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"time"
)

// applyResult is the transport-agnostic outcome of a successful Apply.
// Both the HTTP and the gRPC transport encode it to their respective
// response shapes; the data is identical so a future transport (e.g.
// QUIC) can reuse it without re-deriving the fields.
type applyResult struct {
	// Accepted is true when both the write and the reload succeeded.
	// Today it is always `true` when this struct is non-empty; the
	// field exists for forward compat with a v0.4.0-c "accepted
	// but not yet reloaded" intermediate state.
	Accepted bool
	// ReceivedAt is the wall-clock time the apply arrived, in
	// RFC 3339 nano format. /v1/status surfaces it.
	ReceivedAt string
	// Bytes is the size of the rendered config that was written.
	Bytes int
	// Reloaded is true when the reload command exited 0.
	Reloaded bool
	// ReloadTookMS is the wall-clock cost of the reload command.
	ReloadTookMS int64
}

// Sentinel errors. Each is wrapped with context (e.g. the file path,
// the JSON-parse error) at the call site via `fmt.Errorf("%w: ...",
// sentinel, ...)`. The HTTP wrapper maps them to 4xx / 5xx; the gRPC
// wrapper maps them to `codes.InvalidArgument` / `codes.Internal` /
// `codes.FailedPrecondition`.
var (
	errApplyEmptyConfig      = errors.New("aegis-agent: empty config")
	errApplyNotJSONObject    = errors.New("aegis-agent: config must be a JSON object")
	errApplyInvalidJSON      = errors.New("aegis-agent: config is not valid JSON")
	errApplyConfigPathNotSet = errors.New("aegis-agent: AEGIS_AGENT_SINGBOX_CONFIG_PATH is not configured")
	errApplyReloadCmdNotSet  = errors.New("aegis-agent: AEGIS_AGENT_SINGBOX_RELOAD_CMD is not configured")
)

// applyCore validates the rendered sing-box config, writes it to disk
// atomically, and reloads sing-box. It is the shared implementation
// of the v0.4.0-b HTTP `/v1/apply` and the v0.8.29 gRPC
// `AegisAgent.Apply` RPC.
//
// `config` is the raw bytes of the inner sing-box config (NOT the
// JSON envelope — the envelope is a transport concern; the HTTP
// wrapper decodes it before calling here, and the gRPC wrapper takes
// the inner `bytes config` field directly).
//
// The function returns one of the `errApply*` sentinels on
// failure; the wrapping message carries the file path, the JSON
// parser error, etc. The caller decides the wire-level status code
// (HTTP 4xx/5xx or gRPC `codes.*`).
func applyCore(ctx context.Context, config []byte) (applyResult, error) {
	// 1. Validation. The inner `config` is the sing-box
	// JSON object. We accept only objects (not strings,
	// numbers, arrays, null) because sing-box's own
	// config-file parser expects a top-level object.
	if len(config) == 0 {
		return applyResult{}, errApplyEmptyConfig
	}
	if trimmed := bytes.TrimLeft(config, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
		return applyResult{}, errApplyNotJSONObject
	}
	// The inner object must itself parse as JSON.
	// (json.RawMessage is opaque until you unmarshal
	// it; the envelope decode does not validate the
	// inner shape.)
	var probe any
	if err := json.Unmarshal(config, &probe); err != nil {
		return applyResult{}, fmt.Errorf("%w: %s", errApplyInvalidJSON, err.Error())
	}

	// 2. Env-var sanity. Both defaults are non-empty, so
	// an empty value here means the operator removed
	// them from the systemd unit by mistake; we refuse
	// rather than silently write to a literal
	// "/etc/sing-box/config.json" they did not expect.
	if singboxConfigPath == "" {
		return applyResult{}, errApplyConfigPathNotSet
	}
	if singboxReloadCmd == "" {
		return applyResult{}, errApplyReloadCmdNotSet
	}

	// 3. Write. Atomic on POSIX (write to a temp file in
	// the same directory, fsync, then `os.Rename`).
	// `writeAtomic` is defined in apply.go; it wraps
	// `errApplyWriteFailed` (defined there) for the
	// HTTP wrapper's status code map.
	if err := writeAtomic(singboxConfigPath, config); err != nil {
		return applyResult{}, err
	}

	// 4. Reload. Use the caller's context so a client
	// disconnect cancels the subprocess; layer the
	// configured timeout on top.
	receivedAt := time.Now().UTC().Format(time.RFC3339Nano)
	took, err := runReload(ctx, singboxReloadCmd, singboxReloadTimeout)
	if err != nil {
		return applyResult{}, err
	}

	// 5. Update the in-memory last-apply timestamp so
	// /v1/status reflects the new value, and build the
	// transport-agnostic result.
	lastApplyISO = receivedAt
	log.Printf("apply ok: bytes=%d reload_took_ms=%d target=%s",
		len(config), took.Milliseconds(), singboxConfigPath)
	return applyResult{
		Accepted:     true,
		ReceivedAt:   receivedAt,
		Bytes:        len(config),
		Reloaded:     true,
		ReloadTookMS: took.Milliseconds(),
	}, nil
}
