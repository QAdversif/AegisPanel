// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package noop provides a CoreProvider that does nothing
// useful at runtime but implements every method on the
// interface with a sensible default. It exists for two reasons:
//
//  1. Tests can spin up a Registry with noop as the only
//     provider to exercise the render/apply/parse code paths
//     without pulling in a real core implementation.
//  2. The dev panel can register noop as a fallback so the
//     UI works end-to-end before any real provider is wired
//     in — the trade-off is that noop's RenderConfig returns
//     an empty config, so the agent side won't have anything
//     to apply.
//
// The provider does NOT auto-register. The caller decides
// when (and whether) to put it in the registry.
package noop

import (
	"context"
	"fmt"

	"github.com/QAdversif/AegisPanel/internal/cores"
)

// Provider is a no-op CoreProvider. Its zero value is ready
// for use; no constructor is required.
type Provider struct {
	name    string
	version string
}

// New returns a noop provider with the given name / version.
// Pass these through from a config so the panel can
// distinguish multiple noop instances if needed.
func New(name, version string) *Provider {
	if name == "" {
		name = "noop"
	}
	if version == "" {
		version = "0.0.0-dev"
	}
	return &Provider{name: name, version: version}
}

// Name implements cores.CoreProvider.
func (p *Provider) Name() string { return p.name }

// Version implements cores.CoreProvider.
func (p *Provider) Version() string { return p.version }

// Capabilities implements cores.CoreProvider. The noop
// provider advertises only the protocols that are guaranteed
// to be a no-op (i.e. none of them) — so Capabilities returns
// an empty flag set, which the panel UI renders as "noop
// provider, cannot render any protocols".
func (p *Provider) Capabilities() cores.Capabilities {
	return cores.Capabilities{
		Name:    p.name,
		Version: p.version,
		Flags:   nil,
	}
}

// RenderConfig implements cores.CoreProvider. The noop
// provider renders a minimal-but-valid empty config so the
// agent can ingest it without complaint. Anything past
// "I exist" would be a lie, so we deliberately stop here.
func (p *Provider) RenderConfig(_ context.Context, cfg cores.CoreConfig) (string, error) {
	if cfg.Experimental == nil {
		cfg.Experimental = map[string]any{}
	}
	cfg.Experimental["noop_provider"] = p.name
	return renderJSON(cfg)
}

// ValidateConfig implements cores.CoreProvider. The noop
// provider accepts any non-empty JSON.
func (p *Provider) ValidateConfig(_ context.Context, raw []byte) error {
	if len(raw) == 0 {
		return fmt.Errorf("noop: empty config")
	}
	if !jsonValid(raw) {
		return fmt.Errorf("noop: not valid JSON")
	}
	return nil
}

// Diff implements cores.CoreProvider. We delegate to the
// standard library's diff package would be overkill here —
// noop is allowed to return an empty diff even when the
// configs differ. Tests that need a real diff should use a
// real provider implementation.
func (p *Provider) Diff(prev, next []byte) (string, error) {
	if string(prev) == string(next) {
		return "", nil
	}
	return fmt.Sprintf("--- prev\n+++ next\n%s vs %s", string(prev), string(next)), nil
}

// Apply implements cores.CoreProvider. Noop does nothing and
// reports success — this is intentional. The dev panel
// relies on it to round-trip without an actual node.
func (p *Provider) Apply(_ context.Context, _ string, _ []byte) error {
	return nil
}

// ParseStatus implements cores.CoreProvider. The noop
// provider always reports "noop" so the panel UI shows a
// distinct status from real cores.
func (p *Provider) ParseStatus(_ []byte) (cores.CoreStatus, error) {
	return cores.CoreStatus{
		Status:  "noop",
		Version: p.version,
	}, nil
}

// ParseStats implements cores.CoreProvider. The noop
// provider always returns an empty slice — there is no
// real traffic accounting.
func (p *Provider) ParseStats(_ []byte) ([]cores.UserStat, error) {
	return nil, nil
}
