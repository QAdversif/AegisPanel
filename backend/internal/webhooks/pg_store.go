// SPDX-License-Identifier: AGPL-3.0-or-later
//
// PgStore is the PostgreSQL-backed implementation
// of Store. It owns the three tables introduced in
// v0.7.0:
//
//   - webhook_endpoints   (migration 0001)
//   - webhook_deliveries  (migration 0014)
//   - webhook_dlq         (migration 0014)
//
// # Shape
//
// One row per endpoint / delivery / DLQ entry.
// (endpoint_id) is indexed on both delivery and
// DLQ tables so the per-endpoint history reads are
// a single index scan. (created_at DESC) is
// indexed on both for the list view's "newest
// first" sort.
//
// # JSONB for event payloads
//
// The `payload` column on both `webhook_deliveries`
// and `webhook_dlq` is JSONB. The Store stores the
// exact bytes the panel POSTed (the canonical
// form) so the operator can replay a delivery and
// the receiver sees the same body. The pgx encode
// path uses pgtype.JSONB explicitly so the bytes
// round-trip without re-encoding by the driver.

package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/QAdversif/AegisPanel/internal/crypto/envelope"
)

// jsonRawMessage wraps the JSONB bytes in a
// json.RawMessage so the JSON encoder emits them
// verbatim (rather than base64-encoding the
// []byte). The pgx scan already returns the raw
// bytes from the JSONB column; we just relabel.
func jsonRawMessage(b []byte) any {
	if b == nil {
		return nil
	}
	return json.RawMessage(b)
}

// PgStore is the PostgreSQL-backed Store.
type PgStore struct {
	pool   *pgxpool.Pool
	cipher envelope.SecretCipher
}

// NewPgStore wires a PgStore from an open pgxpool
// and a SecretCipher. The pool is owned by the
// caller — close it when the application shuts
// down. The cipher is consulted on every endpoint
// write (encrypt the secret) and read (decrypt the
// ciphertext back to plaintext for the Service).
//
// `cipher` is an `envelope.SecretCipher` (see
// internal/crypto/envelope). The production wiring
// passes an *envelope.AgeSecretCipher built from
// AEGIS_WEBHOOKS_SECRET_AGE_RECIPIENTS and
// AEGIS_WEBHOOKS_SECRET_AGE_KEY_FILE. Tests can
// pass an *envelope.NoopSecretCipher to bypass
// encryption (the secret round-trips as plaintext
// through the column).
func NewPgStore(pool *pgxpool.Pool, cipher envelope.SecretCipher) *PgStore {
	if cipher == nil {
		// Fail loud: a nil cipher is a wiring
		// bug, not a "fall back to plaintext"
		// signal. The Store interface does not
		// allow plaintext at rest on pg.
		panic("webhooks.PgStore: SecretCipher must not be nil")
	}
	return &PgStore{pool: pool, cipher: cipher}
}

// --- endpoints ---------------------------------------------------------

// CreateEndpoint inserts a new endpoint. Returns
// ErrDuplicate on URL collision (Postgres
// SQLSTATE 23505).
func (s *PgStore) CreateEndpoint(ctx context.Context, e *Endpoint) error {
	if e == nil {
		return fmt.Errorf("create endpoint: nil")
	}
	if e.ID == uuid.Nil {
		return fmt.Errorf("create endpoint: zero id")
	}
	if !e.IsValid() {
		return fmt.Errorf("create endpoint: invalid")
	}
	events, err := encodeEventsJSON(e.Events)
	if err != nil {
		return fmt.Errorf("create endpoint: events: %w", err)
	}
	// v0.7.x: encrypt the secret at the
	// Store boundary so the DB never sees
	// plaintext. The Service hands plaintext
	// to the Store (e.IsValid requires non-
	// empty secret); the Store hands ciphertext
	// to Postgres.
	cipher, err := s.cipher.Encrypt([]byte(e.Secret))
	if err != nil {
		return fmt.Errorf("create endpoint: encrypt secret: %w", err)
	}
	const q = `
		INSERT INTO webhook_endpoints (
			id, url, secret_ciphertext, events, enabled
		) VALUES (
			$1, $2, $3, $4, $5
		)`
	_, err = s.pool.Exec(ctx, q,
		e.ID,
		e.URL,
		cipher,
		events,
		e.Enabled,
	)
	if err != nil {
		return mapPgError(err, "create endpoint")
	}
	return nil
}

