// SPDX-License-Identifier: AGPL-3.0-or-later
//
// Zod schema tests. The CI `frontend` job currently
// has `npm run test` commented out (no test files
// at the time, see the comment in `.github/workflows/ci.yml`).
// This file is the first vitest suite; it covers
// the schemas that the v0.7.1 surface depends on
// (the `useZodForm` calls in `WebhooksView.vue`,
// `UsersView.vue`, and the v0.7.x shared `webhook.ts`).
// Uncomments the npm run test step in `ci.yml` so a
// regression here fails the build.
//
// # Why schema tests
//
// The schemas are the boundary between operator
// intent (typed form state) and the wire payload
// the backend accepts. A regression in the regex
// (e.g. allowing an empty username) or in the
// closed enum (e.g. dropping a state) silently
// produces 400s in production. The schema tests
// pin the public contract so such a regression
// fails the CI gate before it ships.
//
// # What's covered
//
// `primitives.ts` — uuid, isoDateTime, tag, hostPort.
// `user.ts` — create + update (the partial().strict() shape).
// `webhook.ts` — create + update + the closed 18-event enum.

import { describe, expect, it } from 'vitest'

import { isoDateTimeSchema, tagSchema, uuidSchema } from './primitives'
import { userCreateSchema, userUpdateSchema } from './user'
import {
  webhookCreateSchema,
  webhookEventTypeSchema,
  webhookUpdateSchema,
  webhookUrlSchema,
} from './webhook'

// --- primitives --------------------------------------------------------

describe('uuidSchema', () => {
  it('accepts a canonical v4-style UUID', () => {
    expect(
      uuidSchema.parse('550e8400-e29b-41d4-a716-446655440000'),
    ).toBe('550e8400-e29b-41d4-a716-446655440000')
  })

  it('rejects a non-UUID string', () => {
    expect(() => uuidSchema.parse('not-a-uuid')).toThrow()
  })

  it('rejects an empty string', () => {
    expect(() => uuidSchema.parse('')).toThrow()
  })

  it('rejects a UUID with the wrong case shape (bad segment length)', () => {
    // 7-4-4-4-12 — valid; here we feed 8-3-4-4-12.
    expect(() =>
      uuidSchema.parse('550e8400e2-b41d4-a716-446655440000'),
    ).toThrow()
  })
})

describe('isoDateTimeSchema', () => {
  it('accepts a UTC Z-suffix timestamp', () => {
    expect(isoDateTimeSchema.parse('2026-08-01T10:00:00Z')).toBe(
      '2026-08-01T10:00:00Z',
    )
  })

  it('accepts a +00:00 offset', () => {
    expect(isoDateTimeSchema.parse('2026-08-01T10:00:00+00:00')).toBe(
      '2026-08-01T10:00:00+00:00',
    )
  })

  it('rejects a bare date without a time component', () => {
    expect(() => isoDateTimeSchema.parse('2026-08-01')).toThrow()
  })
})

describe('tagSchema', () => {
  it('accepts a lowercase tag', () => {
    expect(tagSchema.parse('production')).toBe('production')
  })

  it('rejects an uppercase character', () => {
    // The Go validator's regex is `^[a-z0-9-]{1,64}$`; the
    // zod side mirrors that. Uppercase characters are a
    // wire-side 400 source of bugs in the past.
    expect(() => tagSchema.parse('Production')).toThrow()
  })

  it('rejects an empty string', () => {
    expect(() => tagSchema.parse('')).toThrow()
  })
})

// --- user ---------------------------------------------------------------

describe('userCreateSchema', () => {
  it('accepts a minimal valid payload and fills defaults', () => {
    const parsed = userCreateSchema.parse({ username: 'alice' })
    expect(parsed).toEqual({
      username: 'alice',
      status: 'active',
      trafficLimitBytes: 0,
      deviceLimit: 3,
    })
  })

  it('rejects a too-short username', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'ab' }),
    ).toThrow(/Username is too short/)
  })

  it('rejects a too-long username', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'a'.repeat(33) }),
    ).toThrow(/Username is too long/)
  })

  it('rejects a username with an uppercase character', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'Alice' }),
    ).toThrow(/Username/)
  })

  it('rejects a username with a space', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'ali ce' }),
    ).toThrow()
  })

  it('rejects an unknown status', () => {
    // The status enum is closed (the Go side mirrors
    // it in `internal/users/service.go`).
    expect(() =>
      userCreateSchema.parse({ username: 'alice', status: 'unknown' }),
    ).toThrow()
  })

  it('accepts a 0-byte traffic limit and rejects negative', () => {
    expect(
      userCreateSchema.parse({ username: 'alice', trafficLimitBytes: 0 })
        .trafficLimitBytes,
    ).toBe(0)
    expect(() =>
      userCreateSchema.parse({ username: 'alice', trafficLimitBytes: -1 }),
    ).toThrow()
  })

  it('rejects a deviceLimit > 64', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'alice', deviceLimit: 65 }),
    ).toThrow()
  })

  it('accepts an optional planId as UUID', () => {
    expect(
      userCreateSchema.parse({
        username: 'alice',
        planId: '550e8400-e29b-41d4-a716-446655440000',
      }).planId,
    ).toBe('550e8400-e29b-41d4-a716-446655440000')
  })

  it('rejects an optional planId that is not a UUID', () => {
    expect(() =>
      userCreateSchema.parse({ username: 'alice', planId: 'plan-1' }),
    ).toThrow()
  })
})

