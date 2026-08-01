// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package nodes implements the panel-side CRUD for the `nodes`
// table defined in migration 0001_initial.sql. A node is a BYO
// server (a single host running sing-box / Xray / Hysteria 2) that
// the panel manages over SSH and exposes VPN endpoints to end
// users.
//
// Phase 0 scope: a Store interface with a working in-memory
// implementation, validation in the Service layer, HTTP handlers
// for list / get / create / update / delete, and unit tests for
// the validation and memory-store paths. A pgx-backed store is
// deliberately deferred to Phase 1 — once a node actually
// registers itself and starts sending gRPC traffic, we'll know
// exactly which fields are worth indexing and which can stay
// JSON-encoded blobs.
package nodes

import (
	"time"

	"github.com/google/uuid"
)

// State is the lifecycle of a node. The string values are stored
// in the database and matched by the `nodes_state_check`
// constraint installed by migration `0006_nodes_state_v2.sql`.
// Do not change a value here without a new migration that
// re-aligns the CHECK constraint; the regression guard in
// `pg_store_state_check_test.go` will fail CI otherwise.
type State string

// State values. The set is closed (see validateState in service.go)
// — any value outside this list is rejected at the Service
// boundary. The same five values are the only ones accepted
// by the `nodes_state_check` constraint (see migration 0006
// and the regression test that pins the two together).
const (
	// StateNew: registered but not yet bootstrapped.
	StateNew State = "new"
	// StateOnline: agent reported healthy recently.
	StateOnline State = "online"
	// StateDraining: out of rotation, no new users.
	StateDraining State = "draining"
	// StateOffline: agent unreachable.
	StateOffline State = "offline"
	// StateDisabled: operator disabled.
	StateDisabled State = "disabled"
)

// Node is the panel-side view of a VPN node. The fields mirror
// the `nodes` table one-to-one; we keep them as a Go struct
// rather than `map[string]any` so that handlers can rely on the
// type at compile time.
type Node struct {
	ID     uuid.UUID `json:"id"`
	Name   string    `json:"name"`
	Region string    `json:"region"`
	State  State     `json:"state"`
	// CapacityHint is operator-supplied (e.g. "1 Gbps", "1000
	// users") and surfaced in the UI. Free-form for now; the
	// dashboard parses it on its own.
	CapacityHint string `json:"capacity_hint,omitempty"`
	// Address is the SSH endpoint the panel uses to reach the
	// node. Format is "host:port"; we do not parse it because
	// the agent protocol embeds the same string.
	Address string `json:"address"`
	// AgentBearer is the panel<->agent auth shared secret
	// (a 32-byte random hex string). It is set during
	// Provision and used on every POST /v1/apply call.
	// The field is JSON-skipped because the bearer is
	// never read by the HTTP layer — it lives only on
	// the panel and is consumed by the cores package's
	// HTTP transport. Logging it would also be a leak.
	//
	// v0.4.0-mvp-batched: added. The column defaults to
	// '' in the DB; v0.3.0 nodes that were provisioned
	// before the migration will have an empty bearer
	// until a Re-Provision re-mints it.
	AgentBearer string `json:"-"`
	// Tags is a small set of free-form labels (e.g. "vless-reality",
	// "shadowsocks-2022", "eu-west-1"). The agent reads them
	// during apply to decide which inbounds to render.
	Tags []string `json:"tags,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// IsValid reports whether the node carries the minimum data the
// Service layer requires to accept it. It is intentionally
// permissive — heavy validation lives in `Service.Create` so it
// can return rich errors per-field. IsValid is the cheap
// pre-flight check used by the store to reject obviously broken
// inserts.
func (n *Node) IsValid() bool {
	return n != nil && n.Name != "" && n.Region != "" && n.Address != ""
}