// GetEndpoint returns the endpoint with the given
// id, or ErrNotFound.
func (s *PgStore) GetEndpoint(ctx context.Context, id uuid.UUID) (*Endpoint, error) {
	const q = baseEndpointSelect + ` WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	e, err := scanEndpointWithCipher(row, s.cipher)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: endpoint id %s", ErrNotFound, id)
		}
		return nil, err
	}
	return e, nil
}

// ListEndpoints returns every endpoint, sorted by
// CreatedAt asc.
func (s *PgStore) ListEndpoints(ctx context.Context) ([]*Endpoint, error) {
	const q = baseEndpointSelect + ` ORDER BY created_at ASC`
	rows, err := s.pool.Query(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("list endpoints: query: %w", err)
	}
	defer rows.Close()
	out := make([]*Endpoint, 0)
	for rows.Next() {
		e, err := scanEndpointWithCipher(rows, s.cipher)
		if err != nil {
			return nil, fmt.Errorf("list endpoints: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list endpoints: rows: %w", err)
	}
	return out, nil
}

// UpdateEndpoint replaces the stored copy of
// e.ID. Returns ErrNotFound if no such endpoint
// exists; ErrDuplicate if the URL rename would
// collide.
func (s *PgStore) UpdateEndpoint(ctx context.Context, e *Endpoint) error {
	if e == nil {
		return fmt.Errorf("update endpoint: nil")
	}
	if e.ID == uuid.Nil {
		return fmt.Errorf("update endpoint: zero id")
	}
	if !e.IsValid() {
		return fmt.Errorf("update endpoint: invalid")
	}
	events, err := encodeEventsJSON(e.Events)
	if err != nil {
		return fmt.Errorf("update endpoint: events: %w", err)
	}
	// v0.7.x: encrypt the (possibly rotated)
	// secret before update. The handler may
	// pass an empty string when the operator
	// does not want to rotate — but the
	// validator (e.IsValid) requires non-empty,
	// so we never UPDATE a NULL/empty secret.
	cipher, err := s.cipher.Encrypt([]byte(e.Secret))
	if err != nil {
		return fmt.Errorf("update endpoint: encrypt secret: %w", err)
	}
	const q = `
		UPDATE webhook_endpoints SET
			url = $2,
			secret_ciphertext = $3,
			events = $4,
			enabled = $5,
			last_delivery_at = $6,
			last_status_code = $7,
			updated_at = NOW()
		WHERE id = $1`
	var lastDeliveryAt pgtype.Timestamptz
	if e.LastDeliveryAt != nil {
		lastDeliveryAt = pgtype.Timestamptz{Time: *e.LastDeliveryAt, Valid: true}
	}
	var lastStatusCode pgtype.Int4
	if e.LastStatusCode != nil {
		lastStatusCode = pgtype.Int4{Int32: int32(*e.LastStatusCode), Valid: true} // #nosec G115 -- HTTP status codes fit in int16; cap is 999
	}
	tag, err := s.pool.Exec(ctx, q,
		e.ID,
		e.URL,
		cipher,
		events,
		e.Enabled,
		lastDeliveryAt,
		lastStatusCode,
	)
	if err != nil {
		return mapPgError(err, "update endpoint")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: endpoint id %s", ErrNotFound, e.ID)
	}
	return nil
}

// DeleteEndpoint removes the endpoint with the
// given id. The (endpoint_id) FK on
// webhook_deliveries has ON DELETE CASCADE so the
// delivery history is removed in the same
// transaction. The DLQ rows are kept (the FK is
// not enforced on the DLQ side).
func (s *PgStore) DeleteEndpoint(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_endpoints WHERE id = $1`, id)
	if err != nil {
		return mapPgError(err, "delete endpoint")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: endpoint id %s", ErrNotFound, id)
	}
	return nil
}

// --- deliveries --------------------------------------------------------

