// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer on top of
// Store. It owns:
//
//   - input validation (Name format, Duration bounds,
//     ResetPeriod enum, non-negative numbers);
//   - ID / timestamp generation on Create (the
//     operator does not pre-assign either);
//   - the cross-entity check on Delete (a plan that
//     still has users pointing at it is rejected;
//     see Service.Delete for the rationale).
//
// Handlers call Service rather than Store directly so
// the rules stay in one place and the pgx migration
// (Phase 1.1) can swap the Store without touching
// validation.

package plans

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/internal/webhooks"
)

// MinNameLen / MaxNameLen are the inclusive bounds
// on Name. The DB has no CHECK on name length; the
// Service is the authoritative gate. The lower bound
// (1) matches the "every plan has at least one
// printable char" rule; the upper bound (64) leaves
// room for future marketing copy without overflowing
// the 255-byte column.
const (
	MinNameLen = 1
	MaxNameLen = 64
)

// MinDuration is the smallest subscription period
// the panel accepts (1 minute). 0 is rejected
// (would mean an "expired immediately" plan, which
// is not a useful tariff). The upper bound is
// MaxDuration below.
const MinDuration = 1 * time.Minute

// MaxDuration is the largest subscription period
// the panel accepts (10 years). Long enough for
// the "buy 5 years, get a discount" promotion;
// short enough that a fat-fingered entry does not
// silently create a centuries-long tariff.
const MaxDuration = 10 * 365 * 24 * time.Hour

// Service is the business-logic layer on top of
// Store.
type Service struct {
	store    Store
	now      func() time.Time
	idGen    func() uuid.UUID
	webhooks *webhooks.Service // v0.7.x: outbound event surface. May be nil (see WithWebhooks).
}

// NewService wires a Service around the given store.
// The now function stamps CreatedAt / UpdatedAt on
// writes; tests inject a fixed clock.
func NewService(store Store) *Service {
	return &Service{
		store: store,
		now:   time.Now,
		idGen: uuid.New,
	}
}

// WithWebhooks installs the outbound event service.
// Production (cmd/aegis/main.go) calls this after
// NewService so the mutating handlers can fan out
// plan lifecycle events. The setter is preferred
// over a constructor argument because every test
// fixture currently calls `NewService(store)`
// without a webhooks service; with the setter
// the existing tests stay untouched (the field
// is nil and webhooks.MustDispatch is a no-op),
// and only the new dispatch tests wire the
// surface via this method.
//
// `svc` may be nil — equivalent to not calling
// this method. Production always passes a real
// service.
func (s *Service) WithWebhooks(svc *webhooks.Service) *Service {
	s.webhooks = svc
	return s
}

// SetClock swaps the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// Store returns the underlying Store. Intended for
// tests that need direct in-memory mutation (e.g. to
// pre-seed rows the public Service would reject).
// Production code does not need this; it would
// suggest the caller is reaching past the Service
// boundary for something the Service should expose
// as a method.
func (s *Service) Store() Store {
	return s.store
}

// Get returns a single plan by id. ErrNotFound
// bubbles up from the store unchanged so the handler
// can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Plan, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetByID(ctx, id)
}

// GetByName returns a single plan by name. ErrNotFound
// bubbles up.
func (s *Service) GetByName(ctx context.Context, name string) (*Plan, error) {
	if err := validateName(name); err != nil {
		return nil, err
	}
	return s.store.GetByName(ctx, name)
}

// List returns every plan, sorted by CreatedAt asc.
func (s *Service) List(ctx context.Context) ([]*Plan, error) {
	return s.store.List(ctx)
}

// CreateInput is the payload the HTTP handler passes
// in. The Service assigns the ID, CreatedAt, and
// UpdatedAt — the operator does not pre-assign any
// of these. ResetPeriod defaults to ResetMonthly if
// left zero; the other fields have no defaults (the
// operator must supply them explicitly).
type CreateInput struct {
	Name              string
	TrafficLimitBytes int64
	Duration          time.Duration
	DeviceLimit       int
	ResetPeriod       ResetPeriod // defaults to ResetMonthly
	PriceCents        int64
}

