# feat(webhooks): wire Service.Dispatch to all mutating handlers

Closes the v0.7.0 known limitation: in v0.7.0 the
outbound webhook surface is wired end-to-end
(endpoints, deliveries, DLQ, retry worker) but
NO package emits events. The dispatcher sits
idle until an operator hand-rolls a trigger
somewhere. v0.7.x wires every mutating handler
to the dispatcher so creating a plan, deleting
a host, finishing a backup, etc. fans the event
out to every subscribed endpoint.

## What this PR does

* New `webhooks.MustDispatch` helper
  (`backend/internal/webhooks/dispatcher.go`)
  with three properties the raw Service.Dispatch
  does not have:
  * **Non-blocking** — errors are LOGGED and
    DROPPED, never returned. The HTTP handler
    has already committed the write; a webhook
    failure must not turn a 2xx into a 5xx and
    cause a client-side retry that re-applies
    the same mutation.
  * **Nil-safe** — accepts a nil *Service and
    silently no-ops. Unit tests construct
    `NewService(store)` without a webhooks
    service; the field is nil and the dispatch
    is a no-op. The existing test suites stay
    untouched.
  * **Bounded context** — a sub-context with
    a 5s deadline so a hung receiver cannot
    block the HTTP handler. The request
    context is propagated so a client-side
    disconnect cancels the dispatch.

* Per-package `WithWebhooks(svc)` setter on
  every mutating `Service` struct (users, plans,
  nodes, hosts, inbounds, backups). The setter
  is preferred over a constructor argument so
  the existing ~167 `NewService(...)` test
  fixtures stay untouched. The dispatch is
  only fired AFTER the row is persisted, so a
  receiver that acts on `user.created` sees a
  committed row.

* `cmd/aegis/main.go` wires the single
  `webhooksSvc` into every mutating service
  right after each service is constructed.

## What events fire when

| Package  | Operation     | Event                  |
|----------|---------------|------------------------|
| users    | Create        | user.created           |
| users    | Update        | user.updated           |
| users    | Delete        | user.deleted           |
| users    | RotateToken   | user.updated           |
| plans    | Create        | plan.created           |
| plans    | Update        | plan.updated           |
| plans    | Delete        | plan.deleted           |
| nodes    | Create        | node.created           |
| nodes    | Update        | node.updated           |
| nodes    | Delete        | node.deleted           |
| hosts    | Create        | host.created           |
| hosts    | Update        | host.updated           |
| hosts    | Delete        | host.deleted           |
| inbounds | Create        | inbound.created        |
| inbounds | Update        | inbound.updated        |
| inbounds | Delete        | inbound.deleted        |
| backups  | Insert row    | backup.created         |
| backups  | Success       | backup.completed       |
| backups  | Failure       | backup.failed          |

`RotateSubToken` is a v0.5+ feature; the event
type would be `user.token_rotated` but the closed
event enum does not include it yet, so the
rotation fires `user.updated` (the sub_token
column change is the update payload a receiver
can diff on). v0.7.x keeps the enum stable; a
dedicated event type is a v0.8+ follow-up.

## Delete payload

For `*.deleted` events, the row is gone by the
time the dispatch fires. The payload is a
small `map[string]string{"id": "..."}` carrying
only the identifier. Receivers that want the
full pre-deletion state can re-fetch from the
panel or rely on a local cache. v0.7.x keeps
this lightweight to avoid forcing the panel to
keep tombstones.

## Tests

* New `webhooks.Spy` test helper
  (`backend/internal/webhooks/spy.go`) —
  cross-package test double that wires an
  in-memory webhooks.Service with a no-op
  HTTP dialer. Cross-package tests construct
  a Spy, subscribe an endpoint to the event
  they care about, run the mutating operation,
  and assert on the recorded Delivery row.

* `backend/internal/plans/dispatcher_test.go`
  — Create/Update/Delete each assert the
  matching event fires.

* Same pattern in users, nodes, hosts,
  inbounds. Backups has two tests: the success
  path (created + completed) and the failure
  path (created + failed).

* `TestService_WithoutWebhooks_NoDispatch` in
  plans and users — sanity: the existing
  `NewService(store)` path (no `WithWebhooks`
  call) does not panic on the dispatch call.

* `go test ./...` — all 21 backend packages
  pass. 326 unit tests, all green.
* `go test -tags=integration ./...` —
  compiles clean; pg tests skip without
  `INTEGRATION_DATABASE_URL`.
* `golangci-lint run ./...` — 0 issues.

## Files

* `backend/internal/webhooks/dispatcher.go` (new, 113 lines)
* `backend/internal/webhooks/spy.go` (new, 153 lines)
* `backend/internal/{users,plans,nodes,hosts,inbounds,backups}/service.go` (+webhooks field, +WithWebhooks setter, +MustDispatch calls)
* `backend/internal/{users,plans,nodes,hosts,inbounds,backups}/dispatcher_test.go` (new test files)
* `backend/cmd/aegis/main.go` (wire webhooksSvc)

Total: 14 files, +1365/-24.

## Out of scope

* Audit-log call-site wiring is the
  sibling concern (every mutation also
  writes an `audit_log` row). v0.7.x is
  webhooks-only; the audit call-sites are
  a v0.6.x follow-up that the ROADMAP
  tracks separately. The `Service` struct
  pattern is the same (a `audits` field +
  setter), so a future PR mirrors this
  one line-for-line.
* sops Go library envelope around the
  Service-level `webhooksSvc` config is
  also out of scope; the recipients are
  passed as plaintext env vars in the
  same way the v0.7.0 secrets infra
  (PR #119) handles .env encryption.

Refs ROADMAP v0.7.x "wiring `Service.Dispatch`
to every mutating handler".