// CreateDelivery inserts a new delivery row.
func (s *PgStore) CreateDelivery(ctx context.Context, d *Delivery) error {
	if d == nil {
		return fmt.Errorf("create delivery: nil")
	}
	if d.ID == uuid.Nil {
		return fmt.Errorf("create delivery: zero id")
	}
	const q = `
		INSERT INTO webhook_deliveries (
			id, endpoint_id, event_type, payload,
			request_url, request_body, signature, timestamp,
			status_code, response_body, error, attempt, duration_ms
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8,
			$9, $10, $11, $12, $13
		)`
	var statusCode pgtype.Int4
	if d.StatusCode != nil {
		statusCode = pgtype.Int4{Int32: int32(*d.StatusCode), Valid: true} // #nosec G115 -- HTTP status codes fit in int16; cap is 999
	}
	var durationMs pgtype.Int4
	if d.DurationMs != nil {
		durationMs = pgtype.Int4{Int32: int32(*d.DurationMs), Valid: true} // #nosec G115 -- bounded by DispatchTimeout*MaxAttempts = ~10s*6 = 60s
	}
	payload := jsonRawMessage(d.Payload)
	_, err := s.pool.Exec(ctx, q,
		d.ID,
		d.EndpointID,
		string(d.EventType),
		payload,
		d.RequestURL,
		d.RequestBody,
		d.Signature,
		d.Timestamp,
		statusCode,
		nullableText(d.ResponseBody),
		nullableText(d.Error),
		d.Attempt,
		durationMs,
	)
	if err != nil {
		return mapPgError(err, "create delivery")
	}
	return nil
}

// ListDeliveriesByEndpoint returns the delivery
// history for the given endpoint, sorted by
// CreatedAt desc.
func (s *PgStore) ListDeliveriesByEndpoint(ctx context.Context, endpointID uuid.UUID, limit int) ([]*Delivery, error) {
	limit = clampLimit(limit)
	const q = baseDeliverySelect + `
		WHERE endpoint_id = $1
		ORDER BY created_at DESC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, endpointID, limit)
	if err != nil {
		return nil, fmt.Errorf("list deliveries: query: %w", err)
	}
	defer rows.Close()
	out := make([]*Delivery, 0)
	for rows.Next() {
		d, err := scanDelivery(rows)
		if err != nil {
			return nil, fmt.Errorf("list deliveries: scan: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list deliveries: rows: %w", err)
	}
	return out, nil
}

// --- DLQ ---------------------------------------------------------------

// EnqueueDLQ inserts a new DLQ entry.
func (s *PgStore) EnqueueDLQ(ctx context.Context, entry *DLQEntry) error {
	if entry == nil {
		return fmt.Errorf("enqueue dlq: nil")
	}
	if entry.ID == uuid.Nil {
		return fmt.Errorf("enqueue dlq: zero id")
	}
	const q = `
		INSERT INTO webhook_dlq (
			id, endpoint_id, endpoint_url, event_type,
			payload, last_error, attempts, last_attempt_at
		) VALUES (
			$1, $2, $3, $4,
			$5, $6, $7, $8
		)`
	payload := jsonRawMessage(entry.Payload)
	_, err := s.pool.Exec(ctx, q,
		entry.ID,
		entry.EndpointID,
		entry.EndpointURL,
		string(entry.EventType),
		payload,
		entry.LastError,
		entry.Attempts,
		entry.LastAttemptAt,
	)
	if err != nil {
		return mapPgError(err, "enqueue dlq")
	}
	return nil
}

// ListDLQ returns every DLQ entry, sorted by
// EnqueuedAt desc.
func (s *PgStore) ListDLQ(ctx context.Context, limit int) ([]*DLQEntry, error) {
	limit = clampLimit(limit)
	const q = baseDLQSelect + `
		ORDER BY enqueued_at DESC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, limit)
	if err != nil {
		return nil, fmt.Errorf("list dlq: query: %w", err)
	}
	defer rows.Close()
	out := make([]*DLQEntry, 0)
	for rows.Next() {
		e, err := scanDLQ(rows)
		if err != nil {
			return nil, fmt.Errorf("list dlq: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list dlq: rows: %w", err)
	}
	return out, nil
}

