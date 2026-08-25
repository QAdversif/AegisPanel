// SPDX-License-Identifier: AGPL-3.0-or-later
//
// This file is intentionally empty. The
// `AgentCertIssuer` adapter was originally here but
// was moved to `internal/app/agentca_adapter.go`
// in v0.8.30 PR 1c to break the import cycle
// (`agentca` -> `bootstrap` -> `nodes` ->
// `bootstrap`). The adapter belongs in the `app`
// package because `app` is the only package that
// can import both `agentca` (the producer) and
// `bootstrap` (the consumer of the interface).

package agentca
