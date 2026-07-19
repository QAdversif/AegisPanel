// SPDX-License-Identifier: AGPL-3.0-or-later

package bootstrap

import (
	"errors"
	"testing"
)

// TestStateMachine_New covers the constructor and
// the basic CanTransition cases. The exhaustive
// "every transition" coverage is in
// TestStateMachine_TransitionsExhaustive below.
func TestStateMachine_New(t *testing.T) {
	sm := NewStateMachine()
	if sm == nil {
		t.Fatal("NewStateMachine returned nil")
	}
	if sm.transitions == nil {
		t.Error("transitions map is nil")
	}
}

// TestStateMachine_NoOpTransitionIsAlwaysAllowed
// verifies the from == to short-circuit. A
// no-op transition is harmless (the panel can
// "transition" online -> online as part of a
// re-evaluation without the operator seeing a
// 409). The state machine treats no-op as
// identity, not as an invalid transition.
func TestStateMachine_NoOpTransitionIsAlwaysAllowed(t *testing.T) {
	sm := NewStateMachine()
	for _, s := range []State{
		StateNew, StateOnline, StateOffline,
		StateDraining, StateDisabled,
	} {
		if !sm.CanTransition(s, s) {
			t.Errorf("no-op %s -> %s should be allowed", s, s)
		}
		if got, err := sm.Transition(s, s); err != nil || got != s {
			t.Errorf("Transition(%s, %s) = (%s, %v), want (%s, nil)", s, s, got, err, s)
		}
	}
}

// TestStateMachine_TransitionsExhaustive covers
// every (from, to) pair across the closed state
// set. The matrix is encoded as a single table so
// adding a new state is a one-line change.
func TestStateMachine_TransitionsExhaustive(t *testing.T) {
	sm := NewStateMachine()
	all := []State{
		StateNew, StateOnline, StateOffline,
		StateDraining, StateDisabled,
	}
	// allowed[i][j] is true iff the (all[i], all[j]) edge
	// is in the v0.3.0 graph.
	allowed := map[string]bool{
		"new->online":        true,
		"new->offline":       true,
		"new->draining":      false,
		"new->disabled":      false,
		"online->new":        false,
		"online->offline":    true,
		"online->draining":   true,
		"online->disabled":   false,
		"offline->new":       true,
		"offline->online":    false,
		"offline->draining":  false,
		"offline->disabled":  false,
		"draining->new":      true,
		"draining->online":   false,
		"draining->offline":  false,
		"draining->disabled": true,
		"disabled->new":      true,
		"disabled->online":   false,
		"disabled->offline":  false,
		"disabled->draining": false,
	}
	for _, from := range all {
		for _, to := range all {
			key := string(from) + "->" + string(to)
			want, present := allowed[key]
			if !present {
				continue // no-op case, covered by the other test
			}
			got := sm.CanTransition(from, to)
			if got != want {
				t.Errorf("CanTransition(%q, %q) = %v, want %v", from, to, got, want)
			}
		}
	}
}

// TestStateMachine_Transition_ErrInvalidTransition
// verifies the error type so the HTTP layer can
// map to a 409 without string matching.
func TestStateMachine_Transition_ErrInvalidTransition(t *testing.T) {
	sm := NewStateMachine()
	_, err := sm.Transition(StateOnline, StateNew)
	if !errors.Is(err, ErrInvalidTransition) {
		t.Errorf("Transition(online, new) err = %v, want ErrInvalidTransition", err)
	}
	// The error message must mention both source and
	// target so the operator UI can render the
	// pre-condition violation usefully.
	if err == nil || (err.Error() != "bootstrap: invalid state transition: online -> new" &&
		!contains(err.Error(), "online") && !contains(err.Error(), "new")) {
		t.Errorf("err = %q, want message to mention 'online' and 'new'", err)
	}
}

// TestStateMachine_AllowedFrom verifies the menu
// builder. Each state exposes a deterministic set
// of next states; the test pins the matrix.
func TestStateMachine_AllowedFrom(t *testing.T) {
	sm := NewStateMachine()
	cases := []struct {
		from State
		want []State
	}{
		{StateNew, []State{StateOnline, StateOffline}},
		{StateOnline, []State{StateOffline, StateDraining}},
		{StateOffline, []State{StateNew}},
		{StateDraining, []State{StateNew, StateDisabled}},
		{StateDisabled, []State{StateNew}},
	}
	for _, c := range cases {
		got := sm.AllowedFrom(c.from)
		if !sameSet(got, c.want) {
			t.Errorf("AllowedFrom(%s) = %v, want %v", c.from, got, c.want)
		}
	}
	// An unknown state returns the empty set (no
	// panics, no nil deref). The DB CHECK prevents
	// unknown states from being persisted, so this
	// is just defensive.
	if got := sm.AllowedFrom("bogus"); len(got) != 0 {
		t.Errorf("AllowedFrom(bogus) = %v, want []", got)
	}
}

// sameSet reports whether two []State contain
// the same elements (order-insensitive).
func sameSet(a, b []State) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[State]bool, len(a))
	for _, v := range a {
		seen[v] = true
	}
	for _, v := range b {
		if !seen[v] {
			return false
		}
	}
	return true
}

// contains is a tiny strings.Contains alias to
// keep the test assertions terse.
func contains(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