// GetDLQ returns the DLQ entry with the given id.
func (s *PgStore) GetDLQ(ctx context.Context, id uuid.UUID) (*DLQEntry, error) {
	const q = baseDLQSelect + ` WHERE id = $1`
	row := s.pool.QueryRow(ctx, q, id)
	e, err := scanDLQ(row)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("%w: dlq id %s", ErrNotFound, id)
		}
		return nil, err
	}
	return e, nil
}

// DeleteDLQ removes a DLQ entry.
func (s *PgStore) DeleteDLQ(ctx context.Context, id uuid.UUID) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM webhook_dlq WHERE id = $1`, id)
	if err != nil {
		return mapPgError(err, "delete dlq")
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("%w: dlq id %s", ErrNotFound, id)
	}
	return nil
}

// --- pending retries (v0.7.x) -----------------------------------------

// EnqueueRetry registers a retry for the given
// delivery. Idempotent: re-enqueueing the same
// delivery_id updates the next_attempt_at.
func (s *PgStore) EnqueueRetry(ctx context.Context, deliveryID uuid.UUID, nextAttemptAt time.Time) error {
	const q = `
		INSERT INTO webhook_pending_retries (
			delivery_id, next_attempt_at
		) VALUES (
			$1, $2
		)
		ON CONFLICT (delivery_id) DO UPDATE
		SET next_attempt_at = EXCLUDED.next_attempt_at`
	_, err := s.pool.Exec(ctx, q, deliveryID, nextAttemptAt)
	if err != nil {
		return mapPgError(err, "enqueue retry")
	}
	return nil
}

// DequeueRetry removes a retry row. Idempotent:
// a no-op when the row is already gone.
func (s *PgStore) DequeueRetry(ctx context.Context, deliveryID uuid.UUID) error {
	const q = `DELETE FROM webhook_pending_retries WHERE delivery_id = $1`
	// Intentionally ignore RowsAffected — the
	// delete is idempotent.
	_, err := s.pool.Exec(ctx, q, deliveryID)
	if err != nil {
		return mapPgError(err, "dequeue retry")
	}
	return nil
}

// ListDueRetries returns up to `limit` delivery
// IDs whose next_attempt_at is at or before `now`,
// ordered by next_attempt_at asc.
func (s *PgStore) ListDueRetries(ctx context.Context, now time.Time, limit int) ([]uuid.UUID, error) {
	limit = clampLimit(limit)
	const q = `
		SELECT delivery_id
		FROM webhook_pending_retries
		WHERE next_attempt_at <= $1
		ORDER BY next_attempt_at ASC
		LIMIT $2`
	rows, err := s.pool.Query(ctx, q, now, limit)
	if err != nil {
		return nil, fmt.Errorf("list due retries: query: %w", err)
	}
	defer rows.Close()
	out := make([]uuid.UUID, 0)
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list due retries: scan: %w", err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list due retries: rows: %w", err)
	}
	return out, nil
}

// --- shared column lists / scanners ------------------------------------

const baseEndpointSelect = `
	SELECT
		id, url, secret_ciphertext, events, enabled,
		last_delivery_at, last_status_code,
		created_at, updated_at
	FROM webhook_endpoints`

const baseDeliverySelect = `
	SELECT
		id, endpoint_id, event_type, payload,
		request_url, request_body, signature, timestamp,
		status_code, response_body, error, attempt, duration_ms,
		created_at
	FROM webhook_deliveries`

const baseDLQSelect = `
	SELECT
		id, endpoint_id, endpoint_url, event_type,
		payload, last_error, attempts, last_attempt_at,
		enqueued_at
	FROM webhook_dlq`

type rowScanner interface {
	Scan(dest ...any) error
}

// scanEndpoint reads an endpoint row and decrypts
// the secret. The cipher is owned by the store
// that called scanEndpoint (passed in by the
// caller — see scanEndpointWithCipher).
func scanEndpointWithCipher(row rowScanner, cipher envelope.SecretCipher) (*Endpoint, error) {
	var (
		e              Endpoint
		secretCipher   []byte
		eventsRaw      []byte
		lastDeliveryAt pgtype.Timestamptz
		lastStatusCode pgtype.Int4
	)
	if err := row.Scan(
		&e.ID,
		&e.URL,
		&secretCipher,
		&eventsRaw,
		&e.Enabled,
		&lastDeliveryAt,
		&lastStatusCode,
		&e.CreatedAt,
		&e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	// v0.7.x: decrypt the secret at the Store
	// boundary. The Service receives plaintext.
	plain, err := cipher.Decrypt(secretCipher)
	if err != nil {
		return nil, fmt.Errorf("scan endpoint: decrypt secret: %w", err)
	}
	e.Secret = string(plain)
	if len(eventsRaw) > 0 {
		if err := json.Unmarshal(eventsRaw, &e.Events); err != nil {
			return nil, fmt.Errorf("scan endpoint: events: %w", err)
		}
	}
	if lastDeliveryAt.Valid {
		t := lastDeliveryAt.Time
		e.LastDeliveryAt = &t
	}
	if lastStatusCode.Valid {
		v := int(lastStatusCode.Int32)
		e.LastStatusCode = &v
	}
	return &e, nil
}

func scanDelivery(row rowScanner) (*Delivery, error) {
	var (
		d           Delivery
		payloadRaw  []byte
		statusCode  pgtype.Int4
		durationMs  pgtype.Int4
		responseRaw pgtype.Text
		errorRaw    pgtype.Text
	)
	if err := row.Scan(
		&d.ID,
		&d.EndpointID,
		&d.EventType,
		&payloadRaw,
		&d.RequestURL,
		&d.RequestBody,
		&d.Signature,
		&d.Timestamp,
		&statusCode,
		&responseRaw,
		&errorRaw,
		&d.Attempt,
		&durationMs,
		&d.CreatedAt,
	); err != nil {
		return nil, err
	}
	d.Payload = payloadRaw
	if statusCode.Valid {
		v := int(statusCode.Int32)
		d.StatusCode = &v
	}
	if durationMs.Valid {
		v := int(durationMs.Int32)
		d.DurationMs = &v
	}
	if responseRaw.Valid {
		d.ResponseBody = responseRaw.String
	}
	if errorRaw.Valid {
		d.Error = errorRaw.String
	}
	return &d, nil
}

func scanDLQ(row rowScanner) (*DLQEntry, error) {
	var (
		entry      DLQEntry
		payloadRaw []byte
	)
	if err := row.Scan(
		&entry.ID,
		&entry.EndpointID,
		&entry.EndpointURL,
		&entry.EventType,
		&payloadRaw,
		&entry.LastError,
		&entry.Attempts,
		&entry.LastAttemptAt,
		&entry.EnqueuedAt,
	); err != nil {
		return nil, err
	}
	entry.Payload = payloadRaw
	return &entry, nil
}

// mapPgError translates pgx errors into the
// package's sentinel errors. Currently catches
// SQLSTATE 23505 (unique_violation) and maps to
// ErrDuplicate. Other errors pass through with
// the operation prefix.
func mapPgError(err error, op string) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return fmt.Errorf("%w: %s: %s", ErrDuplicate, op, pgErr.ConstraintName)
	}
	return fmt.Errorf("%s: %w", op, err)
}

// encodeEventsJSON marshals the Events slice to
// JSON for the JSONB column. nil/empty slice
// renders as `[]`, not `null`, so the WHERE
// clause on the column does not have to special-
// case NULL.
func encodeEventsJSON(events []EventType) ([]byte, error) {
	if events == nil {
		return []byte(`[]`), nil
	}
	return json.Marshal(events)
}

// nullableText converts an empty string into a
// SQL NULL so the column is genuinely NULL (and
// not an empty string, which `SELECT ... IS NULL`
// would not match).
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// clampLimit applies the package's default + max
// bounds to a caller-supplied limit. 0 means
// default; values above MaxListLimit are clamped
// down.
func clampLimit(limit int) int {
	if limit <= 0 {
		return DefaultListLimit
	}
	if limit > MaxListLimit {
		return MaxListLimit
	}
	return limit
}

// Compile-time check.
var _ Store = (*PgStore)(nil)
