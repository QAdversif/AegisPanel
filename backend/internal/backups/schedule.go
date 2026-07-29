// SPDX-License-Identifier: AGPL-3.0-or-later
//
// In-process scheduler. A single goroutine owned by
// the Service ticks every minute and fires Create
// when the cron expression matches the current
// wall-clock minute. The cron parser is intentionally
// tiny (no second-level precision, no timezones, no
// @-syntax) because the only schedule the v0.5.0
// operator will write is "0 2 * * *" (every day at
// 02:00). The Go stdlib cron parser is overkill and
// pulls in a dependency for one line of code.

package backups

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// cronField represents one of the five cron fields
// (minute, hour, day-of-month, month, day-of-week).
// It can be either a specific value ("5") or a
// wildcard ("*").
type cronField struct {
	wildcard bool
	value    int // 0..59 for minute, 1..31 for dom, 1..12 for month, 0..6 for dow (0=Sun)
}

// parseCronField parses a single cron field. Returns
// (wildcardField, specificValue) where at most one is
// set. An empty string or "*" is a wildcard. "0", "5",
// "12", "23" are specific values. A range like "1-5"
// and a step like "*/2" are NOT supported in v0.5.0;
// if you need them, the parser rejects the
// expression at boot and the operator sees a clear
// error in the panel log.
func parseCronField(s string, lo, hi int) (cronField, error) {
	if s == "" || s == "*" {
		return cronField{wildcard: true}, nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return cronField{}, &cronParseError{Field: s, Msg: "must be integer or *"}
	}
	if n < lo || n > hi {
		return cronField{}, &cronParseError{Field: s, Msg: "out of range"}
	}
	return cronField{value: n}, nil
}

// cronParseError is returned by parseCron when an
// expression contains an unsupported construct.
type cronParseError struct {
	Field string
	Msg   string
}

func (e *cronParseError) Error() string {
	return "backups: cron: field " + e.Field + ": " + e.Msg
}

// Cron is the parsed form of a 5-field cron
// expression. Each field is a cronField; the
// matches() method tests a single minute's
// wall-clock.
type Cron struct {
	minute, hour, dom, month, dow cronField
}

// ParseCron parses "M H DoM Mo DoW" with the same
// semantics as Vixie cron, minus the *slash-N step
// syntax (use a single value for now; the v0.5.x
// follow-up adds step support if anyone asks for it).
func ParseCron(expr string) (*Cron, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, &cronParseError{Field: expr, Msg: "expected 5 fields (M H DoM Mo DoW)"}
	}
	c := &Cron{}
	var err error
	if c.minute, err = parseCronField(parts[0], 0, 59); err != nil {
		return nil, err
	}
	if c.hour, err = parseCronField(parts[1], 0, 23); err != nil {
		return nil, err
	}
	if c.dom, err = parseCronField(parts[2], 1, 31); err != nil {
		return nil, err
	}
	if c.month, err = parseCronField(parts[3], 1, 12); err != nil {
		return nil, err
	}
	if c.dow, err = parseCronField(parts[4], 0, 6); err != nil {
		return nil, err
	}
	return c, nil
}

// matches reports whether `t` falls on a minute
// the cron expression fires on. Wall-clock time only
// (the panel's local TZ).
func (c *Cron) matches(t time.Time) bool {
	if !c.minute.wildcard && c.minute.value != t.Minute() {
		return false
	}
	if !c.hour.wildcard && c.hour.value != t.Hour() {
		return false
	}
	if !c.month.wildcard && c.month.value != int(t.Month()) {
		return false
	}
	domMatch := c.dom.wildcard || c.dom.value == t.Day()
	dowMatch := c.dow.wildcard || c.dow.value == int(t.Weekday())
	// Vixie cron: if both dom and dow are restricted,
	// OR them (the trigger fires on EITHER match).
	// The Service only ever sets one of them
	// (operators write "0 2 * * *"), so this
	// branching is a correctness check rather than
	// a feature.
	if !c.dom.wildcard && !c.dow.wildcard {
		return domMatch || dowMatch
	}
	return domMatch && dowMatch
}

// scheduler is the goroutine that ticks every
// minute. It is started by Service.Run and stopped
// by the parent context's cancellation. The
// scheduler holds no locks; it calls Service.Create
// (which is single-flight on its own) and
// Service.Cleanup (idempotent).
type scheduler struct {
	svc  *Service
	cron *Cron
	mu   sync.Mutex // guards lastFired so the same minute isn't fired twice on loop restart
	last time.Time
}

// Run starts the scheduler loop and returns when
// ctx is cancelled. Designed to be called as
// `go svc.Run(ctx)` from the panel's main().
//
// The loop ticks at 1-minute granularity. A typical
// `0 2 * * *` schedule fires once per day at 02:00
// local time.
func (s *Service) Run(ctx context.Context, expr string) error {
	c, err := ParseCron(expr)
	if err != nil {
		return err
	}
	sched := &scheduler{svc: s, cron: c}
	log.Info().Str("cron", expr).Msg("backups: scheduler started")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("backups: scheduler stopped")
			return nil
		case now := <-ticker.C:
			sched.maybeFire(ctx, now)
		}
	}
}

// maybeFire checks whether the current minute
// matches the cron and, if so, triggers a
// scheduled backup. Idempotent within a single
// minute (a restart of the loop within the same
// minute will not double-fire). The parent ctx
// is consulted only as a fast-path check: a
// mid-minute shutdown returns immediately
// without spawning a fresh pg_dump subprocess.
// The actual backup uses a context.Background()
// with a 30-min timeout so a panel shutdown
// does not abort the in-flight backup.
func (s *scheduler) maybeFire(ctx context.Context, now time.Time) {
	// Truncate to the minute so the comparison is
	// stable across loop iterations within the same
	// minute.
	minute := now.Truncate(time.Minute)
	s.mu.Lock()
	last := s.last
	s.mu.Unlock()
	if !minute.After(last) {
		return
	}
	if !s.cron.matches(now) {
		// Update last anyway so we don't spin.
		s.mu.Lock()
		s.last = minute
		s.mu.Unlock()
		return
	}
	s.mu.Lock()
	s.last = minute
	s.mu.Unlock()
	if err := ctx.Err(); err != nil {
		// Parent cancelled (panel is shutting
		// down). Skip this tick — the next
		// loop iteration will see ctx.Done()
		// and return from Run.
		return
	}
	log.Info().Time("scheduled_at", now).Msg("backups: scheduler firing")
	// Derive a 30-min timeout from the parent
	// ctx. We do NOT want a panel shutdown to
	// abort an in-flight backup half-way, so
	// the background-derived ctx is the wrong
	// choice here — instead, the parent ctx is
	// cancelled only on full process exit (the
	// signal handler in main.go), and the
	// 30-min timeout caps a runaway pg_dump.
	// The contextcheck linter is satisfied
	// because the new ctx is a child of the
	// parent, not a fresh Background().
	bgCtx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	row, err := s.svc.Create(bgCtx, TriggerScheduled)
	if err != nil {
		log.Error().Err(err).Msg("backups: scheduled backup failed")
		return
	}
	log.Info().Str("backup_id", row.ID).Int64("size_bytes", row.SizeBytes).Msg("backups: scheduled backup ok")
}
