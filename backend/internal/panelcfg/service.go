// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer on top of
// Store. It owns:
//
//   - the rotation policy (a fresh 16-char hex path
//     on every Rotate, with an optional grace window
//     the operator sets per call);
//   - the path validator (length 4-64, [a-z0-9-]
//     charset — the router concatenates the path with
//     `/sub/<token>`, so the path must be a single URL
//     segment);
//   - the "active path" cache the router reads on
//     every request (the underlying Store is queried
//     directly; the cache is just a thread-safe
//     read-through that survives the boot-time
//     mount).
//
// Phase 0 ships the rotation policy. Phase 1+ will
// add per-tenant paths (a separate Store + a small
// routing-key resolver).

package panelcfg

import (
	"context"
	"fmt"
	"time"
)

// Service is the panelcfg business-logic layer.
type Service struct {
	store Store
	now   func() time.Time
}

// NewService wires a Service around the given
// store. The clock defaults to time.Now; tests can
// pass a fixed clock via SetClock.
func NewService(store Store) *Service {
	return &Service{store: store, now: time.Now}
}

// SetClock swaps the time source. Intended for tests
// only.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// GetActive returns the currently-active sub_path.
// The router calls this on every request that
// resolves to a `/<sub_path>/sub/<token>` mount.
//
// The returned path is the rotated path; the empty
// string is the default (no rotated mount). The
// router treats the empty string as "no second
// mount" and exposes only the documented
// `/api/v1/sub/<token>` path.
func (s *Service) GetActive(ctx context.Context) (*SubPathConfig, error) {
	return s.store.GetActive(ctx)
}

// Rotate generates a fresh random sub_path and
// makes it the active row. The old active row is
// deactivated. If `graceWindow` is positive, the
// old row's `ExpiresAt` is set so the router can
// serve the old path for that window; otherwise the
// old path stops working immediately (the 3X-UI
// convention).
//
// Returns the new active row. A second Rotate call
// immediately after is a no-op in terms of "active
// path" but produces a second row in the history
// (one row per Rotate, newest wins for the
// "active" predicate).
func (s *Service) Rotate(ctx context.Context, graceWindow time.Duration) (*SubPathConfig, error) {
	newPath, err := NewRandomSubPath()
	if err != nil {
		return nil, fmt.Errorf("panelcfg: generate new sub_path: %w", err)
	}
	return s.store.SetActive(ctx, newPath, graceWindow)
}

// RotateTo rotates the sub_path to the given value
// (instead of a random one). The path is validated
// before the write; an invalid path returns
// ErrInvalidPath. The admin surface uses this for
// the "set explicit path" action — operators who
// want a memorable path ("aegis-prod-2026") can
// pick one themselves.
func (s *Service) RotateTo(ctx context.Context, path string, graceWindow time.Duration) (*SubPathConfig, error) {
	if path == "" {
		return nil, ErrEmpty
	}
	if err := ValidatePath(path); err != nil {
		return nil, err
	}
	return s.store.SetActive(ctx, path, graceWindow)
}

// Reset deactivates the current rotated path and
// restores the default empty sub_path. The
// operator uses this for "go back to the documented
// /api/v1/sub/<token> path" after a rotation
// experiment. The reset is itself a row: the
// sentinel row is re-activated.
func (s *Service) Reset(ctx context.Context) (*SubPathConfig, error) {
	return s.store.Reset(ctx)
}
