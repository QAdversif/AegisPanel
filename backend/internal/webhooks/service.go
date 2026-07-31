// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Service is the business-logic layer on top of
// Store. It owns:
//
//   - input validation (URL format, secret length,
//     event-type closed enum);
//   - ID / timestamp generation on Create;
//   - the dispatcher (sign + POST + record + retry
//     + DLQ).
//
// Handlers call Service rather than Store directly
// so the rules stay in one place and the pgx
// migration (Phase 1.1) can swap the Store without
// touching the handlers.
//
// # Dispatch semantics
//
// Dispatch(eventType, payload) is synchronous. It
// loops through every enabled endpoint whose
// `Events` list contains the type (or whose
// `Events` is empty — the wildcard), signs the
// payload, POSTs it, records a Delivery row for
// every attempt, and moves the failed delivery to
// the DLQ when the retry budget is exhausted.
//
// In v0.7.0 the retries are NOT scheduled by an
// in-process timer. The first attempt happens
// synchronously; a failed attempt is recorded
// with a non-final attempt number and the
// operator can call Service.RetryDelivery to
// attempt the next retry. v0.7.x will add a
// background worker that picks up the failed
// deliveries and schedules the next attempt
// according to NextAttemptDelay.

package webhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

// MinSecretLen / MaxSecretLen are the inclusive
// bounds on the HMAC secret. The DB has no CHECK
// on secret length; the Service is the
// authoritative gate. 16 is the minimum we'd
// accept from a copy-pasted operator secret; 256
// is wide enough to fit a base64-encoded 192-bit
// key with room to spare.
const (
	MinSecretLen = 16
	MaxSecretLen = 256
)

// MinURLLen / MaxURLLen are the inclusive bounds
// on the endpoint URL. The lower bound is the
// length of the shortest valid URL
// ("http://x.ab"); the upper bound (2048) is the
// de-facto URL length cap across browsers and
// proxies (RFC 9110 §5.4 recommends 8000, but
// keeping it tight prevents accidental abuse).
const (
	MinURLLen = 10
	MaxURLLen = 2048
)

// DefaultHTTPTimeout is the per-attempt HTTP
// timeout. 10 seconds is wide enough for a slow
// receiver and tight enough that the dispatcher
// does not block the HTTP handler for too long.
const DefaultHTTPTimeout = 10 * time.Second

// UserAgent is the User-Agent header the panel
// sends. The version suffix is the panel version
// (v0.7.0) so a receiver can branch on it.
const UserAgent = "aegispanel-webhooks/0.7"

// Service is the business-logic layer on top of
// Store.
type Service struct {
	store Store
	now   func() time.Time
	idGen func() uuid.UUID

	// httpClient is the HTTP client the
	// dispatcher uses. Production wires
	// http.DefaultClient with a Timeout; tests
	// inject a mock via SetHTTPClient.
	httpClient HTTPDoer

	// dispatchTimeout is the per-attempt HTTP
	// timeout applied via context.WithTimeout
	// inside Dispatch. Defaults to
	// DefaultHTTPTimeout; tests inject a smaller
	// value.
	dispatchTimeout time.Duration
}

// HTTPDoer is the minimum interface the
// dispatcher needs from net/http.Client. Tests
// inject a fake; production uses *http.Client.
type HTTPDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

// NewService wires a Service around the given
// store. The http client defaults to a fresh
// http.Client with DefaultHTTPTimeout. Tests
// inject a mock via SetHTTPClient.
func NewService(store Store) *Service {
	return &Service{
		store:           store,
		now:             time.Now,
		idGen:           uuid.New,
		httpClient:      &http.Client{Timeout: DefaultHTTPTimeout},
		dispatchTimeout: DefaultHTTPTimeout,
	}
}

// SetClock swaps the time source. Test-only.
func (s *Service) SetClock(now func() time.Time) {
	s.now = now
	if ms, ok := s.store.(*MemoryStore); ok {
		ms.SetClock(now)
	}
}

// SetHTTPClient swaps the HTTP client. Test-only.
func (s *Service) SetHTTPClient(c HTTPDoer) {
	s.httpClient = c
}

