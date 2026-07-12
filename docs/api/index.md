---
title: API reference
---

# API reference

> 🚧 Under construction. The OpenAPI spec is generated from the Go
> handlers in `backend/api/`. It will be published here once the
> `auth`, `users`, `nodes`, and `hosts` modules reach Phase 1.

## Conventions

- **Base URL:** `https://<panel-domain>/api/v1`
- **Format:** JSON, `snake_case` keys, ISO-8601 timestamps.
- **Auth:** `Authorization: Bearer <token>` for admin endpoints;
  HMAC-SHA256 webhooks for inbound events.
- **Idempotency:** `Idempotency-Key` header on `POST` / `PUT` / `PATCH`
  / `DELETE`.
- **Rate limit:** 100 req / minute per token; `429` with
  `Retry-After`.
- **Versioning:** breaking changes bump the URL; non-breaking changes
  bump the `X-Api-Minor-Version` header.

## Planned endpoints (Phase 1+)

- `POST   /api/v1/auth/login`
- `POST   /api/v1/auth/refresh`
- `GET    /api/v1/nodes`
- `POST   /api/v1/nodes`
- `POST   /api/v1/nodes/{id}/install-agent`
- `GET    /api/v1/hosts`
- `POST   /api/v1/hosts`
- `GET    /api/v1/users`
- `GET    /api/v1/users/{id}/subscription`
- `GET    /api/v1/subscriptions/{token}` — the user-facing
  subscription endpoint (auto-detects client format).
- `POST   /api/v1/webhooks/payment`
- `GET    /api/v1/health`

See `ARCHITECTURE.md` §13 for the full Cabinet API surface.