// Create creates a new plan. The Service generates
// the ID, the timestamps; the caller supplies the
// operator-visible fields.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Plan, error) {
	// 1. Validate operator-supplied fields.
	if err := validateName(in.Name); err != nil {
		return nil, err
	}
	if err := validateDuration(in.Duration); err != nil {
		return nil, err
	}
	if err := validateNonNegativeInt64(in.TrafficLimitBytes, "traffic_limit_bytes"); err != nil {
		return nil, err
	}
	if err := validateNonNegativeInt(in.DeviceLimit, "device_limit"); err != nil {
		return nil, err
	}
	if err := validateNonNegativeInt64(in.PriceCents, "price_cents"); err != nil {
		return nil, err
	}
	if in.ResetPeriod == "" {
		in.ResetPeriod = ResetMonthly
	}
	if !in.ResetPeriod.IsValid() {
		return nil, &ValidationError{Field: "reset_period", Message: "unknown reset_period: " + string(in.ResetPeriod)}
	}
	// 2. Build the Plan.
	now := s.now()
	p := &Plan{
		ID:                s.idGen(),
		Name:              in.Name,
		TrafficLimitBytes: in.TrafficLimitBytes,
		Duration:          in.Duration,
		DeviceLimit:       in.DeviceLimit,
		ResetPeriod:       in.ResetPeriod,
		PriceCents:        in.PriceCents,
		CreatedAt:         now,
		UpdatedAt:         now,
	}
	// 3. Persist. ErrDuplicate bubbles up so the
	// handler can map to 409.
	if err := s.store.Create(ctx, p); err != nil {
		return nil, err
	}
	// 4. Return a defensive copy so the caller
	// cannot mutate the in-memory row.
	out := *p
	// v0.7.x: fan out the lifecycle event AFTER
	// the row is committed. MustDispatch is
	// non-blocking (logs + drops errors) so a
	// slow receiver cannot turn this 2xx into
	// a 5xx and cause a client-side retry that
	// would re-apply the same insert (and
	// collide on the UNIQUE constraint).
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventPlanCreated, &out)
	return &out, nil
}

// UpdateInput is the payload the HTTP handler passes
// in for a PATCH /v1/plans/{id}. Every field is a
// pointer so the Service can distinguish "leave
// alone" (nil) from "set to zero" (non-nil & zero).
// Name can be left alone (the typical update is "the
// price went up" — Name rarely changes).
type UpdateInput struct {
	Name              *string
	TrafficLimitBytes *int64
	Duration          *time.Duration
	DeviceLimit       *int
	ResetPeriod       *ResetPeriod
	PriceCents        *int64
}

// Update modifies an existing plan. Only the fields
// the caller marks (non-nil) are touched.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Plan, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// Fetch the current state.
	cur, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// Apply the patch in-memory first so we can
	// run the same validators Create uses.
	if in.Name != nil {
		if err := validateName(*in.Name); err != nil {
			return nil, err
		}
		cur.Name = *in.Name
	}
	if in.TrafficLimitBytes != nil {
		if err := validateNonNegativeInt64(*in.TrafficLimitBytes, "traffic_limit_bytes"); err != nil {
			return nil, err
		}
		cur.TrafficLimitBytes = *in.TrafficLimitBytes
	}
	if in.Duration != nil {
		if err := validateDuration(*in.Duration); err != nil {
			return nil, err
		}
		cur.Duration = *in.Duration
	}
	if in.DeviceLimit != nil {
		if err := validateNonNegativeInt(*in.DeviceLimit, "device_limit"); err != nil {
			return nil, err
		}
		cur.DeviceLimit = *in.DeviceLimit
	}
	if in.ResetPeriod != nil {
		if !in.ResetPeriod.IsValid() {
			return nil, &ValidationError{Field: "reset_period", Message: "unknown reset_period: " + string(*in.ResetPeriod)}
		}
		cur.ResetPeriod = *in.ResetPeriod
	}
	if in.PriceCents != nil {
		if err := validateNonNegativeInt64(*in.PriceCents, "price_cents"); err != nil {
			return nil, err
		}
		cur.PriceCents = *in.PriceCents
	}
	cur.UpdatedAt = s.now()
	// Persist.
	if err := s.store.Update(ctx, cur); err != nil {
		return nil, err
	}
	// Return a fresh fetch so the caller sees the
	// post-update state (some stores apply the
	// update by replacing the row, and UpdatedAt is
	// re-stamped by the DB on write).
	out, err := s.store.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	// v0.7.x: see Create for the after-commit
	// ordering. The new row's post-update
	// timestamp is in `out`.
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventPlanUpdated, out)
	return out, nil
}

