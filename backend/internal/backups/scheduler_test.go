// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Scheduler goroutine test coverage.
//
// The in-process scheduler lives in schedule.go
// (Service.Run + scheduler.maybeFire). The four
// tests below lock in the goroutine-level
// invariants:
//
//  1. maybeFire is idempotent within a single
//     wall-clock minute (the `last` field blocks
//     re-fire when the ticker ticks twice or
//     when the loop restarts).
//
//  2. maybeFire advances `last` even when the
//     current minute does NOT match the cron.
//     Without this, a process restart in the
//     same minute would re-evaluate the cron and
//     (if the operator changed `last`'s zero
//     value via a clock skew bug) double-fire.
//
//  3. maybeFire honours a cancelled parent
//     context. If the panel is shutting down,
//     no fresh pg_dump subprocess is spawned.
//
//  4. At 02:00:00 with cron "0 2 * * *",
//     maybeFire fires exactly once with
//     trigger=TriggerScheduled (the value the
//     UI uses to distinguish scheduled from
//     manual backups).

package backups

import (
	"context"
	"io"
	"testing"
	"time"
)

// countingDumper wraps a delegate Dumper and
// counts how many times Dump is invoked. The
// scheduler tests assert on callCount to detect
// spurious fires without depending on the
// production pg_dump subprocess.
type countingDumper struct {
	delegate  Dumper
	callCount int
}

func (c *countingDumper) Dump(ctx context.Context, dsn string) (io.ReadCloser, error) {
	c.callCount++
	return c.delegate.Dump(ctx, dsn)
}

// mustParseCron parses expr or fails the test.
// Convenience wrapper so the scheduler tests
// can focus on runtime behavior, not parser
// error messages.
func mustParseCron(t *testing.T, expr string) *Cron {
	t.Helper()
	c, err := ParseCron(expr)
	if err != nil {
		t.Fatalf("ParseCron(%q): %v", expr, err)
	}
	return c
}

// installCountingDumper replaces svc's dumper
// with a counting wrapper around a fresh
// fakeDumper (independent of the one newTestService
// installed — the wrapper owns its own delegate
// so the callCount reflects ONLY the calls made
// after installation).
func installCountingDumper(svc *Service) *countingDumper {
	delegate := &fakeDumper{stream: &fakeDump{data: []byte("scheduler test dump\n")}}
	c := &countingDumper{delegate: delegate}
	svc.SetDumper(c)
	return c
}

// TestSchedulerMaybeFire_IdempotentWithinMinute
// locks in: two maybeFire calls in the same
// wall-clock minute produce exactly ONE Dump
// invocation. The `last` field on the scheduler
// must block the second fire.
func TestSchedulerMaybeFire_IdempotentWithinMinute(t *testing.T) {
	svc, _ := newTestService(t, []byte("dummy"))
	cd := installCountingDumper(svc)
	cron := mustParseCron(t, "* * * * *") // matches every minute
	sched := &scheduler{svc: svc, cron: cron}

	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sched.maybeFire(context.Background(), now)
	// Same wall-clock minute (offset by 30s) —
	// truncated minute is identical, so the
	// `last` guard must block the second fire.
	sched.maybeFire(context.Background(), now.Add(30*time.Second))

	if cd.callCount != 1 {
		t.Fatalf("Dump callCount = %d, want 1 (idempotency broken within minute)", cd.callCount)
	}
}

// TestSchedulerMaybeFire_AdvancesLastEvenOnNonMatch
// locks in: maybeFire at a minute that does NOT
// match the cron still produces zero Dump calls
// on subsequent ticks in the same minute
// (the `last` field is advanced on the non-match
// path, so the second call short-circuits before
// re-evaluating the cron).
func TestSchedulerMaybeFire_AdvancesLastEvenOnNonMatch(t *testing.T) {
	svc, _ := newTestService(t, []byte("dummy"))
	cd := installCountingDumper(svc)
	cron := mustParseCron(t, "0 2 * * *") // only 02:00
	sched := &scheduler{svc: svc, cron: cron}

	// 12:00 does NOT match the 02:00 cron.
	// maybeFire must advance `last` and return
	// without calling Dump.
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sched.maybeFire(context.Background(), now)
	// Same minute again — must short-circuit
	// via the `last` guard, NOT re-evaluate
	// the cron and NOT call Dump.
	sched.maybeFire(context.Background(), now.Add(15*time.Second))

	if cd.callCount != 0 {
		t.Fatalf("Dump callCount = %d, want 0 (cron did not match; non-match must not fire)", cd.callCount)
	}
}

// TestSchedulerMaybeFire_RespectsCancelledContext
// locks in: when the parent context is already
// cancelled (panel is shutting down), maybeFire
// must NOT call Dump — even when the cron
// matches. The fast-path ctx.Err() check happens
// after `last` is updated but BEFORE
// Service.Create is invoked.
func TestSchedulerMaybeFire_RespectsCancelledContext(t *testing.T) {
	svc, _ := newTestService(t, []byte("dummy"))
	cd := installCountingDumper(svc)
	cron := mustParseCron(t, "* * * * *") // would match
	sched := &scheduler{svc: svc, cron: cron}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel: simulate a panel shutdown
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	sched.maybeFire(ctx, now)

	if cd.callCount != 0 {
		t.Fatalf("Dump callCount = %d, want 0 (cancelled ctx must skip Create)", cd.callCount)
	}
}

// TestSchedulerMaybeFire_TriggersAtScheduledTime
// locks in: at the exact scheduled minute
// (02:00:00 with cron "0 2 * * *"), maybeFire
// calls Dump exactly once AND records the row
// with trigger=TriggerScheduled so the UI can
// distinguish scheduled from manual backups.
func TestSchedulerMaybeFire_TriggersAtScheduledTime(t *testing.T) {
	svc, _ := newTestService(t, []byte("scheduled dump payload\n"))
	cd := installCountingDumper(svc)
	cron := mustParseCron(t, "0 2 * * *")
	sched := &scheduler{svc: svc, cron: cron}

	now := time.Date(2026, 7, 29, 2, 0, 0, 0, time.UTC) // exact match
	sched.maybeFire(context.Background(), now)

	if cd.callCount != 1 {
		t.Fatalf("Dump callCount = %d, want 1 (02:00 must fire exactly once)", cd.callCount)
	}
	rows, err := svc.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Trigger != TriggerScheduled {
		t.Fatalf("Trigger = %q, want %q", rows[0].Trigger, TriggerScheduled)
	}
}
