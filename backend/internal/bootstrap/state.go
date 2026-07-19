// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package bootstrap is the BYO-Node flow: the panel
// SSHes into a freshly-registered host, installs
// the aegis-agent, and transitions the node through
// its lifecycle. v0.3.0 ships the package skeleton
// (state machine + secrets + provisioner); the v0.4.0
// "Batched Apply" milestone replaces the placeholder
// agent with the real one.
//
// # State machine
//
// Every node has a `state` column (see migration
// 0001 + 0006). The set of valid states is closed;
// any value outside is rejected at the DB CHECK
// constraint AND at the Go model level (the
// `validateState` helper in internal/nodes). The
// state machine in this file is the *transition*
// graph: which (from, to) pairs are allowed, and
// what triggers each transition. v0.3.0 wires
// these transitions to the provisioner:
//
//	new        --(SSH probe OK)--> online
//	new        --(SSH probe FAIL)--> offline
//	offline    --(operator retry)--> new
//	online     --(heartbeat miss)--> offline
//	online     --(operator action)--> draining
//	draining   --(operator action)--> disabled
//	disabled   --(operator action)--> new (decomission)
//
// "draining" + "disabled" are operator-only
// transitions (no automatic trigger). v0.3.0 only
// ships the `new`/`online`/`offline` triangle; the
// rest land in v0.5.0 (decommissioning flow) per
// the v9 §21 roadmap.
//
// # Why a state machine at all
//
// Without it, every handler that wants to set
// state has to re-derive the allowed transitions.
// A typoed transition ("online -> suspended") is
// silent — the DB CHECK lets it through if the
// new value is in the allowed set. Centralising
// the graph in this file gives us:
//   1. A single source of truth for the
//      transitions.
//   2. A unit-test surface that does not need a
//      database (the State() function is pure).
//   3. A typed error (`ErrInvalidTransition`) so
//      the HTTP layer can map to a 409 without
//      re-implementing the validation.

package bootstrap

import (
	"errors"
	"fmt"
)

// State is the per-node lifecycle. The type is
// a `string` to keep the bootstrap package free
// of the nodes dependency (which would create an
// import cycle: nodes imports bootstrap for the
// provision handler, bootstrap imports nodes for
// the State type). The string values are the
// canonical DB CHECK allow-list from migration
// 0006 — converting from `nodes.State` to
// `bootstrap.State` is a plain string copy.
type State string

// State values. The set matches the v0.3.0
// `nodes.state` CHECK constraint (migration 0006).
const (
	StateNew      State = "new"
	StateOnline   State = "online"
	StateOffline  State = "offline"
	StateDraining State = "draining"
	StateDisabled State = "disabled"
)

// ErrInvalidTransition is returned by StateMachine.Transition
// when the (from, to) pair is not in the allowed graph.
// The HTTP layer maps it to a 409 Conflict.
var ErrInvalidTransition = errors.New("bootstrap: invalid state transition")

// StateMachine is the per-node transition graph. The
// methods are pure (no I/O); the provisioner holds
// an instance and delegates every state change to
// it. The instance is stateless after construction
// so a single StateMachine can be shared across
// every node in the panel.
type StateMachine struct {
	// transitions is the adjacency map: for every
	// "from" state, the set of "to" states that
	// are allowed. The map is built once at
	// construction and never mutated.
	transitions map[State]map[State]struct{}
}

// NewStateMachine builds the v0.3.0 transition
// graph. Future PRs extend the graph; the API
// stays the same.
func NewStateMachine() *StateMachine {
	sm := &StateMachine{
		transitions: make(map[State]map[State]struct{}),
	}
	// v0.3.0 bootstrap transitions.
	sm.allow(StateNew, StateOnline)  // SSH probe + agent install OK
	sm.allow(StateNew, StateOffline) // SSH probe or install failed
	sm.allow(StateOffline, StateNew) // operator "retry provisioning"
	// v0.5+ transitions reserved here so the
	// state machine library is forward-compatible
	// with the decommissioning / heartbeat-miss
	// work. They are documented but not yet
	// driven by any provisioner code.
	sm.allow(StateOnline, StateDraining)   // operator drains
	sm.allow(StateOnline, StateOffline)    // heartbeat miss
	sm.allow(StateDraining, StateDisabled) // operator disables
	sm.allow(StateDisabled, StateNew)      // operator recommissions
	sm.allow(StateDraining, StateNew)      // operator un-drains
	return sm
}

// allow adds a single (from, to) edge to the
// graph. Internal — called once from the
// constructor.
func (sm *StateMachine) allow(from, to State) {
	if sm.transitions[from] == nil {
		sm.transitions[from] = make(map[State]struct{})
	}
	sm.transitions[from][to] = struct{}{}
}

// CanTransition reports whether the (from, to)
// edge is in the graph. The HTTP layer uses it
// to render "this action is not available in
// the current state" before the operator clicks
// the button.
func (sm *StateMachine) CanTransition(from, to State) bool {
	if from == to {
		return true // no-op transition
	}
	if next, ok := sm.transitions[from]; ok {
		_, allowed := next[to]
		return allowed
	}
	return false
}

// Transition validates the (from, to) edge and
// returns the new state. The caller is expected
// to persist the new state via nodes.Service.Update.
//
// The error is ErrInvalidTransition when the
// edge is not in the graph; the error's message
// includes both the source and target state so
// the operator UI can render "cannot go from
// 'online' to 'new'".
func (sm *StateMachine) Transition(from, to State) (State, error) {
	if !sm.CanTransition(from, to) {
		return from, fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, from, to)
	}
	return to, nil
}

// AllowedFrom returns the set of states reachable
// from `from`. The HTTP layer renders the
// operator's action menu from this set (a node
// in `new` state exposes "Provision" + "Delete";
// a node in `online` state exposes "Drain" +
// "Decommission"). Returns a freshly-allocated
// slice; callers may mutate.
func (sm *StateMachine) AllowedFrom(from State) []State {
	next, ok := sm.transitions[from]
	if !ok {
		return []State{}
	}
	out := make([]State, 0, len(next))
	for s := range next {
		out = append(out, s)
	}
	return out
}
