// SPDX-License-Identifier: AGPL-3.0-or-later
//go:build integration

// Integration tests for the pgx-backed webhook
// Store. Gated on the `integration` build tag and
// on INTEGRATION_DATABASE_URL via testutil.MustNewPool.
//
// The fixture is provided by testutil: it DROPs
// and re-CREATEs the database, then runs every
// migration in `backend/migrations/`. Each test
// truncates the three webhook tables (CASCADE so
// FK dependents are wiped too) so order does not
// matter; the test process is the only writer.

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/QAdversif/AegisPanel/testutil"
)

// writeAgeIdentity serialises an X25519 identity
// to a temp file in the standard `age-keygen`
// format and returns the path. The file is
// cleaned up automatically via `t.Cleanup`. The
// resulting path is what NewAgeSecretCipher
// expects as the `identityFile` argument.
func writeAgeIdentity(t *testing.T, id *age.X25519Identity) string {
	t.Helper()
	recipient := id.Recipient()
	// The standard `age-keygen` output is the
	// recipient comment line + the identity
	// line. Both are required for the round-trip
	// to be realistic.
	content := "# public key: " + recipient.String() + "\n" + id.String() + "\n"
	dir := t.TempDir()
	path := filepath.Join(dir, "age-identity.key")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("writeAgeIdentity: WriteFile: %v", err)
	}
	return path
}

// newPgStore opens a fresh pgxpool via testutil
// and returns a *PgStore wired with a NoopSecretCipher
// (the existing tests assert the plaintext secret
// round-trips through the Store; the sops-envelope
// tests below use newPgStoreWithCipher to exercise
// the real age path). The webhook tables are
// truncated (CASCADE) so the test starts from an
// empty state; the same truncate is re-applied on
// test exit.
func newPgStore(t *testing.T) *PgStore {
	t.Helper()
	return newPgStoreWithCipher(t, NewNoopSecretCipher())
}

// newPgStoreWithCipher is the lower-level helper
// that lets a test pass a specific cipher (the
// age-envelope tests build a real AgeSecretCipher
// from a generated age key pair and assert
// ciphertext-at-rest via a direct DB query).
func newPgStoreWithCipher(t *testing.T, cipher SecretCipher) *PgStore {
	t.Helper()
	pool := testutil.MustNewPool(t)
	if _, err := pool.Exec(context.Background(),
		`TRUNCATE TABLE webhook_dlq, webhook_deliveries, webhook_pending_retries, webhook_endpoints CASCADE`); err != nil {
		t.Fatalf("TRUNCATE webhook_*: %v", err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(),
			`TRUNCATE TABLE webhook_dlq, webhook_deliveries, webhook_pending_retries, webhook_endpoints CASCADE`)
	})
	return NewPgStore(pool, cipher)
}

// TestPgStore_EndpointRoundTrip covers the basic
// endpoint CRUD against the pgx-backed store.
func TestPgStore_EndpointRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://pg.example.com/h",
		Secret:  "pg-webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Events:  []EventType{EventUserCreated, EventPlanCreated},
		Enabled: true,
	}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	got, err := s.GetEndpoint(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.URL != e.URL {
		t.Errorf("URL = %q, want %q", got.URL, e.URL)
	}
	if len(got.Events) != 2 {
		t.Errorf("Events len = %d, want 2", len(got.Events))
	}
	// List.
	list, err := s.ListEndpoints(ctx)
	if err != nil {
		t.Fatalf("ListEndpoints: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListEndpoints: len = %d, want 1", len(list))
	}
	// Delete.
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	if _, err := s.GetEndpoint(ctx, e.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEndpoint after delete: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_DeliveryRoundTrip covers the
// delivery-history table: insert, read back,
// list per endpoint.
func TestPgStore_DeliveryRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://pg2.example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	sc := 200
	dm := 42
	d := &Delivery{
		ID:           uuid.New(),
		EndpointID:   e.ID,
		EventType:    EventUserCreated,
		Payload:      []byte(`{"x":1}`),
		RequestURL:   e.URL,
		RequestBody:  []byte(`{"x":1}`),
		Signature:    "sha256=deadbeef",
		StatusCode:   &sc,
		ResponseBody: "ok",
		Attempt:      1,
		DurationMs:   &dm,
	}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	got, err := s.ListDeliveriesByEndpoint(ctx, e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 delivery, got %d", len(got))
	}
	jsonEqual(t, got[0].Payload, map[string]any{"x": float64(1)})
	if got[0].StatusCode == nil || *got[0].StatusCode != 200 {
		t.Errorf("StatusCode round-trip mismatch: got %v", got[0].StatusCode)
	}
}

// jsonEqual compares two JSON byte strings by
// parsing both into a generic structure. Postgres
// JSONB normalises whitespace on read-back
// (e.g. `{"x":1}` -> `{"x": 1}`), so byte-level
// comparison is unreliable; the parsed values
// must match instead.
func jsonEqual(t *testing.T, raw []byte, want any) {
	t.Helper()
	var got any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%q)", err, raw)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Payload mismatch: got %v, want %v (raw=%q)", got, want, raw)
	}
}

