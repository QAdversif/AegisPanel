// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package builder bridges the panel's data model
// (inbounds, hosts, users) to a cores.CoreConfig the
// core providers (sing-box today, xray tomorrow) can
// render.
//
// # Why this lives in `internal/cores/builder`, not in
// `internal/cores` proper
//
// `internal/cores` is the model layer: CoreConfig,
// Provider, Apply, registry. The builder depends on
// the panel's `inbounds` / `hosts` / `users` services
// for its input, which would create a cycle if it
// sat in the same package as the model. The
// `builder/` subpackage is the seam: model on the
// left, panel on the right.
//
// # Phase 1 model (v0.4.0-b / v0.5.0)
//
// The sing-box renderer in this era is single-user
// per inbound — the protocol-level `users` array
// inside the rendered config carries exactly one
// user (the operator's credential, encoded in
// `inbound.Params["uuid"]` or `["password"]`).
// Multi-user rendering lands with the inbound-
// templates work in a later phase. The builder
// therefore does NOT consult the panel's user
// table today; the FlushFn that consumes the
// CoreConfig produces a stable hash until the
// operator edits the inbound's params or adds new
// inbounds to the node. The infrastructure
// (BatchedApplier + Enqueue) is the deliverable; a
// future "Phase 2" PR re-wires the user table into
// the per-inbound `users` array.
//
// The user's HostsAllowlist / HostsBlocklist are
// likewise persisted but not consulted here — they
// drive the *subscription* renderer, not the
// node's running config.
package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/inbounds"
)

// ListInboundsByNode is the slice of the inbounds
// service the builder needs. Declared as an
// interface so the builder can be unit-tested with
// a fake and the integration tests can wire the
// real *inbounds.Service. The interface name
// intentionally describes the verb (what the
// caller needs), not the implementer.
type ListInboundsByNode interface {
	ListByNode(ctx context.Context, nodeID uuid.UUID) ([]*inbounds.Inbound, error)
}

// CoreRenderer is the slice of a cores.CoreProvider
// the FlushFn needs. Declared as an interface so
// the builder stays free of any provider-specific
// import (singbox today; xray tomorrow). The
// method set is exactly what `singbox.Provider`
// exposes for rendering+applying a CoreConfig.
type CoreRenderer interface {
	RenderConfig(ctx context.Context, cfg cores.CoreConfig) (string, error)
	Apply(ctx context.Context, nodeID string, cfg []byte) error
}

// BuildCoreConfigForNode returns the CoreConfig the
// sing-box provider (or any other v0.5.0+ provider)
// needs to render the running config for `nodeID`.
//
// The function is the seam where panel-side data
// turns into a provider-shaped DTO. The function is
// pure (no Apply) so the BatchedApplier's FlushFn
// can decide separately whether the rendered
// payload has changed before calling Apply.
//
// Disabled inbounds are skipped: they are kept in
// the DB so the operator can re-enable them, but
// the running config on the node must not include
// them. Empty result (no enabled inbounds) is a
// valid render state — the provider emits an
// outbounds-only config.
func BuildCoreConfigForNode(
	ctx context.Context,
	src ListInboundsByNode,
	nodeID uuid.UUID,
) (cores.CoreConfig, error) {
	all, err := src.ListByNode(ctx, nodeID)
	if err != nil {
		return cores.CoreConfig{}, fmt.Errorf("builder: list inbounds for node %s: %w", nodeID, err)
	}

	specs := make([]cores.InboundSpec, 0, len(all))
	params := make(map[string]any, len(all))
	for _, inb := range all {
		if inb == nil {
			continue
		}
		if !inb.Enabled {
			continue
		}
		tag := inb.Name
		if tag == "" {
			// Fall back to ID-as-tag. A blank Name is
			// rejected by Service.IsValid, so this
			// branch is for defensive completeness
			// only (a future code path that bypasses
			// the service validator).
			tag = inb.ID.String()
		}
		specs = append(specs, cores.InboundSpec{
			Tag:    tag,
			Type:   string(inb.Protocol),
			HostID: "", // Phase 1: no separate host config; the inbound's Params carry the full picture.
		})
		// The inbound's Params is the protocol-level
		// config blob (port, tls, transport, etc.).
		// It is already shaped for the sing-box
		// renderer; copy it through verbatim. A nil
		// map still renders correctly (the sing-box
		// renderer's requireString on uuid will
		// fail with a useful error, not a nil deref).
		if inb.Params != nil {
			params[tag] = inb.Params
		} else {
			params[tag] = map[string]any{}
		}
	}

	return cores.CoreConfig{
		Inbounds: specs,
		Experimental: map[string]any{
			// Match the singbox.ExperimentalInboundParamsKey
			// constant (value: "inbound_params"). Hardcoded
			// here to keep the builder free of any
			// provider-specific dependency: the singbox
			// package imports cores (for CoreConfig), not
			// the other way around. If a future provider
			// needs a different key, it can either reuse
			// this one or fork the builder into a
			// provider-specific shape; the singbox contract
			// is fixed by singbox.ExperimentalInboundParamsKey.
			"inbound_params": params,
		},
	}, nil
}

// NewFlushFn returns a cores.FlushFn the
// BatchedApplier invokes at the end of every
// coalescing window. The flush rebuilds the
// node's CoreConfig from the current inbounds,
// renders it through `renderer.RenderConfig`, and
// POSTs the result via `renderer.Apply`.
//
// Errors are logged and swallowed (returned to
// the BatchedApplier, which logs and moves on to
// the next window — a transient render/apply
// failure must not block subsequent flushes).
//
// The closure captures the inbounds source +
// renderer + node identity, so the BatchedApplier
// can be created once per node and need not be
// told which node it serves per flush.
func NewFlushFn(
	src ListInboundsByNode,
	renderer CoreRenderer,
	nodeID uuid.UUID,
	nodeName string,
) cores.FlushFn {
	return func(flushCtx context.Context, deltas []cores.Delta) error {
		start := time.Now()
		coreCfg, err := BuildCoreConfigForNode(flushCtx, src, nodeID)
		if err != nil {
			log.Error().Err(err).Str("node", nodeName).
				Msg("v0.5.0: BatchedApplier: builder failed; skipping Apply")
			return err
		}
		rendered, err := renderer.RenderConfig(flushCtx, coreCfg)
		if err != nil {
			log.Error().Err(err).Str("node", nodeName).
				Msg("v0.5.0: BatchedApplier: render failed; skipping Apply")
			return err
		}
		if err := renderer.Apply(flushCtx, nodeID.String(), []byte(rendered)); err != nil {
			log.Error().Err(err).Str("node", nodeName).
				Msg("v0.5.0: BatchedApplier: Apply failed")
			return err
		}
		log.Info().
			Str("node", nodeName).
			Int("deltas", len(deltas)).
			Int("inbounds", len(coreCfg.Inbounds)).
			Dur("took", time.Since(start)).
			Msg("v0.5.0: BatchedApplier flushed")
		return nil
	}
}