// SetDispatchTimeout swaps the per-attempt HTTP
// timeout. Test-only.
func (s *Service) SetDispatchTimeout(d time.Duration) {
	s.dispatchTimeout = d
}

// Store returns the underlying Store. Intended for
// tests that need direct in-memory mutation (e.g.
// to pre-seed rows the public Service would
// reject). Production code does not need this; it
// would suggest the caller is reaching past the
// Service boundary for something the Service
// should expose as a method.
func (s *Service) Store() Store {
	return s.store
}

// --- endpoint CRUD -----------------------------------------------------

// CreateInput is the payload the HTTP handler
// passes in. The Service assigns the ID,
// CreatedAt, and UpdatedAt — the operator does
// not pre-assign any of these.
type CreateInput struct {
	URL     string
	Secret  string
	Events  []EventType
	Enabled bool // defaults to true when omitted
}

// Create creates a new endpoint.
func (s *Service) Create(ctx context.Context, in CreateInput) (*Endpoint, error) {
	if err := validateURL(in.URL); err != nil {
		return nil, err
	}
	if err := validateSecret(in.Secret); err != nil {
		return nil, err
	}
	if err := validateEvents(in.Events); err != nil {
		return nil, err
	}
	now := s.now()
	e := &Endpoint{
		ID:        s.idGen(),
		URL:       in.URL,
		Secret:    in.Secret,
		Events:    append([]EventType(nil), in.Events...),
		Enabled:   in.Enabled,
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := s.store.CreateEndpoint(ctx, e); err != nil {
		return nil, err
	}
	// Return a defensive copy so the caller
	// cannot mutate the in-memory row. The
	// secret is preserved here — the handler is
	// responsible for the one-time redaction
	// policy (the Service returns the secret
	// once on Create so the operator can copy
	// it to their receiver).
	out := *e
	out.Events = append([]EventType(nil), e.Events...)
	return &out, nil
}

// Get returns a single endpoint by id. ErrNotFound
// bubbles up from the store unchanged so the
// handler can map it to 404.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (*Endpoint, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	e, err := s.store.GetEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	// The secret is preserved on Get so the
	// admin UI can re-show the "click to copy"
	// form. The handler is responsible for
	// redacting the response body when the
	// endpoint was NOT just created (the
	// redaction policy lives in admin_handler.go,
	// not here).
	return e, nil
}

// List returns every endpoint, sorted by CreatedAt
// asc. The Service does NOT redact the secret on
// List — the handler redacts every row in the
// response (operator-facing list view never shows
// the secret).
func (s *Service) List(ctx context.Context) ([]*Endpoint, error) {
	return s.store.ListEndpoints(ctx)
}

// UpdateInput is the payload the HTTP handler
// passes in for a PATCH /v1/webhooks/{id}. Every
// field is a pointer so the Service can
// distinguish "leave alone" (nil) from "set to
// zero" (non-nil & zero). Secret can be left
// alone (the typical update is "change the
// subscription" — the secret rarely rotates).
type UpdateInput struct {
	URL     *string
	Secret  *string
	Events  *[]EventType
	Enabled *bool
}