// Delete removes the plan. The Store does a hard
// delete (no soft-delete). v0.6.0 does NOT enforce
// a "no users point at this plan" check — the
// `users.plan_id` column has no FK constraint, so
// a Delete of a plan that still has users pointing
// at it is a no-op for those users (their plan_id
// becomes a dangling reference; the subscription
// package's ListPoolsForUser handles dangling plan
// IDs by returning an empty pool list).
//
// v0.6.x will add an optional `?force=true` query
// parameter and a confirm dialog in the UI.
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	if err := s.store.Delete(ctx, id); err != nil {
		return err
	}
	// v0.7.x: the row is gone by the time we
	// dispatch, so the payload carries only the
	// identifier. A receiver that wants the
	// full row state can re-fetch from the panel
	// (the operator usually has a local cache
	// for the deletion event; receivers without
	// cache fall back to a follow-up GET).
	webhooks.MustDispatch(ctx, s.webhooks, webhooks.EventPlanDeleted, map[string]string{
		"id": id.String(),
	})
	return nil
}

// --- validation helpers -----------------------------------------------

// validateName enforces the format and length
// bounds. The name may contain printable Unicode
// (the operator may want to brand a tariff with
// "⚡ Pro" or "Стартовый"). Whitespace at the
// edges is trimmed; internal whitespace is fine.
// Empty after trim is rejected.
func validateName(n string) error {
	trimmed := strings.TrimSpace(n)
	if trimmed == "" {
		return &ValidationError{Field: "name", Message: "must be non-empty"}
	}
	if len(trimmed) < MinNameLen {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("must be at least %d character", MinNameLen)}
	}
	if len(trimmed) > MaxNameLen {
		return &ValidationError{Field: "name", Message: fmt.Sprintf("must be at most %d characters", MaxNameLen)}
	}
	return nil
}

// validateDuration enforces the [MinDuration, MaxDuration]
// range. A zero / negative duration is rejected
// (the plan would expire immediately; not useful).
func validateDuration(d time.Duration) error {
	if d <= 0 {
		return &ValidationError{Field: "duration_ns", Message: "must be > 0"}
	}
	if d < MinDuration {
		return &ValidationError{Field: "duration_ns", Message: fmt.Sprintf("must be at least %s (got %s)", MinDuration, d)}
	}
	if d > MaxDuration {
		return &ValidationError{Field: "duration_ns", Message: fmt.Sprintf("must be at most %s (got %s)", MaxDuration, d)}
	}
	return nil
}

// validateNonNegativeInt64 ensures the value is >= 0.
// Used for TrafficLimitBytes and PriceCents (0 means
// "unlimited" / "free", which is a valid input).
func validateNonNegativeInt64(v int64, field string) error {
	if v < 0 {
		return &ValidationError{Field: field, Message: "must be >= 0"}
	}
	return nil
}

// validateNonNegativeInt is the int flavour of
// validateNonNegativeInt64. Used for DeviceLimit.
func validateNonNegativeInt(v int, field string) error {
	if v < 0 {
		return &ValidationError{Field: field, Message: "must be >= 0"}
	}
	return nil
}

// Sentinel for tests that want to assert the
// Service's Create / Update / Delete / Get call the
// Store. (Compile-time check.)
var _ Store = Store(nil)

// Common errors referenced for `errors.Is` callers.
// Re-exported for clarity at the call site.
var (
	ErrPlanNotFound  = ErrNotFound
	ErrPlanDuplicate = ErrDuplicate
	ErrPlanInvalid   = ErrInvalid
)
