// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Transport selector. Reads `AEGIS_AGENT_TRANSPORT` from the
// environment and returns the matching `Client`.
//
// # v0.8.29 contract
//
// `AEGIS_AGENT_TRANSPORT` is one of `http` (default), `grpc`,
// or `dual`. Today only `http` and `grpc` are implemented; `dual`
// lands in v0.8.30 alongside the mTLS cert bootstrap (gRPC
// first, HTTP fallback during a rollout window). Empty env
// value is treated as `http` (back-compat with the v0.4.0-b
// behaviour — the BatchedApplier keeps using the HTTP path
// until the v0.8.31 migration CLI rotates a node).
//
// # v0.8.30+ contract
//
// `AEGIS_AGENT_TRANSPORT` keeps the same name. `http` is
// deprecated (the panel logs a warning on first use). `grpc`
// is the canonical mode; `dual` adds the HTTP fallback for
// in-flight nodes that have not yet been rotated. v0.8.32
// removes the env var entirely — the only Client is the gRPC
// transport.

package agentgrpc

import (
	"fmt"
	"os"
)

// envTransport is the env-var name the selector reads. The
// double-quoted form on `os.Getenv` is intentional: an unset
// variable returns "" and the selector falls back to the
// default (http).
const envTransport = "AEGIS_AGENT_TRANSPORT"

// defaultTransport is the v0.8.29 default. v0.8.30 logs a
// deprecation warning when the default kicks in; v0.8.32
// removes the default and the gRPC transport is the only
// valid value.
const defaultTransport = "http"

// New returns a Client matching the `AEGIS_AGENT_TRANSPORT`
// env var. The `resolver` is shared by both transports
// (the HTTP one calls `GetBearer` / `Refresh` on 401, the
// gRPC one on `Unauthenticated`, and both call
// `ResolveAddr` to map a node UUID to a host:port). Returns
// a non-nil error when the env value is unknown — the
// panel refuses to boot rather than silently fall back
// to the default, because a typo in the env var during an
// mTLS rollout would otherwise point the BatchedApplier at
// a transport the operator thought was off.
func New(resolver NodeResolver) (Client, error) {
	mode := os.Getenv(envTransport)
	if mode == "" {
		mode = defaultTransport
	}
	switch mode {
	case "http":
		return newHTTPTransport(resolver)
	case "grpc":
		return newGRPCTransport(resolver)
	case "dual":
		// `dual` is reserved for v0.8.30+; today the
		// panel returns an error so an operator who
		// sets the env var does not get a silent
		// fallback to either transport.
		return nil, fmt.Errorf("agentgrpc: AEGIS_AGENT_TRANSPORT=%q lands in v0.8.30; current release supports http|grpc", mode)
	default:
		return nil, fmt.Errorf("agentgrpc: unknown AEGIS_AGENT_TRANSPORT=%q (expected http|grpc)", mode)
	}
}
