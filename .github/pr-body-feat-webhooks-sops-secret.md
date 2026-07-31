# feat(webhooks): age envelope on endpoint secret

Closes the v0.7.0 known limitation: in v0.7.0 the
HMAC secret in `webhook_endpoints.secret` was
stored as plaintext TEXT. v0.7.x moves the column
under the age encryption envelope so the DB
never sees the plaintext.

## What this PR does

* New `backend/internal/webhooks/secret.go`
  defines the `SecretCipher` interface and two
  implementations:
  * `AgeSecretCipher` — uses the filippo.io/age
    X25519 + ChaCha20-Poly1305 construction. The
    recipient list seals new endpoints; the
    identity opens them. Multi-recipient support
    covers the key-rotation use case.
  * `NoopSecretCipher` — passes plaintext
    through unchanged. Used by the dev-mode
    MemoryStore and unit tests.

* Migration `0018_webhook_endpoints_secret_ciphertext.sql`
  renames `secret` to `secret_ciphertext` and
  changes the column type from TEXT to BYTEA.
  Both ALTERs are single-line per the sqlfluff
  LT02 rule. The migration is destructive (no
  production data: live deploy is v0.4.0) and
  the Down migration restores the plaintext
  column with a clear caveat that the data is
  lost.

* `PgStore` accepts a `SecretCipher` in its
  constructor, encrypts on `CreateEndpoint` /
  `UpdateEndpoint`, and decrypts on the shared
  `scanEndpointWithCipher` helper. The Service
  interface is unchanged; the Service always
  sees plaintext. The `NewPgStore` constructor
  panics on a nil cipher (a wiring bug, not a
  fallback to plaintext).

* `cmd/aegis/main.go` builds the `AgeSecretCipher`
  from the operator's recipient list and identity
  file. The same identity file is shared with the
  sops CLI (PR #119) and the `age-keygen` tool.

* `internal/config/config.go` gains two env
  flags:
  `AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS`
  (comma-separated `age1...` public keys) and
  `AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE` (path to
  the panel's identity file).

## Why age and not the sops Go library

The panel's out-of-band secrets infra (PR #119)
uses the `sops` CLI to encrypt the .env file on
the operator's machine; the panel reads the
already-decrypted env at boot. The sops Go
library is heavy and designed for structured
data; it uses age as one of its backends (the
same X25519 + ChaCha20-Poly1305 primitive we use
here) but the JSON envelope is metadata, not
security. For a single 32-byte HMAC key, age
directly is the right tool: same primitive, no
metadata overhead, and a much smaller
dependency surface.

If a future use case needs sops's full envelope
(e.g. encrypting a structured config blob with
multiple recipients and rotation metadata),
the `SecretCipher` interface is the seam where
the sops-backed implementation would slot in.

## Why a separate table was not needed (v0.7.x retry worker)

The retry worker (PR #146, shipped earlier in
this batch) introduced a separate
`webhook_pending_retries` table as the work
queue. The secret envelope does NOT need a
similar split: the secret is read at the moment
of a delivery (not a periodic retry), the
decryption is microseconds, and there is no
"scheduled" state to track. The Store does the
encrypt on write and decrypt on read; the rest
of the panel is unaffected.

## Files

* `backend/internal/webhooks/secret.go` (new, 195 lines)
* `backend/internal/webhooks/secret_test.go` (new, 219 lines)
* `backend/internal/webhooks/pg_store.go` (cipher plumbing)
* `backend/internal/webhooks/pg_store_integration_test.go` (newPgStoreWithCipher, TestPgStore_SecretAge_RoundTrip)
* `backend/migrations/0018_webhook_endpoints_secret_ciphertext.sql` (new, 41 lines)
* `backend/internal/config/config.go` (2 new flags)
* `backend/cmd/aegis/main.go` (build cipher, pass to NewPgStore)
* `backend/go.mod` / `go.sum` (add filippo.io/age v1.3.1, filippo.io/hpke v0.4.0)

Total: 9 files, +1019/-20.

## Test plan

* `go test ./internal/webhooks/...` — 82 unit
  tests pass (was 72 before this PR).
* `go test -tags=integration ./internal/webhooks/...`
  — new `TestPgStore_SecretAge_RoundTrip` covers
  the full end-to-end: real age key pair, real
  Postgres, real ciphertext at rest.
* `go test ./...` — all 21 backend packages
  still pass.
* `golangci-lint run ./...` — 0 issues.

The integration test generates a fresh age key
pair in memory, writes it to a temp file in the
standard `age-keygen` format, and asserts:
(1) the column type is BYTEA (migration 0018),
(2) the bytes do NOT contain the plaintext,
(3) the bytes are longer than the plaintext
(proving encryption overhead),
(4) a different identity cannot decrypt the
ciphertext (proving the key binding).

## Out of scope (deferred to v0.7.x follow-ups)

* Background worker for retry (PR A, shipped as #146).
* Wiring `Service.Dispatch` to every mutating
  handler (PR C in the v0.7.x roadmap).
* Shared zod schema extraction (PR D in the v0.7.x roadmap).
* Events multi-select in the create/edit dialog (PR E in the v0.7.x roadmap).

Refs ROADMAP v0.7.x "sops envelope on webhook secret".