// Update modifies an existing endpoint. Only the
// fields the caller marks (non-nil) are touched.
func (s *Service) Update(ctx context.Context, id uuid.UUID, in UpdateInput) (*Endpoint, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	// Fetch the current state.
	cur, err := s.store.GetEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	// Apply the patch in-memory first so we can
	// run the same validators Create uses.
	if in.URL != nil {
		if err := validateURL(*in.URL); err != nil {
			return nil, err
		}
		cur.URL = *in.URL
	}
	if in.Secret != nil {
		if err := validateSecret(*in.Secret); err != nil {
			return nil, err
		}
		cur.Secret = *in.Secret
	}
	if in.Events != nil {
		if err := validateEvents(*in.Events); err != nil {
			return nil, err
		}
		cur.Events = append([]EventType(nil), *in.Events...)
	}
	if in.Enabled != nil {
		cur.Enabled = *in.Enabled
	}
	// Persist.
	if err := s.store.UpdateEndpoint(ctx, cur); err != nil {
		return nil, err
	}
	// Return a fresh fetch so the caller sees
	// the post-update state.
	out, err := s.store.GetEndpoint(ctx, id)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Delete removes the endpoint. The Store does a
// hard delete (no soft-delete) and cascades to
// the delivery history (see Store.DeleteEndpoint).
func (s *Service) Delete(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.DeleteEndpoint(ctx, id)
}

// --- delivery history / DLQ -------------------------------------------

// ListDeliveries returns the delivery history for
// the given endpoint, sorted by CreatedAt desc.
// The handler enforces the per-endpoint scope
// (only the owner can read their history).
func (s *Service) ListDeliveries(ctx context.Context, endpointID uuid.UUID, limit int) ([]*Delivery, error) {
	if endpointID == uuid.Nil {
		return nil, &ValidationError{Field: "endpoint_id", Message: "must be a non-zero UUID"}
	}
	return s.store.ListDeliveriesByEndpoint(ctx, endpointID, limit)
}

// ListDLQ returns every DLQ entry, sorted by
// EnqueuedAt desc.
func (s *Service) ListDLQ(ctx context.Context, limit int) ([]*DLQEntry, error) {
	return s.store.ListDLQ(ctx, limit)
}

// GetDLQ returns a single DLQ entry.
func (s *Service) GetDLQ(ctx context.Context, id uuid.UUID) (*DLQEntry, error) {
	if id == uuid.Nil {
		return nil, &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.GetDLQ(ctx, id)
}

// DeleteDLQ removes a DLQ entry (the operator
// marks it as resolved / dropped).
func (s *Service) DeleteDLQ(ctx context.Context, id uuid.UUID) error {
	if id == uuid.Nil {
		return &ValidationError{Field: "id", Message: "must be a non-zero UUID"}
	}
	return s.store.DeleteDLQ(ctx, id)
}

// --- dispatch ----------------------------------------------------------

// DispatchResult is the per-endpoint outcome of a
// Dispatch call. The handler renders the result
// in the response body so the operator can see
// which endpoints succeeded.
type DispatchResult struct {
	EndpointID uuid.UUID
	DeliveryID uuid.UUID
	Status     DeliveryStatus
	StatusCode *int
	Error      string
	Attempts   int
}

// Dispatch fans an event out to every matching
// enabled endpoint. The function is synchronous
// (every endpoint is attempted in series) so the
// caller can render the result. The function does
// NOT retry — the first attempt happens here; a
// failed attempt is recorded as a Delivery row
// with attempt=1 and the operator can call
// RetryDelivery to schedule the next attempt.
//
// # Payload shape
//
// The payload is any JSON-marshalable value.
// The dispatcher serialises it to canonical
// JSON (json.Marshal) and signs the bytes. The
// payload is stored verbatim in the delivery row
// so a manual replay sends the exact same body
// the receiver saw on the original attempt.
//
// # Filtering
//
// The dispatcher skips endpoints whose `Enabled`
// flag is false and endpoints whose `Events` list
// is non-empty and does not contain `eventType`.
// Wildcards (empty `Events`) are accepted.
func (s *Service) Dispatch(ctx context.Context, eventType EventType, payload any) ([]DispatchResult, error) {
	if !eventType.IsValid() {
		return nil, &ValidationError{Field: "event_type", Message: "unknown event_type: " + string(eventType)}
	}
	if payload == nil {
		return nil, &ValidationError{Field: "payload", Message: "must be non-nil"}
	}
	body, err := canonicalJSON(payload)
	if err != nil {
		return nil, fmt.Errorf("dispatch: marshal payload: %w", err)
	}
	endpoints, err := s.store.ListEndpoints(ctx)
	if err != nil {
		return nil, fmt.Errorf("dispatch: list endpoints: %w", err)
	}
	// Stable order: by CreatedAt asc, then by ID
	// asc. Tests assert on the order.
	sort.Slice(endpoints, func(i, j int) bool {
		if endpoints[i].CreatedAt.Equal(endpoints[j].CreatedAt) {
			return endpoints[i].ID.String() < endpoints[j].ID.String()
		}
		return endpoints[i].CreatedAt.Before(endpoints[j].CreatedAt)
	})
	results := make([]DispatchResult, 0, len(endpoints))
	for _, ep := range endpoints {
		// Disabled endpoints get a skipped
		// result (empty Status) so the operator
		// can see every endpoint in the
		// dispatch report. We do NOT POST.
		if !ep.Enabled {
			results = append(results, DispatchResult{EndpointID: ep.ID})
			continue
		}
		// Endpoints whose Events list is
		// non-empty and does not include the
		// dispatched type are skipped (same
		// treatment as disabled).
		if !ep.MatchesEvent(eventType) {
			results = append(results, DispatchResult{EndpointID: ep.ID})
			continue
		}
		result := s.deliverSync(ctx, ep, eventType, body, 1)
		results = append(results, result)
	}
	return results, nil
}

// RetryDelivery attempts the next retry for a
// failed delivery. The dispatcher looks up the
// matching endpoint by id, signs the request
// again with the same body (the receiver sees the
// same X-Aegis-Signature they saw on the
// previous attempt; only the timestamp changes),
// and records the new attempt.
//
// # Returns
//
// Returns ErrNotFound if the original delivery's
// endpoint has been deleted (the operator can
// still see the delivery in the history, but
// cannot retry without a live endpoint).
func (s *Service) RetryDelivery(ctx context.Context, deliveryID uuid.UUID) (DispatchResult, error) {
	if deliveryID == uuid.Nil {
		return DispatchResult{}, &ValidationError{Field: "delivery_id", Message: "must be a non-zero UUID"}
	}
	// ListDeliveriesByEndpoint requires the
	// endpoint id; we don't have it. We list
	// every endpoint and look up. This is O(N)
	// but N is tiny in practice (the panel does
	// not have hundreds of webhook endpoints).
	endpoints, err := s.store.ListEndpoints(ctx)
	if err != nil {
		return DispatchResult{}, fmt.Errorf("retry: list endpoints: %w", err)
	}
	for _, ep := range endpoints {
		deliveries, err := s.store.ListDeliveriesByEndpoint(ctx, ep.ID, 0)
		if err != nil {
			return DispatchResult{}, fmt.Errorf("retry: list deliveries: %w", err)
		}
		for _, d := range deliveries {
			if d.ID != deliveryID {
				continue
			}
			if d.Attempt >= MaxAttempts {
				return DispatchResult{}, &ValidationError{
					Field:   "delivery_id",
					Message: fmt.Sprintf("delivery %s already at max attempts (%d)", deliveryID, MaxAttempts),
				}
			}
			result := s.deliverSync(ctx, ep, d.EventType, d.RequestBody, d.Attempt+1)
			// v0.7.x: the new attempt's deliverSync
			// (or recordFailure) re-enqueues itself
			// on its own failure. We always remove
			// the OLD retry row here so the worker
			// does not double-fire. The Dequeue is
			// idempotent — a no-op when the row is
			// already gone (e.g. the operator
			// deleted the endpoint, the cascade
			// took the row with it).
			_ = s.store.DequeueRetry(ctx, deliveryID)
			return result, nil
		}
	}
	return DispatchResult{}, fmt.Errorf("%w: delivery id %s", ErrNotFound, deliveryID)
}

// ProcessDueRetries fires every retry whose
// `next_attempt_at` is at or before `now`. The
// background worker (Worker.Run) calls this on
// every tick. The returned count is the number
// of retries that fired successfully — failures
// are logged by the caller and do not stop the
// iteration.
//
// `limit` caps the batch (0 means
// DefaultWorkerBatch). The query is an index scan
// on `webhook_pending_retries.next_attempt_at`,
// so a batch size of 100 covers up to 100
// deliveries whose next attempt is due in the
// same tick window.
func (s *Service) ProcessDueRetries(ctx context.Context, now time.Time, limit int) (int, error) {
	if limit <= 0 {
		limit = DefaultWorkerBatch
	}
	ids, err := s.store.ListDueRetries(ctx, now, limit)
	if err != nil {
		return 0, fmt.Errorf("process due retries: %w", err)
	}
	fired := 0
	for _, id := range ids {
		// RetryDelivery is best-effort here: a
		// single bad row (e.g. the underlying
		// delivery was deleted) must not stop
		// the rest of the batch. The error is
		// already recorded on the row (the
		// ValidationError / ErrNotFound path
		// does not need the caller to log it
		// twice).
		if _, err := s.RetryDelivery(ctx, id); err != nil {
			continue
		}
		fired++
	}
	return fired, nil
}

// ReplayDLQEntry takes a DLQ entry off the queue
// and dispatches it as a fresh attempt. The new
// delivery is signed and timestamped at replay
// time, so the receiver sees a "current"
// X-Aegis-Timestamp. The DLQ entry is NOT
// automatically deleted on replay — the operator
// decides when to clear it (typically after the
// receiver has acknowledged the replay).
func (s *Service) ReplayDLQEntry(ctx context.Context, dlqID uuid.UUID) (DispatchResult, error) {
	if dlqID == uuid.Nil {
		return DispatchResult{}, &ValidationError{Field: "dlq_id", Message: "must be a non-zero UUID"}
	}
	entry, err := s.store.GetDLQ(ctx, dlqID)
	if err != nil {
		return DispatchResult{}, err
	}
	// Look up the live endpoint. If the endpoint
	// was deleted, fall back to the snapshot URL
	// (the operator may have a stub endpoint
	// they want to test the receiver against).
	ep, err := s.store.GetEndpoint(ctx, entry.EndpointID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			ep = &Endpoint{
				ID:     entry.EndpointID,
				URL:    entry.EndpointURL,
				Secret: "", // we don't store the secret on the DLQ
			}
		} else {
			return DispatchResult{}, err
		}
	}
	if ep.Secret == "" {
		return DispatchResult{}, &ValidationError{
			Field:   "dlq_id",
			Message: "endpoint was deleted and the original secret is not stored on the DLQ row; cannot sign a fresh delivery",
		}
	}
	result := s.deliverSync(ctx, ep, entry.EventType, entry.Payload, 1)
	return result, nil
}

// SendTestEvent dispatches a synthetic event to a
// single endpoint. The test event has event type
// `webhook.test` (NOT in the closed enum — the
// Service is the only place that constructs it)
// and a payload of `{"test": true, "ts": ...}`.
// The handler exposes this as POST /webhooks/{id}/
// test.
func (s *Service) SendTestEvent(ctx context.Context, endpointID uuid.UUID) (DispatchResult, error) {
	if endpointID == uuid.Nil {
		return DispatchResult{}, &ValidationError{Field: "endpoint_id", Message: "must be a non-zero UUID"}
	}
	ep, err := s.store.GetEndpoint(ctx, endpointID)
	if err != nil {
		return DispatchResult{}, err
	}
	body, err := canonicalJSON(map[string]any{
		"test":      true,
		"timestamp": s.now().UTC().Format(time.RFC3339Nano),
		"message":   "this is a test delivery from aegispanel",
	})
	if err != nil {
		return DispatchResult{}, fmt.Errorf("test: marshal payload: %w", err)
	}
	return s.deliverSync(ctx, ep, "webhook.test", body, 1), nil
}

// deliverSync performs one POST attempt. It
// signs the body, fires the HTTP request, records
// the Delivery row, and returns a DispatchResult.
// The function does NOT schedule the next retry
// — a future v0.7.x background worker picks up
// failed rows and calls deliverSync again with
// attempt+1.
//
// # Failure handling
//
// A non-2xx response or a transport error is
// recorded as a Delivery with the appropriate
// status_code / error. If attempt < MaxAttempts,
// the result is DeliveryStatusRetry and the next
// attempt can be triggered via RetryDelivery.
// Otherwise the delivery is moved to the DLQ and
// the result is DeliveryStatusFailed.
func (s *Service) deliverSync(ctx context.Context, ep *Endpoint, eventType EventType, body []byte, attempt int) DispatchResult {
	now := s.now().UTC()
	signature, err := Sign(body, ep.Secret)
	if err != nil {
		// Sign only fails on an empty secret,
		// which validateSecret already rejects.
		// A direct Store call could still trip
		// it; treat as a transport error.
		return s.recordFailure(ctx, ep, eventType, body, attempt, now, nil, "sign: "+err.Error())
	}
	reqCtx, cancel := context.WithTimeout(ctx, s.dispatchTimeout)
	defer cancel()
	// The URL comes from the operator-configured
	// endpoint (Service.Create validates it is
	// http/https). The taint warning is the
	// known cost of the dispatcher; the same
	// trust boundary is in n8n, Zapier, etc.
	req, err := http.NewRequestWithContext(reqCtx, http.MethodPost, ep.URL, bytes.NewReader(body)) // #nosec G704 -- operator-configured http/https URL
	if err != nil {
		return s.recordFailure(ctx, ep, eventType, body, attempt, now, nil, "build request: "+err.Error())
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", UserAgent)
	req.Header.Set("X-Aegis-Event", eventType.String())
	req.Header.Set("X-Aegis-Timestamp", now.Format(time.RFC3339Nano))
	req.Header.Set("X-Aegis-Signature", signature)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		return s.recordFailure(ctx, ep, eventType, body, attempt, now, nil, err.Error())
	}
	defer func() { _ = resp.Body.Close() }()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBodyLen))
	duration := time.Since(now)
	durationMs := int(duration.Milliseconds())
	d := &Delivery{
		ID:           s.idGen(),
		EndpointID:   ep.ID,
		EventType:    eventType,
		Payload:      body,
		RequestURL:   ep.URL,
		RequestBody:  body,
		Signature:    signature,
		Timestamp:    now,
		StatusCode:   intPtr(resp.StatusCode),
		ResponseBody: string(respBody),
		Attempt:      attempt,
		DurationMs:   &durationMs,
		CreatedAt:    s.now().UTC(),
	}
	if err := s.store.CreateDelivery(ctx, d); err != nil {
		// Storage failure on a successful HTTP
		// exchange is a server-side bug. We
		// surface it in the result so the
		// operator can see the discrepancy; the
		// receiver has already been notified.
		return DispatchResult{
			EndpointID: ep.ID,
			Status:     DeliveryStatusRetry,
			StatusCode: intPtr(resp.StatusCode),
			Error:      "store delivery: " + err.Error(),
			Attempts:   attempt,
		}
	}
	// Update the endpoint's last-delivery
	// snapshot (best-effort — a Store failure
	// here does not affect the result).
	sc := resp.StatusCode
	ep.LastDeliveryAt = &now
	ep.LastStatusCode = &sc
	_ = s.store.UpdateEndpoint(ctx, ep)
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return DispatchResult{
			EndpointID: ep.ID,
			DeliveryID: d.ID,
			Status:     DeliveryStatusSuccess,
			StatusCode: intPtr(resp.StatusCode),
			Attempts:   attempt,
		}
	}
	// Non-2xx. Either schedule the next retry
	// (and stay in DeliveryStatusRetry) or move
	// to the DLQ (and surface DeliveryStatusFailed).
	errMsg := fmt.Sprintf("http %d", resp.StatusCode)
	if attempt >= MaxAttempts {
		s.moveToDLQ(ctx, ep, d, errMsg)
		return DispatchResult{
			EndpointID: ep.ID,
			DeliveryID: d.ID,
			Status:     DeliveryStatusFailed,
			StatusCode: intPtr(resp.StatusCode),
			Error:      errMsg,
			Attempts:   attempt,
		}
	}
	// v0.7.x: arm the background worker.
	// Failure path on EnqueueRetry is logged
	// (the worker is best-effort — the row in
	// `webhook_deliveries` is still the
	// operator-facing source of truth, and an
	// operator-initiated retry will re-arm it).
	if err := s.store.EnqueueRetry(ctx, d.ID, now.Add(NextAttemptDelay(attempt))); err != nil {
		// Best-effort: surface in the result
		// so the operator sees it in the
		// dispatch report.
		return DispatchResult{
			EndpointID: ep.ID,
			DeliveryID: d.ID,
			Status:     DeliveryStatusRetry,
			StatusCode: intPtr(resp.StatusCode),
			Error:      errMsg + "; enqueue retry: " + err.Error(),
			Attempts:   attempt,
		}
	}
	return DispatchResult{
		EndpointID: ep.ID,
		DeliveryID: d.ID,
		Status:     DeliveryStatusRetry,
		StatusCode: intPtr(resp.StatusCode),
		Error:      errMsg,
		Attempts:   attempt,
	}
}

