feat(subscription): per-sub_token rate limiting + Retry-After (PR-K)

Sixth sub-PR of v0.2.0-mvp-agent. The public
`/sub/{token}` and the rotated `/{sub_path}/sub/
{token}` endpoints are now rate-limited per
sub_token. The limiter is a small in-memory
token-bucket keyed by the sub_token (the
credential). A 429 response with a Retry-After
header replaces the previous "every request
succeeds" behaviour, which was the obvious gap
that v0.1.0 shipped with.

## Backend

* `internal/ratelimit/ratelimit.go` — new
  package, 8 tests in `ratelimit_test.go`.
  Per-key token bucket with `rps` (sustained
  rate) and `burst` (initial capacity). The
  refill-on-read algorithm keeps the math in
  floats so sub-token rates (e.g. `rps=0.5`)
  work without the integer-counter bug.
  Supports a `SetMaxKeys` cap with LRU
  eviction (off by default; v0.2.0 panels do
  not need a cap), and an `idle` parameter
  that resets a stale bucket to full after
  the configured inactivity. The single
  method `Allow(key) (bool, time.Duration)`
  returns whether the key may proceed and
  the wall-clock duration until the next
  token. `Allow()` on a nil receiver
  always returns `(true, 0)` so callers do
  not have to nil-check before wiring the
  optional limiter. The package is designed
  to be storage-agnostic — v0.3 swaps the
  in-memory map for Redis when the panel
  runs in multi-replica mode (the same
  swap point that the auth `MemoryStore` →
  `PgStore` already exercises).

* `internal/subscription/handler.go` —
  `Handler` gains a `limiter` field plus
  `WithLimiter` setter; `Router` is kept as
  the no-limiter convenience form and
  `RouterWithLimiter(svc, limiter)` is the
  explicit "rate limiting enabled" form. The
  rate-limit check runs BEFORE the user
  lookup, so a 429 does not cost a database
  round-trip. The over-budget response is
  `429 Too Many Requests` with a
  `Retry-After` header (rounded up to the
  next integer second) and a small JSON body
  (`{"error":"rate limit exceeded"}`).

* `internal/subscription/handler_test.go`
  — two new tests: `TestHandler_RateLimited_
  Returns429` (3 requests at rps=1, burst=2;
  the third is 429 with a non-empty
  Retry-After) and `TestHandler_NoRateLimitBy
  Default` (Router with no limiter; 100
  requests in a row all succeed, confirming
  the v0.1.0 behaviour is the default when
  no limiter is wired).

* `internal/router/router.go` — `Build`
  gains a `*ratelimit.Limiter` parameter. The
  default `/api/v1/sub` mount and the
  rotated `/{sub_path}/sub` mount share the
  SAME limiter instance, so a single
  sub_token has one bucket regardless of
  which URL the caller uses. Sharing the
  limiter is intentional: an attacker who
  alternates between the two URLs would
  otherwise double their effective budget.
  The router's test (one extra argument)
  is updated to pass `nil` (the v0.1.0
  behaviour).

* `internal/config/config.go` — three new
  knobs:
  * `AEGIS_SUBSCRIPTION_RATELIMIT_RPS`
    (default 1; 0 = disabled)
  * `AEGIS_SUBSCRIPTION_RATELIMIT_BURST`
    (default 5)
  * `AEGIS_SUBSCRIPTION_RATELIMIT_MAX_KEYS`
    (default 50000; 0 = no cap)
  The defaults are tuned for the
  single-user-with-multiple-devices usage
  model: a phone + laptop + tablet + desktop
  can all wake up at once after a 24h client
  poll cycle and still fit inside the burst
  budget. A real attacker spraying many
  tokens to find one valid hits the per-
  sub_token bucket the moment they pick a
  victim.

* `cmd/aegis/main.go` — new
  `newSubscriptionRateLimiter(cfg)` helper
  builds the limiter from the config knobs
  and logs a one-line `INF` summary at boot
  with the effective `rps` / `burst` /
  `max_keys`. When `RPS <= 0` the limiter is
  `nil` and the handler behaves exactly as
  in v0.1.0 (no throttling). The limiter is
  created BEFORE the HTTP server so the
  first request already has a budget
  allocated.

## Quality

* `go test ./...` — clean (all 16 packages
  pass; the new `internal/ratelimit` has 8
  tests, all green; the existing
  `internal/subscription` handler tests
  pass unchanged because the default
  `Router(svc)` form still skips the
  limiter).
* `go build ./...` — clean.
* `gofmt -l` — clean on the LF view of
  every changed file. (The Windows CRLF
  noise on the four pre-existing files —
  `config.go`, `cmd/aegis/main.go`,
  `router_test.go`, `handler_test.go` — is
  unchanged; the .gitattributes
  renormalizes on Linux so CI sees LF.
  See KNOWN_LIMITATIONS.md.)
* `go vet ./...` — clean.
* `staticcheck ./internal/ratelimit/...
  ./internal/subscription/...
  ./internal/router/... ./internal/config/...
  ./cmd/aegis/...` — clean.
* `gocritic check -enableAll` on the
  changed files — clean.

## Out of scope (later PRs)

* Per-IP second dimension on the key.
  v0.2.0's key is the sub_token only — a
  stolen token shares its bucket regardless
  of the attacker's IP, which is exactly
  the v0.2.0 goal (defend credential
  scraping). The per-IP dimension lands
  with the audit log UI in PR-M, where the
  per-user "suspicious activity" view
  motivates the second key axis.
* Redis-backed limiter. v0.2.0's in-memory
  map is fine for a single-replica panel
  with at most a few thousand unique
  tokens. The Redis swap is part of the
  v0.3 multi-replica work (per the v9
  §3.4 roadmap) and the `Limiter` interface
  is already storage-agnostic; the Redis
  implementation is a 30-line drop-in.
* Per-format and per-method throttling
  (`?target=html` getting a different budget
  than `?target=base64` so a phone-camera
  scan can co-exist with a desktop client
  poll). v0.3+ if a real conflict surfaces.
* Token-bucket metrics (`Size()` and
  per-key 429 counts). The `Limiter.Size()`
  hook is in place; a Prometheus counter
  for the 429 rate lands with the obs
  work in v0.3.

## Refs

* `ARCHITECTURE.md` v9 §10.4 (the public
  subscription endpoint)
* `KNOWN_LIMITATIONS.md` ("Real subscription
  rate-limiting" entry — closed in v0.2
  (PR-K))
* `internal/router/router.go` (the two
  subscription mounts that share the
  limiter)

Co-authored-by: Aegis Dev <dev@aegis.local>
