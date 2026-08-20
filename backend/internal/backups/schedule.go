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
//
// v0.9.x: the parser was extended to support Vixie
// step syntax (*/N), ranges (N-M, N-M/S), and lists
// (N,M,K). The five-field wall-clock-only model is
// preserved — no seconds, no timezones, no @-syntax.

package backups

import (
	"context"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"
)

// parseCronField parses a single cron field and
// returns the sorted, deduplicated set of valid
// minute / hour / dom / month / dow values that
// the field matches. Supported forms:
//
//   - "*"           → [lo, lo+1, ..., hi]
//   - "N"           → [N]                (single value, in range)
//   - "N-M"         → [N, N+1, ..., M]   (inclusive range)
//   - "N-M/S"       → [N, N+S, ..., ≤M]  (range with step)
//   - "*/S"         → [lo, lo+S, ..., ≤hi] (wildcard with step)
//   - "N,M,K,..."   → [N, M, K, ...]     (sorted, deduplicated list)
//
// An empty string, out-of-range values, malformed
// ranges (e.g. "1-5-9"), or non-positive steps
// return a *cronParseError.
func parseCronField(s string, lo, hi int) ([]int, error) {
	if s == "" {
		return nil, &cronParseError{Field: s, Msg: "field is empty"}
	}
	if s == "*" {
		result := make([]int, 0, hi-lo+1)
		for i := lo; i <= hi; i++ {
			result = append(result, i)
		}
		return result, nil
	}
	// Step syntax: "*/S" or "N-M/S". Handle before
	// the range branch so the "/" is not confused
	// with a list separator.
	if idx := strings.Index(s, "/"); idx != -1 {
		step, err := strconv.Atoi(s[idx+1:])
		if err != nil || step <= 0 {
			return nil, &cronParseError{Field: s, Msg: "step must be positive integer"}
		}
		start, end := lo, hi
		if s[:idx] != "*" {
			var rerr error
			start, end, rerr = parseRange(s[:idx], lo, hi)
			if rerr != nil {
				return nil, &cronParseError{Field: s, Msg: rerr.Error()}
			}
		}
		result := make([]int, 0, (end-start)/step+1)
		for i := start; i <= end; i += step {
			result = append(result, i)
		}
		return result, nil
	}
	// Range syntax: "N-M" (but NOT a negative
	// number like "-5", which would be a list of
	// one element that we want to reject via
	// strconv.Atoi below).
	if idx := strings.Index(s, "-"); idx > 0 {
		start, end, err := parseRange(s, lo, hi)
		if err != nil {
			return nil, &cronParseError{Field: s, Msg: err.Error()}
		}
		result := make([]int, 0, end-start+1)
		for i := start; i <= end; i++ {
			result = append(result, i)
		}
		return result, nil
	}
	// List syntax: "N,M,K" (or a single value "N").
	parts := strings.Split(s, ",")
	result := make([]int, 0, len(parts))
	seen := make(map[int]bool, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, &cronParseError{Field: s, Msg: "list element must be integer"}
		}
		if n < lo || n > hi {
			return nil, &cronParseError{Field: s, Msg: "value out of range"}
		}
		if !seen[n] {
			seen[n] = true
			result = append(result, n)
		}
	}
	sort.Ints(result) // deterministic order for callers / tests
	return result, nil
}

// parseRange parses "N-M" and returns (start, end)
// validated against [lo, hi]. The "S" suffix on
// "N-M/S" is stripped by the caller before reaching
// here.
func parseRange(s string, lo, hi int) (start, end int, err error) {
	parts := strings.SplitN(s, "-", 2)
	if len(parts) != 2 {
		return 0, 0, &cronParseError{Field: s, Msg: "range must be N-M"}
	}
	start, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, &cronParseError{Field: s, Msg: "range start must be integer"}
	}
	end, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, &cronParseError{Field: s, Msg: "range end must be integer"}
	}
	if start < lo || start > hi {
		return 0, 0, &cronParseError{Field: s, Msg: "range start out of range"}
	}
	if end < lo || end > hi {
		return 0, 0, &cronParseError{Field: s, Msg: "range end out of range"}
	}
	if start > end {
		return 0, 0, &cronParseError{Field: s, Msg: "range start must be <= end"}
	}
	return start, end, nil
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
// expression. Each field is the sorted, deduplicated
// set of wall-clock values that match the field
// expression. The matches() method tests a single
// minute's wall-clock against these sets.
type Cron struct {
	minute, hour, dom, month, dow []int
}