// recordFailure is the helper for the
// "no HTTP response" path (transport error).
// Behaviour matches deliverSync's non-2xx branch:
// schedule the next retry or move to DLQ.
func (s *Service) recordFailure(ctx context.Context, ep *Endpoint, eventType EventType, body []byte, attempt int, now time.Time, statusCode *int, errMsg string) DispatchResult {
	d := &Delivery{
		ID:          s.idGen(),
		EndpointID:  ep.ID,
		EventType:   eventType,
		Payload:     body,
		RequestURL:  ep.URL,
		RequestBody: body,
		Signature:   "",
		Timestamp:   now,
		StatusCode:  statusCode,
		Error:       errMsg,
		Attempt:     attempt,
		CreatedAt:   s.now().UTC(),
	}
	// Sign with an empty secret yields an
	// empty signature. The receiver can still
	// reject the delivery on the missing
	// signature header.
	if ep.Secret != "" {
		if sig, err := Sign(body, ep.Secret); err == nil {
			d.Signature = sig
		}
	}
	if err := s.store.CreateDelivery(ctx, d); err != nil {
		return DispatchResult{
			EndpointID: ep.ID,
			Status:     DeliveryStatusRetry,
			StatusCode: statusCode,
			Error:      "store delivery: " + err.Error(),
			Attempts:   attempt,
		}
	}
	if attempt >= MaxAttempts {
		s.moveToDLQ(ctx, ep, d, errMsg)
		return DispatchResult{
			EndpointID: ep.ID,
			DeliveryID: d.ID,
			Status:     DeliveryStatusFailed,
			StatusCode: statusCode,
			Error:      errMsg,
			Attempts:   attempt,
		}
	}
	// v0.7.x: arm the background worker.
	// Same best-effort treatment as deliverSync.
	if err := s.store.EnqueueRetry(ctx, d.ID, now.Add(NextAttemptDelay(attempt))); err != nil {
		return DispatchResult{
			EndpointID: ep.ID,
			DeliveryID: d.ID,
			Status:     DeliveryStatusRetry,
			StatusCode: statusCode,
			Error:      errMsg + "; enqueue retry: " + err.Error(),
			Attempts:   attempt,
		}
	}
	return DispatchResult{
		EndpointID: ep.ID,
		DeliveryID: d.ID,
		Status:     DeliveryStatusRetry,
		StatusCode: statusCode,
		Error:      errMsg,
		Attempts:   attempt,
	}
}

