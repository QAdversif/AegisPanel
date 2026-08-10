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
// v0.8.x: the builder now populates
// `InboundSpec.HostID` via the `LookupHostForInbound`
// source (the host→inbound lookup from
// `internal/hosts.Service.HostsForInbound`). The
// field was previously always empty (see the
// `HostID: ""` line in the loop below); it is the
// panel-side reference to the host this inbound
// belongs to, and the canonical key the user-side
// credential filter would key off. The actual
// per-user filter at render time is a follow-up
// (it needs a per-user context in the FlushFn, which
// the BatchedApplier does not carry today); the
// v0.8.x work here is the prerequisite lookup.
// See `docs/comparison/remnawave.md:118-119` and
// `docs/ROADMAP.md` for the upstream design notes.
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
	"github.com/QAdversif/AegisPanel/internal/inboundtemplates"
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

// LookupHostForInbound is the slice of the hosts
// service the builder needs (v0.8.x host→node
// mapping). Returns the host id that owns the
// (nodeID, inboundID) endpoint pair, or nil if no
// host references the pair. The (nil, nil) case is
// the "this inbound is not in any host" state — a
// common scenario for test fixtures and for
// operator pre-provisioning. The builder
// populates `InboundSpec.HostID = ""` in that
// case (Phase 1 contract: HostID is the panel's
// reference to the host, not a required render
// input). Added in v0.8.x as the pre-req for the
// Builder-side filter per the `builder.go:32-41`
// TODO and the `docs/comparison/remnawave.md:118`
// "the canonical place" comment.
type LookupHostForInbound interface {
	HostsForInbound(ctx context.Context, nodeID, inboundID uuid.UUID) (*uuid.UUID, error)
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

// ListUsersAllowedForNode is the slice of the users
// service the v0.8.10+ per-user credential filter
// needs. Returns the set of active user IDs the
// host-allow/block filter admits for `nodeID` (per
// the `users.Service.AllowedUsersForNode` method,
// which in turn uses the v0.8.x host→node expansion
// via `internal/hosts.Service.NodesForHost`).
//
// The builder calls this source once per
// `BuildCoreConfigForNode` invocation (i.e. once per
// BatchedApplier flush, not once per inbound) and
// filters the per-inbound credential list by the
// returned user-ID set. A nil implementation skips
// the filter (default-allow; matches the v0.8.0-v0.8.9
// render contract). A nil return from the lookup
// also skips the filter (no users resolved, the
// legacy behaviour keeps every credential). A
// returned error is treated as fail-soft: log a
// warning, treat as if nil were returned. The
// v0.8.10+ behaviour — a non-empty allow-set that
// does NOT contain a credential's user drops the
// credential — only kicks in when the lookup
// succeeds with a non-nil result.
//
// This is the second half of the v0.7.x Phase 2
// multi-user TODO (see `KNOWN_LIMITATIONS.md` "Phase 2
// multi-user sing-box render — open issues"): the
// BatchedApplier fan-out was already narrowed by
// host in v0.8.x (PR #192); this method narrows the
// per-node credential list by the same filter, so a
// user without access to the node does not appear
// in the rendered `users: [...]` array. Wired in
// `cmd/aegis/main.go` via `a.Users`.
type ListUsersAllowedForNode interface {
	AllowedUsersForNode(ctx context.Context, nodeID uuid.UUID) ([]uuid.UUID, error)
}

// LookupTemplatesByID is the slice of the inbound-
// templates service the v0.8.13+ inbound-templates
// renderer integration needs. The builder calls it
// once per `BuildCoreConfigForNode` invocation with
// the deduplicated `TemplateID`s of every inbound on
// the node; the result is consulted for each
// inbound's `params[tag]` entry (the inbound's
// inline `params` is the fallback when the lookup
// doesn't return a row for the inbound's TemplateID).
//
// The lookup is a single batch query at the storage
// layer (`WHERE id = ANY($1)`); the interface
// accepts a slice to keep the call-site simple and
// the storage layer efficient. Template ids that
// don't resolve are omitted from the result; the
// builder treats a missing entry as "use the
// inbound's inline params" (fail-soft, same pattern
// as the host source and users source). A nil
// implementation keeps the v0.8.0-v0.8.12 default
// behaviour (every inbound uses its inline `params`).
// A lookup error is fail-soft: log a warning, treat
// as if nil were returned. Wired in
// `cmd/aegis/main.go` via `a.InboundTemplates`.
type LookupTemplatesByID interface {
	GetManyByID(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*inboundtemplates.InboundTemplate, error)
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
//
// v0.8.x: the `hostSrc` argument supplies the
// host→inbound lookup (see `LookupHostForInbound`).
// For every enabled inbound the builder queries the
// host service and writes the result to
// `InboundSpec.HostID`. The field is what the
// sing-box renderer needs for outbound group
// rendering (v0.8.x+; gated on user demand
// "duplicate host names in subscription" per
// `docs/comparison/remnawave.md:319`) and is the
// canonical reference the user-side credential
// filter would key off. A nil `hostSrc` skips the
// lookup (the rendered HostID stays "" — the same
// v0.8.0-v0.8.7 behaviour). A lookup error is
// logged and treated as "no host for this inbound"
// (HostID = ""), matching the fail-soft pattern the
// credentials source already follows.
//
// v0.8.10+ (per-user credential filter): the
// `usersSrc` argument supplies the per-node user
// allow-set (see `ListUsersAllowedForNode`). The
// builder calls it ONCE per `BuildCoreConfigForNode`
// invocation (one DB round-trip per node flush, not
// per inbound) and filters every per-inbound
// credential list by the returned user-ID set. A
// nil `usersSrc` skips the filter (the rendered
// `users: [...]` array keeps every credential — the
// v0.8.0-v0.8.9 default-allow behaviour, matching
// the v0.8.0-v0.8.7 `inbSrc` / `hostSrc` nil-skip
// convention). A nil return from the lookup is
// treated the same as nil `usersSrc` (legacy
// behaviour). A non-empty return is used as the
// allow-set: a credential whose `UserID` is not in
// the set is dropped. This closes the second half
// of the v0.7.x Phase 2 multi-user TODO
// (`KNOWN_LIMITATIONS.md` "Phase 2 multi-user
// sing-box render — open issues"); the first half
// (BatchedApplier fan-out narrowing by host) was
// v0.8.x (PR #192).
//
// v0.8.13+ (inbound-templates renderer integration):
// the `templateSrc` argument supplies the
// per-template params (see `LookupTemplatesByID`).
// The builder collects the deduplicated `TemplateID`s
// of every inbound on the node, asks the source for
// the matching templates in ONE round-trip, and
// uses each template's `Params` as the
// `params[tag]` value when the inbound's
// `TemplateID` resolves in the result. A nil
// `templateSrc` keeps the v0.8.0-v0.8.12 default
// (every inbound uses its inline `params`). A nil
// return from the lookup is treated the same as nil
// `templateSrc` (the per-inbound inline `params` is
// the fallback for every inbound — the v0.8.0-v0.8.12
// behaviour). A missing entry in the result (the
// template was deleted or the inbound's TemplateID
// is stale) falls back to the inbound's inline
// `params` per inbound — fail-soft, matching the
// `hostSrc` and `usersSrc` fail-soft patterns. A
// lookup error is fail-soft: log a warning, treat
// as if nil were returned. The renderer's
// per-inbound params is then consumed by the
// sing-box provider's `RenderConfig` exactly as the
// inline `params` was — no provider-side change.
func BuildCoreConfigForNode(
	ctx context.Context,
	inbSrc ListInboundsByNode,
	hostSrc LookupHostForInbound,
	credSrc ListCredentialsByInbound,
	usersSrc ListUsersAllowedForNode,
	templateSrc LookupTemplatesByID,
	nodeID uuid.UUID,
) (cores.CoreConfig, error) {
	all, err := inbSrc.ListByNode(ctx, nodeID)
	if err != nil {
		return cores.CoreConfig{}, fmt.Errorf("builder: list inbounds for node %s: %w", nodeID, err)
	}

	// v0.8.13+ inbound-templates renderer integration:
	// collect the deduplicated TemplateIDs of every
	// inbound on the node, then ask the source for
	// the matching templates in ONE round-trip. The
	// result is consulted in the per-inbound loop
	// below: when an inbound's TemplateID is set and
	// resolves in the result, the template's Params
	// replaces the inbound's inline Params as the
	// `params[tag]` value. A nil `templateSrc` keeps
	// the v0.8.0-v0.8.12 default (every inbound
	// uses its inline Params). A nil return from the
	// lookup is the same as nil `templateSrc` (every
	// inbound falls back to its inline Params). A
	// missing entry in the result (the template was
	// deleted or the inbound's TemplateID is stale)
	// also falls back to the inbound's inline Params
	// per-inbound. A lookup error is fail-soft (log
	// + treat as nil), matching the credentials
	// source, host source, and users source patterns.
	// The result is `map[uuid.UUID]map[string]any`
	// (keyed by TemplateID) — an inbound's TemplateID
	// is the lookup key in the per-inbound loop.
	var templateParams map[uuid.UUID]map[string]any
	if templateSrc != nil {
		ids := make([]uuid.UUID, 0)
		seen := make(map[uuid.UUID]struct{}, len(all))
		for _, inb := range all {
			if inb == nil || inb.TemplateID == nil {
				continue
			}
			tid := *inb.TemplateID
			if _, ok := seen[tid]; ok {
				continue
			}
			seen[tid] = struct{}{}
			ids = append(ids, tid)
		}
		if len(ids) > 0 {
			tpls, terr := templateSrc.GetManyByID(ctx, ids)
			switch {
			case terr != nil:
				log.Warn().Err(terr).
					Str("node", nodeID.String()).
					Int("templates", len(ids)).
					Msg("builder: template lookup failed; falling back to inline inbound.params for every inbound")
			case len(tpls) > 0:
				templateParams = make(map[uuid.UUID]map[string]any, len(tpls))
				for tid, tpl := range tpls {
					if tpl == nil {
						continue
					}
					templateParams[tid] = tpl.Params
				}
			}
		}
	}

	// v0.8.10+ per-user credential filter: resolve
	// the per-node user allow-set ONCE per build call
	// (one DB round-trip per node flush, not per
	// inbound). A nil `usersSrc` keeps the v0.8.0-v0.8.9
	// default-allow behaviour (every credential
	// passes). A nil return from the lookup is the
	// same as nil `usersSrc`. A non-nil, non-empty
	// set is used as the allow-set: a credential
	// whose UserID is not in the set is dropped.
	// A lookup error is fail-soft (log + treat as nil),
	// matching the credentials source and host source.
	var allowedUsers map[uuid.UUID]struct{}
	if usersSrc != nil {
		ids, uerr := usersSrc.AllowedUsersForNode(ctx, nodeID)
		switch {
		case uerr != nil:
			// Fail-soft: the per-inbound credential
			// queries already follow this pattern;
			// the BatchedApplier's 20s flush window
			// means a fatal error here would block
			// every node from rendering during a
			// transient pg blip.
			log.Warn().Err(uerr).
				Str("node", nodeID.String()).
				Msg("builder: list users for node failed; falling back to default-allow (no per-user filter)")
		case len(ids) > 0:
			allowedUsers = make(map[uuid.UUID]struct{}, len(ids))
			for _, id := range ids {
				allowedUsers[id] = struct{}{}
			}
		default:
			// Empty allow-set (the lookup succeeded
			// and returned [] or nil): every
			// credential is dropped. The downstream
			// effect is "no users on this node" —
			// the sing-box renderer emits an empty
			// `users: [...]` array, the agent
			// refuses all protocol auth on that
			// inbound. This is the fail-closed
			// semantic for a panel where every
			// active user is filtered out by their
			// host allow/block set.
			//
			// We use a non-nil empty map as the
			// sentinel for "filter is active,
			// allow-set is empty" — distinct from
			// `allowedUsers == nil` (no source / no
			// result / fail-soft = default-allow).
			allowedUsers = make(map[uuid.UUID]struct{})
		}
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
		// v0.8.x: populate InboundSpec.HostID via
		// the host→inbound lookup. A nil hostSrc
		// keeps the v0.8.0-v0.8.7 behaviour
		// (HostID = ""). A nil result from a real
		// lookup means "this inbound is not
		// referenced by any host's endpoint" — a
		// valid state for test fixtures and
		// pre-provisioning. A lookup error is
		// logged and treated as "no host"; the
		// render still proceeds with HostID = "".
		var hostID string
		if hostSrc != nil {
			id, herr := hostSrc.HostsForInbound(ctx, nodeID, inb.ID)
			if herr != nil {
				log.Warn().Err(herr).
					Str("node", nodeID.String()).
					Str("inbound", tag).
					Msg("builder: host lookup failed; falling back to empty HostID")
			} else if id != nil {
				hostID = id.String()
			}
		}
		specs = append(specs, cores.InboundSpec{
			Tag:    tag,
			Type:   string(inb.Protocol),
			HostID: hostID,
		})
		// The inbound's Params is the protocol-level
		// config blob (port, tls, transport, etc.).
		// It is already shaped for the sing-box
		// renderer; copy it through verbatim. A nil
		// map still renders correctly (the sing-box
		// renderer's requireString on uuid will
		// fail with a useful error, not a nil deref).
		//
		// v0.8.13+ inbound-templates: when the
		// inbound has a TemplateID and the lookup
		// returned a row for it, use the template's
		// Params instead of the inbound's inline
		// Params. The inbound's inline Params stays
		// in the DB for backwards compat (the
		// v0.8.0-v0.8.12 default path) but is
		// ignored by the renderer when TemplateID is
		// set + resolves. A missing entry in the
		// lookup result (template deleted, stale
		// TemplateID) falls back to the inbound's
		// inline Params — same fail-soft pattern as
		// the users source and host source.
		switch {
		case inb.TemplateID != nil && templateParams != nil:
			if tp, ok := templateParams[*inb.TemplateID]; ok {
				params[tag] = tp
				break
			}
			// Stale TemplateID — fall through to the
			// inline-params path with a warning.
			log.Warn().
				Str("node", nodeID.String()).
				Str("inbound", tag).
				Str("template_id", inb.TemplateID.String()).
				Msg("builder: template_id set on inbound but template not found; falling back to inline params")
			if inb.Params != nil {
				params[tag] = inb.Params
			} else {
				params[tag] = map[string]any{}
			}
		case inb.Params != nil:
			params[tag] = inb.Params
		default:
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
		//
		// v0.8.10+ per-user filter: when
		// `allowedUsers` is non-nil (a real
		// lookup succeeded with a non-empty
		// result), drop credentials whose `UserID`
		// is not in the set. A nil `allowedUsers`
		// (no source / no result / fail-soft) keeps
		// every credential — the v0.8.0-v0.8.9
		// default-allow contract. An empty
		// `allowedUsers` set (the lookup
		// succeeded and returned []) drops every
		// credential for this node — the
		// fail-closed "no users allowed" semantic.
		var valueSlice []credentials.Credential
		if allowedUsers == nil {
			valueSlice = make([]credentials.Credential, len(creds))
			for i, c := range creds {
				if c != nil {
					valueSlice[i] = *c
				}
			}
		} else {
			valueSlice = make([]credentials.Credential, 0, len(creds))
			for _, c := range creds {
				if c == nil {
					continue
				}
				if _, ok := allowedUsers[c.UserID]; !ok {
					continue
				}
				valueSlice = append(valueSlice, *c)
			}
			if len(valueSlice) == 0 {
				continue
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
// host→inbound lookup + credentials source +
// users source + renderer + node identity, so the
// BatchedApplier can be created once per node and
// need not be told which node it serves per flush.
// v0.8.x adds the `hostSrc` argument; nil skips the
// host lookup (HostID stays ""), matching the
// v0.8.0-v0.8.7 render contract. v0.8.10+ adds the
// `usersSrc` argument; nil skips the per-user
// credential filter (every credential passes), the
// v0.8.0-v0.8.9 default-allow contract. v0.8.13+
// adds the `templateSrc` argument; nil keeps the
// v0.8.0-v0.8.12 default (every inbound uses its
// inline `params`); a non-nil source reads the
// per-template params at flush time and uses them
// for every inbound that has a `TemplateID` resolving
// in the result.
func NewFlushFn(
	inbSrc ListInboundsByNode,
	hostSrc LookupHostForInbound,
	credSrc ListCredentialsByInbound,
	usersSrc ListUsersAllowedForNode,
	templateSrc LookupTemplatesByID,
	renderer CoreRenderer,
	nodeID uuid.UUID,
	nodeName string,
) cores.FlushFn {
	return func(flushCtx context.Context, deltas []cores.Delta) error {
		start := time.Now()
		coreCfg, err := BuildCoreConfigForNode(flushCtx, inbSrc, hostSrc, credSrc, usersSrc, templateSrc, nodeID)
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