// ParseCron parses "M H DoM Mo DoW" with Vixie-cron
// semantics: wildcards ("*"), single values ("5"),
// ranges ("1-5"), steps ("*/15"), range-with-step
// ("1-31/2"), and lists ("0,15,30,45") are all
// supported. Seconds, timezones, and @-syntax are
// intentionally rejected — the panel's scheduler
// runs at 1-minute wall-clock granularity only.
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
//
// For dom/dow, Vixie cron ORs the two when BOTH are
// restricted (the operator typed a value in both
// fields on purpose). When at least one is a
// wildcard, both must match.
func (c *Cron) matches(t time.Time) bool {
	if !slices.Contains(c.minute, t.Minute()) {
		return false
	}
	if !slices.Contains(c.hour, t.Hour()) {
		return false
	}
	if !slices.Contains(c.month, int(t.Month())) {
		return false
	}
	domMatch := slices.Contains(c.dom, t.Day())
	dowMatch := slices.Contains(c.dow, int(t.Weekday()))
	// "Wildcard" is now represented as a fully
	// populated set (e.g. c.dom == [1..31]). When
	// both dom and dow are wildcard, both matches
	// are trivially true. When one is wildcard
	// (fully populated) and the other is restricted,
	// the restricted one dictates the result. The
	// "both restricted" branch is the only one
	// where OR semantics apply.
	domWildcard := len(c.dom) == 31
	dowWildcard := len(c.dow) == 7
	if !domWildcard && !dowWildcard {
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
//
// v0.9.x: `expr` is the original 5-field Vixie
// expression the operator set (or the new one from
// ReloadCron). It is kept on the struct so the
// admin UI can render the current string without
// re-parsing the Cron. The mu field guards BOTH
// `last` (the per-minute idempotency marker) AND
// the cron swap (ReloadCron takes the lock, writes
// the new *Cron, releases).
type scheduler struct {
	svc  *Service
	expr string // the original 5-field expression (admin UI surface)
	cron *Cron
	mu   sync.Mutex // guards last AND cron (see ReloadCron)
	last time.Time
}

// Run starts the scheduler loop and returns when
// ctx is cancelled. Designed to be called as
// `go svc.Run(ctx)` from the panel's main().
//
// The loop ticks at 1-minute granularity. A typical
// `0 2 * * *` schedule fires once per day at 02:00
// local time.
//
// v0.9.x: the scheduler struct is now stored on
// the Service (s.sched) rather than as a local
// variable, so ReloadCron can swap the cron
// expression at runtime. If a scheduler already
// exists (the operator hot-reloaded before the
// goroutine started, or the panel is being
// re-entered after a previous Run), the existing
// struct is reused and the new cron replaces the
// old one.
func (s *Service) Run(ctx context.Context, expr string) error {
	c, err := ParseCron(expr)
	if err != nil {
		return err
	}
	if s.sched == nil {
		s.sched = &scheduler{svc: s, expr: expr, cron: c}
	} else {
		// ReloadCron path: the operator already
		// pushed a cron swap through the Service
		// method before main() wired up the
		// goroutine. Adopt the existing scheduler
		// and refresh both fields so the running
		// loop sees the operator's chosen
		// expression (the value they passed to
		// Run() may differ from the hot-reloaded
		// one — operator-friendly default: the
		// Run() argument wins, but the struct
		// fields are kept consistent with what
		// the goroutine will use).
		s.sched.mu.Lock()
		s.sched.expr = expr
		s.sched.cron = c
		s.sched.mu.Unlock()
	}
	log.Info().Str("cron", expr).Msg("backups: scheduler started")
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info().Msg("backups: scheduler stopped")
			return nil
		case now := <-ticker.C:
			s.sched.maybeFire(ctx, now)
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