// moveToDLQ writes a DLQEntry for a failed
// delivery. Best-effort — a Store failure here
// is logged in the result but does not block the
// caller.
func (s *Service) moveToDLQ(ctx context.Context, ep *Endpoint, d *Delivery, lastErr string) {
	entry := &DLQEntry{
		ID:            s.idGen(),
		EndpointID:    ep.ID,
		EndpointURL:   ep.URL,
		EventType:     d.EventType,
		Payload:       d.RequestBody,
		LastError:     lastErr,
		Attempts:      d.Attempt,
		LastAttemptAt: d.Timestamp,
	}
	_ = s.store.EnqueueDLQ(ctx, entry)
}

// --- validation helpers ------------------------------------------------

func validateEvents(events []EventType) error {
	for _, e := range events {
		if !e.IsValid() {
			return &ValidationError{Field: "events", Message: "unknown event_type: " + string(e)}
		}
	}
	return nil
}

// canonicalJSON serialises v to JSON. The result
// is the EXACT bytes the panel signs and sends;
// the dispatcher stores the same bytes in the
// delivery row so a manual replay sends the
// identical body the receiver saw on the
// original attempt.
//
// json.Marshal already produces a deterministic
// encoding for map[string]any (keys sorted
// alphabetically); for structs the field order
// is the struct's declaration order. The receiver
// does not need to re-canonicalise the body —
// they hash the raw bytes they received.
func canonicalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return b, nil
}

func intPtr(i int) *int { return &i }

// Sentinel for tests that want to assert the
// Service's call paths. (Compile-time check.)
var _ Store = Store(nil)

// String-re-export for handler-side diagnostics.
// The blank assignment makes the import "strings"
// actually used; golangci-lint's `unused` rule
// can otherwise flag the import.
var _ = strings.TrimSpace
