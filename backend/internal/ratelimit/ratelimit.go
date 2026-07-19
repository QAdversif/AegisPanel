// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Package ratelimit implements a small in-memory
// per-key token-bucket rate limiter for the panel's
// HTTP surface.
//
// # Scope
//
// v0.2.0 only needs to defend the public subscription
// endpoint from credential-scraping (an attacker
// fan-out a stolen sub_token across many IPs /
// fingerprints) and the per-IP case (one IP hitting
// many distinct tokens). The limiter therefore
// exposes a single method:
//
//	Allow(key string) (allowed bool, retryAfter time.Duration)
//
// The caller decides what the key is (a sub_token, an
// IP, a sub_token|ip composite, etc.). The limiter
// itself is storage-agnostic; v0.3+ can swap the
// in-memory map for Redis when the panel runs in
// multi-replica mode (PR-L's OpenAPI codegen ships
// the storage interface).
//
// # Algorithm
//
// Token bucket per key. The bucket holds at most
// `burst` tokens; the bucket refills at `rps` tokens
// per second. Every Allow() call consumes one token.
// The call returns false when the bucket is empty,
// with a `retryAfter` hint that is the wall-clock
// duration until the next token is available.
//
// We use a per-bucket `lastRefill` timestamp + a
// refilled-on-read algorithm (no background ticker).
// The math is:
//
//	elapsed = now - lastRefill
//	tokens = min(burst, tokens + elapsed * rps)
//	if tokens >= 1:
//	  tokens -= 1
//	  allow = true
//	else:
//	  allow = false
//	  retryAfter = (1 - tokens) / rps
//	lastRefill = now
//
// `retryAfter` is the time the caller should wait
// before retrying. The HTTP layer surfaces it as the
// `Retry-After` header on a 429 response.
//
// # Memory cap
//
// A panel that processes one request per second for
// a year still only has ~31.5 M unique keys in the
// map. Realistic panel scale is "tens of thousands of
// unique tokens" — well within an in-memory map. The
// limiter does not currently evict idle keys
// (memory growth is bounded by the number of
// distinct sub_tokens + IPs the panel has ever seen);
// the v0.3 Redis swap is the right place to add a
// max-key-count + LRU eviction policy.
package ratelimit

import (
	"sync"
	"time"
)

// Limiter is a per-key token bucket rate limiter.
// The zero value is NOT ready — call New.
type Limiter struct {
	mu      sync.Mutex
	keys    map[string]*bucket
	rps     float64
	burst   float64
	idle    time.Duration
	now     func() time.Time
	maxKeys int // 0 = no cap
}

// bucket is the per-key state. `tokens` may be
// fractional (a request "in flight" leaves a half
// token) — the rate math is in floats to avoid the
// "burst of 1" never refilling bug of integer token
// counters.
type bucket struct {
	tokens     float64
	lastRefill time.Time
	lastSeen   time.Time
}

// New builds a Limiter with the given per-second rate
// and burst size.
//
//	rate   — sustained requests-per-second per key
//	         (0 = disabled; Allow() always returns true).
//	burst — maximum bucket size; also the initial
//	        capacity of a brand-new key (i.e. a fresh
//	        IP can make `burst` requests immediately).
//	idle  — after this much inactivity, a key's
//	        bucket is fully refilled. 0 = never
//	        refill-on-idle (the standard token-bucket
//	        behaviour where the bucket just keeps the
//	        last `tokens` count).
//
// The default panel preset is rate=1, burst=5,
// idle=10*time.Minute (a stale sub_token that has
// not been seen in 10 min gets a fresh burst budget
// the next time it appears). The values are
// configurable through the AEGIS_RATELIMIT_*
// environment variables; see internal/config.
func New(rate, burst float64, idle time.Duration) *Limiter {
	return &Limiter{
		keys:    make(map[string]*bucket),
		rps:     rate,
		burst:   burst,
		idle:    idle,
		now:     time.Now,
		maxKeys: 0,
	}
}

// SetClock replaces the time source. Test-only.
func (l *Limiter) SetClock(now func() time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.now = now
}

// SetMaxKeys caps the number of tracked keys. When
// the cap is hit, an Allow() call evicts the
// least-recently-seen key before inserting the new
// one. 0 = no cap (the default; v0.2.0 panels do
// not need a cap). The Redis swap in v0.3 will
// switch the policy to an LRU + TTL combination
// rather than this in-memory eviction.
func (l *Limiter) SetMaxKeys(n int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.maxKeys = n
}

// Allow reports whether the given key may proceed
// right now. When false, retryAfter is the wall-clock
// duration the caller should wait before retrying.
// retryAfter is rounded up to the nearest millisecond
// so the HTTP `Retry-After` header is always at
// least "1ms" (the smallest non-zero integer-second
// value the spec allows is "1"; we use the millis
// form for sub-second hints).
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	if l == nil || l.rps <= 0 {
		// Disabled limiter — short-circuit to allow.
		return true, 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b, ok := l.keys[key]
	if !ok {
		// New key: cap eviction then insert a full
		// bucket so the first burst is honoured.
		if l.maxKeys > 0 && len(l.keys) >= l.maxKeys {
			l.evictOldestLocked(now)
		}
		b = &bucket{
			tokens:     l.burst,
			lastRefill: now,
			lastSeen:   now,
		}
		l.keys[key] = b
	}
	// Idle refill: if the bucket has been idle for
	// longer than `idle`, treat it as fresh. This
	// bounds the "inactive key has stale tokens"
	// issue without changing the per-burst rate.
	if l.idle > 0 {
		if now.Sub(b.lastSeen) > l.idle {
			b.tokens = l.burst
			b.lastRefill = now
		}
	}
	// Refill on read: the standard token-bucket
	// formula. Using floats keeps sub-token math
	// (e.g. burst=1, rps=0.5) accurate.
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.rps
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
		b.lastRefill = now
	}
	b.lastSeen = now
	if b.tokens >= 1 {
		b.tokens -= 1
		return true, 0
	}
	// Compute the wall-clock duration until the
	// bucket has at least one token. tokens < 1
	// here so the deficit is `1 - tokens`.
	deficit := 1.0 - b.tokens
	seconds := deficit / l.rps
	return false, time.Duration(seconds * float64(time.Second))
}

// evictOldestLocked removes the bucket whose lastSeen
// is the oldest. The caller must hold l.mu.
//
// Linear scan over the map. Acceptable for v0.2.0 —
// the cap is operator-configured and the typical
// cap is "tens of thousands of keys", which a single
// goroutine can scan in microseconds. v0.3 swaps the
// in-memory map for Redis + TTL; this function goes
// away with that change.
func (l *Limiter) evictOldestLocked(now time.Time) {
	var oldestKey string
	var oldestSeen time.Time
	first := true
	for k, b := range l.keys {
		if first || b.lastSeen.Before(oldestSeen) {
			oldestKey = k
			oldestSeen = b.lastSeen
			first = false
		}
	}
	_ = now // reserved for an LRU timestamp print in a future debug hook
	if oldestKey != "" {
		delete(l.keys, oldestKey)
	}
}

// Size returns the current number of tracked keys.
// Useful for metrics + tests.
func (l *Limiter) Size() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}