describe('userUpdateSchema', () => {
  it('accepts an empty patch', () => {
    expect(userUpdateSchema.parse({})).toEqual({})
  })

  it('accepts a single-field patch', () => {
    expect(
      userUpdateSchema.parse({ deviceLimit: 7 }),
    ).toEqual({ deviceLimit: 7 })
  })

  it('rejects unknown keys (the `.strict()` contract)', () => {
    // The Go handler mirrors `.strict()` semantics by
    // returning 400 on unknown JSON keys. The zod side
    // must do the same so the operator gets a UI error
    // before round-trip.
    expect(() =>
      userUpdateSchema.parse({ totallyUnknown: 'value' }),
    ).toThrow()
  })
})

// --- webhook -----------------------------------------------------------

describe('webhookEventTypeSchema', () => {
  it('accepts every member of the closed 18-event set', () => {
    const expected = [
      'user.created', 'user.updated', 'user.deleted',
      'plan.created', 'plan.updated', 'plan.deleted',
      'node.created', 'node.updated', 'node.deleted',
      'host.created', 'host.updated', 'host.deleted',
      'backup.created', 'backup.completed', 'backup.failed',
      'inbound.created', 'inbound.updated', 'inbound.deleted',
    ]
    expect(webhookEventTypeSchema.options).toEqual(expected)
  })

  it('rejects an unknown event type', () => {
    expect(() =>
      webhookEventTypeSchema.parse('user.archived'),
    ).toThrow()
  })
})

describe('webhookUrlSchema', () => {
  it('accepts an https URL', () => {
    expect(
      webhookUrlSchema.parse('https://example.com/hook'),
    ).toBe('https://example.com/hook')
  })

  it('accepts an http URL', () => {
    expect(webhookUrlSchema.parse('http://10.0.0.1:8080/hook')).toBe(
      'http://10.0.0.1:8080/hook',
    )
  })

  it('rejects a non-http(s) scheme', () => {
    expect(() => webhookUrlSchema.parse('ftp://x')).toThrow()
    expect(() => webhookUrlSchema.parse('javascript:alert(1)')).toThrow()
  })

  it('rejects a too-short URL', () => {
    expect(() => webhookUrlSchema.parse('http://a')).toThrow()
  })
})

describe('webhookCreateSchema', () => {
  it('accepts a minimal valid payload', () => {
    const parsed = webhookCreateSchema.parse({
      url: 'https://example.com/h',
      secret: 'sixteen-byte-secret-aaaaaaaaa',
    })
    expect(parsed.url).toBe('https://example.com/h')
    expect(parsed.secret).toBe('sixteen-byte-secret-aaaaaaaaa')
    expect(parsed.events).toEqual([]) // default
    expect(parsed.enabled).toBe(true) // default
  })

  it('rejects a too-short secret', () => {
    // 8 chars, under the 16-char minimum.
    expect(() =>
      webhookCreateSchema.parse({
        url: 'https://example.com/h',
        secret: 'short-aa',
      }),
    ).toThrow()
  })

  it('rejects a too-long secret', () => {
    expect(() =>
      webhookCreateSchema.parse({
        url: 'https://example.com/h',
        secret: 'a'.repeat(257),
      }),
    ).toThrow()
  })

  it('rejects an unknown event in the events array', () => {
    expect(() =>
      webhookCreateSchema.parse({
        url: 'https://example.com/h',
        secret: 'sixteen-byte-secret-aaaaaaaaa',
        events: ['user.created', 'user.archived'],
      }),
    ).toThrow()
  })
})

describe('webhookUpdateSchema', () => {
  it('accepts an empty patch (only `.strict()` enforces no unknown keys)', () => {
    expect(webhookUpdateSchema.parse({})).toEqual({})
  })

  it('accepts a partial events list', () => {
    expect(
      webhookUpdateSchema.parse({
        events: ['user.created', 'plan.deleted'],
      }),
    ).toEqual({ events: ['user.created', 'plan.deleted'] })
  })

  it('accepts an empty secret string as "leave unchanged"', () => {
    // The v0.7.1 WebhooksView edit dialog submits an
    // empty string when the operator does not type a
    // new secret. The backend's UpdateInput treats
    // the absent `secret` key as "no rotation"; the
    // schema must therefore accept the empty string.
    expect(webhookUpdateSchema.parse({ secret: '' })).toEqual({
      secret: '',
    })
  })

  it('rejects a too-short secret in the patch (when not empty)', () => {
    expect(() =>
      webhookUpdateSchema.parse({ secret: 'short' }),
    ).toThrow()
  })

  it('rejects unknown keys (the `.strict()` contract)', () => {
    expect(() =>
      webhookUpdateSchema.parse({ totallyUnknown: 1 }),
    ).toThrow()
  })
})