// TestPgStore_DLQRoundTrip covers the DLQ table:
// enqueue, read back, list.
func TestPgStore_DLQRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	entry := &DLQEntry{
		ID:            uuid.New(),
		EndpointID:    uuid.New(),
		EndpointURL:   "https://dlq.example.com/h",
		EventType:     EventUserCreated,
		Payload:       []byte(`{"a":1}`),
		LastError:     "http 500",
		Attempts:      6,
		LastAttemptAt: time.Now().UTC(),
	}
	if err := s.EnqueueDLQ(ctx, entry); err != nil {
		t.Fatalf("EnqueueDLQ: %v", err)
	}
	got, err := s.GetDLQ(ctx, entry.ID)
	if err != nil {
		t.Fatalf("GetDLQ: %v", err)
	}
	if got.LastError != "http 500" {
		t.Errorf("LastError = %q, want %q", got.LastError, "http 500")
	}
	jsonEqual(t, got.Payload, map[string]any{"a": float64(1)})
	// List.
	list, err := s.ListDLQ(ctx, 0)
	if err != nil {
		t.Fatalf("ListDLQ: %v", err)
	}
	if len(list) != 1 {
		t.Errorf("ListDLQ: len = %d, want 1", len(list))
	}
	// Delete.
	if err := s.DeleteDLQ(ctx, entry.ID); err != nil {
		t.Fatalf("DeleteDLQ: %v", err)
	}
	if _, err := s.GetDLQ(ctx, entry.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDLQ after delete: err = %v, want ErrNotFound", err)
	}
}

