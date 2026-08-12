## What

The v0.8.23 live smoke test caught a ninth
silent production bug: after a successful
`POST /api/v1/nodes/{id}/provision` (which
now finally returns 200 with
`{"new_state":"online"}` after the
v0.8.17 → v0.8.23 chain), the node row in
the DB still shows `state: "new"`. The
`BootstrapNodeProvider.Update` adapter
silently did nothing.

### Root cause

`backend/internal/nodes/handler.go:169`
called `a.Svc.Update(ctx, current.ID,
UpdateInput{})` with an EMPTY `UpdateInput`.
`UpdateInput` is a pointer-field struct
where nil pointers mean "leave alone". With
no non-nil fields, the underlying service
update wrote nothing — and the operator's
UI/state machine silently disagreed with the
provision response.

The mutation `current.State = State(row.State)`
just modified the in-memory struct; the
empty `UpdateInput{}` meant the field never
made it to the SQL `UPDATE` statement.

### Fix

Pass the new state through `UpdateInput.State`:

```go
newState := State(row.State)
_, err = a.Svc.Update(ctx, current.ID, UpdateInput{
    State: &newState,
})
```

One line of real change, plus a comment
block documenting the pre-PR-#234 bug.

## Tests

The pre-existing
`TestProvisioner_InstallSuccess_*` and
`TestProvisioner_RetryFromOffline_*` tests in
`internal/bootstrap/provisioner_test.go`
assert `row.State == string(StateOnline)`
after a successful provision. Those tests
were passing — but the assertion runs
against the in-memory `mockNodeProvider`,
which uses a different Update path than the
production `BootstrapNodeProvider`. The bug
existed only in the production adapter.

A follow-up test is in scope for the
v0.8.24 patch: extend
`internal/nodes/handler_test.go` to verify
that `BootstrapNodeProvider.Update` propagates
the State field. That test was not added in
this PR to keep the diff small; the v0.8.23
live smoke already exercises the full path
end-to-end (DB round-trip via the panel's
running container, not the in-memory mock).

## Files

- `backend/internal/nodes/handler.go` —
  `BootstrapNodeProvider.Update` now passes
  `State: &newState` to `a.Svc.Update`.

## Verification

v0.8.24's live smoke test on the live server.click:
after `POST /api/v1/nodes/9ded165d.../provision`
returns 200, `GET /api/v1/nodes/9ded165d...`
should return `state: "online"` (not `"new"`).
The DB row should also show `state='online'`
in `psql`.
