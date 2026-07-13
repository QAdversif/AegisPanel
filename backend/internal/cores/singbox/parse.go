// SPDX-License-Identifier: AGPL-3.0-or-later

package singbox

import (
	"github.com/QAdversif/AegisPanel/internal/cores"
)

// ParseStatus implements cores.CoreProvider. The Phase 1
// implementation is a stub that always reports Status="unknown"
// — the agent gRPC transport (which actually has the
// sing-box process status to forward) lands together with
// the real Apply.
//
// The return is *not* an error: the agent status endpoint
// may legitimately return an empty payload during the brief
// window between "agent connected" and "core ready", and
// the panel UI renders "unknown" rather than 500-ing in
// that case.
func (p *Provider) ParseStatus(_ []byte) (cores.CoreStatus, error) {
	return cores.CoreStatus{
		Status:  "unknown",
		Version: p.Version(),
	}, nil
}

// ParseStats implements cores.CoreProvider. The Phase 1
// implementation is a stub that always returns an empty
// slice. sing-box exposes per-user traffic through its
// gRPC StatsService; the agent will call it on a schedule
// and hand the JSON back to the panel. The full
// implementation lives with the agent gRPC transport.
func (p *Provider) ParseStats(_ []byte) ([]cores.UserStat, error) {
	return nil, nil
}
