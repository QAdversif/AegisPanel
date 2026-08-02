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
// # Phase 2 model (v0.7.x — multi-user sing-box render)
//
// The sing-box renderer in this era is multi-user per
// inbound — the protocol-level `users` array inside
// the rendered config carries one entry per
// per-(user, inbound) credential from the
// `user_inbound_credentials` table (PR #167 data
// model, PR #168 renderer signature). The builder is
// the seam that turns the per-inbound rows into the
// `cfg.Experimental[ExperimentalInboundCredentialsKey]`
// map the sing-box renderer reads.
//
// For PR 3 of the Phase 2 plan, the builder does NOT
// filter credentials by user-side `HostsAllowlist` /
// `Blocklist` — every credential for an inbound lands
// in the rendered config for the inbound's node. The
// sing-box sing-box config's per-user routing rules
// (the subscription renderer's concern) are the
// canonical place for the user-side filter; the
// node-side rendered config carries the credential
// material the agent will accept on the wire. A
// future PR that adds a host-to-inbound mapping can
// re-introduce the filter here.
package builder

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/QAdversif/AegisPanel/internal/cores"
	"github.com/QAdversif/AegisPanel/internal/credentials"
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

// ListCredentialsByInbound is the slice of the
// credentials service the builder needs (Phase 2
// multi-user render). Returns every per-(user,
// inbound) credential for the inbound, ordered
// (the sing-box renderer preserves order in the
// rendered `users: [...]` array, so a stable
// sort by user_id is helpful for diff stability).
//
// The return type is `[]*credentials.Credential` to
// match the credentials.Store / credentials.Service
// API (pointer slice, the standard Go shape for
// "rows the caller might mutate"). The builder
// dereferences to a value slice before populating
// the `inbound_credentials` Experimental entry —
// the sing-box renderer's type-assertion
// (`v.([]credentials.Credential)`) requires a
// value slice, and the conversion cost is one
// struct copy per row.
type ListCredentialsByInbound interface {
	ListByInbound(ctx context.Context, inboundID uuid.UUID) ([]*credentials.Credential, error)
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

// experimentalInboundCredentialsKey is the key
// the builder populates in CoreConfig.Experimental
// to hand the per-(user, inbound) credential list
// to the sing-box renderer. Hardcoded as a literal
// to keep the builder free of any provider-specific
// import: the singbox package imports cores (for
// CoreConfig), not the other way around. The
// canonical constant lives in the singbox package
// (`singbox.ExperimentalInboundCredentialsKey`).
// If a future provider needs the same key, it can
// either reuse this literal or fork the builder
// into a provider-specific shape; the singbox
// contract is fixed.
//
// #nosec G101 -- the constant name contains the substring "credentials" but the value is an Experimental-map key, not a credential
const experimentalInboundCredentialsKey = "inbound_credentials"

// BuildCoreConfigForNode returns the CoreConfig the
// sing-box provider (or any other v0.7.x+ provider)
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
//
// Phase 2: the `credSrc` argument supplies the
// per-(user, inbound) credential list for every
// enabled inbound. Credentials are placed in
// `cfg.Experimental[experimentalInboundCredentialsKey]`
// as a `map[string]any` keyed by inbound tag (the
// shape the sing-box renderer expects per the PR 2
// contract — see `singbox.extractCredentialsByTag`).
// Inbounds with no credentials in the source emit
// an empty `users: [...]` array in the rendered
// config (the sing-box renderer's Phase 1 fallback
// path), which is the same behavior as v0.7.2.
func BuildCoreConfigForNode(
	ctx context.Context,
	inbSrc ListInboundsByNode,
	credSrc ListCredentialsByInbound,
	nodeID uuid.UUID,
) (cores.CoreConfig, error) {
	all, err := inbSrc.ListByNode(ctx, nodeID)
	if err != nil {
		return cores.CoreConfig{}, fmt.Errorf("builder: list inbounds for node %s: %w", nodeID, err)
	}

	specs := make([]cores.InboundSpec, 0, len(all))
	params := make(map[string]any, len(all))
	credsByTag := make(map[string]any, len(all))
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
		// Phase 2: fetch the per-(user, inbound)
		// credential list for this inbound. A
		// missing source, a query error, or an
		// empty result is non-fatal — the sing-box
		// renderer falls back to the Phase 1
		// single-user path from params on an
		// empty list. The fail-soft behavior
		// matters for a panel that has not yet
		// provisioned any credentials for an
		// inbound (a fresh install where every
		// credential is empty).
		if credSrc == nil {
			continue
		}
		creds, err := credSrc.ListByInbound(ctx, inb.ID)
		if err != nil {
			// Per-inbound query failure is logged
			// and treated as "no credentials for
			// this inbound" — the sing-box
			// renderer's Phase 1 fallback path
			// takes over. A fatal error here would
			// prevent any node from rendering
			// during a transient pg blip, which is
			// the wrong failure mode for the
			// BatchedApplier's 20s flush window.
			log.Warn().Err(err).
				Str("node", nodeID.String()).
				Str("inbound", tag).
				Msg("builder: list credentials failed; falling back to Phase 1 single-user path")
			continue
		}
		if len(creds) == 0 {
			continue
		}
		// The sing-box renderer's type-assertion
		// on the top-level map requires
		// `map[string]any` (not a typed map of
		// slice values). The per-tag value is
		// `[]credentials.Credential` (a typed
		// value slice held in `any`); the
		// sing-box renderer's
		// `extractCredentialsByTag` asserts the
		// typed slice via
		// `v.([]credentials.Credential)`. The
		// source returns `[]*Credential` (the
		// standard pointer-slice shape from the
		// credentials.Store / Service API); we
		// dereference here to the value slice the
		// renderer expects. See the `multiUserCfg`
		// doc comment in the singbox test file
		// for the full rationale.
		valueSlice := make([]credentials.Credential, len(creds))
		for i, c := range creds {
			if c != nil {
				valueSlice[i] = *c
			}
		}
		credsByTag[tag] = valueSlice
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
			// Phase 2: per-(user, inbound) credentials.
			// The sing-box renderer reads this key in
			// its `extractCredentialsByTag` helper; see
			// the singbox package docstring for the
			// multi-user render contract.
			experimentalInboundCredentialsKey: credsByTag,
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
// credentials source + renderer + node identity,
// so the BatchedApplier can be created once per
// node and need not be told which node it serves
// per flush.
func NewFlushFn(
	inbSrc ListInboundsByNode,
	credSrc ListCredentialsByInbound,
	renderer CoreRenderer,
	nodeID uuid.UUID,
	nodeName string,
) cores.FlushFn {
	return func(flushCtx context.Context, deltas []cores.Delta) error {
		start := time.Now()
		coreCfg, err := BuildCoreConfigForNode(flushCtx, inbSrc, credSrc, nodeID)
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