// TestPgStore_EndpointDuplicateURL covers the
// (url) UNIQUE constraint. Two endpoints with the
// same URL must fail with ErrDuplicate (Postgres
// SQLSTATE 23505).
func TestPgStore_EndpointDuplicateURL(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	url := "https://dup.example.com/h"
	e1 := &Endpoint{ID: uuid.New(), URL: url, Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	e2 := &Endpoint{ID: uuid.New(), URL: url, Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e1); err != nil {
		t.Fatalf("CreateEndpoint e1: %v", err)
	}
	err := s.CreateEndpoint(ctx, e2)
	if !errors.Is(err, ErrDuplicate) {
		t.Fatalf("expected ErrDuplicate, got %v", err)
	}
}

// TestPgStore_DeleteEndpointCascades covers the
// (endpoint_id) FK on webhook_deliveries with ON
// DELETE CASCADE. Deleting an endpoint must
// remove its delivery history.
func TestPgStore_DeleteEndpointCascades(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{ID: uuid.New(), URL: "https://cascade.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1, Payload: []byte(`{}`), RequestBody: []byte(`{}`)}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	got, err := s.ListDeliveriesByEndpoint(ctx, e.ID, 0)
	if err != nil {
		t.Fatalf("ListDeliveriesByEndpoint: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 deliveries after cascade, got %d", len(got))
	}
}

// --- pending retries (v0.7.x) ----------------------------------------

// seedEndpointAndDelivery creates a unique endpoint
// and a delivery row that points at it. The
// returned `*Delivery.ID` is safe to use as the
// `delivery_id` argument of EnqueueRetry — the
// FK on `webhook_pending_retries.delivery_id` will
// accept it. The `urlSuffix` is appended to a
// fixed prefix so the URL is unique across calls
// (the table has a UNIQUE constraint on `url`).
func seedEndpointAndDelivery(t *testing.T, s *PgStore, urlSuffix string) uuid.UUID {
	t.Helper()
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://seed-" + urlSuffix + ".example.com/h",
		Secret:  "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa",
		Enabled: true,
	}
	if err := s.CreateEndpoint(context.Background(), e); err != nil {
		t.Fatalf("seed: CreateEndpoint: %v", err)
	}
	d := &Delivery{
		ID:          uuid.New(),
		EndpointID:  e.ID,
		EventType:   EventUserCreated,
		Payload:     []byte(`{"seed":true}`),
		RequestBody: []byte(`{"seed":true}`),
		RequestURL:  e.URL,
		Signature:   "sha256=00",
		Timestamp:   time.Now().UTC(),
		Attempt:     1,
	}
	if err := s.CreateDelivery(context.Background(), d); err != nil {
		t.Fatalf("seed: CreateDelivery: %v", err)
	}
	return d.ID
}

// TestPgStore_PendingRetries_RoundTrip covers the
// enqueue / dequeue / list-due flow against the
// pgx-backed store. The migration 0017 must have
// created the `webhook_pending_retries` table;
// testutil runs every migration on MustNewPool.
func TestPgStore_PendingRetries_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	now := time.Now().UTC()
	// Real delivery rows so the FK on
	// `webhook_pending_retries.delivery_id` is
	// satisfied.
	d1 := seedEndpointAndDelivery(t, s, "roundtrip-1")
	d2 := seedEndpointAndDelivery(t, s, "roundtrip-2")
	d3 := seedEndpointAndDelivery(t, s, "roundtrip-3")
	if err := s.EnqueueRetry(ctx, d1, now.Add(1*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d1: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d2, now.Add(5*time.Second)); err != nil {
		t.Fatalf("EnqueueRetry d2: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d3, now.Add(1*time.Hour)); err != nil {
		t.Fatalf("EnqueueRetry d3: %v", err)
	}
	// At `now+10s`, d1 and d2 are due; d3 is not.
	due, err := s.ListDueRetries(ctx, now.Add(10*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("expected 2 due rows, got %d", len(due))
	}
	// Ordered by next_attempt_at asc: d1 then d2.
	if due[0] != d1 || due[1] != d2 {
		t.Errorf("ListDueRetries = [%s, %s], want [%s, %s]", due[0], due[1], d1, d2)
	}
	// Dequeue d1 and verify it falls out of the
	// list.
	if err := s.DequeueRetry(ctx, d1); err != nil {
		t.Fatalf("DequeueRetry d1: %v", err)
	}
	due, err = s.ListDueRetries(ctx, now.Add(10*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries #2: %v", err)
	}
	if len(due) != 1 || due[0] != d2 {
		t.Errorf("after Dequeue: due = %v, want [%s]", due, d2)
	}
	// Idempotent: re-dequeueing d1 is a no-op.
	if err := s.DequeueRetry(ctx, d1); err != nil {
		t.Errorf("DequeueRetry idempotent: %v", err)
	}
}

// TestPgStore_EnqueueRetry_UpdatesOnConflict
// verifies the ON CONFLICT DO UPDATE semantic:
// re-enqueueing the same delivery_id updates the
// scheduled time in place.
func TestPgStore_EnqueueRetry_UpdatesOnConflict(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	d := seedEndpointAndDelivery(t, s, "upsert")
	t1 := time.Now().UTC()
	t2 := t1.Add(10 * time.Second)
	if err := s.EnqueueRetry(ctx, d, t1); err != nil {
		t.Fatalf("EnqueueRetry #1: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d, t2); err != nil {
		t.Fatalf("EnqueueRetry #2: %v", err)
	}
	// At t1+5s, the original schedule (t1) is
	// already past — but the upsert replaced it
	// with t2, which is 10s in the future. So
	// the row is NOT due at t1+5s.
	got, err := s.ListDueRetries(ctx, t1.Add(5*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 due at t1+5s, got %d", len(got))
	}
	// At t2+1s, the row IS due.
	got, err = s.ListDueRetries(ctx, t2.Add(1*time.Second), 0)
	if err != nil {
		t.Fatalf("ListDueRetries #2: %v", err)
	}
	if len(got) != 1 || got[0] != d {
		t.Errorf("ListDueRetries at t2+1s = %v, want [%s]", got, d)
	}
}

// TestPgStore_EndpointDeleteCascades_PendingRetries
// verifies the ON DELETE CASCADE on the
// (delivery_id) FK: removing the underlying
// delivery row removes the pending retry too.
// The pg path is the production one; the
// in-memory store has no cascade (it leaves
// dangling rows that the worker logs and
// skips via Service.RetryDelivery returning
// ErrNotFound).
func TestPgStore_EndpointDeleteCascades_PendingRetries(t *testing.T) {
	ctx := context.Background()
	s := newPgStore(t)
	e := &Endpoint{ID: uuid.New(), URL: "https://cascade2.example.com/h", Secret: "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa", Enabled: true}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}
	d := &Delivery{ID: uuid.New(), EndpointID: e.ID, EventType: EventUserCreated, Attempt: 1, Payload: []byte(`{}`), RequestBody: []byte(`{}`)}
	if err := s.CreateDelivery(ctx, d); err != nil {
		t.Fatalf("CreateDelivery: %v", err)
	}
	if err := s.EnqueueRetry(ctx, d.ID, time.Now().UTC()); err != nil {
		t.Fatalf("EnqueueRetry: %v", err)
	}
	if err := s.DeleteEndpoint(ctx, e.ID); err != nil {
		t.Fatalf("DeleteEndpoint: %v", err)
	}
	// The pending-retry row cascade-deleted
	// with the underlying delivery.
	got, err := s.ListDueRetries(ctx, time.Now().UTC(), 0)
	if err != nil {
		t.Fatalf("ListDueRetries: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 pending retries after cascade, got %d", len(got))
	}
}

// --- age secret envelope (v0.7.x) -------------------------------------

// TestPgStore_SecretAge_RoundTrip is the end-to-end
// proof that the secret envelope works against a
// real age key pair:
//
//  1. Generate an age X25519 identity + recipient
//     in-memory (no on-disk key file needed; the
//     helper writes a temp file from the recipient
//     string and a separate temp file from the
//     identity, then constructs an AgeSecretCipher
//     with both).
//
//  2. Insert an endpoint with a known plaintext
//     secret. The Store encrypts before INSERT.
//
//  3. Read the endpoint back. The Store decrypts
//     and returns plaintext. The plaintext must
//     match the original byte-for-byte.
//
//  4. Bypass the Store and query the column
//     directly. The bytes must NOT contain the
//     plaintext substring (proving ciphertext at
//     rest) and the column type must be BYTEA
//     (proving the migration 0018 type change).
//
//  5. Try to decrypt the column bytes with a
//     DIFFERENT identity (a fresh key pair). The
//     decrypt must fail (proving the ciphertext is
//     tied to the operator's key, not a global
//     "age" symmetric key).
func TestPgStore_SecretAge_RoundTrip(t *testing.T) {
	ctx := context.Background()

	// Generate operator's key pair.
	operatorIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity: %v", err)
	}
	operatorRecipient := operatorIdentity.Recipient()

	// Write the identity to a temp file in the
	// standard age-keygen format. The cipher
	// helper reads this exact format.
	identityPath := writeAgeIdentity(t, operatorIdentity)

	cipher, err := NewAgeSecretCipher(
		[]string{operatorRecipient.String()},
		identityPath,
	)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher: %v", err)
	}

	// Use a dedicated PgStore so the TRUNCATE in
	// newPgStoreWithCipher doesn't clobber any
	// rows the other tests in this file might
	// be racing for. (testutil's advisory lock
	// already serialises cross-package, so this
	// is belt-and-suspenders.)
	s := newPgStoreWithCipher(t, cipher)

	plaintext := "webhook-fixture-secret-aaaaaaaaaaaaaaaaaaaaaaaa"
	e := &Endpoint{
		ID:      uuid.New(),
		URL:     "https://envelope.example.com/h",
		Secret:  plaintext,
		Enabled: true,
	}
	if err := s.CreateEndpoint(ctx, e); err != nil {
		t.Fatalf("CreateEndpoint: %v", err)
	}

	// 3. Read back: plaintext round-trips.
	got, err := s.GetEndpoint(ctx, e.ID)
	if err != nil {
		t.Fatalf("GetEndpoint: %v", err)
	}
	if got.Secret != plaintext {
		t.Errorf("Secret = %q, want %q (plaintext round-trip failed)", got.Secret, plaintext)
	}

	// 4. Direct DB query: column is BYTEA, does
	// not contain the plaintext.
	pool := testutil.MustNewPool(t)
	var raw []byte
	var colType string
	if err := pool.QueryRow(ctx,
		`SELECT secret_ciphertext, pg_typeof(secret_ciphertext)::text FROM webhook_endpoints WHERE id = $1`,
		e.ID,
	).Scan(&raw, &colType); err != nil {
		t.Fatalf("direct SELECT: %v", err)
	}
	if colType != "bytea" {
		t.Errorf("secret_ciphertext column type = %q, want bytea (migration 0018)", colType)
	}
	if bytes.Contains(raw, []byte(plaintext)) {
		t.Errorf("secret_ciphertext column contains plaintext (envelope NOT applied): %q", raw)
	}
	// Sanity: ciphertext is non-empty and is
	// longer than the plaintext (age adds a
	// header + nonce + MAC overhead).
	if len(raw) == 0 {
		t.Errorf("ciphertext is empty")
	}
	if len(raw) <= len(plaintext) {
		t.Errorf("ciphertext length %d, want > plaintext length %d", len(raw), len(plaintext))
	}

	// 5. A different identity cannot decrypt
	// the operator's ciphertext.
	otherIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatalf("GenerateX25519Identity (other): %v", err)
	}
	otherPath := writeAgeIdentity(t, otherIdentity)
	otherCipher, err := NewAgeSecretCipher(
		[]string{otherIdentity.Recipient().String()},
		otherPath,
	)
	if err != nil {
		t.Fatalf("NewAgeSecretCipher (other): %v", err)
	}
	if _, err := otherCipher.Decrypt(raw); err == nil {
		t.Errorf("expected decrypt to fail with the wrong identity, got success")
	}
}
